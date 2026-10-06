package service

import (
	"math"
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
	update.TrafficMultiplier = 2.5
	got, _, err := (&InboundService{}).UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got.TrafficMultiplier != 2.5 {
		t.Fatalf("returned TrafficMultiplier = %v, want 2.5", got.TrafficMultiplier)
	}

	var reloaded model.Inbound
	if err := database.GetDB().First(&reloaded, ib.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TrafficMultiplier != 2.5 {
		t.Fatalf("persisted TrafficMultiplier = %v, want 2.5 (a fractional weight must survive the round trip)", reloaded.TrafficMultiplier)
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
		t.Fatalf("returned TrafficMultiplier = %v, want 1", got.TrafficMultiplier)
	}

	var reloaded model.Inbound
	if err := database.GetDB().First(&reloaded, ib.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TrafficMultiplier != 1 {
		t.Fatalf("persisted TrafficMultiplier = %v, want 1", reloaded.TrafficMultiplier)
	}
}

func TestNormalizeTrafficMultiplier(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 1}, {-5, 1}, {1, 1}, {1.5, 1.5}, {2, 2}, {2.5, 2.5}, {1000, 1000},
	}
	for _, c := range cases {
		if got := normalizeTrafficMultiplier(c.in); got != c.want {
			t.Errorf("normalizeTrafficMultiplier(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	// NaN is not >= 1, so a bad payload cannot poison every later multiply.
	if got := normalizeTrafficMultiplier(math.NaN()); got != 1 {
		t.Errorf("normalizeTrafficMultiplier(NaN) = %v, want 1", got)
	}
}

// The weight is applied per metered delta and rounded to a whole byte, so a
// fractional weight must never accumulate as a fraction nor drift over many
// deltas. 1.5x is the case this exists for (a CN2GIA route).
func TestWeightTraffic_FractionalWeightRoundsPerDelta(t *testing.T) {
	cases := []struct {
		delta      int64
		multiplier float64
		want       int64
	}{
		{0, 1.5, 0},
		{1, 1.5, 2}, // 1.5 rounds half away from zero, i.e. up
		{2, 1.5, 3},
		{10, 1.5, 15},
		{1_000_000, 1.5, 1_500_000},
		{100, 1, 100},   // 1x is a passthrough, not a rounding exercise
		{100, 0.5, 100}, // below 1 is treated as unweighted, never a discount
		{100, math.NaN(), 100},
		{-5, 1.5, 0}, // a counter reset must not become negative usage
	}
	for _, c := range cases {
		if got := WeightTraffic(c.delta, c.multiplier); got != c.want {
			t.Errorf("WeightTraffic(%d, %v) = %d, want %d", c.delta, c.multiplier, got, c.want)
		}
	}

	// Over a run of odd-sized deltas the error must stay bounded by one byte per
	// delta rather than growing: the sum of 100 one-byte deltas at 1.5x is 200
	// (each rounds 1.5 -> 2), and 100 bytes as a single delta is 150.
	var many int64
	for i := 0; i < 100; i++ {
		many += WeightTraffic(1, 1.5)
	}
	if many != 200 {
		t.Errorf("100 one-byte deltas at 1.5x = %d, want 200", many)
	}
	one := WeightTraffic(100, 1.5)
	if one != 150 {
		t.Errorf("one 100-byte delta at 1.5x = %d, want 150", one)
	}
	// The bound is one byte per delta pair, so it cannot compound without bound.
	if diff := many - one; diff < 0 || diff > 100 {
		t.Errorf("rounding drift over 100 deltas = %d bytes, want within [0,100]", diff)
	}
}
