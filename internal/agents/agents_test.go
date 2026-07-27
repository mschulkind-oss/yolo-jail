package agents

import "testing"

// B5: machine-wide state is registry DATA, not a hardcoded branch in the mount
// assembler. Claude's credential dir is the only entry today, and the mount must be
// byte-identical to the `if agent == "claude"` branch this replaced.
func TestSharedDirsForIsClaudeOnlyAndSelectionGated(t *testing.T) {
	got := SharedDirsFor([]string{"claude", "codex", "pi"})
	if len(got) != 1 || got[0] != ".claude-shared-credentials" {
		t.Errorf("SharedDirsFor = %v, want [.claude-shared-credentials]", got)
	}
	// Selection-gated: no claude, no machine-wide mount.
	if got := SharedDirsFor([]string{"codex", "pi"}); len(got) != 0 {
		t.Errorf("SharedDirsFor without claude = %v, want none", got)
	}
	// Deduped across a repeated selection.
	if got := SharedDirsFor([]string{"claude", "claude"}); len(got) != 1 {
		t.Errorf("SharedDirsFor duplicate = %v, want one entry", got)
	}
	// The tier is deliberately narrow: anything here leaks between workspaces by
	// design, so a new entry should be a conscious decision rather than drift.
	var all []string
	for _, name := range Order {
		spec, _ := Get(name)
		all = append(all, spec.SharedDirs...)
	}
	if len(all) != 1 {
		t.Errorf("SharedDirs across all agents = %v; adding one leaks state between "+
			"workspaces by design — confirm that is intended, then update this test", all)
	}
}
