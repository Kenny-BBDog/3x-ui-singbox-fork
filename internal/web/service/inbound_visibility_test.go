package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Panel accounts are peers over one estate, so an administrator added later has
// to see what the first one built. Scoping these three reads by account is what
// made a second account open onto an empty panel.
func TestInboundReadsSpanEveryAccount(t *testing.T) {
	setupConflictDB(t)

	inbounds := []*model.Inbound{
		{UserId: 1, Tag: "in-41001", Enable: true, Port: 41001, Protocol: model.VLESS, Settings: `{"clients":[]}`},
		{UserId: 2, Tag: "in-41002", Enable: true, Port: 41002, Protocol: model.VLESS, Settings: `{"clients":[]}`},
	}
	for _, inbound := range inbounds {
		if err := database.GetDB().Create(inbound).Error; err != nil {
			t.Fatalf("create inbound %s: %v", inbound.Tag, err)
		}
	}

	svc := &InboundService{}

	full, err := svc.GetInbounds()
	if err != nil {
		t.Fatalf("GetInbounds: %v", err)
	}
	if len(full) != len(inbounds) {
		t.Fatalf("GetInbounds returned %d inbounds, want all %d", len(full), len(inbounds))
	}

	slim, err := svc.GetInboundsSlim()
	if err != nil {
		t.Fatalf("GetInboundsSlim: %v", err)
	}
	if len(slim) != len(inbounds) {
		t.Fatalf("GetInboundsSlim returned %d inbounds, want all %d", len(slim), len(inbounds))
	}

	options, err := svc.GetInboundOptions()
	if err != nil {
		t.Fatalf("GetInboundOptions: %v", err)
	}
	if len(options) != len(inbounds) {
		t.Fatalf("GetInboundOptions returned %d inbounds, want all %d", len(options), len(inbounds))
	}
}
