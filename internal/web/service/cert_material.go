package service

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/atomicfile"
)

// CertMaterialRoot is the only directory tree a node accepts TLS material into.
const CertMaterialRoot = "/etc/letsencrypt/live/"

// CertMaterial is one TLS certificate and its key, as the master distributes
// them to a node. Paths are absolute and must sit under the caller's root.
type CertMaterial struct {
	CertFile string
	KeyFile  string
	Cert     []byte
	Key      []byte
}

// InstallCertMaterial replaces a certificate/key pair and reports whether either
// file changed, so the caller reloads only when there is something to load.
func InstallCertMaterial(root string, m CertMaterial) (bool, error) {
	return installCertMaterial(root, m, atomicfile.Write)
}

// fileWriter is the atomic-write primitive, injectable so the rollback below can
// be exercised rather than merely asserted in a comment.
type fileWriter func(path string, data []byte, perm os.FileMode) error

func installCertMaterial(root string, m CertMaterial, write fileWriter) (changed bool, err error) {
	certPath, err := resolveUnder(root, m.CertFile)
	if err != nil {
		return false, err
	}
	keyPath, err := resolveUnder(root, m.KeyFile)
	if err != nil {
		return false, err
	}
	// sing-box reads both files at process start and the pair cannot be swapped
	// as one atomic operation, so a mismatch here is an outage, not a bad write.
	if _, err := tls.X509KeyPair(m.Cert, m.Key); err != nil {
		return false, fmt.Errorf("refusing a certificate that does not match its key: %w", err)
	}

	certChanged, err := needsWrite(certPath, m.Cert)
	if err != nil {
		return false, err
	}
	keyChanged, err := needsWrite(keyPath, m.Key)
	if err != nil {
		return false, err
	}
	if !certChanged && !keyChanged {
		return false, nil
	}
	for _, dir := range []string{filepath.Dir(certPath), filepath.Dir(keyPath)} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}

	previousKey, previousKeyErr := os.ReadFile(keyPath)
	if err = write(keyPath, m.Key, 0o600); err != nil {
		return false, err
	}
	if err = write(certPath, m.Cert, 0o644); err != nil {
		// Restore the key, so a failure here cannot leave a mismatched pair.
		if previousKeyErr == nil {
			_ = write(keyPath, previousKey, 0o600)
		}
		return false, err
	}
	return true, nil
}

// needsWrite reports whether path is missing or holds different bytes.
func needsWrite(path string, want []byte) (bool, error) {
	have, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return !bytes.Equal(have, want), nil
}

// resolveUnder confines a master-supplied path to root. The route is reachable
// with a node-sync token, which without this could write anywhere on the node.
func resolveUnder(root, path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("certificate path %q is not absolute", path)
	}
	clean := filepath.Clean(path)
	prefix := filepath.Clean(root) + string(os.PathSeparator)
	if !strings.HasPrefix(clean, prefix) {
		return "", fmt.Errorf("certificate path %q is outside %s", path, root)
	}
	return clean, nil
}
