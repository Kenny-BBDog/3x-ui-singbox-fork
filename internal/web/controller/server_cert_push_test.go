package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
)

// pushCertPair returns a self-signed certificate and its matching key as PEM.
func pushCertPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "push-cert.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"push-cert.test"},
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

type certPushEnv struct {
	engine  *gin.Engine
	root    string
	certPEM []byte
	keyPEM  []byte
	reloads *int
}

// pushCertEngine wires the production auth chain onto the real route, so a
// missing nodeSyncScopeAllow entry surfaces here as a 403 instead of only in
// production — no other test enumerates that allowlist.
func pushCertEngine(t *testing.T) *certPushEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))

	for name, scope := range map[string]string{"node-sync": model.ApiScopeNodeSync, "monitor": model.ApiScopeMonitor} {
		row := &model.ApiToken{Name: name, Token: crypto.HashTokenSHA256(name + "-token"), Enabled: true, Scope: scope}
		if err := database.GetDB().Create(row).Error; err != nil {
			t.Fatalf("seed %s token: %v", name, err)
		}
	}

	root := t.TempDir()
	prevRoot := certMaterialRoot
	certMaterialRoot = root
	reloads := 0
	prevReload := reloadSingbox
	reloadSingbox = func() error { reloads++; return nil }
	t.Cleanup(func() {
		certMaterialRoot = prevRoot
		reloadSingbox = prevReload
	})

	engine := gin.New()
	a := &APIController{}
	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth, a.enforceTokenScope)
	// initRouter rather than NewServerController: the same routes, without the
	// status ticker that needs a live web server.
	(&ServerController{}).initRouter(api.Group("/server"))

	certPEM, keyPEM := pushCertPair(t)
	return &certPushEnv{engine: engine, root: root, certPEM: certPEM, keyPEM: keyPEM, reloads: &reloads}
}

type pushCertResponse struct {
	Success bool `json:"success"`
	Obj     struct {
		Changed  bool `json:"changed"`
		Reloaded bool `json:"reloaded"`
	} `json:"obj"`
}

func (e *certPushEnv) push(t *testing.T, token string, cert, key []byte) (int, pushCertResponse) {
	t.Helper()
	dir := filepath.Join(e.root, "push-cert.test")
	form := url.Values{
		"certFile": {filepath.Join(dir, "fullchain.pem")},
		"keyFile":  {filepath.Join(dir, "privkey.pem")},
		"cert":     {string(cert)},
		"key":      {string(key)},
	}
	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/pushCertMaterial", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)

	var out pushCertResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// A node-sync token must reach the route, and a changed push must both write the
// material and reload the core — otherwise the node keeps serving the old
// certificate from memory while believing it took the new one.
func TestPushCertMaterialNodeSyncWritesAndReloads(t *testing.T) {
	env := pushCertEngine(t)

	code, out := env.push(t, "node-sync-token", env.certPEM, env.keyPEM)
	if code != http.StatusOK || !out.Success {
		t.Fatalf("node-sync push refused: %d %+v", code, out)
	}
	if !out.Obj.Changed || !out.Obj.Reloaded {
		t.Fatalf("first push reported changed=%v reloaded=%v, want both true", out.Obj.Changed, out.Obj.Reloaded)
	}
	if *env.reloads != 1 {
		t.Fatalf("reload called %d times, want 1", *env.reloads)
	}
	onDisk, err := os.ReadFile(filepath.Join(env.root, "push-cert.test", "fullchain.pem"))
	if err != nil {
		t.Fatalf("read pushed certificate: %v", err)
	}
	if string(onDisk) != string(env.certPEM) {
		t.Fatal("the pushed certificate did not land on disk")
	}
}

func TestPushCertMaterialRejectsMonitorScope(t *testing.T) {
	env := pushCertEngine(t)

	code, out := env.push(t, "monitor-token", env.certPEM, env.keyPEM)
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a monitor token", code)
	}
	if out.Success {
		t.Fatal("a monitor token was allowed to push TLS material")
	}
	if *env.reloads != 0 {
		t.Fatalf("reload called %d times for a refused push", *env.reloads)
	}
}

// Catches a reconcile that restarts the core on every tick: identical material
// must be a no-op, or every connection on the node drops once per tick for a
// certificate that did not change.
func TestPushCertMaterialIdenticalMaterialIsANoOp(t *testing.T) {
	env := pushCertEngine(t)

	if _, out := env.push(t, "node-sync-token", env.certPEM, env.keyPEM); !out.Obj.Changed {
		t.Fatalf("seed push did not report a change: %+v", out)
	}
	code, out := env.push(t, "node-sync-token", env.certPEM, env.keyPEM)
	if code != http.StatusOK || !out.Success {
		t.Fatalf("second push refused: %d %+v", code, out)
	}
	if out.Obj.Changed || out.Obj.Reloaded {
		t.Fatalf("identical material reported changed=%v reloaded=%v", out.Obj.Changed, out.Obj.Reloaded)
	}
	if *env.reloads != 1 {
		t.Fatalf("reload called %d times across two identical pushes, want 1", *env.reloads)
	}
}

// Catches a push aimed outside the certificate tree. The route is reachable by
// a node-sync token, which without this could overwrite anything on the node.
func TestPushCertMaterialRejectsEscapingPath(t *testing.T) {
	env := pushCertEngine(t)

	outside := filepath.Join(filepath.Dir(env.root), "escaped.pem")
	form := url.Values{
		"certFile": {outside},
		"keyFile":  {outside},
		"cert":     {string(env.certPEM)},
		"key":      {string(env.keyPEM)},
	}
	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/pushCertMaterial", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer node-sync-token")
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)

	var out pushCertResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Success {
		t.Fatalf("a path outside the certificate tree was accepted: %s", w.Body.String())
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("the escaping path was written anyway")
	}
	if *env.reloads != 0 {
		t.Fatalf("reload called %d times for a refused push", *env.reloads)
	}
}
