package sub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// Geo asset names the subscription server mirrors for Clash/Mihomo clients.
// These come from MetaCubeX meta-rules-dat, which is the same source
// mihomo's own geodata loader expects.
const (
	GeoAssetGeoIP   = "geoip.dat"
	GeoAssetGeoSite = "geosite.dat"
	GeoAssetCountry = "country.mmdb"
	GeoAssetASN     = "GeoLite2-ASN.mmdb"

	// defaultGeoTTL is how long a cached asset is served before a refresh is
	// attempted, matching the 30-day window the previous sidecar used.
	defaultGeoTTL = 30 * 24 * time.Hour
)

// geoUpstream is an ordered list of mirrors for one asset; the first that
// answers wins.
var geoUpstreams = map[string][]string{
	GeoAssetGeoIP: {
		"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat",
		"https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.dat",
	},
	GeoAssetGeoSite: {
		"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
		"https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat",
	},
	GeoAssetCountry: {
		"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb",
		"https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/country.mmdb",
	},
	GeoAssetASN: {
		"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb",
		"https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/GeoLite2-ASN.mmdb",
	},
}

// GeoAssetNames returns the assets the subscription server can serve.
func GeoAssetNames() []string {
	return []string{GeoAssetGeoIP, GeoAssetGeoSite, GeoAssetCountry, GeoAssetASN}
}

// GeoDir returns the asset cache directory. It lives next to the panel binary
// folder so the same volume that holds the panel also holds the cache.
func GeoDir() string {
	return filepath.Join(config.GetBinFolderPath(), "geox")
}

// geoxOnce guards per-asset download locks.
var (
	geoxLocksMu sync.Mutex
	geoxLocks   = map[string]*sync.Mutex{}
)

func geoxLock(name string) *sync.Mutex {
	geoxLocksMu.Lock()
	defer geoxLocksMu.Unlock()
	if l, ok := geoxLocks[name]; ok {
		return l
	}
	l := &sync.Mutex{}
	geoxLocks[name] = l
	return l
}

// GeoAssetPath resolves an asset name to its cache path, rejecting anything
// that is not a known asset so the handler cannot be used to read arbitrary
// files.
func GeoAssetPath(name string) (string, bool) {
	base := filepath.Base(strings.TrimSpace(name))
	if _, ok := geoUpstreams[base]; !ok {
		return "", false
	}
	return filepath.Join(GeoDir(), base), true
}

// EnsureGeoAsset returns a usable local path for the asset, downloading (or
// refreshing) it when missing or older than ttl. A cached file is preferred
// over a failed download, so a temporarily unreachable upstream never breaks
// clients that already have the asset. ctx bounds the download, so a slow
// mirror cannot hold the request open past its own deadline.
func (a *SUBController) EnsureGeoAsset(ctx context.Context, name string) (string, error) {
	target, ok := GeoAssetPath(name)
	if !ok {
		return "", fmt.Errorf("unknown geo asset: %q", name)
	}

	lock := geoxLock(name)
	lock.Lock()
	defer lock.Unlock()

	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		if time.Since(info.ModTime()) < defaultGeoTTL {
			return target, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}

	client := a.settingService.NewProxiedHTTPClient(120 * time.Second)
	var lastErr error
	for _, url := range geoUpstreams[name] {
		if err := downloadGeoAsset(ctx, client, url, target); err != nil {
			lastErr = err
			logger.Warningf("geox: mirror %s failed for %s: %v", url, name, err)
			continue
		}
		logger.Infof("geox: cached %s from %s", name, url)
		return target, nil
	}

	// Fall back to a stale copy if one exists.
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		logger.Warningf("geox: all mirrors failed for %s, serving stale copy (%v)", name, lastErr)
		return target, nil
	}
	return "", fmt.Errorf("all mirrors failed for %s: %w", name, lastErr)
}

// downloadGeoAsset fetches url into dest atomically. A response that is not a
// 200, or is shorter than a plausible geo database, is rejected so a captive
// portal or error page never lands in the cache.
func downloadGeoAsset(ctx context.Context, client *http.Client, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return err
	}
	// A geo database is always at least a few hundred KB; anything smaller is
	// an error page or an HTML redirect body.
	if written < 64<<10 {
		return fmt.Errorf("suspiciously small payload (%d bytes)", written)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// GeoCacheStats reports what is currently cached, for diagnostics.
type GeoCacheStats struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// GeoCacheStatus lists cached assets with size, so an operator can see which
// geo databases the panel is serving and when they were last refreshed.
func GeoCacheStatus() []GeoCacheStats {
	out := make([]GeoCacheStats, 0, len(geoUpstreams))
	for _, name := range GeoAssetNames() {
		path, _ := GeoAssetPath(name)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			out = append(out, GeoCacheStats{Name: name})
			continue
		}
		out = append(out, GeoCacheStats{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return out
}
