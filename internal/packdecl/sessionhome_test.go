package packdecl

// sessionhome_test.go pins the top-level `session_homes` declaration
// (docs/design/per-session-profiles.md §4.4, SP-D13): decoded strictly, every refusal the plan
// names, and the shipped pi pack's one declaration. Through Decode and the real validator.

import (
	"reflect"
	"strings"
	"testing"

	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

const sessionHomeState = `{"kind":"state","at":".tool","scope":"workspace"}`

func decodeSessionHomes(t *testing.T, homes string) (*Manifest, []string) {
	t.Helper()
	return Decode([]byte(`{"session_homes":` + homes + `,"contributes":[` + sessionHomeState + `]}`))
}

func TestASessionHomeDeclarationDecodes(t *testing.T) {
	m, problems := decodeSessionHomes(t, `[{"dir":".tool/agent","env":"TOOL_DIR",
	  "shared":["auth.json","sessions/"],"prefer":{"agents/":"~/.agents"}}]`)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := []SessionHome{{Dir: ".tool/agent", Env: "TOOL_DIR", Shared: []string{"auth.json", "sessions/"},
		Prefer: map[string]string{"agents/": "~/.agents"}}}
	if got := m.SessionHomes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionHomes() = %+v, want %+v", got, want)
	}
	got := m.SessionHomes()
	got[0].Shared[0] = "edited"
	if m.SessionHomes()[0].Shared[0] != "auth.json" {
		t.Error("SessionHomes() aliases the manifest")
	}
}

func TestASessionHomeDeclarationRefuses(t *testing.T) {
	cases := []struct{ name, homes, want string }{
		{"absolute dir", `[{"dir":"/tool/agent","env":"TOOL_DIR"}]`, "session_homes[0].dir: must be relative"},
		{"dotdot dir", `[{"dir":".tool/../x","env":"TOOL_DIR"}]`, "session_homes[0].dir: must not contain"},
		{"empty dir", `[{"env":"TOOL_DIR"}]`, "session_homes[0].dir: is required"},
		{"dir not under a state dir", `[{"dir":".other/agent","env":"TOOL_DIR"}]`, "session_homes[0].dir: \".other/agent\" is not under"},
		{"dir equal to the state dir", `[{"dir":".tool","env":"TOOL_DIR"}]`, "session_homes[0].dir: \".tool\" is not under"},
		{"bad env", `[{"dir":".tool/agent","env":"1BAD"}]`, "session_homes[0].env: \"1BAD\" is not a valid"},
		{"missing env", `[{"dir":".tool/agent"}]`, "session_homes[0].env: \"\" is not a valid"},
		{"nested shared name", `[{"dir":".tool/agent","env":"T","shared":["a/b"]}]`, "session_homes[0].shared[0]: \"a/b\" must be one name"},
		{"lock shared name", `[{"dir":".tool/agent","env":"T","shared":["auth.json.lock"]}]`, "session_homes[0].shared[0]: \"auth.json.lock\" is a lock"},
		{"lock dir shared name", `[{"dir":".tool/agent","env":"T","shared":["x.lock/"]}]`, "is a lock"},
		{"dotdot shared name", `[{"dir":".tool/agent","env":"T","shared":[".."]}]`, "must be one name"},
		{"empty shared name", `[{"dir":".tool/agent","env":"T","shared":[""]}]`, "must be one name"},
		{"prefer outside the home", `[{"dir":".tool/agent","env":"T","prefer":{"agents/":"/etc"}}]`, "session_homes[0].prefer[\"agents/\"]: \"/etc\" must start with ~/"},
		{"prefer dotdot", `[{"dir":".tool/agent","env":"T","prefer":{"agents/":"~/../x"}}]`, "must not contain"},
		{"prefer lock name", `[{"dir":".tool/agent","env":"T","prefer":{"a.lock":"~/.a"}}]`, "is a lock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := decodeSessionHomes(t, c.homes)
			if !containsSubstr(problems, c.want) {
				t.Errorf("problems = %v, want one containing %q", problems, c.want)
			}
		})
	}
}

// The shipped pi pack decodes strictly and declares exactly one session home: its config dir,
// stock pi's own relocation variable, and the shared names the plan's measurements list.
func TestTheShippedPiPackDeclaresOneSessionHome(t *testing.T) {
	m := decodeShippedPack(t, officialpacks.FS, "pi")
	homes := m.SessionHomes()
	if len(homes) != 1 {
		t.Fatalf("pi declares %d session homes, want 1: %+v", len(homes), homes)
	}
	h := homes[0]
	// The variable's spelling lives in packs/pi alone (SP-P4); here it need only be one.
	if h.Dir != ".pi/agent" || !ValidEnvName(h.Env) {
		t.Errorf("pi's session home = %+v", h)
	}
	for _, name := range []string{"auth.json", "models-store.json", "mcp-auth.json", "sessions/"} {
		found := false
		for _, s := range h.Shared {
			found = found || s == name
		}
		if !found {
			t.Errorf("pi's shared names %v lack %q", h.Shared, name)
		}
	}
	if h.Prefer["agents/"] != "~/.agents" {
		t.Errorf("pi's prefer = %v, want agents/ -> ~/.agents", h.Prefer)
	}
}

// The shipped omp pack declares one session home on stock pi's variable (SP-D37): its own
// agent directory, its login store shared, and no lock among the shared names (SP-D10).
func TestTheShippedOmpPackDeclaresOneSessionHome(t *testing.T) {
	omp := decodeShippedPack(t, officialpacks.FS, "omp").SessionHomes()
	pi := decodeShippedPack(t, officialpacks.FS, "pi").SessionHomes()
	if len(omp) != 1 || len(pi) != 1 {
		t.Fatalf("omp declares %d session homes and pi %d, want 1 each", len(omp), len(pi))
	}
	h := omp[0]
	if h.Dir != ".oh-omp/agent" || h.Env != pi[0].Env {
		t.Errorf("omp's session home = %+v, want .oh-omp/agent on pi's %s", h, pi[0].Env)
	}
	found := false
	for _, s := range h.Shared {
		found = found || s == "agent.db"
		if strings.HasSuffix(s, ".lock") {
			t.Errorf("omp shares the lock %q", s)
		}
	}
	if !found {
		t.Errorf("omp's shared names %v lack its login store agent.db", h.Shared)
	}
}
