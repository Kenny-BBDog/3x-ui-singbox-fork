package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// The panel's inbound form writes realitySettings.settings.publicKey. An
// inbound created by hand, by import, or by a node sync can carry only
// privateKey — subscriptions then shipped a REALITY node without public-key and
// mihomo refused the entire profile. These vectors pin the derivation.
const (
	testRealityPrivate = "sHJ0Hs2w7KalDufKJ1VfgVUdUgDdPp9Q4kXNThUPcX4"
	testRealityPublic  = "_x6r0I0sbtmkOo76ufoHfGxD87-ZC37I8HqcvZbabxo"
)

func TestRealityPublicKeyFromPrivate(t *testing.T) {
	if got := realityPublicKeyFromPrivate(testRealityPrivate); got != testRealityPublic {
		t.Fatalf("realityPublicKeyFromPrivate() = %q, want %q", got, testRealityPublic)
	}
}

func TestRealityPublicKeyFromPrivate_AcceptsKeyForms(t *testing.T) {
	// URL-safe unpadded (what REALITY emits), padded, and standard alphabets.
	for _, form := range []string{
		testRealityPrivate,
		testRealityPrivate + "=",
		strings.ReplaceAll(testRealityPrivate, "-", "+"),
		"  " + testRealityPrivate + "  ",
	} {
		if got := realityPublicKeyFromPrivate(form); got != testRealityPublic {
			t.Errorf("form %q: got %q, want %q", form, got, testRealityPublic)
		}
	}
}

func TestRealityPublicKeyFromPrivate_RejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "   ", "not-a-key", "AAAA"} {
		if got := realityPublicKeyFromPrivate(bad); got != "" {
			t.Errorf("input %q: got %q, want empty", bad, got)
		}
	}
}

func TestRealityPublicKeyFor_PrefersStoredValue(t *testing.T) {
	// A stored key wins even if it disagrees with the private key, so an
	// operator who pasted a specific pair keeps control.
	got := realityPublicKeyFor(
		map[string]any{"publicKey": "stored-value"},
		map[string]any{"privateKey": testRealityPrivate},
	)
	if got != "stored-value" {
		t.Fatalf("got %q, want the stored value", got)
	}
}

func TestRealityPublicKeyFor_DerivesFromPrivate(t *testing.T) {
	got := realityPublicKeyFor(nil, map[string]any{"privateKey": testRealityPrivate})
	if got != testRealityPublic {
		t.Fatalf("got %q, want the derived %q", got, testRealityPublic)
	}
	// Stored settings block present but empty is the real-world shape.
	got = realityPublicKeyFor(map[string]any{}, map[string]any{"privateKey": testRealityPrivate})
	if got != testRealityPublic {
		t.Fatalf("empty settings block: got %q, want %q", got, testRealityPublic)
	}
}

func TestRealityPublicKeyFor_NoKeyAnywhere(t *testing.T) {
	if got := realityPublicKeyFor(nil, nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := realityPublicKeyFor(nil, map[string]any{}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// End-to-end through the Clash renderer: an inbound whose reality settings hold
// only privateKey must still yield a proxy with public-key set.
func TestClashRealityNode_DerivesPublicKeyWhenSettingsAbsent(t *testing.T) {
	inbound := &model.Inbound{
		Id:       1,
		Protocol: model.VLESS,
		Tag:      "test",
		Port:     443,
		Remark:   "Reality",
		Settings: `{"clients":[{"id":"11111111-1111-1111-1111-111111111111","email":"u@test.com","enable":true,"subId":"s1"}]}`,
		StreamSettings: `{"network":"tcp","security":"reality",
			"realitySettings":{"dest":"www.cloudflare.com:443",
				"serverNames":["www.cloudflare.com"],"privateKey":"` + testRealityPrivate + `",
				"shortIds":["abcdef01"]}}`,
	}

	svc := &SubClashService{SubService: NewSubService("")}
	stream := svc.streamData(inbound.StreamSettings)
	reality, ok := stream["realitySettings"].(map[string]any)
	if !ok {
		t.Fatalf("streamData did not normalize realitySettings: %#v", stream)
	}
	if got, _ := reality["publicKey"].(string); got != testRealityPublic {
		t.Fatalf("normalized publicKey = %q, want %q", got, testRealityPublic)
	}

	proxy := map[string]any{}
	if !svc.applySecurity(proxy, "reality", stream) {
		t.Fatal("applySecurity returned false for a valid reality stream")
	}
	opts, ok := proxy["reality-opts"].(map[string]any)
	if !ok {
		t.Fatalf("reality-opts missing: %#v", proxy)
	}
	if got, _ := opts["public-key"].(string); got != testRealityPublic {
		t.Fatalf("reality-opts.public-key = %q, want %q", got, testRealityPublic)
	}
	// Guard the field that made mihomo reject the profile outright.
	encoded, _ := json.Marshal(proxy)
	if !strings.Contains(string(encoded), `"public-key":"`) {
		t.Fatalf("rendered proxy has no public-key: %s", encoded)
	}
}
