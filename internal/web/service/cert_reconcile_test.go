package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

func certReconcileNode(t *testing.T, mgr *runtime.Manager, name string, fake *fakeNodeRuntime) *model.Node {
	t.Helper()
	node := &model.Node{Name: name, Address: "127.0.0.1", Port: 2096, ApiToken: "tok", Enable: true, Status: "online"}
	if err := database.GetDB().Create(node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	mgr.SetRuntimeOverride(node.Id, fake)
	return node
}

func singboxCertInbound(t *testing.T, nodeID, port int, certPath, keyPath string) *model.Inbound {
	t.Helper()
	settings, err := json.Marshal(map[string]any{
		"certificate_path": certPath,
		"key_path":         keyPath,
		"clients":          []any{},
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	ib := &model.Inbound{
		UserId: 1, NodeID: &nodeID, Tag: fmt.Sprintf("sb-%d", port), Enable: true,
		Port: port, Protocol: model.AnyTLS, Settings: string(settings),
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	return ib
}

func plainNodeInbound(t *testing.T, nodeID, port int) *model.Inbound {
	t.Helper()
	ib := &model.Inbound{
		UserId: 1, NodeID: &nodeID, Tag: fmt.Sprintf("plain-%d", port), Enable: true,
		Port: port, Protocol: model.VLESS, Settings: clientsSettings(t, nil),
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	return ib
}

func writeCertPair(t *testing.T, dir, leaf string) (string, string) {
	t.Helper()
	certPath := filepath.Join(dir, leaf+"-fullchain.pem")
	keyPath := filepath.Join(dir, leaf+"-privkey.pem")
	if err := os.WriteFile(certPath, []byte("cert-"+leaf), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key-"+leaf), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

// A node's certificate is only ever renewed by the master, so a path its own
// inbounds reference but the reconcile never pushes stays frozen until it
// expires. The reverse matters too: the master is co-tenant to other businesses
// and must not ship their material to a VPN node.
func TestPushCertsToNodesPushesOnlyMaterialItsInboundsReference(t *testing.T) {
	setupBulkDB(t)
	mgr := useTestRuntimeManager(t)
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "monster")
	// The master holds this too, for a different business. No inbound below
	// references it, so no node may receive it.
	writeCertPair(t, dir, "other-business")

	served := &fakeNodeRuntime{}
	nodeServed := certReconcileNode(t, mgr, "served", served)
	singboxCertInbound(t, nodeServed.Id, 47001, certPath, keyPath)

	missing := &fakeNodeRuntime{}
	nodeMissing := certReconcileNode(t, mgr, "missing", missing)
	singboxCertInbound(t, nodeMissing.Id, 47002,
		filepath.Join(dir, "gone-fullchain.pem"), filepath.Join(dir, "gone-privkey.pem"))

	unreferenced := &fakeNodeRuntime{}
	nodeUnreferenced := certReconcileNode(t, mgr, "unreferenced", unreferenced)
	plainNodeInbound(t, nodeUnreferenced.Id, 47003)

	results, err := (&NodeService{}).PushCertsToNodes(context.Background())
	if err != nil {
		t.Fatalf("PushCertsToNodes: %v", err)
	}
	byNode := map[int]CertPushResult{}
	for _, r := range results {
		byNode[r.NodeID] = r
	}

	got := served.pushedMaterials()
	if len(got) != 1 {
		t.Fatalf("referenced node received %d pushes, want 1", len(got))
	}
	if got[0].CertFile != certPath || got[0].KeyFile != keyPath {
		t.Fatalf("pushed paths = %q/%q, want %q/%q", got[0].CertFile, got[0].KeyFile, certPath, keyPath)
	}
	if got[0].Cert != "cert-monster" || got[0].Key != "key-monster" {
		t.Fatalf("pushed material = %+v, want the master's current pair", got[0])
	}
	if r := byNode[nodeServed.Id]; r.Pushed != 1 || r.Error != "" {
		t.Fatalf("served node result = %+v, want one push and no error", r)
	}
	if got := missing.pushedMaterials(); len(got) != 0 {
		t.Fatalf("node referencing material the master does not hold received %+v", got)
	}
	if r := byNode[nodeMissing.Id]; r.Pushed != 0 || r.Error == "" {
		t.Fatalf("unreadable master material result = %+v, want an error rather than a push", r)
	}
	if got := unreferenced.pushedMaterials(); len(got) != 0 {
		t.Fatalf("node with no cert inbound received %+v", got)
	}
	if r := byNode[nodeUnreferenced.Id]; r.Pushed != 0 || r.Error != "" {
		t.Fatalf("node with no cert inbound result = %+v, want a silent no-op", r)
	}
}

// A converged node must cost a report, not a write: the node owns the file, so
// its answer is the only authoritative one, and a repeated push of identical
// material must not restart its sing-box every tick.
func TestPushCertsToNodesReportsConvergedNodeWithoutPushing(t *testing.T) {
	setupBulkDB(t)
	mgr := useTestRuntimeManager(t)
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "monster")

	converged := &fakeNodeRuntime{}
	converged.pushCertUnchanged.Store(true)
	node := certReconcileNode(t, mgr, "converged", converged)
	singboxCertInbound(t, node.Id, 47004, certPath, keyPath)

	results, err := (&NodeService{}).PushCertsToNodes(context.Background())
	if err != nil {
		t.Fatalf("PushCertsToNodes: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want one per enabled node", len(results))
	}
	if results[0].Pushed != 0 || results[0].Unchanged != 1 || results[0].Error != "" {
		t.Fatalf("converged node result = %+v, want pushed=0 unchanged=1", results[0])
	}
}

// Two inbounds of one node serving one domain must produce one push, not one
// per inbound: a second push would be a second restart.
func TestPushCertsToNodesDeduplicatesSharedMaterial(t *testing.T) {
	setupBulkDB(t)
	mgr := useTestRuntimeManager(t)
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "monster")

	fake := &fakeNodeRuntime{}
	node := certReconcileNode(t, mgr, "shared", fake)
	singboxCertInbound(t, node.Id, 47005, certPath, keyPath)
	singboxCertInbound(t, node.Id, 47006, certPath, keyPath)

	results, err := (&NodeService{}).PushCertsToNodes(context.Background())
	if err != nil {
		t.Fatalf("PushCertsToNodes: %v", err)
	}
	if got := fake.pushedMaterials(); len(got) != 1 {
		t.Fatalf("two inbounds on one domain produced %d pushes, want 1", len(got))
	}
	if results[0].Pushed != 1 {
		t.Fatalf("result = %+v, want pushed=1", results[0])
	}
}

// A disabled node cannot be reached, so the reconcile must not spend a request
// on it — and must not report it as a failure of the certificate rollout.
func TestPushCertsToNodesSkipsDisabledNodes(t *testing.T) {
	setupBulkDB(t)
	mgr := useTestRuntimeManager(t)
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "monster")

	off := &fakeNodeRuntime{}
	node := certReconcileNode(t, mgr, "disabled", off)
	singboxCertInbound(t, node.Id, 47007, certPath, keyPath)
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", node.Id).
		Update("enable", false).Error; err != nil {
		t.Fatalf("disable node: %v", err)
	}

	results, err := (&NodeService{}).PushCertsToNodes(context.Background())
	if err != nil {
		t.Fatalf("PushCertsToNodes: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("disabled node produced %+v, want it skipped", results)
	}
	if got := off.pushedMaterials(); len(got) != 0 {
		t.Fatalf("disabled node received %+v", got)
	}
}

// A node serving several domains must not lose the readable ones when one of
// them cannot be resolved on the master: a domain skipped here is a certificate
// that stops following the renewal.
func TestPushCertsToNodesKeepsPushingAfterOneDomainFails(t *testing.T) {
	setupBulkDB(t)
	mgr := useTestRuntimeManager(t)
	dir := t.TempDir()
	certPath, keyPath := writeCertPair(t, dir, "monster")

	fake := &fakeNodeRuntime{}
	node := certReconcileNode(t, mgr, "partial", fake)
	// Sorts ahead of the readable pair, so the failure is the one hit first.
	singboxCertInbound(t, node.Id, 47008,
		filepath.Join(dir, "absent-fullchain.pem"), filepath.Join(dir, "absent-privkey.pem"))
	singboxCertInbound(t, node.Id, 47009, certPath, keyPath)

	results, err := (&NodeService{}).PushCertsToNodes(context.Background())
	if err != nil {
		t.Fatalf("PushCertsToNodes: %v", err)
	}
	got := fake.pushedMaterials()
	if len(got) != 1 || got[0].CertFile != certPath {
		t.Fatalf("readable domain after an unresolvable one got %+v, want the monster pair", got)
	}
	if results[0].Pushed != 1 || results[0].Error == "" {
		t.Fatalf("result = %+v, want the push counted and the failure reported", results[0])
	}
}
