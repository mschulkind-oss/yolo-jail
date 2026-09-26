package entrypoint

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// capabilitymcp_test.go measures CAPABILITY-DRIVEN MCP DELIVERY — the rule that omits an
// MCP server when the active authentication source already performs the job that server
// declares with `provides` (docs/reference/mcp-configuration.md#capability-driven-mcp-delivery).
//
// EVERY TEST HERE DRIVES THE BOOT LOOP, ConfigurePackSurfaces, over the packs yolo
// actually ships and the wire tables the launcher actually composes
// (packload.ComposeProviders / ResolveProfiles / ProfilesWireTable). Nothing here calls
// the filter, the resolver, or a derive directly, and that is the whole point of the
// file: the thing being replaced was a per-agent branch in two derive.lua scripts, so a
// test that exercised a Go helper would pass with the feature disconnected from every
// render — the shape AGENTS.md records this repo shipping five times.
//
// What that buys, concretely — delete any ONE of these and a test here goes red:
// the filter call in luahook.buildDeriveCtxTable; the NativeCapabilities field
// packsurfaces.go sets on the derive ctx; surfaceSelectionFor's resolution of it; the
// `capabilities` key shippedProviderEntry composes; packdecl's copy of the field into
// ProviderContribution; or the `capabilities` declaration in any of the four shipped
// pack.json files.

// tavily is the worked case the rule was written for: a personal search MCP that declares
// the job it does and the credential it needs.
func tavilyServer() map[string]any {
	return map[string]any{
		"command":      "npx",
		"args":         []any{"-y", "tavily-mcp"},
		"provides":     "web_search",
		"requires_env": []any{"TAVILY_API_KEY"},
	}
}

// capabilityJailVars builds the launch environment a jail sees for one selection:
// the MCP table, and the three wire tables composed the way the launcher composes them
// (profilechannel.go's launchEnv) rather than written out by hand. Composing them here is
// what makes this an end-to-end measurement — a `capabilities` key that never reached
// YOLO_PROVIDERS would fail these tests, and a hand-written table would hide that.
func capabilityJailVars(t *testing.T, packs []*packload.Pack, servers map[string]any,
	useProfiles map[string]string) map[string]string {
	t.Helper()
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatalf("ComposeProviders: %v", err)
	}
	if providers == nil {
		// A pack set that declares no provider composes nothing, which the launcher
		// emits as an empty table (jsonDumpsOrEmptyObj).
		providers = jsonx.NewOrderedMap()
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	use := jsonx.NewOrderedMap()
	for _, agent := range sortedKeysOf(useProfiles) {
		use.Set(agent, useProfiles[agent])
	}
	return map[string]string{
		"YOLO_MCP_SERVERS":  dumpJSON(t, servers),
		"YOLO_PROVIDERS":    dumpJSON(t, providers),
		"YOLO_PROFILES":     dumpJSON(t, packload.ProfilesWireTable(resolved)),
		"YOLO_USE_PROFILES": dumpJSON(t, use),
		// The credential the tavily entry gates on. Present in every case but the one
		// test that measures its absence, so capability eligibility is what the other
		// tests are reading.
		"TAVILY_API_KEY": "tvly-test",
	}
}

func dumpJSON(t *testing.T, v any) string {
	t.Helper()
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		t.Fatalf("encoding a wire table: %v", err)
	}
	return s
}

func sortedKeysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Small and fixed in these tests; a stable order keeps the composed table stable.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// capabilityPacks returns the named shipped packs, in the order named.
func capabilityPacks(t *testing.T, names ...string) []*packload.Pack {
	t.Helper()
	all, err := embeddedPackSet()
	if err != nil {
		t.Fatalf("embedded packs: %v", err)
	}
	out := make([]*packload.Pack, 0, len(names))
	for _, want := range names {
		found := false
		for _, p := range all {
			if p.Name == want {
				out = append(out, p)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("shipped pack %q not found", want)
		}
	}
	return out
}

// renderCapabilityJail runs the boot loop and returns the Env it rendered into, failing
// the test on any generator failure — a surface that did not render is not a measurement
// of what the surface contains.
func renderCapabilityJail(t *testing.T, packs []*packload.Pack, vars map[string]string) *Env {
	t.Helper()
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars}
	ConfigurePackSurfaces(e, packs)
	if failures := e.GenFailures(); len(failures) > 0 {
		t.Fatalf("boot render failed: %v", failures)
	}
	return e
}

