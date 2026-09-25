package packdecl

// configlist_test.go pins the `config-list` kind's DECLARATION
// (docs/reference/pack-system.md#adding-entries-to-an-array-config-list): the shape it
// decodes into, every refusal its validation makes, and the projection the engine
// reads. What the engine then DOES with an entry — the fold, the capture, the refusal of a
// surface that cannot capture per entry yet — is internal/agentcfg's and is tested there.

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeListEntry(t *testing.T, entry string) (*Manifest, []string) {
	t.Helper()
	return Decode([]byte(`{"name": "p", "contributes": [` + entry + `]}`))
}

// The motivating case decodes clean and reaches the projection with all three fields, in
// declaration order — a field that decodes but never leaves the Contribution is an entry
// no fold can append.
func TestConfigListDecodesAndTravels(t *testing.T) {
	m, problems := Decode([]byte(`{
		"name": "personal",
		"contributes": [
			{"kind": "config-list", "surface": "pi/settings", "path": "/packages",
			 "add": ["git:github.com/mschulkind/kilo-pi-provider"]},
			{"kind": "config-list", "surface": "pi/settings", "path": "/models/glm-5.3/tags",
			 "add": [1, {"a": [true]}]},
			{"kind": "config-list", "surface": "pi/settings", "path": "/packages", "add": []}
		]
	}`))
	if len(problems) != 0 {
		t.Fatalf("valid config-list contributions were refused: %v", problems)
	}
	got := m.ConfigListContributions()
	if len(got) != 3 {
		t.Fatalf("ConfigListContributions() = %d entries, want 3", len(got))
	}
	if got[0].Surface != "pi/settings" || got[0].Path != "/packages" {
		t.Errorf("entry 0 = %+v, want surface pi/settings, path /packages", got[0])
	}
	var add []string
	if err := json.Unmarshal(got[0].Add, &add); err != nil || len(add) != 1 ||
		add[0] != "git:github.com/mschulkind/kilo-pi-provider" {
		t.Errorf("entry 0 Add = %s (%v), want the one kilo package", got[0].Add, err)
	}
	// Declaration order is fold order, so it must survive the projection.
	if got[1].Path != "/models/glm-5.3/tags" {
		t.Errorf("entry 1 Path = %q — declaration order was not kept", got[1].Path)
	}
	// An EMPTY add is a declared no-op, and it must stay distinguishable from an absent one
	// (which is refused below) all the way to the engine.
	if string(got[2].Add) != "[]" {
		t.Errorf("entry 2 Add = %q, want [] (an empty add is a no-op, not an absent one)", got[2].Add)
	}
	// Other kinds never leak into the projection.
	if ovs := m.ConfigOverlayContributions(); len(ovs) != 0 {
		t.Errorf("config-list entries leaked into ConfigOverlayContributions: %+v", ovs)
	}
}

