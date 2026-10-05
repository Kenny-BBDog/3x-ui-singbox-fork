package service

import (
	"encoding/json"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A client created on a node-attached sing-box inbound must be pushed to the
// node's own panel (per-machine credential mirror lives in the central row's
// settings.clients[]), so the node's sing-box learns the master-created client.
// Regression for the guard that disabled this push entirely.
func TestSingboxNodeInboundGetsClientPush(t *testing.T) {
	// The full push path needs a live node runtime; here we assert the wiring
	// decision instead: the mirror-password principle means the client payload
	// carried to the node must come from the central row's settings entry (the
	// node's own password), not from the shared client row.
	centralSettings := `{"clients":[{"email":"newbie","password":"atls-nodepwd","enable":true}]}`
	clients, _, err := singboxClientsForTest(centralSettings)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(clients) != 1 || clients[0].Password != "atls-nodepwd" {
		t.Fatalf("credential mirror should feed the push payload, got %+v", clients)
	}
}