// renderedMCPServers reads the mcpServers table one agent's rendered surface carries:
// claude's out of ~/.claude.json (the rmw surface), agy's out of its mcp_config.json (the
// computed one). Two modes, two files, one question.
func renderedMCPServers(t *testing.T, e *Env, agent string) map[string]any {
	t.Helper()
	var path string
	switch agent {
	case "claude":
		path = e.ClaudeJSONPath()
	case "agy":
		path = filepath.Join(e.AgyDir(), "mcp_config.json")
	default:
		t.Fatalf("no MCP surface reader for agent %q", agent)
	}
	got := decodeJSONFile(t, path)
	servers, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("%s rendered no mcpServers table: %#v", agent, got)
	}
	return servers
}

// TestTavilyIsDeliveredForKiloAndRemovedForEveryNativeSearchSource is the acceptance
// boundary's first clause. It walks every authentication source a shipped pack can put
// under an agent and asserts the same rule at each: the source's own declaration decides,
// never the agent's name.
//
// The facts being pinned (supplied 2026-09-16): the Codex, Z.AI, Claude and agy sources
// declare web_search; Kilo does not, and neither does Bedrock. Two of these rows are
// BEHAVIOUR CHANGES and are the reason the rule was written — kilo used to lose its search
// MCP to claude's `not isBedrock and not isCodex` branch, and the codex source used to
// keep one it does not need.
func TestTavilyIsDeliveredForKiloAndRemovedForEveryNativeSearchSource(t *testing.T) {
	cases := []struct {
		name    string
		agent   string
		packs   []string
		profile string // "" = no profile: the agent's built-in source
		want    bool   // is tavily delivered?
	}{
		{"claude subscription is native", "claude", []string{"claude"}, "", false},
		{"claude on bedrock is not", "claude", []string{"claude"}, "bedrock", true},
		{"claude on codex is native", "claude", []string{"claude", "openai-auth"}, "codex", false},
		{"claude on kilo is not", "claude", []string{"claude", "kilo"}, "kilo", true},
		{"claude on zai is native", "claude", []string{"claude", "zai"}, "zai", false},
		{"agy's built-in source is native", "agy", []string{"agy"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packs := capabilityPacks(t, tc.packs...)
			use := map[string]string{}
			if tc.profile != "" {
				use[tc.agent] = tc.profile
			}
			vars := capabilityJailVars(t, packs, map[string]any{"tavily": tavilyServer()}, use)
			e := renderCapabilityJail(t, packs, vars)
			_, present := renderedMCPServers(t, e, tc.agent)["tavily"]
			if present != tc.want {
				t.Errorf("tavily delivered = %v, want %v — the active source decides, not the agent",
					present, tc.want)
			}
		})
	}
}

// TestChangingAProfileChangesOnlyThatTargetsMCPResult is the acceptance boundary's second
// clause: the rule is evaluated per RENDER TARGET, so two agents in one jail can disagree
// about the same server.
//
// It renders claude and agy in ONE boot — the arrangement that would catch a resolution
// keyed by anything jail-wide (the pack set, the providers table, a first-profile-wins
// walk), since agy has no profile at all while claude does.
func TestChangingAProfileChangesOnlyThatTargetsMCPResult(t *testing.T) {
	packs := capabilityPacks(t, "claude", "agy", "kilo")
	servers := map[string]any{"tavily": tavilyServer()}

	onKilo := renderCapabilityJail(t, packs,
		capabilityJailVars(t, packs, servers, map[string]string{"claude": "kilo"}))
	if _, present := renderedMCPServers(t, onKilo, "claude")["tavily"]; !present {
		t.Error("claude on kilo lost tavily — kilo declares no web_search")
	}
	if _, present := renderedMCPServers(t, onKilo, "agy")["tavily"]; present {
		t.Error("agy kept tavily while claude's profile was kilo — agy's own source is native, " +
			"and another target's selection must not speak for it")
	}

	// The SAME jail with claude's profile dropped: claude flips, agy does not move.
	noProfile := renderCapabilityJail(t, packs,
		capabilityJailVars(t, packs, servers, map[string]string{}))
	if _, present := renderedMCPServers(t, noProfile, "claude")["tavily"]; present {
		t.Error("claude with no profile kept tavily — its built-in source declares web_search")
	}
	if _, present := renderedMCPServers(t, noProfile, "agy")["tavily"]; present {
		t.Error("agy's result moved with claude's profile; it must depend on agy's source alone")
	}
}

