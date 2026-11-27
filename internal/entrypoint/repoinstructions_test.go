package entrypoint

// repoinstructions_test.go is the BEHAVIORAL proof of the jail's half of `repo_instructions`
// (docs/reference/workspace-instruction-files.md): the boot reads YOLO_REPO_INSTRUCTIONS, hands it to
// every pack LoadJailPacks loads, and the agent's own file is the one that carries the setting. Each
// test reads the file the agent would read, or the flags its launcher would carry. Deleting
// LoadJailPacks' ApplyInstructions call, or the Collect pass that places the declaration, fails them.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/repoinstr"
)

// stagedInstructionsPack writes a one-pack YOLO_PACK_ROOT shaped like packs/claude and packs/pi: a
// claude settings surface that already carries one of the user's own exclusions, the claude
// declaration over it, and pi's launch-flag declaration.
func stagedInstructionsPack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"agents","contributes":[
 {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
   "defaults":{"claudeMdExcludes":["/mine/**/CLAUDE.md"]}}]},
 {"kind":"config","config":[{"agent":"codex","name":"config","codec":"toml","path":"~/.codex/config.toml"}]},
 {"kind":"instructions","agent":"codex","bin":"codex","reads":["AGENTS.md"],
   "ignore":{"set":{"surface":"codex/config","config":{"project_doc_max_bytes":0}}}},
 {"kind":"instructions","agent":"claude","bin":"claude","reads":["AGENTS.md","CLAUDE.md"],
   "ignore":{"list":{"surface":"claude/settings","path":"/claudeMdExcludes",
     "add":["${workspace}/**/AGENTS.md","${workspace}/**/CLAUDE.md"]}}},
 {"kind":"instructions","agent":"pi","bin":"pi","reads":["AGENTS.md"],
   "ignore":{"launch":{"flags":["--no-context-files"]}},
   "redeliver":{"flag":"--append-system-prompt","file":".pi/agent/AGENTS.md"}},
 {"kind":"instructions","agent":"agy","bin":"agy","reads":["GEMINI.md"]}
]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func bootWithRepoInstructions(t *testing.T, wire string) (*Env, string) {
	t.Helper()
	e, _ := overlayRenderEnv(t)
	e.Vars["YOLO_PACK_ROOT"] = stagedInstructionsPack(t)
	// A temporary workspace, never the literal /workspace: CI's runner has none and cannot make one,
	// while a jail has one, so the literal passed in a jail and failed in CI.
	e.Workspace = t.TempDir()
	if wire != "" {
		e.Vars[repoinstr.EnvName] = wire
	}
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot failed: %v", fails)
	}
	return e, e.Home
}

func excludesAt(t *testing.T, home string) []any {
	t.Helper()
	got, _ := readRenderedJSON(t, home, ".claude/settings.json")["claudeMdExcludes"].([]any)
	return got
}

func TestBootRendersClaudesExclusionsAnchoredAtTheWorkspaceBesideTheUsersOwn(t *testing.T) {
	e, home := bootWithRepoInstructions(t, `{"mode":"ignore"}`)
	ws := e.Workspace
	want := []any{"/mine/**/CLAUDE.md", ws + "/**/AGENTS.md", ws + "/**/CLAUDE.md"}
	if got := excludesAt(t, home); !reflect.DeepEqual(got, want) {
		t.Errorf("claudeMdExcludes = %#v, want the user's entry then the workspace-anchored ones %#v", got, want)
	}
}

func TestBootRendersNothingForAKeepLaunch(t *testing.T) {
	for _, wire := range []string{"", `{"mode":"keep"}`} {
		_, home := bootWithRepoInstructions(t, wire)
		if got := excludesAt(t, home); !reflect.DeepEqual(got, []any{"/mine/**/CLAUDE.md"}) {
			t.Errorf("wire %q: claudeMdExcludes = %#v, want only the user's own entry", wire, got)
		}
	}
}

func TestBootHandsPiItsFlagAndTheBriefingBackAndAgyNothing(t *testing.T) {
	e, _ := bootWithRepoInstructions(t, `{"mode":"replace"}`)
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	if got := launchFlagsFor(e, packs, "pi"); got == nil ||
		!reflect.DeepEqual(got.Flags, []string{"--no-context-files", "--append-system-prompt", e.Home + "/.pi/agent/AGENTS.md"}) {
		t.Errorf("pi's carrier flags = %+v", got)
	}
	if got := launchFlagsFor(e, packs, "agy"); got != nil {
		t.Errorf("an agent with no setting was handed flags %+v; nothing masks or rewrites it", got)
	}
	if got := launchFlagsFor(e, packs, "claude"); got != nil {
		t.Errorf("claude's setting is a rendered file, not a flag, in a jail: %+v", got)
	}
}

// codex's setting is an INTEGER in its TOML file: the manifest's JSON round trip makes it a float,
// which the codec would write as `0.0` and codex would refuse.
func TestBootWritesCodexsProjectDocLimitAsAnInteger(t *testing.T) {
	_, home := bootWithRepoInstructions(t, `{"mode":"ignore"}`)
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "project_doc_max_bytes = 0\n") {
		t.Errorf("codex config:\n%s", data)
	}
	_, home = bootWithRepoInstructions(t, "")
	if data, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); strings.Contains(string(data), "project_doc_max_bytes") {
		t.Errorf("a keep launch wrote the setting:\n%s", data)
	}
}
