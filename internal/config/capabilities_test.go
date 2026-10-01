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
// §6.1 clause 1, §6.2's "the active agent"), over selections resolved the way validation resolves
// them and profiles folded from the config's `profile` key, as `yolo check` hands them.
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
		// Any one agent's active source satisfies the launch: the gate cannot know which agent
		// a session will start.
		{"one agent of two", `["claude", "pi"]`,
			`{"required_capabilities": ["web_search"]}`, nil},
		// THE SHELF. pi's and codex's packs pull in openai-auth, whose openai-codex provider
		// declares web_search; nothing runs on it without a profile, so it satisfies nothing,
		// and §6.2 names pi and codex as lacking native search.
		{"a provider no profile selects: pi", `["pi"]`,
			`{"required_capabilities": ["web_search"]}`, []string{"web_search"}},
		{"a provider no profile selects: codex", `["codex"]`,
			`{"required_capabilities": ["web_search"]}`, []string{"web_search"}},
		{"a pack-shipped provider no agent runs on", `["zai"]`,
			`{"required_capabilities": ["web_search"]}`, []string{"web_search"}},
		// A profile makes the provider the agent's source, and replaces its built-in login.
		{"a profile selecting a provider that declares it", `["pi"]`,
			`{"required_capabilities": ["web_search"], "profile": "codex"}`, nil},
		{"a profile selecting another pack's provider", `["pi", "zai"]`,
			`{"required_capabilities": ["web_search"], "profile": "zai"}`, nil},
		{"a profile leaving a built-in login that declares it", `["claude"]`,
			`{"required_capabilities": ["web_search"], "profile": "bedrock"}`,
			[]string{"web_search"}},
		// Every entry of an active set counts, not only the primary a session starts on.
		{"a later entry of an active set", `["pi"]`,
			`{"required_capabilities": ["web_search"], "profile": ["bedrock", "codex"]}`, nil},
		// The provider is read from the COMPOSITION, so the user's layer applies to it: a list
		// replaces the pack's, and a null removes the provider the profile selects.
		{"a user list over the active provider", `["pi", "zai"]`,
			`{"required_capabilities": ["web_search"], "profile": "zai",
			  "providers": {"zai": {"capabilities": []}}}`, []string{"web_search"}},
		{"a user null over the active provider", `["pi", "zai"]`,
			`{"required_capabilities": ["web_search"], "profile": "zai",
			  "providers": {"zai": null}}`, []string{"web_search"}},
		// The user's own declaration counts whether or not a profile selects the provider:
		// the over-permission the gate shipped with, which this census keeps.
		{"a user's own declaration, unselected", `["pi"]`,
			`{"required_capabilities": ["web_search"],
			  "providers": {"zai": {"capabilities": ["web_search"]}}}`, nil},
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
			cfg := decode(t, tc.cfg)
			got, err := UnmetCapabilities(cfg, ConfigCapabilityLaunch(cfg))
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
	launch := &CapabilityLaunch{Packs: func() ([]*packload.Pack, bool) {
		called = true
		return nil, true
	}}
	for _, cfg := range []string{`{}`, `{"required_capabilities": ["code_editing"]}`,
		`{"required_capabilities": ["web_search"], "providers": {"x": {"capabilities": ["web_search"]}}}`} {
		if _, err := UnmetCapabilities(decode(t, cfg), launch); err != nil {
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
	unread := &CapabilityLaunch{Packs: func() ([]*packload.Pack, bool) { return nil, false }}
	got, err := UnmetCapabilities(cfg, unread)
	if err == nil || strings.Join(got, ",") != "web_search" {
		t.Errorf("an incomplete selection leaving web_search unmet = (%v, %v), want "+
			"([web_search], an error)", got, err)
	}

	selectionHome(t, `["claude"]`)
	claude, _ := UserScopeSelectedPacks()
	partial := &CapabilityLaunch{Packs: func() ([]*packload.Pack, bool) { return claude, false }}
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
	have, err := CapabilitySatisfiers(cfg, []*packload.Pack{zai, agy}, nil)
	if err == nil || !strings.Contains(err.Error(), "the provider table did not compose") {
		t.Fatalf("CapabilitySatisfiers error = %v, want the composition failure", err)
	}
	if have["web_search"] == "" {
		t.Errorf("agy's built-in login still declares web_search; a failed provider composition "+
			"must not cost the other sources: %v", have)
	}
}

// TestUnmetCapabilitiesSaysWhenTheProfilesDidNotResolve: which provider a profiled agent runs on
// is unknown when the profiles do not resolve — the launch's channel composition refuses that
// itself — so the census reports it beside the names instead of a verdict. An option the
// selected provider does not declare is the resolution's own refusal.
func TestUnmetCapabilitiesSaysWhenTheProfilesDidNotResolve(t *testing.T) {
	home := useProfileKeysHome(t)
	write(t, home+"/.config/yolo-jail/config.jsonc", `{
	  "packs": ["pi", "zai"],
	  "profiles": {"zai": {"provider": "zai", "no_such_option": "x"}}
	}`)
	cfg := decode(t, `{"required_capabilities": ["web_search"], "profile": "zai"}`)
	got, err := UnmetCapabilities(cfg, ConfigCapabilityLaunch(cfg))
	if err == nil || !strings.Contains(err.Error(), "the profiles did not resolve") ||
		strings.Join(got, ",") != "web_search" {
		t.Errorf("profiles that did not resolve = (%v, %v), want ([web_search], the "+
			"resolution's failure)", got, err)
	}
}
