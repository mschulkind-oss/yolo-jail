package packload

// agentprotocols_test.go is the SHIPPED-PACK census for the agent's wire declaration
// (docs/reference/protocol-resolution.md#the-three-declarations): which of the packs yolo
// ships declare `protocols`, with what, and why each value is the one its own derive reads.
//
// R3 is the risk this file exists against: "an agent pack declares its protocols wrongly
// and a working setup starts refusing". A declaration nothing measures is a guess, so the
// census is pinned to a table AND the one agent whose address lands in an ENVIRONMENT
// variable is pinned against its real derive.lua. The agents whose address lands in a
// CONFIG surface are rendered by internal/entrypoint's deriveComputedLayer, which is where
// their half of this pairing is measured (providerderive_test.go); what is testable here is
// that the manifest says what those derives read.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// shippedProtocols is the census: every embedded pack that installs an agent CLI, and the
// wires that CLI can be pointed at. The provenance for each row is its own derive.lua —
// which `endpoints.<key>` the producer reads, in the order it tries them:
//
//   - claude   reads `p.endpoints.anthropic` and nothing else.
//   - copilot  prefers `p.endpoints.anthropic`, falls back to `p.endpoints.openai` (D-3).
//   - pi       prefers `prov.endpoints.openai`, then `openai-responses`.
//   - opencode reads `prov.endpoints.openai`.
//   - oh-omp   walks {"openai", "anthropic"} in that stable preference order.
//   - codex    prefers `prov.endpoints.openai-responses`, falls back to `prov.endpoints.openai`.
//
// A nil value means the pack DECLARES NOTHING, which §4.1 resolves as unconstrained: every
// provider is direct. `agy` is the one shipped case — its derive composes no provider
// address at all, so there is no wire to name.
var shippedProtocols = map[string][]string{
	"claude":   {"anthropic"},
	"copilot":  {"anthropic", "openai"},
	"pi":       {"openai", "openai-responses"},
	"opencode": {"openai"},
	"oh-omp":   {"openai", "anthropic"},
	"codex":    {"openai-responses", "openai"},
	"agy":      nil,
}

// Every shipped pack that installs a CLI is in the census, and every census row matches
// the manifest. The FIRST half is the drift guard: a new agent pack fails here until
// somebody decides what it speaks, which is the only way a declaration stays a decision
// rather than a field people forget.
func TestShippedAgentPacksDeclareTheirProtocols(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Embedded() {
		for _, bin := range p.InstallBins() {
			want, known := shippedProtocols[bin]
			if !known {
				t.Errorf("pack %s installs %q and the protocol census does not mention it — "+
					"decide what wires it speaks (a `protocols` list on its `program`), or add it "+
					"to shippedProtocols with a nil value and say in the comment why it is "+
					"unconstrained", p.Name, bin)
				continue
			}
			seen[bin] = true
			got := p.Decl.SpokenProtocols(bin)
			if len(got) != len(want) {
				t.Errorf("pack %s: %s declares protocols %v, census says %v", p.Name, bin, got, want)
				continue
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("pack %s: %s protocols[%d] = %q, census says %q (ORDER IS PREFERENCE)",
						p.Name, bin, i, got[i], want[i])
				}
			}
		}
	}
	for bin := range shippedProtocols {
		if !seen[bin] {
			t.Errorf("the census names %q and no embedded pack installs it — a stale row", bin)
		}
	}
}

// THE DECLARATION IS MEASURED AGAINST THE REAL DERIVE, not against itself. claude declares
// `anthropic`; the shipped packs/claude/derive.lua composes an address for a provider that
// offers anthropic, and the resolver refuses a provider that offers only openai. If the
// declaration and the derive ever disagree, the declaration is the thing that would start
// refusing working setups (R3), so they are pinned together.
func TestClaudeDeclaresTheProtocolItsDeriveReads(t *testing.T) {
	claude := realClaudePack(t)
	if got := claude.Decl.SpokenProtocols("claude"); len(got) != 1 || got[0] != "anthropic" {
		t.Fatalf("packs/claude declares protocols %v, want [anthropic]", got)
	}
	for _, tc := range []struct {
		protocol string
		wantURL  string
	}{
		{"anthropic", "https://vendor.example/anthropic"},
		{"openai", ""}, // no common protocol: the launch refuses rather than composing
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			vendor := providerPack(t, `,"endpoints":{"`+tc.protocol+
				`":{"base_url":"https://vendor.example/`+tc.protocol+`"}}`)
			packs := []*Pack{claude, vendor}
			providers, err := ComposeProviders(nil, packs)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := ResolveProfiles(packs, nil, providers)
			if err != nil {
				t.Fatal(err)
			}
			vars, err := AgentEnv(packs, providers, map[string]string{"claude": "sel"},
				"claude", "sel", func(string) (string, bool) { return "", false },
				WithResolvedProfiles(resolved))
			if tc.wantURL == "" {
				if err == nil {
					t.Fatalf("a provider offering only %q must refuse, got vars %#v — the manifest's "+
						"`protocols` and the derive's own endpoint read must agree", tc.protocol, vars)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			for _, v := range vars {
				if v.Key == "ANTHROPIC_BASE_URL" {
					got = v.Value
				}
			}
			if got != tc.wantURL {
				t.Errorf("a provider offering only %q gave ANTHROPIC_BASE_URL=%q, want %q", tc.protocol, got, tc.wantURL)
			}
		})
	}
}

