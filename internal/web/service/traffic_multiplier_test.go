package service

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// setupTrafficDB starts a fresh DB for a traffic-accounting test (spec 0002).
func setupTrafficDB(t *testing.T) {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
}

func reloadTraffic(t *testing.T, email string) xray.ClientTraffic {
	t.Helper()
	var row xray.ClientTraffic
	if err := database.GetDB().Model(xray.ClientTraffic{}).Where("email = ?", email).First(&row).Error; err != nil {
		t.Fatalf("reload %s: %v", email, err)
	}
	return row
}

// A weighted delta must raise both the weighted columns (what quota enforcement
// reads) and the raw columns (the audit pair), independently. This is the
// arithmetic the multiplier exists for: 2x on the route, 1x on the truth.
func TestAddClientTraffic_WeightedAndRawAccumulateIndependently(t *testing.T) {
	setupTrafficDB(t)
	db := database.GetDB()

	const email = "weighted-user"
	if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: email, Enable: true, Total: 1000}).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	svc := InboundService{}
	// 100 raw bytes metered on a 2x route.
	if err := svc.addClientTraffic(db, []*xray.ClientTraffic{
		{Email: email, Up: 200, Down: 0, RawUp: 100, RawDown: 0},
	}); err != nil {
		t.Fatalf("addClientTraffic: %v", err)
	}

	got := reloadTraffic(t, email)
	if got.Up != 200 {
		t.Errorf("weighted up = %d, want 200 (100 raw at 2x)", got.Up)
	}
	if got.RawUp != 100 {
		t.Errorf("raw up = %d, want 100", got.RawUp)
	}
	// A second poll on a 1x route: 50 raw, weighted equal.
	if err := svc.addClientTraffic(db, []*xray.ClientTraffic{
		{Email: email, Up: 50, Down: 0, RawUp: 50, RawDown: 0},
	}); err != nil {
		t.Fatalf("addClientTraffic (2): %v", err)
	}
	got = reloadTraffic(t, email)
	if got.Up != 250 {
		t.Errorf("weighted up = %d, want 250 (200 + 50)", got.Up)
	}
	if got.RawUp != 150 {
		t.Errorf("raw up = %d, want 150 (100 + 50)", got.RawUp)
	}
}

// A caller that reports only weighted values — xray's own poller, which has no
// multiplier concept — must still advance the raw pair, not leave it behind and
// not double-count. Without this, raw would drift from up/down on every deploy.
func TestAddClientTraffic_WeightedOnlyCallerAdvancesRaw(t *testing.T) {
	setupTrafficDB(t)
	db := database.GetDB()

	const email = "xray-user"
	if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: email, Enable: true}).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}
	svc := InboundService{}
	// No RawUp/RawDown set, as xray's poller does.
	if err := svc.addClientTraffic(db, []*xray.ClientTraffic{
		{Email: email, Up: 70, Down: 30},
	}); err != nil {
		t.Fatalf("addClientTraffic: %v", err)
	}

	got := reloadTraffic(t, email)
	if got.Up != 70 || got.Down != 30 {
		t.Errorf("weighted = %d/%d, want 70/30", got.Up, got.Down)
	}
	if got.RawUp != 70 || got.RawDown != 30 {
		t.Errorf("raw = %d/%d, want 70/30 (must track weighted when no raw is reported)",
			got.RawUp, got.RawDown)
	}
	if got.Up != got.RawUp {
		t.Errorf("raw must equal weighted while every inbound is 1x")
	}
}

// Quota enforcement must read the WEIGHTED value, or a 2x route would not
// deplete a quota twice as fast (spec 0002, Goal). This asserts the stored total
// the comparison uses, not the helper, because the comparison in production reads
// these columns.
func TestAddClientTraffic_WeightedValueIsWhatQuotaComparesAgainst(t *testing.T) {
	setupTrafficDB(t)
	db := database.GetDB()

	const email = "quota-user"
	const quota = 100
	if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: email, Enable: true, Total: quota}).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}
	svc := InboundService{}
	// 60 raw bytes on a 2x route: 120 weighted, which exceeds the 100 quota.
	if err := svc.addClientTraffic(db, []*xray.ClientTraffic{
		{Email: email, Up: 120, Down: 0, RawUp: 60, RawDown: 0},
	}); err != nil {
		t.Fatalf("addClientTraffic: %v", err)
	}

	got := reloadTraffic(t, email)
	weighted := got.Up + got.Down
	if weighted < quota {
		t.Fatalf("weighted usage %d must reach the %d quota (60 raw at 2x = 120)", weighted, quota)
	}
	raw := got.RawUp + got.RawDown
	if raw >= quota {
		t.Errorf("raw usage %d must stay under the quota; the weight is what depletes it", raw)
	}
}
