package sub

import (
	"fmt"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Group membership comes from the role an inbound declares, never from its name.
// The 2026-10-07 outage was a rename that silently emptied the residential group
// because membership was a pattern over names; the rename case below is the
// regression that must never come back.
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
  filter: by-role:direct
  proxies:
  - ♻️ 自动选择（线路）
- name: ♻️ 自动选择（线路）
  type: url-test
  include-all: true
  filter: by-role:direct
  url: http://www.gstatic.com/generate_204
- name: 🧪 手工过滤
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

const (
	residentialGroups = "🏠 住宅出口"
	residentialURL    = "♻️ 自动选择（住宅）"
	directGroups      = "🚀 普通线路"
	directURL         = "♻️ 自动选择（线路）"
	manualGroup       = "🧪 手工过滤"
)

func filledByRole(g clashGroup) bool { return !g.hasFilter && !g.includeAll }

// The regression: renaming a line must not change its group membership, in either
// role. This is precisely what emptied the residential group in production.
func TestRoleGroup_MembershipSurvivesRename(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()

	const originalResidential = "🇺🇸 普林斯顿 NJ 1 x1.5"
	const originalDirect = "🇺🇸 洛杉矶·TLS｜T1｜x1.5"
	ibRes := seedRoleInbound(t, "sub-role", originalResidential, "res1", 30001, true)
	ibDir := seedRoleInbound(t, "sub-role", originalDirect, "line1", 8445, false)

	before := clashGroups(t, renderRoleGroups(t, NewSubService("")))
	for _, group := range []string{residentialGroups, residentialURL} {
		if !hasMember(before[group], originalResidential) {
			t.Fatalf("%s missing the residential line; members = %v", group, before[group].members)
		}
		if !filledByRole(before[group]) {
			t.Fatalf("%s kept a filter/include-all after being filled by role: %+v", group, before[group])
		}
	}
	for _, group := range []string{directGroups, directURL} {
		if !hasMember(before[group], originalDirect) {
			t.Fatalf("%s missing the direct line; members = %v", group, before[group].members)
		}
	}
	if hasMember(before[residentialGroups], originalDirect) {
		t.Fatalf("direct line leaked into the residential group: %v", before[residentialGroups].members)
	}
	if hasMember(before[directGroups], originalResidential) {
		t.Fatalf("residential line leaked into the direct group: %v", before[directGroups].members)
	}

	// Rename both to something no pattern over names would match.
	const renamedResidential = "🇺🇸 住宅｜普林斯顿｜1.5×"
	const renamedDirect = "🇺🇸 洛杉矶·ANYTLS｜T1｜1.5×"
	if err := db.Model(ibRes).Update("remark", renamedResidential).Error; err != nil {
		t.Fatalf("rename residential: %v", err)
	}
	if err := db.Model(ibDir).Update("remark", renamedDirect).Error; err != nil {
		t.Fatalf("rename direct: %v", err)
	}

	after := clashGroups(t, renderRoleGroups(t, NewSubService("")))
	for _, group := range []string{residentialGroups, residentialURL} {
		if !hasMember(after[group], renamedResidential) {
			t.Fatalf("a rename emptied %s; members = %v", group, after[group].members)
		}
		if hasMember(after[group], originalResidential) {
			t.Fatalf("%s still lists the old name %q after the rename", group, originalResidential)
		}
	}
	for _, group := range []string{directGroups, directURL} {
		if !hasMember(after[group], renamedDirect) {
			t.Fatalf("a rename emptied %s; members = %v", group, after[group].members)
		}
		if hasMember(after[group], originalDirect) {
			t.Fatalf("%s still lists the old name %q after the rename", group, originalDirect)
		}
	}
}

// A group with a real filter is the template's business, not the panel's: it must
// survive untouched, or the two membership mechanisms would silently merge.
func TestRoleGroup_RealFilterIsLeftAlone(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 普林斯顿 NJ 1 x1.5", "res1", 30001, true)
	seedRoleInbound(t, "sub-role", "🇺🇸 洛杉矶·TLS｜T1｜x1.5", "line1", 8445, false)

	manual := clashGroups(t, renderRoleGroups(t, NewSubService("")))[manualGroup]
	if !manual.hasFilter || manual.filter != "^🇺🇸 洛杉矶" {
		t.Fatalf("a real filter was rewritten: %+v", manual)
	}
	if !manual.includeAll {
		t.Fatalf("include-all was dropped from a filter group: %+v", manual)
	}
	if len(manual.members) != 0 {
		t.Fatalf("the panel filled a real-filter group; members = %v", manual.members)
	}
}

// An unrecognised role leaves the keyword in the config rather than silently
// emptying it, so a bad keyword is diagnosable from the emitted YAML.
func TestRoleGroup_UnknownRoleIsLeftInPlace(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 普林斯顿 NJ 1 x1.5", "res1", 30001, true)

	clash := NewSubClashService(true, strings.Replace(roleGroupTemplate, "by-role:residential", "by-role:legacy", 1), NewSubService(""))
	out, _, err := clash.GetClash("sub-role", "sub.example.com")
	if err != nil {
		t.Fatalf("GetClash: %v", err)
	}
	if !strings.Contains(out, "by-role:legacy") {
		t.Fatalf("an unrecognised role keyword was not left in place for diagnosis:\n%s", out)
	}
}

// The internal role marker is bookkeeping; mihomo rejects the whole config if it
// reaches the YAML.
func TestRoleGroup_MarkerNeverReachesTheYAML(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 普林斯顿 NJ 1 x1.5", "res1", 30001, true)
	seedRoleInbound(t, "sub-role", "🇺🇸 洛杉矶·TLS｜T1｜x1.5", "line1", 8445, false)

	out := renderRoleGroups(t, NewSubService(""))
	if strings.Contains(out, proxyRoleMarker) {
		t.Fatalf("internal marker %q leaked into the emitted config:\n%s", proxyRoleMarker, out)
	}
}

// Nothing residential means the residential groups fail empty rather than wrong,
// and the direct groups must not pick it up.
func TestRoleGroup_NoResidentialYieldsNoResidentialMembers(t *testing.T) {
	initSubDB(t)
	seedRoleInbound(t, "sub-role", "🇺🇸 洛杉矶·TLS｜T1｜x1.5", "line1", 8445, false)

	groups := clashGroups(t, renderRoleGroups(t, NewSubService("")))
	if got := groups[residentialURL].members; len(got) != 0 {
		t.Fatalf("%s = %v, want no members when nothing is residential", residentialURL, got)
	}
	if groups[residentialURL].hasFilter {
		t.Fatalf("the residential role keyword was left in place: %+v", groups[residentialURL])
	}
	if !hasMember(groups[directURL], "🇺🇸 洛杉矶·TLS｜T1｜x1.5") {
		t.Fatalf("the direct group lost its member: %v", groups[directURL].members)
	}
}
