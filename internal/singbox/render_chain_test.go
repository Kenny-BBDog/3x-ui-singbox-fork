package singbox

import (
	"encoding/json"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A residential-pool anytls inbound with an sbOutbound must render the chained
// socks outbound plus a route rule pinning the inbound to it. Stats-only
// inbounds (no sbOutbound) must stay chain-free.
func TestRenderConfigResidentialChain(t *testing.T) {
	resi := &model.Inbound{
		Tag:      "res-30001",
		Enable:   true,
		Port:     30001,
		Protocol: model.AnyTLS,
		Settings: `{"sbOutbound":{"type":"socks","tag":"resout-30001","server":"isp.decodo.com","server_port":10001,"egressUser":"spc8vh23","egressPassword":"pw"},"alpn":["h2","http/1.1"],"certificate_path":"/cert.pem","key_path":"/key.pem","clients":[{"email":"cust","password":"atls-x","enable":true}]}`,
	}
	plain := &model.Inbound{
		Tag:      "inbound-dmit-anytls",
		Enable:   true,
		Port:     8445,
		Protocol: model.AnyTLS,
		Settings: `{"alpn":["h2","http/1.1"],"certificate_path":"/cert.pem","key_path":"/key.pem","clients":[{"email":"cust","password":"atls-x","enable":true}]}`,
	}

	inputs := []RenderInput{
		{Inbound: *resi, Enabled: map[string]bool{"cust": true}},
		{Inbound: *plain, Enabled: map[string]bool{"cust": true}},
	}
	raw, err := RenderConfig(inputs, "secret")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var cfg struct {
		Outbounds []struct {
			Type     string `json:"type"`
			Tag      string `json:"tag"`
			Server   string `json:"server"`
			Version  string `json:"version,omitempty"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"outbounds"`
		Route *struct {
			Rules []struct {
				Inbound  []string `json:"inbound"`
				Outbound string   `json:"outbound"`
			} `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	foundSocks := false
	for _, o := range cfg.Outbounds {
		if o.Tag == "resout-30001" {
			foundSocks = true
			if o.Type != "socks" || o.Server != "isp.decodo.com" {
				t.Fatalf("socks outbound malformed: %+v", o)
			}
			if o.Version != "5" {
				t.Fatalf("socks version must be 5, got %q", o.Version)
			}
			if o.Username != "spc8vh23" || o.Password == "" {
				t.Fatalf("socks user must be lifted to top-level username/password: %+v", o)
			}
		}
	}
	if !foundSocks {
		t.Fatal("chained socks outbound missing")
	}
	if cfg.Route == nil || len(cfg.Route.Rules) != 1 {
		t.Fatalf("route rules wrong: %+v", cfg.Route)
	}
	if cfg.Route.Rules[0].Inbound[0] != "res-30001" || cfg.Route.Rules[0].Outbound != "resout-30001" {
		t.Fatalf("route rule wrong: %+v", cfg.Route.Rules[0])
	}
}