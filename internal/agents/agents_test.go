package agents

import (
	"os"
	"strings"
	"testing"
)

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

// D5: "no agent by default" is ALREADY SUPPORTED — `agents: []` yields an empty
// selection that every consumer handles. Verified in a real nested jail: it boots.
//
// What is NOT changed here is the DEFAULT itself. DefaultAgents stays ["claude"],
// because flipping it to [] is a breaking UX change for every existing user (their
// jail would come up with no agent after an upgrade), and that is a product decision
// rather than an implementation one. The machinery is ready either way; this test
// pins that readiness so the switch is a one-line change plus a migration note.
func TestEmptyAgentSelectionIsSupported(t *testing.T) {
	// ResolveAgents distinguishes "nil = use the default" from "explicitly empty".
	if got := ResolveAgents(nil); len(got) == 0 {
		t.Error("nil should fall back to DefaultAgents")
	}
	if got := ResolveAgents([]string{}); len(got) != 0 {
		t.Errorf("an explicitly empty selection must stay empty, got %v", got)
	}

	// Every derived view must tolerate it without panicking or inventing an agent.
	if got := SharedDirsFor([]string{}); len(got) != 0 {
		t.Errorf("SharedDirsFor([]) = %v, want none", got)
	}
	home := t.TempDir()
	staging, err := PrepareSkills("c-empty", home, []string{}, false)
	if err != nil {
		t.Fatalf("PrepareSkills with no agents: %v", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("no agents selected but skills were staged: %v", entries)
	}
}

// F2: the credential-boundary field set is {HostFiles, Briefing.HostSource, Skills}.
// This pins the SHAPE of that claim so a future pack-declared-surface mechanism cannot
// cover only the field named "HostFiles" and believe it covered the boundary.
func TestCredentialBoundaryFieldsAreAllHostHomeReads(t *testing.T) {
	for _, name := range Order {
		spec, _ := Get(name)
		// Every host-home read must be a plain relative path under $HOME: an absolute
		// path or a `..` escape would read outside the user's home entirely.
		for field, p := range map[string]string{
			"HostFiles.Dir":       spec.HostFiles.Dir,
			"Briefing.HostSource": spec.Briefing.HostSource,
			"Skills":              spec.Skills,
		} {
			if p == "" {
				continue
			}
			if strings.HasPrefix(p, "/") {
				t.Errorf("%s %s = %q: must be $HOME-relative", name, field, p)
			}
			if strings.Contains(p, "..") {
				t.Errorf("%s %s = %q: must not escape the home dir", name, field, p)
			}
		}
	}
}