// TestAnAbsentTavilyKeyRemovesTavilyAfterCapabilityEligibility is the third clause. The
// two gates are independent and both must hold: eligibility does not rescue a server whose
// credential is missing, and a present credential does not rescue one the source makes
// redundant.
//
// ORDER NOTE: the `requires_env` gate runs inside LoadMCPServers, upstream of the derive
// that applies capability eligibility, so the implementation's order is the reverse of the
// rule's prose. The two are an intersection, so the delivered set is identical either way
// — which is what this test measures. Only the warning a doubly-disqualified server
// produces differs, and that one names the missing variable, which is the more useful of
// the two.
func TestAnAbsentTavilyKeyRemovesTavilyAfterCapabilityEligibility(t *testing.T) {
	packs := capabilityPacks(t, "claude", "kilo")
	servers := map[string]any{"tavily": tavilyServer()}
	onKilo := map[string]string{"claude": "kilo"}

	withKey := renderCapabilityJail(t, packs, capabilityJailVars(t, packs, servers, onKilo))
	if _, present := renderedMCPServers(t, withKey, "claude")["tavily"]; !present {
		t.Fatal("tavily absent with the key set and an eligible source — the case the " +
			"no-key half is measured against")
	}

	vars := capabilityJailVars(t, packs, servers, onKilo)
	delete(vars, "TAVILY_API_KEY")
	noKey := renderCapabilityJail(t, packs, vars)
	if _, present := renderedMCPServers(t, noKey, "claude")["tavily"]; present {
		t.Error("tavily delivered with TAVILY_API_KEY unset — capability eligibility does " +
			"not stand in for the credential")
	}
}

// TestAServerWithAnotherCapabilityOrNoProvidesIsUnaffected is the fourth clause, and the
// one that keeps the rule from being a web-search special case: the match is on the exact
// capability NAME, so everything else passes through a source that declares web_search.
func TestAServerWithAnotherCapabilityOrNoProvidesIsUnaffected(t *testing.T) {
	packs := capabilityPacks(t, "claude")
	servers := map[string]any{
		"tavily":     tavilyServer(),
		"codesearch": map[string]any{"command": "mcp-code", "provides": "code_search"},
		"sequential": map[string]any{"command": "mcp-seq"},
	}
	// No profile: the claude subscription source, which declares web_search and nothing
	// else — so exactly one of these three is redundant.
	e := renderCapabilityJail(t, packs, capabilityJailVars(t, packs, servers, map[string]string{}))
	got := renderedMCPServers(t, e, "claude")
	if _, present := got["tavily"]; present {
		t.Error("tavily survived a source that declares web_search")
	}
	if _, present := got["codesearch"]; !present {
		t.Error("a server providing code_search was dropped by a web_search source — the " +
			"rule matches the capability NAME, not the presence of a `provides`")
	}
	if _, present := got["sequential"]; !present {
		t.Error("a server with no `provides` was dropped — it makes no claim this rule can answer")
	}
}

// TestCapabilityDeclarationsTravelUnderTheWireNames pins the SEAM the four tests above
// read through, in the place a rename would break it silently.
//
// It replaces TestClaudeDeriveReadsTheUseProfilesTable, which pinned the same class of
// seam for the mechanism this one supersedes: claude's derive no longer reads
// ctx.use_profiles (no shipped derive does), so that test's subject is gone. The hazard it
// guarded is not. `capabilities` is written by packload.shippedProviderEntry and read by
// luahook.sourceCapabilities, in different packages with no shared constant, and a jail
// whose source declares nothing simply delivers every MCP server — a silent flip back to
// the pre-rule behaviour with nothing anywhere failing.
func TestCapabilityDeclarationsTravelUnderTheWireNames(t *testing.T) {
	packs := capabilityPacks(t, "claude", "zai", "kilo", "openai-auth")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatalf("ComposeProviders: %v", err)
	}
	for _, tc := range []struct {
		provider string
		want     bool
	}{{"zai", true}, {"openai-codex", true}, {"kilo", false}, {"bedrock", false}} {
		entry, ok := providers.Get(tc.provider)
		if !ok {
			t.Errorf("provider %q is not in the composed table at all", tc.provider)
			continue
		}
		m, ok := entry.(*jsonx.OrderedMap)
		if !ok {
			t.Errorf("provider %q composed to %T, want an object", tc.provider, entry)
			continue
		}
		caps, has := m.Get("capabilities")
		if has != tc.want {
			t.Errorf("providers.%s.capabilities present = %v, want %v (the key luahook reads)",
				tc.provider, has, tc.want)
			continue
		}
		if tc.want {
			list, ok := caps.([]any)
			if !ok || len(list) == 0 || list[0] != "web_search" {
				t.Errorf("providers.%s.capabilities = %#v, want the string list [\"web_search\"]",
					tc.provider, caps)
			}
		}
	}
	// The built-in half: the claude pack speaks for the source a profile-less launch uses,
	// through the bin its program contribution installs.
	if got := packload.NativeCapabilities(packs, "claude"); len(got) != 1 || got[0] != "web_search" {
		t.Errorf("NativeCapabilities(claude) = %#v, want [web_search]", got)
	}
	// And a pack that installs no CLI speaks for no built-in source, which is what keeps
	// the two halves from answering for each other.
	if got := packload.NativeCapabilities(packs, "zai"); len(got) != 0 {
		t.Errorf("NativeCapabilities(zai) = %#v, want none — zai installs no CLI", got)
	}
}
