package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// UpdateInbound copies each editable field onto the stored row explicitly. A
// field missing from that list is silently discarded while the request still
// answers success, so a multiplier set from the panel answered 200 and left the
// weight at 1 — which also meant the node never received it, because the value
// propagates from the stored row.
func TestUpdateInbound_PersistsTrafficMultiplier(t *testing.T) {
	setupConflictDB(t)

	ib := makeInboundWithSubSortIndex("in-7101-tcp", 7101, 1)
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	update := *ib
	update.TrafficMultiplier = 2
	got, _, err := (&InboundService{}).UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got.TrafficMultiplier != 2 {
		t.Fatalf("returned TrafficMultiplier = %d, want 2", got.TrafficMultiplier)
	}

	var reloaded model.Inbound
	if err := database.GetDB().First(&reloaded, ib.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TrafficMultiplier != 2 {
		t.Fatalf("persisted TrafficMultiplier = %d, want 2", reloaded.TrafficMultiplier)
	}
}

// A payload that omits the field binds as 0 (gin's int decoding). 0 must become 1,
// because the meter treats any stored weight as a real weight and a route is
// unweighted, not "free", when it is not configured.
func TestUpdateInbound_ZeroTrafficMultiplierBecomesOne(t *testing.T) {
	setupConflictDB(t)

	ib := makeInboundWithSubSortIndex("in-7102-tcp", 7102, 1)
	ib.TrafficMultiplier = 3
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	update := *ib
	update.TrafficMultiplier = 0
	got, _, err := (&InboundService{}).UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got.TrafficMultiplier != 1 {
		t.Fatalf("returned TrafficMultiplier = %d, want 1", got.TrafficMultiplier)
	}

	var reloaded model.Inbound
	if err := database.GetDB().First(&reloaded, ib.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TrafficMultiplier != 1 {
		t.Fatalf("persisted TrafficMultiplier = %d, want 1", reloaded.TrafficMultiplier)
	}
}

func TestNormalizeTrafficMultiplier(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 1}, {-5, 1}, {1, 1}, {2, 2}, {1000, 1000},
	}
	for _, c := range cases {
		if got := normalizeTrafficMultiplier(c.in); got != c.want {
			t.Errorf("normalizeTrafficMultiplier(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
