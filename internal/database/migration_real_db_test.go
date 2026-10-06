package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// TestMigrateTrafficMultiplierWiden_RealDatabase runs the real startup migration
// path against a copy of a production database. It is opt-in because it needs one:
//
//	XUI_REAL_DB=/path/to/x-ui.db go test ./internal/database/ -run RealDatabase -v
//
// The copy must arrive with its -wal and -shm siblings, or uncommitted changes are
// invisible.
//
// This is the check that a schema change is safe: the column goes from the INTEGER
// the first release declared to the REAL a fractional weight needs, on a table the
// running panel is actively writing to. It asserts the column becomes REAL, every
// stored value survives unchanged (they are all whole numbers, so 1 stays 1), no
// other table moves, and running it twice is a no-op.
func TestMigrateTrafficMultiplierWiden_RealDatabase(t *testing.T) {
	src := os.Getenv("XUI_REAL_DB")
	if src == "" {
		t.Skip("set XUI_REAL_DB to a copy of a real x-ui.db to run this")
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "x-ui.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := src + suffix
		data, err := os.ReadFile(from)
		if err != nil {
			if suffix == "" {
				t.Fatalf("read %s: %v", from, err)
			}
			continue
		}
		if err := os.WriteFile(dst+suffix, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst+suffix, err)
		}
	}

	before := snapshotRealDB(t, dst)
	if before.inboundCount == 0 {
		t.Skip("the supplied database has no inbounds")
	}
	t.Logf("before: column type=%s inbounds=%d client_traffics=%d up+down=%d",
		before.colType, before.inboundCount, before.trafficRows, before.upPlusDown)

	os.Setenv("XUI_DB_FOLDER", dir)
	t.Cleanup(func() { _ = CloseDB() })
	if err := InitDB(dst); err != nil {
		t.Fatalf("InitDB (the real startup path) on a real database: %v", err)
	}

	after := snapshotRealDB(t, dst)
	t.Logf("after:  column type=%s inbounds=%d client_traffics=%d up+down=%d",
		after.colType, after.inboundCount, after.trafficRows, after.upPlusDown)

	// SQLite reports the declared type in upper case, so compare case-insensitively.
	if strings.ToLower(after.colType) != "real" {
		t.Errorf("traffic_multiplier type = %q, want \"real\" (a fractional weight needs it)", after.colType)
	}
	if after.inboundCount != before.inboundCount {
		t.Errorf("inbounds rows: %d -> %d, want unchanged", before.inboundCount, after.inboundCount)
	}
	if after.trafficRows != before.trafficRows {
		t.Errorf("client_traffics rows: %d -> %d, want unchanged", before.trafficRows, after.trafficRows)
	}
	if after.upPlusDown != before.upPlusDown {
		t.Errorf("accumulated up+down moved: %d -> %d, want unchanged (a type change must not touch usage)",
			before.upPlusDown, after.upPlusDown)
	}
	// Every value survives, and the whole numbers still compare equal to the same
	// whole numbers: no value conversion happened, so no quota figure shifts.
	if after.multiplierSum != before.multiplierSum {
		t.Errorf("sum of traffic_multiplier: %v -> %v, want unchanged", before.multiplierSum, after.multiplierSum)
	}

	// Second run: the column is already REAL, so the widen must do nothing.
	if err := CloseDB(); err != nil {
		t.Fatalf("CloseDB: %v", err)
	}
	mid := snapshotRealDB(t, dst)
	if err := InitDB(dst); err != nil {
		t.Fatalf("InitDB second time: %v", err)
	}
	final := snapshotRealDB(t, dst)
	if final.colType != mid.colType || final.multiplierSum != mid.multiplierSum || final.upPlusDown != mid.upPlusDown {
		t.Errorf("a second startup changed the database: type %s->%s, multiplier sum %v->%v, up+down %d->%d",
			mid.colType, final.colType, mid.multiplierSum, final.multiplierSum, mid.upPlusDown, final.upPlusDown)
	}
	t.Logf("second startup: no further change (type=%s)", final.colType)
}

type realDBSnapshot struct {
	colType       string
	inboundCount  int
	trafficRows   int
	upPlusDown    int64
	multiplierSum float64
}

func snapshotRealDB(t *testing.T, path string) realDBSnapshot {
	t.Helper()
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer conn.Close()

	var s realDBSnapshot

	// In its own function so the row cursor is closed by defer on every path,
	// including the failure ones (sqlclosecheck / rowserrcheck).
	func() {
		rows, err := conn.Query("PRAGMA table_info(inbounds)")
		if err != nil {
			t.Fatalf("pragma table_info: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				t.Fatalf("scan pragma: %v", err)
			}
			if name == "traffic_multiplier" {
				s.colType = ctype
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read pragma rows: %v", err)
		}
	}()

	if err := conn.QueryRow("select count(*) from inbounds").Scan(&s.inboundCount); err != nil {
		t.Fatalf("count inbounds: %v", err)
	}
	if err := conn.QueryRow("select count(*) from client_traffics").Scan(&s.trafficRows); err != nil {
		t.Fatalf("count client_traffics: %v", err)
	}
	if err := conn.QueryRow("select coalesce(sum(up+down),0) from client_traffics").Scan(&s.upPlusDown); err != nil {
		t.Fatalf("sum traffic: %v", err)
	}
	if err := conn.QueryRow("select coalesce(sum(traffic_multiplier),0) from inbounds").Scan(&s.multiplierSum); err != nil {
		t.Fatalf("sum multiplier: %v", err)
	}
	return s
}

var _ = model.Inbound{}
