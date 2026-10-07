package sub

import (
	"fmt"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Membership of a residential group comes from the chained egress an inbound
// declares, never from its name. The 2026-10-07 outage was a rename that silently
// emptied that group because membership was a pattern over names; the rename case
// below is the regression that must never come back.
//
// The panel only ever fills groups that opted in by role. A group carrying a real
// `filter` is left untouched and stays the client's to resolve, which is asserted
// here too so the two mechanisms cannot be confused for one another.
const roleGroupTemplate = `
proxy-groups:
- name: 🏠 住宅出口
  type: select
  include-all: true
  filter: by-role:residential
  proxies:
  - ♻️ 自动选择（住宅）
- name: ♻️ 自动选择（住宅）
  type: url-test
  include-all: true
  filter: by-role:residential
  url: http://www.gstatic.com/generate_204
- name: 🚀 普通线路
  type: select
  include-all: true
  filter: ^🇺🇸 洛杉矶
rules:
- MATCH,🏠 住宅出口
`

// seedRoleInbound inserts a VLESS inbound and its client. When residential is set
// the inbound declares a chained egress, which is the only thing that makes it one.
func seedRoleInbound(t *testing.T, subId, remark, tag string, port int, residential bool) *model.Inbound {
	t.Helper()
	email := tag + "@e"
	uuid := "11111111-2222-4333-8444-" + fmt.Sprintf("%012d", port)
	egress := ""
	if residential {
		egress = fmt.Sprintf(`,"sbOutbound":{"tag":"resout-%s","type":"socks","server":"isp.example","server_port":10001}`, tag)
	}
	settings := fmt.Sprintf(
		`{"clients":[{"id":%q,"email":%q,"subId":%q,"enable":true}],"decryption":"none"%s}`,
		uuid, email, subId, egress)
	ib := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Listen: "203.0.113.5", Port: port,
		Protocol: model.VLESS, Remark: remark, Settings: settings,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	db := database.GetDB()
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound %s: %v", tag, err)
	}
	client := &model.ClientRecord{Email: email, SubID: subId, UUID: uuid, Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client %s: %v", email, err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatalf("seed client_inbound %s: %v", email, err)
	}
	return ib
}

type clashGroup struct {
	members    []string
	filter     string
	hasFilter  bool
	includeAll bool
}

func clashGroups(t *testing.T, out string) map[string]clashGroup {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parse rendered clash yaml: %v", err)
	}
	rawGroups, _ := doc["proxy-groups"].([]any)
	groups := make(map[string]clashGroup, len(rawGroups))
	for _, raw := range rawGroups {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := group["name"].(string)
		refs, _ := group["proxies"].([]any)
		members := make([]string, 0, len(refs))
		for _, ref := range refs {
			if text, ok := ref.(string); ok {
				members = append(members, text)
			}
		}
		filter, hasFilter := group["filter"].(string)
		includeAll, _ := group["include-all"].(bool)
		groups[name] = clashGroup{members: members, filter: filter, hasFilter: hasFilter, includeAll: includeAll}
	}
	return groups
}

func renderRoleGroups(t *testing.T, sub *SubService) string {
	t.Helper()
	clash := NewSubClashService(true, roleGroupTemplate, sub)
	out, _, err := clash.GetClash("sub-role", "sub.example.com")
	if err != nil {
		t.Fatalf("GetClash: %v", err)
	}
	return out
}

// Proxy names carry an internal disambiguator, so membership is asserted by prefix.
func hasMember(group clashGroup, remark string) bool {
	for _, member := range group.members {
		if strings.HasPrefix(member, remark) {
			return true
		}
	}
	return false
}

// The regression: renaming a residential line must not change its group membership.
func TestRoleGroup_ResidentialMembershipSurvivesRename(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()

	const original = "🇺🇸 普林斯顿 NJ 1 x1.5"
	ib := seedRoleInbound(t, "sub-role", original, "res1", 30001, true)
	seedRoleInbound(t, "sub-role", "🇺🇸 洛杉矶·TLS｜T1｜x1.5", "line1", 8445, false)

	before := clashGroups(t, renderRoleGroups(t, NewSubService("")))
	for _, group := range []string{"🏠 住宅出口", "♻️ 自动选择（住宅）"} {
		if !hasMember(before[group], original) {
			t.Fatalf("%s missing the residential line; members = %v", group, before[group].members)
		}
		if before[group].hasFilter || before[group].includeAll {
			t.Fatalf("%s kept a filter/include-all after being filled by role: %+v", group, before[group])
		}
	}
	if hasMember(before["🏠 住宅出口"], "🇺🇸 洛杉矶·TLS｜T1｜x1.5") {
		t.Fatalf("direct line leaked into the residential group: %v", before["🏠 住宅出口"].members)
	}

	// Rename it to something no pattern over names would match. This is the change
	// that emptied the group in production.
	const renamed = "🇺🇸 住宅｜普林斯顿｜1.5×"
	if err := db.Model(ib).Update("remark", renamed).Error; err != nil {
		t.Fatalf("rename: %v", err)
	}

	after := clashGroups(t, renderRoleGroups(t, NewSubService("")))
	for _, group := range []string{"🏠 住宅出口", "♻️ 自动选择（住宅）"} {
		if !hasMember(after[group], renamed) {
			t.Fatalf("a rename emptied %s; members = %v", group, after[group].members)
		}
	}
	if hasMember(after["🏠 住宅出口"], original) {
		t.Fatalf("%s still lists the old name %q after the rename", "🏠 住宅出口", original)
	}
}

// A group with a real filter is the template's business, not the panel's: it must
// survive untouched, or the two membership mechanisms would silently merge.
func TestRoleGroup_FilterGroupsAreLeftAlone(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 普林斯顿 NJ 1 x1.5", "res1", 30001, true)
	seedRoleInbound(t, "sub-role", "🇺🇸 洛杉矶·TLS｜T1｜x1.5", "line1", 8445, false)

	line := clashGroups(t, renderRoleGroups(t, NewSubService("")))["🚀 普通线路"]
	if !line.hasFilter || line.filter != "^🇺🇸 洛杉矶" {
		t.Fatalf("the line group's filter was rewritten: %+v", line)
	}
	if !line.includeAll {
		t.Fatalf("the line group's include-all was dropped: %+v", line)
	}
	if len(line.members) != 0 {
		t.Fatalf("the panel filled a filter group; members = %v", line.members)
	}
}

// The internal role marker is bookkeeping; mihomo rejects the whole config if it
// reaches the YAML.
func TestRoleGroup_MarkerNeverReachesTheYAML(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 普林斯顿 NJ 1 x1.5", "res1", 30001, true)

	out := renderRoleGroups(t, NewSubService(""))
	if strings.Contains(out, residentialProxyMarker) {
		t.Fatalf("internal marker %q leaked into the emitted config:\n%s", residentialProxyMarker, out)
	}
}

// Nothing residential means the role group fails empty rather than wrong.
func TestRoleGroup_NoResidentialInboundsYieldsNoMembers(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 洛杉矶·TLS｜T1｜x1.5", "line1", 8445, false)

	members := clashGroups(t, renderRoleGroups(t, NewSubService("")))["♻️ 自动选择（住宅）"]
	if len(members.members) != 0 {
		t.Fatalf("♻️ 自动选择（住宅） = %v, want no members when nothing is residential", members.members)
	}
	if members.hasFilter {
		t.Fatalf("the role keyword was left in place: %+v", members)
	}
}
