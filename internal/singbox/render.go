package singbox

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// InboundSettings is the JSON stored in an anytls/hysteria2sb inbound's
// Settings field (structural half only; clients live in the shared clients
// tables exactly as for Xray protocols).
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
}

// ClashAPIAddr is the loopback clash-api controller (kept for UI/preview use).
const ClashAPIAddr = "127.0.0.1:19090"

// V2RayAPIAddr is the loopback gRPC stats service — the same cumulative
// per-user counter mechanism Xray-core exposes. Requires a sing-box binary
// built with the `with_v2ray_api` tag (the fork ships one).
const V2RayAPIAddr = "127.0.0.1:19091"

// RenderInput is one sing-box inbound with its enabled clients.
type RenderInput struct {
	Inbound model.Inbound
	Clients []model.Client
}

// RenderConfig builds the sing-box JSON for the given local sing-box
// inbounds. Callers pass clients already filtered to enabled ones
// (quota/expiry semantics identical to the Xray path).
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
		var s InboundSettings
		if in.Settings != "" {
			if err := json.Unmarshal([]byte(in.Settings), &s); err != nil {
				return nil, fmt.Errorf("inbound %s: bad settings json: %w", in.Tag, err)
			}
		}

		users := make([]SBUser, 0)
		for _, c := range input.Clients {
			// Credential field follows the 3x-ui convention: AnyTLS is a
			// Trojan-style password protocol (Password), Hysteria2 is the
			// Hysteria family (Auth). One client row therefore carries a
			// distinct credential per sing-box protocol, like a client
			// attached to both a VLESS and a Hysteria inbound.
			pw := singboxClientCredential(in.Protocol, c)
			if pw == "" || !c.Enable {
				continue
			}
			users = append(users, SBUser{Password: pw, Name: c.Email})
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

// singboxClientCredential returns the credential a sing-box protocol reads
// from a shared client row. Convention (mirrors upstream's field-per-protocol
// habits): AnyTLS → Password, Hysteria2 → Auth. Auth falls back to Password so
// a client created through the panel's generic form (which fills Password)
// still works on Hysteria2 without a manual edit.
func singboxClientCredential(p model.Protocol, c model.Client) string {
	if p == model.Hysteria2SB {
		if c.Auth != "" {
			return c.Auth
		}
		return c.Password
	}
	if c.Password != "" {
		return c.Password
	}
	return c.Auth
}

// inboundTags returns the enabled sing-box inbound tags (stats.inbounds).
func inboundTags(inputs []RenderInput) []string {
	tags := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if in.Inbound.Enable && in.Inbound.Protocol.IsSingbox() && in.Inbound.NodeID == nil && len(in.Clients) > 0 {
			tags = append(tags, in.Inbound.Tag)
		}
	}
	return tags
}

// userEmails returns every enabled client email across sing-box inbounds
// (stats.users — per-user cumulative counters, the Xray-equivalent).
func userEmails(inputs []RenderInput) []string {
	seen := make(map[string]bool)
	emails := make([]string, 0)
	for _, in := range inputs {
		for _, c := range in.Clients {
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