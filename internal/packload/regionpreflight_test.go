package packload

// regionpreflight_test.go pins the region pre-flight's FACTS and its RENDERER
// (docs/design/bedrock-plumbing.md §8, OQ-BR6): which launches it refuses, and what it says.
// The call sites are pinned where they act — the jail's three arms in internal/cli/run
// (regionpreflight_test.go there), the host notch in internal/cli (hostregion_test.go) — because
// a test here would stay green with every caller deleted.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// regionalPack declares a provider that requires a region, and no region of its own — the
// shape packs/claude ships for bedrock.
func regionalPack(t *testing.T) *Pack {
	return &Pack{Name: "cloudy", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"regional","platform":"cloud","region_env_name":["AWS_REGION","AWS_DEFAULT_REGION"]}]}`)}
}

// asked is one ask per provider, all by one agent (claude) and answered by one lookup: the
// shape of a launch whose one agent selects that provider.
func asked(lookup func(string) (string, bool), providers ...string) []RegionAsk {
	var out []RegionAsk
	for _, p := range providers {
		out = append(out, RegionAsk{Agent: "claude", Provider: p, Lookup: lookup})
	}
	return out
}

// lookupOf answers from a fixed map, as a notch's delivery lookup answers from its channels.
func lookupOf(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

// THE RULE, both halves of the ruling's AND: a selected provider that requires a region
// refuses only when its entry has no `region` AND none of its variables is delivered; either
// one alone is a region, and each is pinned against the same refusing control so a silence
// cannot be the check being inert.
func TestProviderRegionGapsRefuseOnlyWhenNoRegionIsVisible(t *testing.T) {
	p := regionalPack(t)
	packs := []*Pack{p}
	none := lookupOf(nil)

	facts := ProviderRegionGaps(packs, compose(t, nil, packs), asked(none, "regional"), nil, nil)
	if len(facts) == 0 {
		t.Fatal("control: a selected regional provider with no region anywhere must refuse")
	}
	got := strings.Join(facts, "\n")
	for _, want := range []string{"pack cloudy", `provider "regional"`, `no "region"`,
		"neither AWS_REGION nor AWS_DEFAULT_REGION is set",
		`"providers": {"regional": {"region": "<region>"}}`, "AWS_REGION=<region> in an env_sources entry",
		"consulted for a region: each selected provider's composed entry"} {
		if !strings.Contains(got, want) {
			t.Errorf("the facts must say %q:\n%s", want, got)
		}
	}

	// The provider's region, from the user's `providers` entry over the pack's facts.
	user := userProviders(t, `{"regional":{"region":"eu-west-1"}}`)
	if facts := ProviderRegionGaps(packs, compose(t, user, packs), asked(none, "regional"), nil, nil); facts != nil {
		t.Errorf("a region on the composed entry satisfies the requirement:\n%s", strings.Join(facts, "\n"))
	}
	// A region the PACK ships satisfies it too.
	shipped := &Pack{Name: "cloudy", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"regional","platform":"cloud","region":"ap-south-1","region_env_name":["AWS_REGION"]}]}`)}
	if facts := ProviderRegionGaps([]*Pack{shipped}, compose(t, nil, []*Pack{shipped}), asked(none, "regional"), nil, nil); facts != nil {
		t.Errorf("a region the pack ships satisfies the requirement:\n%s", strings.Join(facts, "\n"))
	}
	// Either declared variable, delivered.
	for _, v := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		if facts := ProviderRegionGaps(packs, compose(t, nil, packs),
			asked(lookupOf(map[string]string{v: "us-west-2"}), "regional"), nil, nil); facts != nil {
			t.Errorf("%s delivered satisfies the requirement:\n%s", v, strings.Join(facts, "\n"))
		}
	}
	// An EMPTY value names no region, on the entry or in the environment.
	if facts := ProviderRegionGaps(packs, compose(t, userProviders(t, `{"regional":{"region":""}}`), packs),
		asked(lookupOf(map[string]string{"AWS_REGION": ""}), "regional"), nil, nil); len(facts) == 0 {
		t.Error("an empty region and an empty AWS_REGION must not satisfy the requirement")
	}
}

