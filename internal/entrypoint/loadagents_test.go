package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadAgents used to fall back to a default agent set (claude) whenever
// YOLO_AGENTS was absent or unparseable. That fallback is gone: agents arrive via
// packs, so "the host selected nothing" must provision nothing.
//
// This is the in-jail mirror of TestEmptyAgentSelectionIsSupported, and it is the
// one that would have hidden a regression the longest. The host CLI at least
// prints what it is doing; the entrypoint runs at boot inside the container with
// nobody reading its output, so a reinstated fallback here would install an agent
// CLI and mount its host files with no visible sign that the config never asked
// for it.
func TestLoadAgentsHasNoDefaultSet(t *testing.T) {
	cases := map[string]map[string]string{
		"unset":            {},
		"empty string":     {"YOLO_AGENTS": ""},
		"empty JSON list":  {"YOLO_AGENTS": "[]"},
		"malformed JSON":   {"YOLO_AGENTS": "[not json"},
		"wrong JSON shape": {"YOLO_AGENTS": `{"claude":true}`},
		// A list of names none of which are known agents. Unknown names are dropped,
		// and dropping every one must leave zero — not trigger a fallback.
		"all names unknown": {"YOLO_AGENTS": `["gemini","nosuchagent"]`},
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			vars["JAIL_HOME"] = t.TempDir()
			if got := LoadAgents(NewEnv(vars)); len(got) != 0 {
				t.Errorf("LoadAgents = %v, want none — there is no default agent set", got)
			}
		})
	}

	// A well-formed selection still resolves, in order, with unknown names dropped.
	// Without this the test above would also pass on a LoadAgents that returned nil
	// unconditionally.
	e := NewEnv(map[string]string{
		"JAIL_HOME":   t.TempDir(),
		"YOLO_AGENTS": `["codex","gemini","claude"]`,
	})
	got := LoadAgents(e)
	want := []string{"codex", "claude"}
	if len(got) != len(want) {
		t.Fatalf("LoadAgents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LoadAgents = %v, want %v", got, want)
		}
	}
}

// The boot generators that iterate the selected agents must produce a working
// jail when the selection is empty, rather than erroring or emitting a
// half-written file. A jail with no agent is now a legitimate state (a user with
// no packs configured gets one), so "empty selection" is a supported input to
// provisioning, not an edge case.
func TestGeneratorsTolerateNoAgents(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_AGENTS":    "[]",
		"YOLO_WORKSPACE": filepath.Join(home, "workspace"),
	})

	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers with no agents: %v", err)
	}
	// No agent selected means no agent launcher. The shim dir may hold blocked-tool
	// shims in a real boot, but nothing generated an agent CLI here.
	entries, err := os.ReadDir(e.ShimDir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, ent := range entries {
		t.Errorf("no agents selected but a launcher was written: %s", ent.Name())
	}

	// .bashrc still renders — it just carries no agent aliases.
	if err := GenerateBashrc(e); err != nil {
		t.Fatalf("GenerateBashrc with no agents: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err != nil {
		t.Errorf("no agents selected but .bashrc was not written: %v", err)
	}
	if got := agentAliases(e); got != "" {
		t.Errorf("agentAliases = %q, want empty", got)
	}
}
