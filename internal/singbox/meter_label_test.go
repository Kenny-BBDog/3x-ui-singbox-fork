package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// renderOneClient renders one client "cust" on two anytls inbounds and returns
// the parsed config, so a test can inspect labels and credentials (spec 0002).
func renderTwoInbounds(t *testing.T, tagA, tagB string) (labels []string, passwords map[string]string, statsUsers []string) {
	t.Helper()
	mk := func(tag string) model.Inbound {
		return model.Inbound{
			Tag: tag, Enable: true, Port: 8445, Protocol: model.AnyTLS,
			Settings: `{"certificate_path":"/cert.pem","key_path":"/key.pem",` +
				`"clients":[{"email":"cust","password":"one-shared-password","enable":true}]}`,
		}
	}
	a, b := mk(tagA), mk(tagB)
	inputs := []RenderInput{
		{Inbound: a, Enabled: map[string]bool{"cust": true}},
		{Inbound: b, Enabled: map[string]bool{"cust": true}},
	}
	raw, err := RenderConfig(inputs, "secret")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag   string `json:"tag"`
			Users []struct {
				Name     string `json:"name"`
				Password string `json:"password"`
			} `json:"users"`
		} `json:"inbounds"`
		Experimental struct {
			V2RayAPI struct {
				Stats struct {
					Users []string `json:"users"`
				} `json:"stats"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	passwords = map[string]string{}
	for _, inb := range cfg.Inbounds {
		for _, u := range inb.Users {
			labels = append(labels, u.Name)
			passwords[u.Name] = u.Password
		}
	}
	statsUsers = cfg.Experimental.V2RayAPI.Stats.Users
	return labels, passwords, statsUsers
}

// The meter label must be unique per (inbound, client). A label shared between
// inbounds is what makes traffic unattributable to a route, which is the whole
// reason the multiplier cannot be applied (spec 0002, Finding 1).
func TestMeterLabelIsPerInbound(t *testing.T) {
	labels, _, statsUsers := renderTwoInbounds(t, "inbound-main-anytls", "res-30001")

	if len(labels) != 2 {
		t.Fatalf("want 2 user entries (one per inbound), got %d: %v", len(labels), labels)
	}
	if labels[0] == labels[1] {
		t.Fatalf("labels collide across inbounds: %q", labels[0])
	}
	for _, want := range []string{"inbound-main-anytls|cust", "res-30001|cust"} {
		if !contains(labels, want) {
			t.Errorf("missing label %q in %v", want, labels)
		}
	}
	// stats.users must list every label, or its counter is never enabled.
	if len(statsUsers) != 2 {
		t.Fatalf("stats.users should list both labels, got %v", statsUsers)
	}
	for _, l := range labels {
		if !contains(statsUsers, l) {
			t.Errorf("label %q is rendered but absent from stats.users %v", l, statsUsers)
		}
	}
}

// The label is a metering identity only. The credential must be unchanged, and
// the same on every inbound, so a client's subscription and login are not
// affected by this change (spec 0002, Finding 2).
func TestMeterLabelDoesNotChangeCredential(t *testing.T) {
	labels, passwords, _ := renderTwoInbounds(t, "inbound-main-anytls", "res-30001")

	for _, l := range labels {
		if pw := passwords[l]; pw != "one-shared-password" {
			t.Errorf("label %q has password %q, want the client's own password", l, pw)
		}
	}
	// The password must be identical across inbounds: one credential, many routes.
	seen := map[string]bool{}
	for _, pw := range passwords {
		seen[pw] = true
	}
	if len(seen) != 1 {
		t.Fatalf("a client must keep one password on every inbound, got %v", passwords)
	}
}

// The label must never contain the counter name's own separator, or the counter
// name cannot be constructed.
func TestMeterLabelNeverContainsCounterSeparator(t *testing.T) {
	label := MeterLabel("res-30001", "cust@example.com")
	if strings.Contains(label, ">>>") {
		t.Fatalf("label %q contains the counter separator", label)
	}
}

// SplitMeterLabel must not depend on the email's shape: tags are ours and never
// contain the separator, while an email legitimately might, so the FIRST
// separator is the boundary.
func TestSplitMeterLabel(t *testing.T) {
	cases := []struct {
		label    string
		wantTag  string
		wantMail string
		wantOK   bool
	}{
		{"res-30001|cust", "res-30001", "cust", true},
		{"res-30001|cust@example.com", "res-30001", "cust@example.com", true},
		// An email containing the separator must still split at the tag boundary.
		{"res-30001|weird|mail", "res-30001", "weird|mail", true},
		{"no-separator", "", "", false},
	}
	for _, c := range cases {
		tag, mail, ok := SplitMeterLabel(c.label)
		if tag != c.wantTag || mail != c.wantMail || ok != c.wantOK {
			t.Errorf("SplitMeterLabel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.label, tag, mail, ok, c.wantTag, c.wantMail, c.wantOK)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
