package packdecl

// packdecl_test.go pins the VERSION BOUNDARY behavior of manifest decoding: strict where a
// human is authoring, tolerant where two builds of yolo meet.

import (
	"strings"
	"testing"
)

// A manifest carrying a field this build does not know must decode TOLERANTLY through
// DecodeTolerant — and that is not a nicety, it is what keeps a jail bootable.
//
// The incident: adding the `tier` field to `skills` made every jail refuse to start against
// an older baked image, because the in-jail entrypoint read manifests with
// DisallowUnknownFields:
//
//	yolo-entrypoint: refusing to start the jail: 2 config generator(s) failed:
//	  - load_packs: pack claude: pack.json: json: unknown field "tier"
//
// The host CLI and the entrypoint come from different places — the CLI is freshly built or
// `go install`ed, the entrypoint is baked at the last `just load` — so that skew is a NORMAL
// state. And because the offending manifest is one yolo SHIPS, the user had no way to route
// around it. A field the entrypoint cannot use is a degraded jail; a field it refuses to read
// is no jail at all.
func TestDecodeTolerantIgnoresUnknownFields(t *testing.T) {
	// `skills_tier` stands in for a field a newer build adds — it is exactly the incident's
	// shape, one version later — and futureThing/futureField for ones nothing knows.
	manifest := []byte(`{"name":"acme","futureThing":{"a":1},"skills_tier":"namespaced",
		"contributes":[{"kind":"skills","from":"skills","into":".acme/skills",
		"futureField":"x"}]}`)

	m, problems, skipped := DecodeTolerant(manifest)
	if len(problems) != 0 {
		t.Fatalf("DecodeTolerant must ignore unknown fields, got %v", problems)
	}
	if len(skipped) != 0 {
		t.Fatalf("an unknown FIELD is ignored, not a skipped contribution: %v", skipped)
	}
	if m.Name != "acme" {
		t.Errorf("name = %q, want acme", m.Name)
	}
	if !m.WantsNamespacedSkills() {
		t.Errorf("skills_tier = %q, want namespaced", m.SkillsTier)
	}
	// The KNOWN fields still decode — tolerance must not mean "skip the entry".
	cs := m.Contributions()
	if len(cs) != 1 || cs[0].Kind != KindSkills || cs[0].Into != ".acme/skills" {
		t.Fatalf("known fields must still decode: %+v", cs)
	}
}

// THE RETIRED `tier` IS REFUSED WHERE A HUMAN IS AUTHORING AND TOLERATED AT THE VERSION
// BOUNDARY — the two halves of this test are one decision, and getting it wrong reproduced the
// original incident in mirror image.
//
// The field stays DECODABLE, so the strict decoder does not answer a pack still carrying it with a
// bare `json: unknown field "tier"` — loud, but naming neither the replacement nor the reason.
//
// The TOLERANT half is the sharp one. A retired field is a version-skew fact: after this change a
// manifest yolo SHIPS carries `skills_tier`, and a newer host CLI stages that tree for an OLDER
// baked entrypoint. Refusing there — which is what making this a Validate problem did — meant
// `load_packs` failed and the jail would not start at all, with no route to recovery, since the
// offending manifest is one yolo ships. Caught by running a nested jail against the previous baked
// image, not by any unit test; hence this one.
func TestRetiredContributionTierIsAuthoringOnly(t *testing.T) {
	manifest := []byte(`{"name":"acme","contributes":[` +
		`{"kind":"skills","from":"skills","into":".acme/skills","tier":"namespaced"}]}`)

	// AUTHORING: refused, with the migration named.
	_, problems := Decode(manifest)
	if len(problems) == 0 {
		t.Fatal("a per-contribution `tier` must be refused at authoring time — it declared a " +
			"global property at a per-destination site (S2)")
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"skills_tier", "namespaced"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not mention %q, so it names no way forward:\n%s", want, joined)
		}
	}

	// VERSION BOUNDARY: accepted, and the rest of the manifest still decodes. A jail must boot
	// even when the two ends disagree about which fields exist.
	m, tolerated, _ := DecodeTolerant(manifest)
	if len(tolerated) != 0 {
		t.Errorf("DecodeTolerant refused a RETIRED field, which is a version-skew fact — an older "+
			"baked entrypoint reading a newer staged manifest would refuse to start the jail: %v",
			tolerated)
	}
	if cs := m.Contributions(); len(cs) != 1 || cs[0].Into != ".acme/skills" {
		t.Errorf("tolerance must not mean skipping the entry: %+v", cs)
	}
	// Nothing reads the retired value, so tolerating it changes no behavior — it just boots.
	if m.WantsNamespacedSkills() {
		t.Error("a per-contribution `tier` must not be honored as the pack's opt-in — that would " +
			"resurrect the per-destination tier through the tolerant path")
	}
}

