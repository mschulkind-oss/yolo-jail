package packdecl

// skewvia_test.go pins the VALUE-level half of the skew rule (program-delivery.md §6.2,
// risk R6): `via` is a closed two-value set, so the day a third delivery mechanism ships,
// a newer host staging `via: "uv"` would refuse every jail on a pre-`just load` image —
// the `tier` / unknown-kind shape one level further down. The tolerance is paid BEFORE the
// third value, which is what makes it a prerequisite of the design rather than a fix after.
//
// The split is the same one KindProgram's unknown-KIND sibling has: an author must hear
// (Decode refuses), a jail must boot (DecodeTolerant skips and reports). An EMPTY `via` is
// on neither side of that split — it is malformed in a way BOTH builds understand, so it
// stays a hard problem at both decoders, the class TestDecodeTolerantStillValidatesStructure
// protects.

import (
	"strings"
	"testing"
)

// viaSkewManifest carries the unknown-via program BETWEEN two valid siblings, so the tests
// can pin that a skip disturbs neither neighbor nor their labels.
const viaSkewManifest = `{"name":"acme","contributes":[
	{"kind":"skills","from":"skills","into":".acme/skills"},
	{"kind":"program","bin":"ruff","via":"uv","package":"ruff"},
	{"kind":"env","vars":{"ACME":"1"}}]}`

// TestUnknownViaIsAuthoringFatalAndSkewSkipped: both halves of the decision at once.
func TestUnknownViaIsAuthoringFatalAndSkewSkipped(t *testing.T) {
	manifest := []byte(viaSkewManifest)

	// AUTHORING: refused loudly, naming the value. A `via` typo that silently installed
	// nothing is the worst outcome for a pack author, so validateContribution keeps it.
	_, problems := Decode(manifest)
	if len(problems) == 0 {
		t.Fatal("Decode must refuse an unknown via at authoring time")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, `unknown via "uv"`) {
		t.Errorf("the authoring refusal must name the via value:\n%s", joined)
	}

	// VERSION BOUNDARY: skipped, reported, and NOT a problem — a problem fails the boot
	// (A12), which is the refusal this tolerance exists to prevent.
	m, tolerated, skipped := DecodeTolerant(manifest)
	if len(tolerated) != 0 {
		t.Errorf("DecodeTolerant treated an unknown VIA as a load problem — an older baked "+
			"entrypoint reading a newer staged manifest would refuse to start the jail: %v",
			tolerated)
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one skip note, got %v", skipped)
	}
	// The note names the via value AND the bin, and carries the ORIGINAL index — the
	// position the author sees in pack.json, not the position in the filtered list.
	for _, want := range []string{`"uv"`, `"ruff"`, "contributes[1]"} {
		if !strings.Contains(skipped[0], want) {
			t.Errorf("the skip note must name %s so the degradation is legible: %q", want, skipped[0])
		}
	}

	// Skipped means DROPPED, and the valid siblings are undisturbed.
	cs := m.Contributions()
	if len(cs) != 2 {
		t.Fatalf("want the 2 valid siblings to survive, got %+v", cs)
	}
	if cs[0].Kind != KindSkills || cs[0].Into != ".acme/skills" {
		t.Errorf("sibling before the skipped entry disturbed: %+v", cs[0])
	}
	if cs[1].Kind != KindEnv || cs[1].Vars["ACME"] != "1" {
		t.Errorf("sibling after the skipped entry disturbed: %+v", cs[1])
	}
	// Nothing downstream can install it: the projection the jail reads is empty.
	if installs := m.InstallContributions(); len(installs) != 0 {
		t.Errorf("a skipped program must not reach InstallContributions: %+v", installs)
	}
}

// TestEmptyViaStaysFatalOnBothPaths: the boundary of the tolerance. A program with NO via
// names no mechanism at all, so it installs nothing — malformed in a way both ends of the
// version boundary understand, and therefore never skew. Widening the skip to cover it
// would turn a broken pack into a silently-degraded jail.
func TestEmptyViaStaysFatalOnBothPaths(t *testing.T) {
	for name, manifest := range map[string]string{
		"absent via": `{"name":"a","contributes":[{"kind":"program","bin":"ruff"}]}`,
		"empty via":  `{"name":"a","contributes":[{"kind":"program","bin":"ruff","via":""}]}`,
	} {
		if _, problems := Decode([]byte(manifest)); len(problems) == 0 {
			t.Errorf("%s: Decode must refuse a program with no via", name)
		}
		m, problems, skipped := DecodeTolerant([]byte(manifest))
		if len(problems) == 0 {
			t.Errorf("%s: DecodeTolerant must still report a program with no via — it is "+
				"structure, not skew", name)
		}
		if len(skipped) != 0 {
			t.Errorf("%s: an empty via must not be skipped as skew: %v", name, skipped)
		}
		if m == nil || len(m.Contributions()) != 1 {
			t.Errorf("%s: a structural problem keeps the entry (it is reported, not dropped)", name)
		}
	}
}

// TestKnownViaValuesAreUntouched: the skip must be driven by a value neither `npm` nor
// `installer`, never by every program — a rule that dropped the shipped packs' own
// contributions would take the jail's agents with it.
func TestKnownViaValuesAreUntouched(t *testing.T) {
	manifest := []byte(`{"name":"acme","contributes":[
		{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},
		{"kind":"program","bin":"agy","via":"installer","url":"https://example.invalid/i.sh"}]}`)
	m, problems, skipped := DecodeTolerant(manifest)
	if len(problems) != 0 || len(skipped) != 0 {
		t.Fatalf("known via values must decode clean: problems=%v skipped=%v", problems, skipped)
	}
	if installs := m.InstallContributions(); len(installs) != 2 ||
		installs[0].Kind != "npm" || installs[1].Kind != "native" {
		t.Errorf("both known mechanisms must survive as installs: %+v", installs)
	}
}
