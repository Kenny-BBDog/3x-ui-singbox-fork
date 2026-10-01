package singbox

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// InboundSettings is the JSON stored in an anytls/hysteria2sb inbound's
// Settings field.
//
// Credentials live in Clients here — NOT in the shared clients table — because
// one customer legitimately holds a different sing-box password per machine
// (DMIT-AnyTLS vs LA-AnyTLS). The shared row carries identity, quota and
// expiry; the inbound row carries the credential for the machine it runs on.
// Node sync already ships this JSON verbatim, so a master-managed inbound
// arrives at its node with the correct per-machine passwords intact.
type InboundSettings struct {
	ServerName      string   `json:"server_name"`
	ALPN            []string `json:"alpn"`
	CertificatePath string   `json:"certificate_path"`
	KeyPath         string   `json:"key_path"`
	Insecure        bool     `json:"insecure"`
	// hysteria2
	UpMps        int    `json:"up_mps"`
	DownMps      int    `json:"down_mps"`
	ObfsType     string `json:"obfs_type"`
	ObfsPassword string `json:"obfs_password"`
	// clients: [{email, password|auth, enable, ...}]
	Clients []InboundClient `json:"clients"`
}

// InboundClient is one credential entry inside an inbound's settings.
type InboundClient struct {
	Email    string `json:"email"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
	Enable   *bool  `json:"enable,omitempty"`
}

// Credential resolves the sing-box password for this entry, accepting either
// field so a master row written by either the panel form (password) or the
// hysteria-style field (auth) works.
func (c InboundClient) Credential() string {
	if strings.TrimSpace(c.Password) != "" {
		return c.Password
	}
	return c.Auth
}

// ClashAPIAddr is the loopback clash-api controller (kept for UI/preview use).
const ClashAPIAddr = "127.0.0.1:19090"

// V2RayAPIAddr is the loopback gRPC stats service — the same cumulative
// per-user counter mechanism Xray-core exposes. Requires a sing-box binary
// built with the `with_v2ray_api` tag (the fork ships one).
const V2RayAPIAddr = "127.0.0.1:19091"

// RenderInput is one sing-box inbound with the enable state of its clients.
// Enabled comes from client_traffics (shared quota/expiry enforcement);
// the credential comes from the inbound's own settings.
type RenderInput struct {
	Inbound model.Inbound
	// Enabled maps client email -> whether quota/expiry still allows it.
	// A missing email is treated as enabled.
	Enabled map[string]bool
}

// ParseInboundClients extracts the credential entries from an inbound's
// settings JSON.
func ParseInboundClients(settings string) ([]InboundClient, InboundSettings, error) {
	var s InboundSettings
	if strings.TrimSpace(settings) != "" {
		if err := json.Unmarshal([]byte(settings), &s); err != nil {
			return nil, s, err
		}
	}
	return s.Clients, s, nil
}

// RenderConfig builds the sing-box JSON for the given local sing-box inbounds.
func RenderConfig(inputs []RenderInput, clashSecret string) ([]byte, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	cfg := SBConfig{
		Log: &SBLog{Level: "info", Timestamp: true},
		Experimental: &SBExperimental{
			ClashAPI: &SBClashAPI{
				ExternalController: ClashAPIAddr,
				Secret:             clashSecret,
			},
			V2RayAPI: &SBV2RayAPI{
				Listen: V2RayAPIAddr,
				Stats: &SBV2RayStats{
					Enabled:  true,
					Inbounds: inboundTags(inputs),
					Users:    userEmails(inputs),
				},
			},
		},
		Outbounds: []SBOutbound{{Type: "direct", Tag: "direct"}},
	}

	for _, input := range inputs {
		in := input.Inbound
		if !in.Enable || !in.Protocol.IsSingbox() || in.NodeID != nil {
			continue
		}
		entryClients, s, err := ParseInboundClients(in.Settings)
		if err != nil {
			return nil, fmt.Errorf("inbound %s: bad settings json: %w", in.Tag, err)
		}

		users := make([]SBUser, 0, len(entryClients))
		for _, ec := range entryClients {
			if ec.Email == "" {
				continue
			}
			// disabled by its own flag, or by quota/expiry enforcement
			if ec.Enable != nil && !*ec.Enable {
				continue
			}
			if enabled, known := input.Enabled[ec.Email]; known && !enabled {
				continue
			}
			pw := ec.Credential()
			if pw == "" {
				continue
			}
			users = append(users, SBUser{Password: pw, Name: ec.Email})
		}
		if len(users) == 0 {
			// sing-box rejects empty user arrays for these protocols;
			// skip instead of poisoning the whole config.
			continue
		}

		sbi := SBInbound{
			Type:       sbType(in.Protocol),
			Tag:        in.Tag,
			Listen:     in.Listen,
			ListenPort: in.Port,
			Users:      users,
		}
		if sbi.Listen == "" {
			sbi.Listen = "::"
		}
		alpn := s.ALPN
		if len(alpn) == 0 {
			if in.Protocol == model.Hysteria2SB {
				alpn = []string{"h3"}
			} else {
				alpn = []string{"h2", "http/1.1"}
			}
		}
		sbi.TLS = &SBTLS{
			Enabled:         true,
			ServerName:      s.ServerName,
			ALPN:            alpn,
			CertificatePath: s.CertificatePath,
			KeyPath:         s.KeyPath,
			Insecure:        s.Insecure,
		}
		if in.Protocol == model.Hysteria2SB {
			if s.UpMps > 0 {
				v := s.UpMps
				sbi.UpMps = &v
			}
			if s.DownMps > 0 {
				v := s.DownMps
				sbi.DownMps = &v
			}
			if strings.TrimSpace(s.ObfsType) != "" {
				sbi.Obfs = &SBObfs{Type: s.ObfsType, Password: s.ObfsPassword}
			}
		}
		cfg.Inbounds = append(cfg.Inbounds, sbi)
	}

	if len(cfg.Inbounds) == 0 {
		return nil, nil
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func sbType(p model.Protocol) string {
	if p == model.Hysteria2SB {
		return "hysteria2"
	}
	return "anytls"
}

// inboundTags returns the enabled sing-box inbound tags (stats.inbounds).
func inboundTags(inputs []RenderInput) []string {
	tags := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if in.Inbound.Enable && in.Inbound.Protocol.IsSingbox() && in.Inbound.NodeID == nil {
			clients, _, err := ParseInboundClients(in.Inbound.Settings)
			if err == nil && len(clients) > 0 {
				tags = append(tags, in.Inbound.Tag)
			}
		}
	}
	return tags
}

// userEmails returns every client email across sing-box inbounds
// (stats.users — per-user cumulative counters, the Xray-equivalent).
func userEmails(inputs []RenderInput) []string {
	seen := make(map[string]bool)
	emails := make([]string, 0)
	for _, in := range inputs {
		clients, _, err := ParseInboundClients(in.Inbound.Settings)
		if err != nil {
			continue
		}
		for _, c := range clients {
			if c.Email != "" && !seen[c.Email] {
				seen[c.Email] = true
				emails = append(emails, c.Email)
			}
		}
	}
	return emails
}

// ClashConnections is the clash-api /connections response shape the stats
// job polls: per-connection user attribution and byte counters.
type ClashConnections struct {
	Connections []struct {
		ID       string `json:"id"`
		Upload   int64  `json:"upload"`
		Download int64  `json:"download"`
		User     string `json:"user,omitempty"`
	} `json:"connections"`
	UploadTotal   int64 `json:"uploadTotal"`
	DownloadTotal int64 `json:"downloadTotal"`
}