// SCOPED LIKE THE CREDENTIAL PRE-FLIGHT: a provider nobody selected, a provider whose pack
// declares no requirement, and an entry the user's null dropped each demand nothing.
func TestProviderRegionGapsDemandOnlyWhatASelectionRequires(t *testing.T) {
	p := regionalPack(t)
	packs := []*Pack{p}
	none := lookupOf(nil)
	table := compose(t, nil, packs)

	if facts := ProviderRegionGaps(packs, table, nil, nil, nil); facts != nil {
		t.Errorf("a regional provider NO agent selected must demand nothing:\n%s", strings.Join(facts, "\n"))
	}
	if facts := ProviderRegionGaps(packs, table, asked(none, "other"), nil, nil); facts != nil {
		t.Errorf("selecting another provider must demand nothing of this one:\n%s", strings.Join(facts, "\n"))
	}
	plain := &Pack{Name: "plain", Decl: declFrom(t, `{"contributes":[{"kind":"provider","name":"regional"}]}`)}
	if facts := ProviderRegionGaps([]*Pack{plain}, compose(t, nil, []*Pack{plain}), asked(none, "regional"), nil, nil); facts != nil {
		t.Errorf("a provider whose pack declares no region_env_name requires no region:\n%s", strings.Join(facts, "\n"))
	}
	dropped := compose(t, userProviders(t, `{"regional":null}`), packs)
	if facts := ProviderRegionGaps(packs, dropped, asked(none, "regional"), nil, nil); facts != nil {
		t.Errorf("a null-dropped provider is nobody's requirement:\n%s", strings.Join(facts, "\n"))
	}
}

// A region exported where yolo was launched, and not delivered, is NAMED: it is the one form of
// this mistake the user can see from their own shell. The notch decides what "stranded" means;
// with no stranded reader (the host, whose lookup already covers that shell) nothing is named.
func TestProviderRegionGapsNameAStrandedRegion(t *testing.T) {
	packs := []*Pack{regionalPack(t)}
	table := compose(t, nil, packs)
	inShell := func(name string) bool { return name == "AWS_REGION" }
	facts := ProviderRegionGaps(packs, table, asked(lookupOf(nil), "regional"), inShell,
		RegionConsulted([]string{"/home/u/.env"}, FromPackEnv))
	got := strings.Join(facts, "\n")
	if !strings.Contains(got, "AWS_REGION is set in the environment yolo was launched from, which this launch does not deliver") {
		t.Errorf("a region stranded in the launch shell must be named:\n%s", got)
	}
	if strings.Contains(got, "AWS_DEFAULT_REGION is set in the environment yolo was launched from") {
		t.Errorf("only the stranded variable is named:\n%s", got)
	}
	if !strings.Contains(got, "consulted for a region: each selected provider's composed entry, then "+
		FromEnvSources+": /home/u/.env, "+FromPackEnv) {
		t.Errorf("the consulted line must quote the notch's channels in order:\n%s", got)
	}
	if got := strings.Join(ProviderRegionGaps(packs, table, asked(lookupOf(nil), "regional"), nil, nil), "\n"); strings.Contains(got, "launched from") {
		t.Errorf("with no stranded reader nothing is named stranded:\n%s", got)
	}
}

// THE RENDERER: a verdict line (unindented, which the jail's printer bolds), the facts, the
// ~/.aws/config note the ruling's "unproven emits nothing" asks for, and the hatch; held, the
// override notice over the same facts, and a launch that proceeds.
func TestProviderRegionRefusalWordsTheVerdictAndTheHatch(t *testing.T) {
	facts := []string{"  • a fact", "  consulted for a region: x"}
	lines, refuse := ProviderRegionRefusal(facts, false)
	if !refuse {
		t.Fatal("unheld facts must refuse the launch")
	}
	if lines[0] != "Refusing to launch: a selected provider is reached through a region, and this launch names none." {
		t.Errorf("verdict = %q", lines[0])
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"  • a fact", "A region in ~/.aws/config is not counted",
		"launch anyway with " + paths.AllowMissingProvidersEnv + "=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must say %q:\n%s", want, got)
		}
	}
	for _, l := range lines[1:] {
		if l == "" || (l[0] != ' ' && l[0] != '\t') {
			t.Errorf("only the verdict may be unindented, or the printer bolds a fact as a second verdict: %q", l)
		}
	}

	held, refuse := ProviderRegionRefusal(facts, true)
	if refuse {
		t.Error("the hatch must let the launch proceed")
	}
	if !strings.HasPrefix(held[0], "Warning: "+paths.AllowMissingProvidersEnv+" is set — CONTINUING") ||
		strings.Join(held[1:], "\n") != strings.Join(facts, "\n") || strings.Contains(strings.Join(held, "\n"), "launch anyway") {
		t.Errorf("the held notice must say what it suppresses, over the same facts, without re-offering the hatch:\n%s",
			strings.Join(held, "\n"))
	}
	if lines, refuse := ProviderRegionRefusal(nil, false); lines != nil || refuse {
		t.Error("no facts, no lines and no refusal")
	}
}