// A misspelled `skills_tier` is an ERROR, never a silent downgrade. Reading it as unnamespaced
// would be safe and confusing (the author sees no namespacing and no reason why); reading it as
// namespaced would hand a real home more authority than the pack asked for.
func TestUnknownSkillsTierIsRefused(t *testing.T) {
	_, problems := Decode([]byte(`{"name":"acme","skills_tier":"namespcaed"}`))
	if len(problems) == 0 {
		t.Fatal("a misspelled skills_tier must be refused")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "flat or namespaced") {
		t.Errorf("the refusal must name the legal values: %v", problems)
	}
}

// Strict Decode stays strict: at AUTHORING time an unknown field is a misspelled
// declaration that would silently do nothing, and the author must hear about it. Tolerance
// belongs only at the version boundary.
func TestDecodeStaysStrictForAuthors(t *testing.T) {
	// `allow_exec` is the unknown field on purpose: it was a real config key that authors
	// kept writing into pack.json, where it never belonged and now does not exist at all.
	manifest := []byte(`{"name":"acme","allow_exec":true,
		"contributes":[{"kind":"skills","from":"skills","into":".acme/skills"}]}`)
	if _, problems := Decode(manifest); len(problems) == 0 {
		t.Error("Decode must reject an unknown field — a typo'd key that silently does " +
			"nothing is the worst outcome for a pack author")
	}
}

// Tolerance is about UNKNOWN declarations only. A manifest that is malformed in a way BOTH
// builds understand — a missing "kind", a missing required field — must still fail loudly, or
// the tolerant path becomes a way to ship a broken pack into a jail.
func TestDecodeTolerantStillValidatesStructure(t *testing.T) {
	// A RETIRED field is deliberately absent from this list — it is a version-skew fact rather
	// than a structural one, which is the split TestRetiredContributionTierIsAuthoringOnly pins.
	// An UNKNOWN KIND is absent too, and that is the §3.3a decision rather than an oversight:
	// "a kind this build does not know" is only malformed to the OLDER of the two builds, so it
	// is skew, not structure — TestUnknownKindIsAuthoringFatalAndSkewSkipped pins that split.
	// `bad skills_tier` replaces the old `bad tier` case: a value NEITHER build understands is
	// still malformed in a way both agree on, so it must fail at both decoders.
	for name, manifest := range map[string]string{
		"missing kind":    `{"name":"a","contributes":[{"into":"x"}]}`,
		"missing field":   `{"name":"a","contributes":[{"kind":"skills","from":"skills"}]}`,
		"bad skills_tier": `{"name":"a","skills_tier":"nope"}`,
	} {
		if _, problems, _ := DecodeTolerant([]byte(manifest)); len(problems) == 0 {
			t.Errorf("%s: DecodeTolerant must still report structural problems", name)
		}
	}
}

