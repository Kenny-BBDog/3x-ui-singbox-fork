package sub

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// A geo asset request for a known name must be served from the cache directory,
// and an unknown name must be rejected without touching the filesystem.
func TestGeoAssetServesKnownNameAndRejectsUnknown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Point the cache at a temp dir and seed one asset.
	tmp := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", tmp)
	geoDir := filepath.Join(tmp, "geox")
	if err := os.MkdirAll(geoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 70<<10)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(geoDir, GeoAssetGeoIP), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// The fresh cache must be served without any network access.
	path, ok := GeoAssetPath(GeoAssetGeoIP)
	if !ok {
		t.Fatal("GeoAssetPath rejected a known asset")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("seeded asset not found at %s: %v", path, err)
	}

	// Unknown names must be refused, including traversal attempts.
	for _, bad := range []string{"../etc/passwd", "geoip.dat.bak", "unknown.dat", "..", ""} {
		if _, ok := GeoAssetPath(bad); ok {
			t.Errorf("GeoAssetPath accepted unsafe name %q", bad)
		}
	}

	// Exercise the handler for an unknown name: expect 404.
	a := &SUBController{}
	engine := gin.New()
	g := engine.Group("/")
	g.GET("geox/:name", a.geoAsset)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/geox/nope.dat", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown asset = %d, want 404", w.Code)
	}
}

// A fresh cached asset must be served straight from disk with the expected
// headers, and a HEAD must not include a body.
func TestGeoAssetServesFromCacheWithoutDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", tmp)
	geoDir := filepath.Join(tmp, "geox")
	if err := os.MkdirAll(geoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 80<<10)
	if err := os.WriteFile(filepath.Join(geoDir, GeoAssetGeoSite), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	a := &SUBController{}
	engine := gin.New()
	g := engine.Group("/")
	g.GET("geox/:name", a.geoAsset)
	g.HEAD("geox/:name", a.geoAsset)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/geox/"+GeoAssetGeoSite, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET cached asset = %d, want 200", w.Code)
	}
	if w.Body.Len() != len(payload) {
		t.Errorf("body = %d bytes, want %d", w.Body.Len(), len(payload))
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", cc)
	}

	wh := httptest.NewRecorder()
	engine.ServeHTTP(wh, httptest.NewRequest(http.MethodHead, "/geox/"+GeoAssetGeoSite, nil))
	if wh.Code != http.StatusOK {
		t.Errorf("HEAD cached asset = %d, want 200", wh.Code)
	}
	if wh.Body.Len() != 0 {
		t.Errorf("HEAD returned %d body bytes, want 0", wh.Body.Len())
	}
}

func TestGeoDirectoryUnderBinFolder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", tmp)
	want := filepath.Join(tmp, "geox")
	if got := GeoDir(); got != want {
		t.Errorf("GeoDir() = %q, want %q", got, want)
	}
}
