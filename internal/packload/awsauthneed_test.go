package packload

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// awsauthneed_test.go pins who brings Amazon Bedrock into a launch
// (docs/design/bedrock-plumbing.md OQ-BR9, ruled 2026-09-29: one `bedrock` pack pulls in the
// provider, and every agent pack that binds Bedrock `needs` it, so `"packs": ["codex"]` alone
// gets `-p bedrock`). The `bedrock` pack in turn NEEDS aws-auth, unconditionally, the way it
// was packs/claude's need when the provider was claude's (docs/design/sso-backed-bedrock.md §12
// step 5, moved to packs/bedrock by bedrock-plumbing.md BR-D15): the credential service belongs
// with the provider it serves, so a codex-only selection gets it too. Selecting it changes
// nothing observable on its own: the pointer is gated on the provider's platform and the
// loophole ships disabled.

// shippedByName materializes the embedded official set, keyed by pack name.
func shippedByName(t *testing.T) map[string]*Pack {
	t.Helper()
	shipped, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	byName := map[string]*Pack{}
	for _, p := range shipped {
		byName[p.Name] = p
	}
	return byName
}

// TestBedrockNeedsAWSAuth reads the shipped bedrock pack's declaration, so it fails if the
// `needs` entry is removed, gains a condition, or names a pack that no longer ships.
func TestBedrockNeedsAWSAuth(t *testing.T) {
	byName := shippedByName(t)
	bedrock := byName["bedrock"]
	if bedrock == nil {
		t.Fatal("no shipped bedrock pack")
	}
	found := false
	for _, n := range bedrock.Decl.DeclaredNeeds() {
		if n.Pack == "aws-auth" {
			found = true
			if len(n.WhenBins) != 0 {
				t.Errorf("bedrock's aws-auth need has when_bins %v; it is unconditional, since "+
					"the provider that consumes it is bedrock's own", n.WhenBins)
			}
		}
	}
	if !found {
		t.Fatalf("packs/bedrock declares no need on aws-auth (needs = %+v) — "+
			"docs/design/bedrock-plumbing.md BR-D15, which moved sso-backed-bedrock.md §12 step 5 "+
			"here", bedrock.Decl.DeclaredNeeds())
	}
}

// bedrockBinders is the agent packs whose derives bind Amazon Bedrock — through the agent's own
// Bedrock client (docs/design/bedrock-plumbing.md §6.2). copilot and oh-omp have no native
// client and reach Bedrock only through the wire bridge, under a via profile (`bedrock-bridge`)
// or as the agents a plain profile's carrier carries (carrier.go, wire-bridge-gateway.md WG-I44),
// and agy has no Bedrock transport at all, so none of the three binds it and none needs the pack.
var bedrockBinders = []string{"claude", "codex", "opencode", "pi"}

// TestEveryBedrockAgentAloneGetsTheBedrockProfile runs the production selection closure
// (Selection.Close, the one every notch calls) over each binding agent pack alone, then
// composes and resolves what the launch would, so it fails when a need is dropped, when the
// provider or profile leaves the bedrock pack, or when aws-auth stops joining through it.
func TestEveryBedrockAgentAloneGetsTheBedrockProfile(t *testing.T) {
	byName := shippedByName(t)
	embedded := func(name string) (*Pack, bool) {
		p, ok := byName[name]
		return p, ok
	}
	for _, agent := range bedrockBinders {
		t.Run(agent, func(t *testing.T) {
			p := byName[agent]
			if p == nil {
				t.Fatalf("no shipped %s pack", agent)
			}
			added, causes, err := Selection{Embedded: embedded}.Close([]*Pack{p})
			if err != nil {
				t.Fatal(err)
			}
			set := append([]*Pack{p}, added...)
			var names []string
			for _, q := range set {
				names = append(names, q.Name)
			}
			for _, want := range []string{"bedrock", "aws-auth"} {
				if !slices.Contains(names, want) {
					t.Errorf(`"packs": [%q] closes over %v, want %s among them`, agent, names, want)
				}
			}
			if !strings.Contains(strings.Join(causes, "\n"), "bedrock (needed by "+agent+")") {
				t.Errorf("the closure's cause lines %q do not say %s brought bedrock — the launch "+
					"banner and yolo check print exactly these", causes, agent)
			}
			providers, err := ComposeProviders(nil, set)
			if err != nil {
				t.Fatal(err)
			}
			raw, ok := providers.Get("bedrock")
			entry, isMap := raw.(*jsonx.OrderedMap)
			if !ok || !isMap {
				t.Fatalf("the composed table for %v has no bedrock entry: %v", names, providers.Keys())
			}
			if platform, _ := entry.Get("platform"); platform != "aws-bedrock" {
				t.Errorf("the composed bedrock entry's platform = %v, want aws-bedrock", platform)
			}
			resolved, err := ResolveProfiles(set, nil, providers)
			if err != nil {
				t.Fatal(err)
			}
			if got := ProviderFor(resolved, "bedrock"); got != "bedrock" {
				t.Errorf("-p bedrock with %q alone resolves to provider %q, want bedrock", agent, got)
			}
		})
	}
}

// TestOnlyTheBindingAgentsNeedBedrock pins the other half: an agent pack whose derive cannot
// reach Bedrock does not pull the provider in, so a `-p bedrock` there is refused as a profile
// the launch does not carry rather than accepted to configure nothing.
func TestOnlyTheBindingAgentsNeedBedrock(t *testing.T) {
	byName := shippedByName(t)
	for name, p := range byName {
		if len(p.InstallBins()) == 0 {
			continue
		}
		needs := false
		for _, n := range p.Decl.DeclaredNeeds() {
			if n.Pack == "bedrock" {
				needs = true
			}
		}
		if want := slices.Contains(bedrockBinders, name); needs != want {
			t.Errorf("packs/%s needs bedrock = %v, want %v (bedrockBinders)", name, needs, want)
		}
		// The list is what the derives say: a derive that keys on Bedrock's platform binds it,
		// and one that does not cannot, so a binding added to a derive without the need (or a
		// need left behind by a deleted binding) fails here rather than at a user's launch.
		if binds := strings.Contains(DeriveScript(p), `"aws-bedrock"`); binds != needs {
			t.Errorf("packs/%s's derive keys on \"aws-bedrock\" = %v, but its needs name bedrock = %v", name, binds, needs)
		}
	}
}
