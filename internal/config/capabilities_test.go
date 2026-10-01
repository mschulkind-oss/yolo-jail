package config

// capabilities_test.go pins the capability census's rows: what satisfies a
// `required_capabilities` entry. The two callers' call sites are pinned where they are — the
// launch's gate in internal/cli/run's preflight_test.go, through Run(), and `yolo check`'s
// prediction in internal/cli/check's capabilities_test.go, through its section.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestUnmetCapabilitiesCensusOfTheConfig is the census of the user's own declarations, with
// no pack selected.
func TestUnmetCapabilitiesCensusOfTheConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want []string
	}{
		{"nothing declared", `{}`, nil},
		{"a requirement nothing satisfies", `{"required_capabilities": ["web_search"]}`,
			[]string{"web_search"}},
		{"satisfied by an mcp server's provides", `{
			"required_capabilities": ["web_search"],
			"mcp_servers": {"tavily": {"command": "npx", "provides": "web_search"}}}`, nil},
		{"satisfied by a provider's capabilities", `{
			"required_capabilities": ["web_search"],
			"providers": {"zai": {"capabilities": ["web_search"]}}}`, nil},
		{"the baseline is always met", `{
			"required_capabilities": ["code_editing", "command_execution"]}`, nil},
		// The null cases are the whole reason the census reads merged VALUES rather than
		// key sets: a null removes the entry instead of running it, so it satisfies
		// nothing — otherwise a workspace `"tavily": null` would keep satisfying the
		// capability off the user-level entry it just deleted.
		{"a null-removed server satisfies nothing", `{
			"required_capabilities": ["web_search"],
			"mcp_servers": {"tavily": null}}`, []string{"web_search"}},
		{"a null-removed provider satisfies nothing", `{
			"required_capabilities": ["web_search"],
			"providers": {"zai": null}}`, []string{"web_search"}},
		{"declaration order and de-duplication", `{
			"required_capabilities": ["b", "a", "b"]}`, []string{"b", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UnmetCapabilities(decode(t, tc.cfg), nil)
			if err != nil {
				t.Fatalf("UnmetCapabilities: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("UnmetCapabilities = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUnmetCapabilitiesCountsTheSelectedPacks is the census's pack half (agent-auth-modes.md
// §6.1 clause 1), over selections resolved the way validation resolves them.
func TestUnmetCapabilitiesCountsTheSelectedPacks(t *testing.T) {
	cases := []struct {
		name  string
		packs string
		cfg   string
		want  []string
	}{
		// claude's pack declares web_search for claude's built-in login; agy's for agy's.
		{"an agent's built-in login", `["agy"]`,
			`{"required_capabilities": ["web_search"]}`, nil},
		{"claude, the roadmap's case", `["claude"]`,
			`{"required_capabilities": ["web_search"]}`, nil},
		// zai installs no agent; the provider it ships declares web_search.
		{"a pack-shipped provider", `["zai"]`,
			`{"required_capabilities": ["web_search"]}`, nil},
		// The provider half is the COMPOSITION, so the user's layer applies to it: a null
		// removes the pack's provider, and a list replaces the pack's list.
		{"a user null over a pack's provider", `["zai"]`,
			`{"required_capabilities": ["web_search"], "providers": {"zai": null}}`,
			[]string{"web_search"}},
		{"a user list over a pack's provider", `["zai"]`,
			`{"required_capabilities": ["web_search"], "providers": {"zai": {"capabilities": []}}}`,
			[]string{"web_search"}},
		// Only the SELECTED packs count: copilot declares nothing and needs nothing.
		{"a selection that declares nothing", `["copilot"]`,
			`{"required_capabilities": ["web_search"]}`, []string{"web_search"}},
		{"a name no pack declares", `["claude", "agy", "zai"]`,
			`{"required_capabilities": ["image_generation", "web_search"]}`,
			[]string{"image_generation"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selectionHome(t, tc.packs)
			got, err := UnmetCapabilities(decode(t, tc.cfg), UserScopeSelectedPacks)
			if err != nil {
				t.Fatalf("UnmetCapabilities: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("UnmetCapabilities = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUnmetCapabilitiesResolvesNoPackItDoesNotNeed: a requirement the config satisfies itself
// costs no pack resolution, so a launch with nothing unmet does no work for this census.
func TestUnmetCapabilitiesResolvesNoPackItDoesNotNeed(t *testing.T) {
	called := false
	selected := func() ([]*packload.Pack, bool) {
		called = true
		return nil, true
	}
	for _, cfg := range []string{`{}`, `{"required_capabilities": ["code_editing"]}`,
		`{"required_capabilities": ["web_search"], "providers": {"x": {"capabilities": ["web_search"]}}}`} {
		if _, err := UnmetCapabilities(decode(t, cfg), selected); err != nil {
			t.Fatalf("UnmetCapabilities(%s): %v", cfg, err)
		}
	}
	if called {
		t.Error("the pack selection was resolved for a config that left nothing unmet")
	}
}

// TestUnmetCapabilitiesSaysWhenItCouldNotLook: an unread pack may be the satisfier, so an
// incomplete selection that leaves a name unmet is an error beside the names, never a bare
// verdict. A complete answer needs no error, even from an incomplete selection.
func TestUnmetCapabilitiesSaysWhenItCouldNotLook(t *testing.T) {
	cfg := decode(t, `{"required_capabilities": ["web_search"]}`)
	unread := func() ([]*packload.Pack, bool) { return nil, false }
	got, err := UnmetCapabilities(cfg, unread)
	if err == nil || strings.Join(got, ",") != "web_search" {
		t.Errorf("an incomplete selection leaving web_search unmet = (%v, %v), want "+
			"([web_search], an error)", got, err)
	}

	selectionHome(t, `["claude"]`)
	claude, _ := UserScopeSelectedPacks()
	partial := func() ([]*packload.Pack, bool) { return claude, false }
	if got, err := UnmetCapabilities(cfg, partial); err != nil || len(got) != 0 {
		t.Errorf("a pack that did resolve satisfies the name, so what did not resolve cannot "+
			"matter: got (%v, %v)", got, err)
	}
}

// TestCapabilitySatisfiersReportsAProviderTableThatDidNotCompose: a composition that fails is
// the launch's own refusal, below the census; the census says so instead of answering as if
// the pack's provider were absent, and still counts every other source.
func TestCapabilitySatisfiersReportsAProviderTableThatDidNotCompose(t *testing.T) {
	zai, err := embeddedPackNamed("zai")
	if err != nil {
		t.Fatal(err)
	}
	agy, err := embeddedPackNamed("agy")
	if err != nil {
		t.Fatal(err)
	}
	// The base_url shorthand beside the pack's endpoints is the conflict ComposeProviders
	// refuses. Config validation refuses the shorthand first, so this is reachable only
	// directly — the census must still not misreport it.
	cfg := decode(t, `{"providers": {"zai": {"base_url": "https://example.invalid/v1"}}}`)
	have, err := CapabilitySatisfiers(cfg, []*packload.Pack{zai, agy})
	if err == nil || !strings.Contains(err.Error(), "the provider table did not compose") {
		t.Fatalf("CapabilitySatisfiers error = %v, want the composition failure", err)
	}
	if have["web_search"] == "" {
		t.Errorf("agy's built-in login still declares web_search; a failed provider composition "+
			"must not cost the other sources: %v", have)
	}
}
