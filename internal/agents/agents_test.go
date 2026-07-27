package agents

import (
	"fmt"
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

// D5, now completed: "no agent by default" was already SUPPORTED (`agents: []`
// yielded an empty selection every consumer handled; verified booting in a real
// nested jail). What this test used to also pin was the DEFAULT — DefaultAgents
// == ["claude"], with nil meaning "use it" and an explicit [] meaning "none".
//
// That default is GONE, and with it the nil/empty distinction: an agent reaches a
// jail because a configured pack installs it, and no pack means no agent. So nil
// and empty are the same answer here, and it is the honest one — a registry that
// substituted "claude" for "nothing configured" would put an agent in a jail whose
// config never asked for one, silently. The user-facing half of that decision is a
// printed warning at launch (internal/cli/run), not a fabricated selection.
//
// The rest of this test is the permanent regression: every derived view must
// tolerate an empty selection without panicking and without inventing an agent.
func TestEmptyAgentSelectionIsSupported(t *testing.T) {
	// No default: nil and empty are BOTH "no agents". nil is the case worth
	// pinning — it is what an absent config key and an unset YOLO_AGENTS decode to,
	// so a fallback reintroduced here would be invisible until a jail came up with
	// an agent nobody selected.
	if got := ResolveAgents(nil); len(got) != 0 {
		t.Errorf("ResolveAgents(nil) = %v, want none — there is no default agent set", got)
	}
	if got := ResolveAgents([]string{}); len(got) != 0 {
		t.Errorf("an explicitly empty selection must stay empty, got %v", got)
	}

	// Every derived view must tolerate it without panicking or inventing an agent.
	for _, names := range [][]string{nil, {}} {
		if got := SharedDirsFor(names); len(got) != 0 {
			t.Errorf("SharedDirsFor(%v) = %v, want none", names, got)
		}
	}
	// HERMETIC: PrepareSkills stages under paths.AgentsDir(), which derives from
	// GlobalStorage() -> $HOME. Without redirecting HOME this writes into the real
	// shared store, and a stale skills-<agent> dir left by ANY earlier run (or an
	// earlier iteration of this loop) then shows up as "skills were staged" — a
	// failure that has nothing to do with the code under test. Setting HOME makes
	// each run read only what it wrote.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for i, names := range [][]string{nil, {}} {
		staging, err := PrepareSkills(fmt.Sprintf("c-empty-%d", i), home, names, false)
		if err != nil {
			t.Fatalf("PrepareSkills with no agents (%v): %v", names, err)
		}
		entries, err := os.ReadDir(staging)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("no agents selected (%v) but skills were staged: %v", names, entries)
		}
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