// Every refusal the kind makes, each naming the field so an author can act on it.
func TestConfigListRefusals(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  []string
	}{
		{"missing surface", `{"kind":"config-list","path":"/packages","add":["x"]}`,
			[]string{`needs "surface"`}},
		{"missing path", `{"kind":"config-list","surface":"pi/settings","add":["x"]}`,
			[]string{`needs "path"`}},
		{"dotted path", `{"kind":"config-list","surface":"pi/settings","path":"packages","add":["x"]}`,
			[]string{".path", "RFC 6901", `"/packages"`}},
		{"bad escape", `{"kind":"config-list","surface":"pi/settings","path":"/a~2b","add":["x"]}`,
			[]string{".path", "~2"}},
		{"root-key pointer", `{"kind":"config-list","surface":"pi/settings","path":"/","add":["x"]}`,
			[]string{".path", "empty"}},
		{"trailing slash", `{"kind":"config-list","surface":"pi/settings","path":"/packages/","add":["x"]}`,
			[]string{".path", "empty"}},
		{"missing add", `{"kind":"config-list","surface":"pi/settings","path":"/packages"}`,
			[]string{`needs "add"`}},
		{"null add", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":null}`,
			[]string{".add", "array", "null"}},
		{"scalar add", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":"x"}`,
			[]string{".add", "array", "string"}},
		{"object add", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":{"a":1}}`,
			[]string{".add", "array", "object"}},
		{"null element", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["x",null]}`,
			[]string{".add[1]", "null", "TOML"}},
		{"nested null", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":[{"src":"x","opt":null}]}`,
			[]string{".add[0]/opt", "null", "TOML"}},
		{"null in a nested array", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["x",["a",null]]}`,
			[]string{".add[1]/1", "null"}},
		{"config body", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["x"],` +
			`"config":{"managed":{"packages":["x"]}}}`,
			[]string{`does not take "config"`, "config-overlay"}},
		{"profile", `{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["x"],"profile":"zai"}`,
			[]string{`does not take "profile"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := decodeListEntry(t, tc.entry)
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", tc.entry)
			}
			joined := strings.Join(problems, "\n")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("problems do not mention %q:\n%s", w, joined)
				}
			}
		})
	}
}

// `path` and `add` are the kind's whole body, so on any other kind they are a declaration
// nothing reads — refused, in `profile`'s position and for its reason. config-overlay gets
// the migration named, because it is the kind an author reaching for "append" writes first.
func TestConfigListFieldsRefusedOnOtherKinds(t *testing.T) {
	cases := []struct {
		name, entry, field string
	}{
		{"overlay path", `{"kind":"config-overlay","surface":"pi/settings","path":"/packages",` +
			`"config":{"managed":{"k":1}}}`, "path"},
		{"overlay add", `{"kind":"config-overlay","surface":"pi/settings","add":["x"],` +
			`"config":{"managed":{"k":1}}}`, "add"},
		{"env add", `{"kind":"env","vars":{"A":"1"},"add":["x"]}`, "add"},
		{"requires path", `{"kind":"requires","bin":"jq","path":"/x"}`, "path"},
		// An EMPTY add is still a declared field: `[]` is present, so it is refused too.
		{"env empty add", `{"kind":"env","vars":{"A":"1"},"add":[]}`, "add"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := decodeListEntry(t, tc.entry)
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, `does not take "`+tc.field+`"`) {
				t.Fatalf("%s: %q on a non-config-list kind was not refused by name; problems:\n%s",
					tc.entry, tc.field, joined)
			}
			if strings.HasPrefix(tc.name, "overlay") && !strings.Contains(joined, `"config-list"`) {
				t.Errorf("the config-overlay refusal does not name kind \"config-list\" as the "+
					"migration:\n%s", joined)
			}
		})
	}
}

// ACROSS THE VERSION BOUNDARY the kind is skew, not structure: DecodeTolerant validates
// what it keeps, so a well-formed config-list decodes with no problem on the tolerant path
// too, and a malformed one is refused there exactly as on the strict path (both builds
// understand a list with no "add").
func TestConfigListTolerantPath(t *testing.T) {
	good := `{"name":"p","contributes":[{"kind":"config-list","surface":"pi/settings",` +
		`"path":"/packages","add":["x"]}]}`
	m, problems, skipped := DecodeTolerant([]byte(good))
	if len(problems) != 0 || len(skipped) != 0 {
		t.Fatalf("tolerant decode of a valid config-list: problems=%v skipped=%v", problems, skipped)
	}
	if got := m.ConfigListContributions(); len(got) != 1 || got[0].Path != "/packages" {
		t.Errorf("tolerant projection = %+v", got)
	}
	bad := `{"name":"p","contributes":[{"kind":"config-list","surface":"pi/settings","path":"/packages"}]}`
	if _, problems, _ := DecodeTolerant([]byte(bad)); len(problems) == 0 {
		t.Error("tolerant decode accepted a config-list with no \"add\"")
	}
}

// pack.json is JSON5 on disk, and cleanJSON5 re-marshals it before the strict decode — so
// what the projection carries is canonical JSON, not the author's bytes. An integer entry
// must reach the engine as a number (not a string, not dropped), which is the value the
// engine's canonical-JSON equality then compares.
func TestConfigListJSON5Entries(t *testing.T) {
	m, problems := Decode([]byte(`{
		name: "p",
		contributes: [
			// a comment, and a trailing comma
			{kind: "config-list", surface: "pi/settings", path: "/ports", add: [8080, 'x',],},
		],
	}`))
	if len(problems) != 0 {
		t.Fatalf("JSON5 config-list refused: %v", problems)
	}
	got := m.ConfigListContributions()
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	var add []any
	if err := json.Unmarshal(got[0].Add, &add); err != nil {
		t.Fatalf("Add %q is not a JSON array: %v", got[0].Add, err)
	}
	if len(add) != 2 || add[0] != float64(8080) || add[1] != "x" {
		t.Errorf("Add = %#v, want [8080 \"x\"]", add)
	}
}
