package configmigrate

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// userBrokeredFixture is a user config whose brokered table is the shape the owner's holds: a read
// grant for both brokers and two git grant kinds.
const userBrokeredFixture = `{
  "packs": ["git", "github"],
  // what every workspace may do with the host's logins
  "brokered": {
    "git": {"grant_kinds": ["branch", "create-branch"], "grants": [{"kind": "read", "repo": "*"}]},
    "github": {"grants": [{"kind": "read", "repo": "*"}]}
  }
}
`

// brokerAnswer is what the launch's readers say the current user config grants source: whether
// every workspace reads its own scope, and the grant kinds a project may request, sorted.
type brokerAnswer struct {
	Read  bool
	Kinds []string
}

// launchBrokerAnswer reads source's grants the way the brokers do: the grant kinds through
// BrokeredGrantKindsUsingReadablePolicy (the git and GitHub brokers' own call), and the read
// grant through the legacy user grant reader or, for a readable config, the readable resolver
// over the shipped declarations.
func launchBrokerAnswer(t *testing.T, source string) brokerAnswer {
	t.Helper()
	kinds, canonical, err := config.BrokeredGrantKindsUsingReadablePolicy(source)
	if err != nil {
		t.Fatalf("%s grant kinds: %v", source, err)
	}
	sort.Strings(kinds)
	a := brokerAnswer{Kinds: kinds}
	if !canonical {
		a.Read = slices.Contains(config.BrokeredUserGrants(source), brokerscope.Grant{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo})
		return a
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	var packs []*packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "git" || p.Name == "github" {
			packs = append(packs, p)
		}
	}
	declarations, _, err := config.ReadableDeclarationsForPacks(packs)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: user, SelectedPacks: []string{"git", "github"}, Declarations: declarations})
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range plan.Sources {
		if policy.Source == source {
			a.Read = slices.Contains(policy.Always, policy.ReadCapability)
		}
	}
	return a
}

// TestUserBrokeredConvertsToAllow is the owner's user config: a user-scope brokered table
// converts to the ruled [allow.<source>] tables (config-user.md §6, appendix A.6), the plan
// proves equal, and after Apply the brokers read the same grants they read before.
func TestUserBrokeredConvertsToAllow(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), userBrokeredFixture)

	before := map[string]brokerAnswer{"git": launchBrokerAnswer(t, "git"), "github": launchBrokerAnswer(t, "github")}
	if want := (brokerAnswer{Read: true, Kinds: []string{"branch", "create-branch"}}); !reflect.DeepEqual(before["git"], want) {
		t.Fatalf("legacy fixture: git reads %+v, want %+v", before["git"], want)
	}

	g, err := Discover(Request{UserConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(p.NotConverted) != 0 || !p.Equal {
		t.Fatalf("user brokered not converted: %+v %+v", p.NotConverted, p.Differences)
	}
	if !strings.Contains(p.canonical.grants, `"create-branch"`) {
		t.Fatalf("the proof does not carry the grants the brokers resolve: %q", p.canonical.grants)
	}
	got := string(target(t, p, filepath.Join(dir, "config.jsonc")).Bytes)
	for _, want := range []string{
		"[allow.git]\nalways = [\"fetch\"]\non_request = [\"new_branches\", \"push\"]\n",
		"[allow.github]\nalways = [\"read\"]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("converted file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "brokered") {
		t.Errorf("converted file still says brokered:\n%s", got)
	}

	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	after := map[string]brokerAnswer{"git": launchBrokerAnswer(t, "git"), "github": launchBrokerAnswer(t, "github")}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("the converted config grants differently:\n  before %+v\n  after  %+v", before, after)
	}
}

// TestUserBrokeredGrantKindTheBrokerDoesNotMapStays: a user grant kind the declaring pack maps to
// no capability has no allow spelling, so the source stays unconverted and the reason names the
// kinds that do map.
func TestUserBrokeredGrantKindTheBrokerDoesNotMapStays(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["git"], "brokered": {"git": {"grant_kinds": ["read"], "grants": [{"kind": "read", "repo": "*"}]}}}`+"\n")
	_, err := Plan(discoverFor(t, Request{UserConfigDir: dir}))
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("want the plan refused, got %v", err)
	}
	found := false
	for _, nc := range ineq.Plan.NotConverted {
		if nc.Path == "brokered.git.grant_kinds" && strings.Contains(nc.Reason, `"read"`) && strings.Contains(nc.Reason, "create-branch → new_branches") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no reason names the unmapped kind: %+v", ineq.Plan.NotConverted)
	}
}

// TestUserBrokeredGrantOtherThanReadStays: the user config holds only the read grant on "*"; any
// other grant has no allow spelling, and the reason says where it belongs.
func TestUserBrokeredGrantOtherThanReadStays(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["git"], "brokered": {"git": {"grants": [{"kind": "branch", "repo": "github.com/acme/x", "branch": "main"}]}}}`+"\n")
	_, err := Plan(discoverFor(t, Request{UserConfigDir: dir}))
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("want the plan refused, got %v", err)
	}
	found := false
	for _, nc := range ineq.Plan.NotConverted {
		if nc.Path == "brokered.git.grants" && strings.Contains(nc.Reason, "yolo request") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no reason names the next step: %+v", ineq.Plan.NotConverted)
	}
}

// TestUserBrokeredForAnUnselectedPackStays: an [allow.<source>] table grants nothing unless the
// pack declaring the broker is in packs, so a grant for an unlisted pack is not converted.
func TestUserBrokeredForAnUnselectedPackStays(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"brokered": {"github": {"grants": [{"kind": "read", "repo": "*"}]}}}`+"\n")
	_, err := Plan(discoverFor(t, Request{UserConfigDir: dir}))
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("want the plan refused, got %v", err)
	}
	found := false
	for _, nc := range ineq.Plan.NotConverted {
		if nc.Path == "brokered.github" && strings.Contains(nc.Reason, "yolo pack add github") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no reason names the pack to add: %+v", ineq.Plan.NotConverted)
	}
}

// TestTheProofNamesABrokerGrantDifference pins the layer itself: a converted table that drops a
// grant kind is a difference in the grants the brokers resolve.
func TestTheProofNamesABrokerGrantDifference(t *testing.T) {
	env(t)
	declarations, err := shippedBrokers()
	if err != nil {
		t.Fatal(err)
	}
	old := mustJSON(t, `{"packs": ["git"], "brokered": {"git": {"grant_kinds": ["branch", "create-branch"], "grants": [{"kind": "read", "repo": "*"}]}}}`)
	converted := mustJSON(t, `{"packs": ["git"], "allow": {"git": {"always": ["fetch"], "on_request": ["push"]}}}`)
	diffs := diffValues(brokerGrantsLayer, brokerGrants(old, declarations), brokerGrants(converted, declarations))
	if len(diffs) != 1 || diffs[0].Path != "git.grant_kinds" {
		t.Fatalf("a dropped grant kind was not named: %+v", diffs)
	}
}

func mustJSON(t *testing.T, text string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("%s is not an object", text)
	}
	return m
}
