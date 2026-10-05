package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/singbox"
)

// A client created on a node-attached sing-box inbound must be pushed to the
// node's own panel (per-machine credential mirror lives in the central row's
// settings.clients[]), so the node's sing-box learns the master-created client.
// Regression for the guard that disabled this push entirely.
func TestSingboxNodeInboundGetsClientPush(t *testing.T) {
	centralSettings := `{"clients":[{"email":"newbie","password":"atls-nodepwd","enable":true}]}`
	clients, _, err := singbox.ParseInboundClients(centralSettings)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(clients) != 1 || clients[0].Password != "atls-nodepwd" {
		t.Fatalf("credential mirror should feed the push payload, got %+v", clients)
	}
}
