package packdecl

// skewwireapi_test.go pins the WIRE_API-level half of the skew rule — the fourth closed
// VALUE set a manifest carries, after the kinds, a program's `via` and an env_shape's
// placeholders. An endpoint's `wire_api` names which protocol that URL speaks, and the
// value crosses into the composed providers table — and from there into codex's and pi's
// config files — VERBATIM, with no consumer re-checking it: a typo is not a load error
// anywhere, it is a protocol error the agent reports at first request, from a jail that
// booted green. The strict decode refuses it at authoring time; before this, nothing did.
//
// The split is the one the other closed sets have: an author must hear (Decode refuses,
// naming the enum), a jail must boot (DecodeTolerant drops the VALUE and reports it). The
// degradation is paid at the FINEST grain available — the endpoint's wire_api, not the
// endpoint and not the provider — because the base_url beside it is a fact this build
// still renders, and dropping the provider would unresolve every profile naming it for
// want of a protocol name no build was going to speak anyway.

import (
	"strings"
	"testing"
)

// wireAPISkewManifest carries a provider whose endpoints declare one wire protocol this
// build knows and one it does not, BETWEEN two valid siblings, so the tests can pin that
// a skip disturbs neither neighbor nor the kept half of the endpoint map.
const wireAPISkewManifest = `{"name":"acme","contributes":[
	{"kind":"skills","from":"skills","into":".acme/skills"},
	{"kind":"provider","name":"acme",
	 "api_key_env_name":"ACME_API_KEY",
	 "endpoints":{
	     "openai":{"base_url":"https://api.acme.dev/v4","wire_api":"openai-chat"},
	     "glm":{"base_url":"https://api.acme.dev/glm","wire_api":"openai-chatt"}},
	 "env_shape":{"openai":{"ACME_BASE":"{endpoint}"}}},
	{"kind":"env","vars":{"ACME":"1"}}]}`

// TestUnknownWireAPIIsAuthoringFatalAndSkewSkipped: both halves of the decision at once.
func TestUnknownWireAPIIsAuthoringFatalAndSkewSkipped(t *testing.T) {
	manifest := []byte(wireAPISkewManifest)

	// AUTHORING: refused loudly, naming the value AND the enum it failed to name — the
	// enum is the whole diagnosis, because the failure it prevents surfaces chapters
	// later, inside the agent, as "unsupported wire API".
	_, problems := Decode(manifest)
	if len(problems) == 0 {
		t.Fatal("Decode must refuse an unknown wire_api at authoring time")
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, `"openai-chatt"`) {
		t.Errorf("the authoring refusal must name the typo'd value:\n%s", joined)
	}
	for _, api := range KnownWireAPIs() {
		if !strings.Contains(joined, api) {
			t.Errorf("the authoring refusal must name the enum it enforces, missing %q:\n%s", api, joined)
		}
	}

	// VERSION BOUNDARY: skipped, reported, and NOT a problem — a problem fails the boot
	// (A12), which is the refusal this tolerance exists to prevent.
	m, tolerated, skipped := DecodeTolerant(manifest)
	if len(tolerated) != 0 {
		t.Errorf("DecodeTolerant treated an unknown wire_api as a load problem — an older "+
			"baked entrypoint reading a newer staged manifest would refuse to start the jail: %v",
			tolerated)
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one skip note, got %v", skipped)
	}
	// The note names the provider, the protocol, the field AND the value, and carries
	// the ORIGINAL index — the position the author sees in pack.json.
	for _, want := range []string{"contributes[1]", `"acme"`, `"glm"`, `"openai-chatt"`} {
		if !strings.Contains(skipped[0], want) {
			t.Errorf("the skip note must name %s so the degradation is legible: %q", want, skipped[0])
		}
	}

	// Skipped means DROPPED — and only the wire_api, not the endpoint and not the
	// provider.
	provs := m.Providers()
	if len(provs) != 1 {
		t.Fatalf("want the provider to survive, got %+v", provs)
	}
	if provs[0].Name != "acme" || provs[0].APIKeyEnvName != "ACME_API_KEY" {
		t.Errorf("the provider's own facts must survive the skip: %+v", provs[0])
	}
	if ep := provs[0].Endpoints["glm"]; ep.BaseURL != "https://api.acme.dev/glm" || ep.WireAPI != "" {
		t.Errorf("the endpoint must keep its base_url and lose only the wire_api: %+v", ep)
	}
	if ep := provs[0].Endpoints["openai"]; ep.WireAPI != "openai-chat" {
		t.Errorf("the KNOWN wire_api must survive untouched: %+v", ep)
	}

	// And the valid siblings are undisturbed, through the projections the boot reads.
	if srcs := m.SkillsSources(); len(srcs) != 1 || srcs[0] != "skills" {
		t.Errorf("the skills sibling (before the skip) must survive: %v", srcs)
	}
	if env := m.EnvContributions(); env["ACME"] != "1" {
		t.Errorf("the env sibling (after the skip) must survive: %v", env)
	}
}

