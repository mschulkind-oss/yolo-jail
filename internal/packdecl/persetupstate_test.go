package packdecl

// persetupstate_test.go pins the top-level `per_setup_state` carve-out key
// (docs/design/unified-workspace-home.md §6.3): decoded through JSON and TOML, every refusal
// naming its field, and the older reader that ignores it.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const perSetupStateState = `{"kind":"state","at":".tool","scope":"workspace"}`

func decodePerSetupState(t *testing.T, decls string) (*Manifest, []string) {
	t.Helper()
	return Decode([]byte(`{"per_setup_state":` + decls + `,"contributes":[` + perSetupStateState + `]}`))
}

func TestPerSetupStateDecodesAndRefusesBadNames(t *testing.T) {
	m, problems := decodePerSetupState(t, `[{"dir":".tool","paths":["agent/npm","session-store.db"]}]`)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := []PerSetupState{{Dir: ".tool", Paths: []string{"agent/npm", "session-store.db"}}}
	if got := m.PerSetupState(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PerSetupState() = %+v, want %+v", got, want)
	}
	got := m.PerSetupState()
	got[0].Paths[0] = "edited"
	if m.PerSetupState()[0].Paths[0] != "agent/npm" {
		t.Error("PerSetupState() aliases the manifest")
	}

	cases := []struct{ name, decls, want string }{
		{"dotdot", `[{"dir":".tool","paths":["agent/../x"]}]`, `per_setup_state[0].paths[0]: "agent/../x"`},
		{"absolute", `[{"dir":".tool","paths":["/etc/passwd"]}]`, `per_setup_state[0].paths[0]: "/etc/passwd"`},
		{"glob", `[{"dir":".tool","paths":["a","*.db"]}]`, `per_setup_state[0].paths[1]: "*.db"`},
		{"a lock name", `[{"dir":".tool","paths":["agent/x.lock"]}]`, `per_setup_state[0].paths[0]: "agent/x.lock"`},
		{"empty segment", `[{"dir":".tool","paths":["a//b"]}]`, `per_setup_state[0].paths[0]: "a//b"`},
		{"trailing slash", `[{"dir":".tool","paths":["a/"]}]`, `per_setup_state[0].paths[0]: "a/"`},
		{"empty name", `[{"dir":".tool","paths":[""]}]`, `per_setup_state[0].paths[0]`},
		{"no paths", `[{"dir":".tool"}]`, `per_setup_state[0].paths: is required`},
		{"no dir", `[{"paths":["a"]}]`, `per_setup_state[0].dir: is required`},
		{"dir not a state dir", `[{"dir":".other","paths":["a"]}]`, `per_setup_state[0].dir: ".other" is not one of`},
		{"absolute dir", `[{"dir":"/tool","paths":["a"]}]`, `per_setup_state[0].dir: must be relative`},
		{"duplicate dir", `[{"dir":".tool","paths":["a"]},{"dir":".tool","paths":["b"]}]`, `per_setup_state[1].dir: ".tool" is declared twice`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := decodePerSetupState(t, c.decls)
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, c.want)
			}
		})
	}
}

func TestPerSetupStateDecodesFromTOML(t *testing.T) {
	m, problems := DecodeTOML([]byte(`name = "carve"
per_setup_state = [{ dir = ".tool", paths = ["agent/npm"] }]

[[state]]
at = ".tool"
`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := []PerSetupState{{Dir: ".tool", Paths: []string{"agent/npm"}}}
	if got := m.PerSetupState(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PerSetupState() = %+v, want %+v", got, want)
	}
}

// TestAnOlderDecoderIgnoresPerSetupState: the key is top-level so a reader that predates it
// ignores it, where a field on a contribution would make an older reader drop that contribution
// (the reason session_homes is top-level too). The older reader is modeled by the tolerant JSON
// decode of the manifest's fields that predate the key.
func TestAnOlderDecoderIgnoresPerSetupState(t *testing.T) {
	doc := []byte(`{"name":"carve","per_setup_state":[{"dir":".tool","paths":["agent/npm"]}],
	  "contributes":[` + perSetupStateState + `,{"kind":"state","at":".other","scope":"workspace","future_field":1}]}`)
	var older struct {
		Name        string         `json:"name"`
		Contributes []Contribution `json:"contributes"`
	}
	if err := json.Unmarshal(doc, &older); err != nil {
		t.Fatalf("an older decode must not fail on the key: %v", err)
	}
	if older.Name != "carve" || len(older.Contributes) != 2 {
		t.Fatalf("an older decode lost the pack: %+v", older)
	}
	// This build's tolerant jail read keeps the key AND skips only the contribution it cannot read.
	m, problems, _ := DecodeTolerant(doc)
	if len(problems) != 0 || m == nil || len(m.PerSetupState()) != 1 {
		t.Fatalf("tolerant read: problems=%v manifest=%+v", problems, m)
	}
}
