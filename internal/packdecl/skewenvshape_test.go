package packdecl

// skewenvshape_test.go pins the PLACEHOLDER-level half of the skew rule — the third
// closed VALUE set a manifest carries, after the kinds and a program's `via`. An
// env_shape names which variable takes which of a provider entry's facts, and what a
// variable may name is closed (ValidateProviderEnvShape). So the day a FIFTH placeholder
// ships, a newer host staging a pack that uses it would refuse every jail on a
// pre-`just load` image — the `tier` / unknown-kind / unknown-via shape again, and the
// first one that sits inside a field the launch-time composer would otherwise have
// quietly ignored.
//
// The split is the one the other two levels have: an author must hear (Decode refuses),
// a jail must boot (DecodeTolerant drops the variable and reports it). The degradation is
// paid at the FINEST grain available — the variable, not the protocol and not the
// provider — because the rest of the entry (its endpoint, its credential pointer, its
// other variables) renders exactly as declared, and the composer already treats an
// unknown template as "renders nothing" (agentenv.providerVars). Trading a degraded jail
// for a broken one is what the tolerance exists to prevent.

import (
	"strings"
	"testing"
)

// envShapeSkewManifest carries a provider whose env_shape names one placeholder this
// build knows and one it does not, BETWEEN two valid siblings, so the tests can pin that
// a skip disturbs neither neighbor nor the kept half of the shape.
const envShapeSkewManifest = `{"name":"acme","contributes":[
	{"kind":"skills","from":"skills","into":".acme/skills"},
	{"kind":"provider","name":"acme",
	 "api_key_env_name":"ACME_API_KEY",
	 "endpoints":{"anthropic":{"base_url":"https://api.acme.dev/v4"}},
	 "env_shape":{"anthropic":{
	     "ANTHROPIC_BASE_URL":"{endpoint}",
	     "ACME_CACHE":"{cache}"}}},
	{"kind":"env","vars":{"ACME":"1"}}]}`

// TestUnknownEnvShapePlaceholderIsAuthoringFatalAndSkewSkipped: both halves of the
// decision at once.
func TestUnknownEnvShapePlaceholderIsAuthoringFatalAndSkewSkipped(t *testing.T) {
	manifest := []byte(envShapeSkewManifest)

	// AUTHORING: refused loudly, naming the value. A typo'd placeholder that silently
	// composed nothing is the worst outcome for a pack author, so
	// ValidateProviderEnvShape keeps its refusal on the strict path.
	_, problems := Decode(manifest)
	if len(problems) == 0 {
		t.Fatal("Decode must refuse an unknown env_shape placeholder at authoring time")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, `"{cache}"`) {
		t.Errorf("the authoring refusal must name the placeholder value:\n%s", joined)
	}

	// VERSION BOUNDARY: skipped, reported, and NOT a problem — a problem fails the boot
	// (A12), which is the refusal this tolerance exists to prevent.
	m, tolerated, skipped := DecodeTolerant(manifest)
	if len(tolerated) != 0 {
		t.Errorf("DecodeTolerant treated an unknown PLACEHOLDER as a load problem — an older "+
			"baked entrypoint reading a newer staged manifest would refuse to start the jail: %v",
			tolerated)
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one skip note, got %v", skipped)
	}
	// The note names the provider, the protocol, the variable AND the value, and carries
	// the ORIGINAL index — the position the author sees in pack.json.
	for _, want := range []string{"contributes[1]", `"acme"`, `"anthropic"`, `"ACME_CACHE"`, `"{cache}"`} {
		if !strings.Contains(skipped[0], want) {
			t.Errorf("the skip note must name %s so the degradation is legible: %q", want, skipped[0])
		}
	}

	// Skipped means DROPPED — and only the variable, not the provider and not the rest of
	// its shape.
	provs := m.Providers()
	if len(provs) != 1 {
		t.Fatalf("want the provider to survive, got %+v", provs)
	}
	if provs[0].Name != "acme" || provs[0].APIKeyEnvName != "ACME_API_KEY" {
		t.Errorf("the provider's own facts must survive the skip: %+v", provs[0])
	}
	if got := provs[0].EnvShape["anthropic"]; len(got) != 1 || got["ANTHROPIC_BASE_URL"] != "{endpoint}" {
		t.Errorf("want the unknown variable gone and the known one kept, got %+v", got)
	}

	// And the valid siblings are undisturbed, through the projections the boot reads.
	if srcs := m.SkillsSources(); len(srcs) != 1 || srcs[0] != "skills" {
		t.Errorf("the skills sibling (before the skip) must survive: %v", srcs)
	}
	if env := m.EnvContributions(); env["ACME"] != "1" {
		t.Errorf("the env sibling (after the skip) must survive: %v", env)
	}
}

// TestUnknownPlaceholderAloneLeavesNoHusk: a provider whose EVERY placeholder is unknown
// is a provider this build can deliver no env for — the shape is dropped with it rather
// than left as an empty protocol map something downstream might render.
func TestUnknownPlaceholderAloneLeavesNoHusk(t *testing.T) {
	m, problems, skipped := DecodeTolerant([]byte(`{"name":"acme","contributes":[
		{"kind":"provider","name":"acme",
		 "endpoints":{"openai":{"base_url":"https://api.acme.dev/v4"}},
		 "env_shape":{"openai":{"ACME_CACHE":"{cache}"}}}]}`))
	if len(problems) != 0 {
		t.Fatalf("an unknown placeholder must not be a load problem (A12): %v", problems)
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one skip note, got %v", skipped)
	}
	provs := m.Providers()
	if len(provs) != 1 {
		t.Fatalf("want the provider to survive, got %+v", provs)
	}
	if len(provs[0].EnvShape) != 0 {
		t.Errorf("a shape with nothing left in it must be dropped, got %+v", provs[0].EnvShape)
	}
	if provs[0].Endpoints["openai"].BaseURL == "" {
		t.Errorf("the endpoint is a fact this build still renders and must survive: %+v", provs[0])
	}
}