// THE SHIPPED DECLARATION: packs/claude's `bedrock` provider is the one the ruling is about, so
// it must carry the requirement, naming both variables the ruling counts. Read from the embedded
// packs, so deleting `region_env_name` from packs/claude/pack.json fails here as well as at every
// launch test that selects bedrock with no region.
func TestTheShippedBedrockProviderRequiresARegion(t *testing.T) {
	loaded, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	var claude *Pack
	for _, p := range loaded {
		if p.Name == "claude" {
			claude = p
		}
	}
	if claude == nil {
		t.Fatal("no shipped claude pack")
	}
	table, err := ComposeProviders(jsonx.NewOrderedMap(), []*Pack{claude})
	if err != nil {
		t.Fatal(err)
	}
	facts := ProviderRegionGaps([]*Pack{claude}, table, asked(lookupOf(nil), "bedrock"), nil, nil)
	if got := strings.Join(facts, "\n"); !strings.Contains(got, `pack claude requires a region for provider "bedrock"`) ||
		!strings.Contains(got, "neither AWS_REGION nor AWS_DEFAULT_REGION") {
		t.Errorf("the shipped bedrock provider must require a region from AWS_REGION or AWS_DEFAULT_REGION:\n%s", got)
	}
}

// KEYED ON THE PLATFORM, not the provider's name (OQ-BR2, ruled 2026-09-29, and the review
// finding that BR-D1 predated it): a provider a USER declares with the platform a pack says is
// reached through a region is required one, from the variables that pack names, without
// restating them. A provider with no platform, or one the user relabels, requires nothing.
func TestTheRegionRequirementKeysOnThePlatform(t *testing.T) {
	packs := []*Pack{regionalPack(t)}
	none := lookupOf(nil)

	user := userProviders(t, `{"mine":{"platform":"cloud"}}`)
	got := strings.Join(ProviderRegionGaps(packs, compose(t, user, packs), asked(none, "mine"), nil, nil), "\n")
	for _, want := range []string{`pack cloudy requires a region for provider "mine" (platform "cloud")`,
		"neither AWS_REGION nor AWS_DEFAULT_REGION is set", `"providers": {"mine": {"region": "<region>"}}`} {
		if !strings.Contains(got, want) {
			t.Errorf("a user provider of the platform must be required a region, saying %q:\n%s", want, got)
		}
	}
	satisfied := userProviders(t, `{"mine":{"platform":"cloud","region":"eu-west-9"}}`)
	if facts := ProviderRegionGaps(packs, compose(t, satisfied, packs), asked(none, "mine"), nil, nil); facts != nil {
		t.Errorf("its own region satisfies it:\n%s", strings.Join(facts, "\n"))
	}
	for name, body := range map[string]string{
		"a user provider with no platform":              `{"mine":{"region":""}}`,
		"a user provider of a platform nobody declares": `{"mine":{"platform":"elsewhere"}}`,
		"the pack's provider relabelled by the user":    `{"regional":{"platform":"elsewhere"}}`,
	} {
		selected := "mine"
		if strings.Contains(body, "regional") {
			selected = "regional"
		}
		if facts := ProviderRegionGaps(packs, compose(t, userProviders(t, body), packs),
			asked(none, selected), nil, nil); facts != nil {
			t.Errorf("%s requires no region:\n%s", name, strings.Join(facts, "\n"))
		}
	}
}

// D5'S USER PROVIDER, over the embedded claude pack: `providers.bedrock-eu` with
// "platform": "aws-bedrock" and no region is refused exactly as the shipped `bedrock` is.
func TestAUserBedrockProviderIsRequiredARegionLikeTheShippedOne(t *testing.T) {
	claude := shippedPack(t, "claude")
	user := userProviders(t, `{"bedrock-eu":{"platform":"aws-bedrock"}}`)
	facts := ProviderRegionGaps([]*Pack{claude}, compose(t, user, []*Pack{claude}),
		asked(lookupOf(nil), "bedrock-eu"), nil, nil)
	if got := strings.Join(facts, "\n"); !strings.Contains(got,
		`pack claude requires a region for provider "bedrock-eu" (platform "aws-bedrock")`) ||
		!strings.Contains(got, "neither AWS_REGION nor AWS_DEFAULT_REGION") {
		t.Errorf("a user Bedrock provider must be required a region from claude's variables:\n%s", got)
	}
}
