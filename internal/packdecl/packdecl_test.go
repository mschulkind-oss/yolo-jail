package packdecl

// packdecl_test.go pins the VERSION BOUNDARY behavior of manifest decoding: strict where a
// human is authoring, tolerant where two builds of yolo meet.

import "testing"

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
	// `tier` stands in for any field a newer build adds; futureThing for one nothing knows.
	manifest := []byte(`{"name":"acme","futureThing":{"a":1},
		"contributes":[{"kind":"skills","from":"skills","into":".acme/skills",
		"tier":"namespaced","futureField":"x"}]}`)

	m, problems := DecodeTolerant(manifest)
	if len(problems) != 0 {
		t.Fatalf("DecodeTolerant must ignore unknown fields, got %v", problems)
	}
	if m.Name != "acme" {
		t.Errorf("name = %q, want acme", m.Name)
	}
	// The KNOWN fields still decode — tolerance must not mean "skip the entry".
	cs := m.Contributions()
	if len(cs) != 1 || cs[0].Kind != KindSkills || cs[0].Into != ".acme/skills" {
		t.Fatalf("known fields must still decode: %+v", cs)
	}
	if cs[0].Tier != "namespaced" {
		t.Errorf("tier = %q, want namespaced", cs[0].Tier)
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
	for name, manifest := range map[string]string{
		"unknown kind":  `{"name":"a","contributes":[{"kind":"nonsense"}]}`,
		"missing field": `{"name":"a","contributes":[{"kind":"skills","from":"skills"}]}`,
		"bad tier":      `{"name":"a","contributes":[{"kind":"skills","from":"s","into":"i","tier":"nope"}]}`,
	} {
		if _, problems := DecodeTolerant([]byte(manifest)); len(problems) == 0 {
			t.Errorf("%s: DecodeTolerant must still report structural problems", name)
		}
	}
}