// TestEmptyEnvShapeValueStaysFatalOnBothPaths: the boundary of the tolerance. An empty
// value names no fact at all, in a way BOTH ends of the version boundary understand — the
// same rule an empty `via` follows (TestEmptyViaStaysFatalOnBothPaths). Widening the skip
// to cover it would turn a broken pack into a silently-degraded jail.
func TestEmptyEnvShapeValueStaysFatalOnBothPaths(t *testing.T) {
	manifest := []byte(`{"name":"acme","contributes":[
		{"kind":"provider","name":"acme","env_shape":{"anthropic":{"ANTHROPIC_BASE_URL":""}}}]}`)
	if _, problems := Decode(manifest); len(problems) == 0 {
		t.Error("Decode must refuse an env_shape variable with an empty value")
	}
	m, problems, skipped := DecodeTolerant(manifest)
	if len(problems) == 0 {
		t.Error("DecodeTolerant must still report an env_shape variable with an empty value — " +
			"it is structure, not skew")
	}
	if len(skipped) != 0 {
		t.Errorf("an empty value must not be skipped as skew: %v", skipped)
	}
	if m == nil || len(m.Providers()) != 1 {
		t.Error("a structural problem keeps the entry (it is reported, not dropped)")
	}
}

// TestEnvShapeVocabularyIsOneSet sweeps the known placeholders, the empty value, and
// several no build knows, asserting the equivalence that makes the set single:
//
//	ValidateProviderEnvShape accepts X  ⇔  unknownEnvShapeValueSkip keeps X
//	                                   ⇔  KnownEnvShapeValue(X)
//
// with ONE documented exception, asserted rather than excused: an EMPTY value is refused
// by the validator and still KEPT by the tolerant decoder, because it is malformed in a
// way both ends of the version boundary understand (TestEmptyEnvShapeValueStaysFatalOnBothPaths).
//
// It fails if a placeholder reaches one site's set without reaching
// KnownEnvShapeValue — the drift the `via` vocabulary shipped with once, measured
// (knownVias): both switches taught, the suite green, the jail installing nothing.
func TestEnvShapeVocabularyIsOneSet(t *testing.T) {
	probes := []string{
		EnvShapeEndpoint, EnvShapeKey, EnvShapeRegion,
		EnvShapeModelPrefix + "default}",
		"", "{cache}", "Bearer {key}", "{model:}", "{ENDPOINT}", "{region}{region}",
	}
	for _, v := range probes {
		t.Run("value="+v, func(t *testing.T) {
			known := KnownEnvShapeValue(v)

			// The strict path: one variable, one protocol, so membership is the only
			// thing under test.
			accepted := len(ValidateProviderEnvShape("p",
				map[string]map[string]string{"anthropic": {"X": v}})) == 0
			if accepted != known {
				t.Errorf("ValidateProviderEnvShape accepts=%v, KnownEnvShapeValue=%v — the "+
					"authoring path knows a different placeholder set than the vocabulary does", accepted, known)
			}

			// The tolerant path drops what it does not know, EXCEPT the empty value,
			// which is structure and stays (reported by the validator above).
			c := Contribution{Kind: KindProvider, Name: "acme",
				EnvShape: map[string]map[string]string{"anthropic": {"X": v}}}
			trimmed, notes := unknownEnvShapeValueSkip(0, c)
			kept := len(notes) == 0
			if kept != known && v != "" {
				t.Errorf("unknownEnvShapeValueSkip keeps=%v, KnownEnvShapeValue=%v — the version "+
					"boundary knows a different placeholder set than the vocabulary does", kept, known)
			}
			if v == "" && !kept {
				t.Error("an empty value is structure and must be kept for validateContributionAt to report")
			}
			if kept != (trimmed.EnvShape["anthropic"]["X"] == v) {
				t.Errorf("kept=%v but the variable's survival is %v — the note and the trim disagree",
					kept, trimmed.EnvShape["anthropic"]["X"] == v)
			}
		})
	}
}

// TestKnownEnvShapeValuesCoverTheShippedPlaceholders is the floor under the equivalence
// above, which a predicate that knows NOTHING would satisfy. These four are what the
// shipped packs declare (zai: {endpoint}/{key}; claude's bedrock: {region}/{model:*}), so
// an unknown-by-definition predicate would skip every variable every shipped provider
// composes.
func TestKnownEnvShapeValuesCoverTheShippedPlaceholders(t *testing.T) {
	for _, v := range []string{
		EnvShapeEndpoint, EnvShapeKey, EnvShapeRegion, EnvShapeModelPrefix + "default}",
	} {
		if !KnownEnvShapeValue(v) {
			t.Errorf("KnownEnvShapeValue(%q) = false — a shipped placeholder must be known, or "+
				"every provider declaring it composes nothing", v)
		}
	}
	if KnownEnvShapeValue("") {
		t.Error("KnownEnvShapeValue(\"\") = true — an empty value names no fact and stays a " +
			"hard problem on both paths")
	}
}
