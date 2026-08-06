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

	m, problems := DecodeTolerant(manifest)
	if len(problems) != 0 {
		t.Fatalf("DecodeTolerant must ignore unknown fields, got %v", problems)
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
	m, tolerated := DecodeTolerant(manifest)
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
	manifest := []byte(`{"name":"acme","allow_exec":true,
		"contributes":[{"kind":"skills","from":"skills","into":".acme/skills"}]}`)
	if _, problems := Decode(manifest); len(problems) == 0 {
		t.Error("Decode must reject an unknown field — a typo'd key that silently does " +
			"nothing is the worst outcome for a pack author")
	}
}

// Tolerance is about UNKNOWN fields only. A manifest that is malformed in a way BOTH builds
// understand — an unknown kind, a missing required field — must still fail loudly, or the
// tolerant path becomes a way to ship a broken pack into a jail.
func TestDecodeTolerantStillValidatesStructure(t *testing.T) {
	// A RETIRED field is deliberately absent from this list — it is a version-skew fact rather
	// than a structural one, which is the split TestRetiredContributionTierIsAuthoringOnly pins.
	// `bad skills_tier` replaces the old `bad tier` case: a value NEITHER build understands is
	// still malformed in a way both agree on, so it must fail at both decoders.
	for name, manifest := range map[string]string{
		"unknown kind":    `{"name":"a","contributes":[{"kind":"nonsense"}]}`,
		"missing field":   `{"name":"a","contributes":[{"kind":"skills","from":"skills"}]}`,
		"bad skills_tier": `{"name":"a","skills_tier":"nope"}`,
	} {
		if _, problems := DecodeTolerant([]byte(manifest)); len(problems) == 0 {
			t.Errorf("%s: DecodeTolerant must still report structural problems", name)
		}
	}
}
