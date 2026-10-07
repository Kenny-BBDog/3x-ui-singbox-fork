package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/atomicfile"
)

// testCertPair returns a self-signed certificate and its matching key as PEM.
func testCertPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cert-material.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"cert-material.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func materialFor(t *testing.T, root string, certPEM, keyPEM []byte) CertMaterial {
	t.Helper()
	dir := filepath.Join(root, "monster.example")
	return CertMaterial{
		CertFile: filepath.Join(dir, "fullchain.pem"),
		KeyFile:  filepath.Join(dir, "privkey.pem"),
		Cert:     certPEM,
		Key:      keyPEM,
	}
}

// Catches a push that rewrites identical material: every reconcile tick would
// then churn the files and, worse, report "changed" so the caller restarts
// sing-box and drops every connection on the node for nothing.
func TestInstallCertMaterialIsIdempotent(t *testing.T) {
	root := t.TempDir()
	certPEM, keyPEM := testCertPair(t)
	m := materialFor(t, root, certPEM, keyPEM)

	changed, err := InstallCertMaterial(root, m)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if !changed {
		t.Fatal("first install reported no change")
	}
	if got, err := os.ReadFile(m.CertFile); err != nil || string(got) != string(certPEM) {
		t.Fatalf("certificate on disk = %q (err %v), want the pushed PEM", got, err)
	}

	// Backdate both files: an mtime that survives proves nothing was rewritten.
	past := time.Now().Add(-time.Hour)
	for _, p := range []string{m.CertFile, m.KeyFile} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatalf("backdate %s: %v", p, err)
		}
	}
	changed, err = InstallCertMaterial(root, m)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Fatal("a second push of identical material reported a change")
	}
	for _, p := range []string{m.CertFile, m.KeyFile} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !info.ModTime().Equal(past) {
			t.Fatalf("%s was rewritten: mtime = %s, want %s", p, info.ModTime(), past)
		}
	}
}

// Catches a renewal that silently keeps serving the old certificate.
func TestInstallCertMaterialReplacesChangedContent(t *testing.T) {
	root := t.TempDir()
	certPEM, keyPEM := testCertPair(t)
	m := materialFor(t, root, certPEM, keyPEM)
	if _, err := InstallCertMaterial(root, m); err != nil {
		t.Fatalf("first install: %v", err)
	}

	renewedCert, renewedKey := testCertPair(t)
	m.Cert, m.Key = renewedCert, renewedKey
	changed, err := InstallCertMaterial(root, m)
	if err != nil {
		t.Fatalf("renewal: %v", err)
	}
	if !changed {
		t.Fatal("a renewal reported no change")
	}
	if got, _ := os.ReadFile(m.CertFile); string(got) != string(renewedCert) {
		t.Fatalf("certificate was not replaced")
	}
	if got, _ := os.ReadFile(m.KeyFile); string(got) != string(renewedKey) {
		t.Fatalf("key was not replaced")
	}
}

// Catches a master-supplied path escaping the certificate tree: the route is
// reachable with a node-sync token, which without this could overwrite anything
// on the node's filesystem.
func TestInstallCertMaterialRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	certPEM, keyPEM := testCertPair(t)
	m := materialFor(t, root, certPEM, keyPEM)

	for _, tc := range []struct{ name, path string }{
		{"traversal", filepath.Join(root, "..", "escape.pem")},
		{"absolute elsewhere", "/etc/shadow"},
		{"relative", "relative.pem"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := m
			bad.CertFile = tc.path
			if _, err := InstallCertMaterial(root, bad); err == nil {
				t.Fatalf("accepted certificate path %q", tc.path)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escape.pem")); !os.IsNotExist(err) {
		t.Fatal("a rejected path was written anyway")
	}
}

// Catches a push that would leave sing-box unable to start: it reads both files
// at process start, so a certificate that does not match its key is an outage.
func TestInstallCertMaterialRejectsMismatchedPair(t *testing.T) {
	root := t.TempDir()
	certPEM, _ := testCertPair(t)
	_, otherKey := testCertPair(t)
	m := materialFor(t, root, certPEM, otherKey)

	if _, err := InstallCertMaterial(root, m); err == nil {
		t.Fatal("accepted a certificate that does not match its key")
	}
	if _, err := os.Stat(m.CertFile); !os.IsNotExist(err) {
		t.Fatal("a rejected pair was written anyway")
	}
}

// Catches the half-applied renewal: if the certificate write fails after the key
// was replaced, the node is left holding a pair that does not match and will not
// start. The key must be rolled back to what was there before.
func TestInstallCertMaterialRollsBackKeyWhenCertWriteFails(t *testing.T) {
	root := t.TempDir()
	certPEM, keyPEM := testCertPair(t)
	m := materialFor(t, root, certPEM, keyPEM)
	if _, err := InstallCertMaterial(root, m); err != nil {
		t.Fatalf("first install: %v", err)
	}

	renewedCert, renewedKey := testCertPair(t)
	m.Cert, m.Key = renewedCert, renewedKey

	failing := func(path string, data []byte, perm os.FileMode) error {
		if path == m.CertFile {
			return errors.New("injected certificate write failure")
		}
		return atomicfile.Write(path, data, perm)
	}
	if _, err := installCertMaterial(root, m, failing); err == nil {
		t.Fatal("install reported success despite a failing certificate write")
	}

	keyOnDisk, err := os.ReadFile(m.KeyFile)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if string(keyOnDisk) != string(keyPEM) {
		t.Fatal("key was left rotated after the certificate write failed; the pair no longer matches")
	}
}
