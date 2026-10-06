package service

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// The master must ADD a node's already-weighted delta to the client's row and
// never weight it again — the weight is applied exactly once, on the host that
// metered the bytes (spec 0002, Design). Double-weighting would make a 2x node
// bill 4x, so this is the property the whole node-side design protects.
//
// The scenario is the real one: a client whose local 1x traffic is already on the
// master, then a 2x node reports its own counters. The result must be
// local_raw + node_raw*2, and the raw pair must be local_raw + node_raw.
func TestSetRemoteTraffic_AddsNodeWeightedDeltaWithoutReweighting(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	db := database.GetDB()

	const nodeID = 1
	const email = "pool-user@example.com"
	const uid = "aa11bb22-cc33-4d44-8e55-ff6677889900"

	// The client's row already carries 40 raw bytes from the master's own 1x line.
	if err := db.Create(&xray.ClientTraffic{
		InboundId: 1, Email: email, Enable: true, Total: 10_000,
		Up: 40, Down: 0, RawUp: 40, RawDown: 0,
	}).Error; err != nil {
		t.Fatalf("seed client row: %v", err)
	}
	if err := db.Create(&model.ClientRecord{Email: email, UUID: uid, Enable: true}).Error; err != nil {
		t.Fatalf("seed client record: %v", err)
	}

	id := nodeID
	central := &model.Inbound{
		UserId: 1, NodeID: &id, Tag: "n1-res", Enable: true, Port: 30001,
		Protocol: model.VLESS,
		Settings: `{"clients":[{"email":"` + email + `","id":"` + uid + `","enable":true}]}`,
	}
	if err := db.Create(central).Error; err != nil {
		t.Fatalf("create node inbound: %v", err)
	}

	// The node reports 100 weighted for 50 raw — it already applied its 2x weight.
	// Its own baseline must exist, or the whole value counts as the first delta.
	if err := db.Create(&model.NodeClientTraffic{
		NodeId: nodeID, Email: email, Up: 0, Down: 0, RawUp: 0, RawDown: 0,
	}).Error; err != nil {
		t.Fatalf("seed node baseline: %v", err)
	}

	snap := &runtime.TrafficSnapshot{
		Inbounds: []*model.Inbound{
			{
				Tag: "n1-res", Enable: true, Port: 30001, Protocol: model.VLESS,
				Settings: `{"clients":[{"email":"` + email + `","id":"` + uid + `","enable":true}]}`,
				ClientStats: []xray.ClientTraffic{
					{Email: email, Up: 100, Down: 0, RawUp: 50, RawDown: 0},
				},
			},
		},
	}

	svc := InboundService{}
	if _, err := svc.setRemoteTrafficLocked(nodeID, snap, false, false); err != nil {
		t.Fatalf("setRemoteTrafficLocked: %v", err)
	}

	got := reloadTraffic(t, email)
	// 40 local (1x) + 100 the node already weighted (50 raw at 2x) = 140 weighted.
	if want := int64(140); got.Up != want {
		t.Errorf("weighted up = %d, want %d (local 40 + node's weighted 100; re-weighting would give 40+200)",
			got.Up, want)
	}
	// Raw must be 40 + 50 = 90, the true byte count across both hosts.
	if want := int64(90); got.RawUp != want {
		t.Errorf("raw up = %d, want %d", got.RawUp, want)
	}
	// The invariant that catches double-weighting: weighted - raw = the extra the
	// route cost, here 100 - 50 = 50 on the node side.
	if delta := got.Up - got.RawUp; delta != 50 {
		t.Errorf("weighted-raw = %d, want 50 (the node's 2x surcharge, applied once)", delta)
	}
}