// TestWireAPISkipIsNotAnAmnesty: a skip buys version tolerance, not silence about
// structure. An endpoint whose ONLY content was the unknown wire_api is still missing its
// base_url — malformed in a way BOTH ends of the version boundary understand, so it stays
// fatal on both paths rather than being skipped into an endpoint that declares nothing.
func TestWireAPISkipIsNotAnAmnesty(t *testing.T) {
	manifest := []byte(`{"name":"acme","contributes":[
		{"kind":"provider","name":"acme",
		 "endpoints":{"glm":{"wire_api":"openai-chatt"}}}]}`)
	if _, problems := Decode(manifest); len(problems) == 0 {
		t.Error("Decode must still demand a base_url under an unknown wire_api")
	}
	m, problems, skipped := DecodeTolerant(manifest)
	if len(problems) == 0 {
		t.Error("DecodeTolerant must still demand a base_url — the skip drops a VALUE, not the rules")
	}
	if len(skipped) != 1 {
		t.Fatalf("want the one skew note beside the structural problem, got %v", skipped)
	}
	if m == nil || len(m.Providers()) != 1 {
		t.Error("a structural problem keeps the entry (it is reported, not dropped)")
	}
}

// TestWireAPIVocabularyIsOneSet sweeps the known values, the empty value, and several no
// build knows, asserting the equivalence that makes the set single:
//
//	validateProviderEndpoints accepts X  ⇔  unknownWireAPISkip keeps X
//	                                    ⇔  KnownWireAPI(X) or X == ""
//
// with ONE documented exception, asserted rather than excused: an EMPTY wire_api is no
// claim at all — the field is omitempty, so "" and absent decode to the same fact, and an
// endpoint may legitimately leave the protocol to the consumer's own default. That is the
// opposite of an empty env_shape value, which stays a hard problem on both paths because
// an env_shape entry IS a claim and an empty one is a claim gone wrong.
//
// It fails if a wire_api reaches one site's set without reaching KnownWireAPI — the drift
// the `via` vocabulary shipped with once, measured (knownVias): both switches taught, the
// suite green, the jail installing nothing.
func TestWireAPIVocabularyIsOneSet(t *testing.T) {
	probes := append(append([]string{}, KnownWireAPIs()...),
		"", "openai-chatt", "Anthropic", "chat", "openai-completions/")
	for _, v := range probes {
		t.Run("value="+v, func(t *testing.T) {
			known := KnownWireAPI(v)
			noClaim := v == ""

			// The strict path: one endpoint carrying a good base_url, so membership is
			// the only thing under test.
			accepted := len(validateProviderEndpoints("p", map[string]ProviderEndpoint{
				"openai": {BaseURL: "https://api.acme.dev/v4", WireAPI: v}})) == 0
			if accepted != (known || noClaim) {
				t.Errorf("validateProviderEndpoints accepts=%v, KnownWireAPI=%v — the authoring "+
					"path knows a different wire_api set than the vocabulary does", accepted, known)
			}

			// The tolerant path drops what it does not know; the absent claim stays.
			c := Contribution{Kind: KindProvider, Name: "acme", Endpoints: map[string]ProviderEndpoint{
				"openai": {BaseURL: "https://api.acme.dev/v4", WireAPI: v}}}
			trimmed, notes := unknownWireAPISkip(0, c)
			kept := len(notes) == 0
			if kept != (known || noClaim) {
				t.Errorf("unknownWireAPISkip keeps=%v, KnownWireAPI=%v — the version boundary "+
					"knows a different wire_api set than the vocabulary does", kept, known)
			}
			if kept != (trimmed.Endpoints["openai"].WireAPI == v) {
				t.Errorf("kept=%v but the value's survival is %v — the note and the trim disagree",
					kept, trimmed.Endpoints["openai"].WireAPI == v)
			}
		})
	}
}

// TestKnownWireAPIsCoverTheShippedValues is the floor under the equivalence above, which
// a predicate that knew NOTHING would satisfy. These four are the wire shapes the agents'
// own config files name — packs/zai ships "openai-chat" today — so a predicate that
// dropped one would skip the wire_api of every provider declaring it, and the composed
// entry would fall back to each consumer's default protocol unasked.
func TestKnownWireAPIsCoverTheShippedValues(t *testing.T) {
	for _, v := range []string{"anthropic", "openai-chat", "openai-completions", "responses"} {
		if !KnownWireAPI(v) {
			t.Errorf("KnownWireAPI(%q) = false — a value the agents' config files name must be "+
				"known, or every provider declaring it composes with no wire protocol", v)
		}
	}
	if KnownWireAPI("openai-chatt") {
		t.Error(`KnownWireAPI("openai-chatt") = true — the review's own typo is not a wire protocol`)
	}
}