// Pi owns openai-codex in its own subscription catalog, but selecting that profile still
// crosses AgentEnv's protocol gate before Pi's special-case derive gets to choose the
// built-in provider. Its declared Responses fallback must therefore match the shared auth
// pack's Responses endpoint without changing the bridge's adapter selection.
func TestPiCodexProfilePassesProtocolResolution(t *testing.T) {
	var pi, auth *Pack
	for _, p := range Embedded() {
		switch p.Name {
		case "pi":
			pi = p
		case "openai-auth":
			auth = p
		}
	}
	if pi == nil || auth == nil {
		t.Fatalf("embedded packs: pi=%v openai-auth=%v", pi != nil, auth != nil)
	}
	packs := []*Pack{pi, auth}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AgentEnv(packs, providers, map[string]string{"pi": "codex"}, "pi", "codex",
		func(string) (string, bool) { return "", false }, WithResolvedProfiles(resolved)); err != nil {
		t.Fatalf("Pi's built-in Codex profile must pass protocol resolution: %v", err)
	}
}

// Codex speaks openai-responses natively and authenticates against openai-auth's
// openai-codex provider via OAuth broker. Selecting profile "codex" must pass protocol
// resolution directly without requiring an adapter.
func TestCodexCodexProfilePassesProtocolResolution(t *testing.T) {
	var codex, auth *Pack
	for _, p := range Embedded() {
		switch p.Name {
		case "codex":
			codex = p
		case "openai-auth":
			auth = p
		}
	}
	if codex == nil || auth == nil {
		t.Fatalf("embedded packs: codex=%v openai-auth=%v", codex != nil, auth != nil)
	}
	packs := []*Pack{codex, auth}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AgentEnv(packs, providers, map[string]string{"codex": "codex"}, "codex", "codex",
		func(string) (string, bool) { return "", false }, WithResolvedProfiles(resolved)); err != nil {
		t.Fatalf("Codex's built-in Codex profile must pass protocol resolution: %v", err)
	}
}

// THE DECLARATION ALONE COMPOSES NO ADDRESS: with no adapter selected, the providers table
// for a launch is identical whether or not the agent pack declares its wires. Step 2's
// acceptance criterion as an assertion — "if landing it changes any launch, something read
// it early" — and it survives step 4 because what changes a table is an ADAPTER, never a
// protocol list.
func TestProtocolDeclarationDoesNotTouchTheComposedTable(t *testing.T) {
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	quiet := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"c"}]}`)}
	loud := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"c","protocols":["anthropic"]}]}`)}
	render := func(agent *Pack) string {
		table, err := ComposeProviders(nil, []*Pack{agent, vendor})
		if err != nil {
			t.Fatal(err)
		}
		data, err := jsonx.DumpsCompact(table)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if a, b := render(quiet), render(loud); a != b {
		t.Errorf("declaring `protocols` changed the composed providers table:\n  without: %s\n  with:    %s\n"+
			"A protocol list is a fact about an agent; only an adapter contributes an address", a, b)
	}
}

// THE SHORTHAND IS GONE FROM THE SHIPPED DERIVE, measured through the real
// packs/claude/derive.lua (protocol-resolution.md).
//
// A provider carrying a bare `base_url` used to reach that producer and become
// ANTHROPIC_BASE_URL — including the trailing-/v1 strip, which existed only for this
// path. The same field meant `openai` to pi's derive, so one line of user config pointed
// two agents at two different services, and a local llama.cpp/ollama/vLLM endpoint (all
// OpenAI-speaking) sent claude somewhere it could not talk to. A user config carrying it
// is a validation refusal now; this test is the other half — that the DERIVE no longer
// honors it if one arrives anyway, which is what keeps "removed" from meaning "refused in
// one layer and silently obeyed in the next".
func TestTheShippedClaudeDeriveNoLongerReadsTheShorthand(t *testing.T) {
	claude := realClaudePack(t)
	vendor := &Pack{Name: "vendor", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"p"},
	  {"kind":"profile","name":"sel","provider":"p"}]}`)}
	packs := []*Pack{claude, vendor}
	providers, err := ComposeProviders(
		userProviders(t, `{"p":{"base_url":"http://127.0.0.1:8080/v1"}}`), packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel",
		func(string) (string, bool) { return "", false }, WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vars {
		if v.Key == "ANTHROPIC_BASE_URL" {
			t.Errorf("the shipped derive composed %#v from a bare base_url — the shorthand is "+
				"removed, and a field refused by validation must not be honored by a derive", v)
		}
	}
}