// A baseline persisted before the raw columns existed holds raw 0 while its
// weighted counters carry the node's real totals. This is the state every
// production baseline was in the moment this feature first shipped, so the master
// must seed the raw baseline from the weighted one. Reading raw 0 as "the node has
// metered nothing" instead charges the node's entire accumulated history into
// raw_up a second time on the first tick — measured on production as raw_up ≈
// up + the node's whole total, which is impossible while every multiplier is 1.
func TestSetRemoteTraffic_SeedsRawBaselineFromWeightedForPreRawBaseline(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	db := database.GetDB()

	const nodeID = 1
	const email = "migrated-baseline@example.com"
	const uid = "cc33dd44-ee55-4f66-a077-bb8899001122"

	if err := db.Create(&xray.ClientTraffic{
		InboundId: 1, Email: email, Enable: true, Total: 10_000,
	}).Error; err != nil {
		t.Fatalf("seed client row: %v", err)
	}
	if err := db.Create(&model.ClientRecord{Email: email, UUID: uid, Enable: true}).Error; err != nil {
		t.Fatalf("seed client record: %v", err)
	}
	id := nodeID
	if err := db.Create(&model.Inbound{
		UserId: 1, NodeID: &id, Tag: "n1-migrated", Enable: true, Port: 30003,
		Protocol: model.VLESS,
		Settings: `{"clients":[{"email":"` + email + `","id":"` + uid + `","enable":true}]}`,
	}).Error; err != nil {
		t.Fatalf("create node inbound: %v", err)
	}
	// The baseline as it was written before raw columns existed: weighted totals
	// present, raw left at zero.
	if err := db.Create(&model.NodeClientTraffic{
		NodeId: nodeID, Email: email, Up: 500, Down: 0, RawUp: 0, RawDown: 0,
	}).Error; err != nil {
		t.Fatalf("seed pre-raw baseline: %v", err)
	}

	snap := &runtime.TrafficSnapshot{
		Inbounds: []*model.Inbound{
			{
				Tag: "n1-migrated", Enable: true, Port: 30003, Protocol: model.VLESS,
				Settings: `{"clients":[{"email":"` + email + `","id":"` + uid + `","enable":true}]}`,
				ClientStats: []xray.ClientTraffic{
					{Email: email, Up: 520, Down: 0, RawUp: 520, RawDown: 0},
				},
			},
		},
	}

	svc := InboundService{}
	if _, err := svc.setRemoteTrafficLocked(nodeID, snap, false, false); err != nil {
		t.Fatalf("setRemoteTrafficLocked: %v", err)
	}

	got := reloadTraffic(t, email)
	if want := int64(20); got.Up != want {
		t.Errorf("weighted up = %d, want %d (520 - 500 baseline)", got.Up, want)
	}
	// The regression: 520 here meant the whole node total was charged twice.
	if want := int64(20); got.RawUp != want {
		t.Errorf("raw up = %d, want %d (only the 20-byte delta, not the node's whole 520)", got.RawUp, want)
	}
	if got.RawUp > got.Up {
		t.Errorf("raw up (%d) exceeds weighted up (%d), which no multiplier >= 1 can produce", got.RawUp, got.Up)
	}
}

// A node build predating the multiplier reports no raw pair. The master must fall
// back to treating the weighted value as raw, not add zero and lose the audit
// trail or double-count the fallback.
func TestSetRemoteTraffic_HandlesNodeWithoutRaw(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	db := database.GetDB()

	const nodeID = 1
	const email = "oldnode-user@example.com"
	const uid = "bb22cc33-dd44-4e55-9f66-aa7788990011"

	if err := db.Create(&xray.ClientTraffic{
		InboundId: 1, Email: email, Enable: true, Total: 10_000,
	}).Error; err != nil {
		t.Fatalf("seed client row: %v", err)
	}
	if err := db.Create(&model.ClientRecord{Email: email, UUID: uid, Enable: true}).Error; err != nil {
		t.Fatalf("seed client record: %v", err)
	}
	id := nodeID
	if err := db.Create(&model.Inbound{
		UserId: 1, NodeID: &id, Tag: "n1-old", Enable: true, Port: 30002,
		Protocol: model.VLESS,
		Settings: `{"clients":[{"email":"` + email + `","id":"` + uid + `","enable":true}]}`,
	}).Error; err != nil {
		t.Fatalf("create node inbound: %v", err)
	}
	// Baseline the node at zero so the reported values register as this tick's
	// delta on an already-known inbound (the row exists, so the seed path is not
	// taken).
	if err := db.Create(&model.NodeClientTraffic{
		NodeId: nodeID, Email: email, Up: 0, Down: 0, RawUp: 0, RawDown: 0,
	}).Error; err != nil {
		t.Fatalf("seed node baseline: %v", err)
	}

	snap := &runtime.TrafficSnapshot{
		Inbounds: []*model.Inbound{
			{
				Tag: "n1-old", Enable: true, Port: 30002, Protocol: model.VLESS,
				Settings: `{"clients":[{"email":"` + email + `","id":"` + uid + `","enable":true}]}`,
				// No RawUp/RawDown: an older build.
				ClientStats: []xray.ClientTraffic{{Email: email, Up: 77, Down: 0}},
			},
		},
	}
	svc := InboundService{}
	if _, err := svc.setRemoteTrafficLocked(nodeID, snap, false, false); err != nil {
		t.Fatalf("setRemoteTrafficLocked: %v", err)
	}

	got := reloadTraffic(t, email)
	if got.Up != 77 {
		t.Errorf("weighted up = %d, want 77", got.Up)
	}
	if got.RawUp != 77 {
		t.Errorf("raw up = %d, want 77 (fall back to the weighted value, not zero)", got.RawUp)
	}
}