// AN UNKNOWN KIND IS FATAL WHERE A HUMAN IS AUTHORING AND SKIPPED-AND-REPORTED AT THE VERSION
// BOUNDARY (design: loophole-packaging §3.3a). The two halves are one decision — the same
// asymmetry the retired `tier` established: an author must hear that their declaration is
// unknown, and a jail must boot when the two ends of the version boundary disagree about
// which kinds exist.
//
// The A12 story this pins: the in-jail entrypoint reads manifests tolerantly (TolerateSkew)
// but any load problem FAILS THE BOOT (A12). DecodeTolerant used to route an unknown kind
// through Validate → ValidateKind, a hard problem — so the moment a newer host CLI staged a
// pack declaring a kind the baked entrypoint did not know, every jail on the pre-`just load`
// image refused to start, with no route to recovery when the pack is one yolo ships. The
// `tier` incident, third time.
func TestUnknownKindIsAuthoringFatalAndSkewSkipped(t *testing.T) {
	manifest := []byte(`{"name":"acme","contributes":[
		{"kind":"skills","from":"skills","into":".acme/skills"},
		{"kind":"totally-unknown-kind","from":"x"},
		{"kind":"env","vars":{"ACME":"1"}}]}`)

	// AUTHORING: refused loudly, naming the kind. A typo'd kind that silently rendered
	// nothing would be the worst outcome for a pack author.
	_, problems := Decode(manifest)
	if len(problems) == 0 {
		t.Fatal("Decode must refuse an unknown kind at authoring time")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, `unknown kind "totally-unknown-kind"`) {
		t.Errorf("the authoring refusal must name the kind:\n%s", joined)
	}

	// VERSION BOUNDARY: skipped, reported by name, and NOT a problem — a problem fails
	// the boot (A12), which is exactly what this path exists to avoid.
	m, tolerated, skipped := DecodeTolerant(manifest)
	if len(tolerated) != 0 {
		t.Errorf("DecodeTolerant treated an unknown KIND as a load problem — an older baked "+
			"entrypoint reading a newer staged manifest would refuse to start the jail: %v",
			tolerated)
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one skip note, got %v", skipped)
	}
	for _, want := range []string{`"totally-unknown-kind"`, "contributes[1]"} {
		if !strings.Contains(skipped[0], want) {
			t.Errorf("the skip note must name %s so the degradation is legible: %q", want, skipped[0])
		}
	}
	// The unknown contribution is DROPPED from the loaded manifest, and its valid
	// siblings are undisturbed — a skipped entry must not take the pack down with it.
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
}

// A skipped unknown kind must not shift the labels of its siblings' problems: an author
// reading "contributes[2]" must find the offending entry at index 2 of THEIR pack.json,
// not at index 2 of the filtered list the decoder kept.
func TestSkewSkipKeepsOriginalIndicesInProblems(t *testing.T) {
	manifest := []byte(`{"name":"acme","contributes":[
		{"kind":"future-kind"},
		{"kind":"skills","from":"skills","into":".acme/skills"},
		{"kind":"launch"}]}`)
	_, problems, skipped := DecodeTolerant(manifest)
	if len(skipped) != 1 || !strings.Contains(skipped[0], "contributes[0]") {
		t.Fatalf("want one skip note for contributes[0], got %v", skipped)
	}
	// launch with no bin is malformed in a way both builds understand — still loud, and
	// still labeled with the index the author sees.
	if len(problems) != 1 || !strings.Contains(problems[0], "contributes[2]") {
		t.Errorf("the launch problem must keep its ORIGINAL index contributes[2]: %v", problems)
	}
}

func TestDecodePermitsJSONCCommentsAndTrailingCommas(t *testing.T) {
	manifest := []byte(`{
		// Single-line comment
		"name": "jsonc-pack",
		/* Multi-line
		   block comment */
		"description": "supports jsonc seamlessly",
		"contributes": [
			{
				"kind": "skills",
				"from": "skills",
				"into": ".jsonc/skills", // trailing comma inside item
			},
		], // trailing comma in list
	}`)

	m, problems := Decode(manifest)
	if len(problems) != 0 {
		t.Fatalf("Decode must parse JSONC with comments and trailing commas, got: %v", problems)
	}
	if m.Name != "jsonc-pack" {
		t.Errorf("name = %q, want jsonc-pack", m.Name)
	}
	if len(m.Contributes) != 1 || m.Contributes[0].Into != ".jsonc/skills" {
		t.Errorf("contributions = %+v, want 1 skills contribution", m.Contributes)
	}

	// Also tolerant decoding
	mTolerant, problemsT, skippedT := DecodeTolerant(manifest)
	if len(problemsT) != 0 || len(skippedT) != 0 {
		t.Fatalf("DecodeTolerant must parse JSONC cleanly, got problems: %v, skipped: %v", problemsT, skippedT)
	}
	if mTolerant.Name != "jsonc-pack" {
		t.Errorf("mTolerant name = %q, want jsonc-pack", mTolerant.Name)
	}
}
