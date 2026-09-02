// Package packload_test is EXTERNAL (not `package packload`) so it can import
// internal/packreg, which is what registers the embedded packs with packload. An
// in-package test cannot: packreg imports packload, so the blank import would be a cycle.
// Without it Embedded() is empty and every assertion below passes vacuously.
package packload_test

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs
)

// agentPacks returns the embedded packs that INSTALL an agent — the ones with a
// `program` contribution. The packs that ship no CLI (today audio, host-processes,
// journal, cgroup-delegate, serial and zai) deliver no briefing or skills and are
// not the subject here.
func agentPacks(t *testing.T) []*packload.Pack {
	t.Helper()
	var out []*packload.Pack
	for _, p := range packload.Embedded() {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindProgram {
				out = append(out, p)
				break
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no embedded pack declares a program — this test is not testing anything")
	}
	return out
}

func destsOf(p *packload.Pack, kind packdecl.Kind) []string {
	var out []string
	for _, c := range p.Decl.Contributions() {
		if c.Kind == kind {
			out = append(out, c.Into)
		}
	}
	return out
}

// TestEveryAgentPackDeliversBothSurfaces is the regression for a gap that shipped and
// went unnoticed: the opencode pack declared a `briefing` and NO `skills`, so opencode
// received no pack skills at all at the user level.
//
// It hid for two reasons worth knowing. Nothing errors — an agent with no skills
// destination is simply absent from the union `run.packSkillTargets` builds, which is the
// same shape as "no packs ship skills", a legitimate state. And opencode ALSO auto-loads
// `~/.claude/skills`, so in the common jail where the claude pack is selected too, skills
// appeared to work; only an opencode-alone jail exposed it.
//
// So the invariant is asserted structurally: a pack that installs an agent delivers BOTH
// surfaces or names itself here with a reason. An agent that genuinely has no user-level
// skills directory is a legitimate exception — it is just not one anybody may add silently.
func TestEveryAgentPackDeliversBothSurfaces(t *testing.T) {
	// name → why this agent has no skills destination. Empty today; an entry needs a
	// reason a reader can check, not a name.
	noSkills := map[string]string{}

	for _, p := range agentPacks(t) {
		if got := destsOf(p, packdecl.KindBriefing); len(got) == 0 {
			t.Errorf("pack %q installs an agent but declares no `briefing` destination — "+
				"every house rule the user writes is silently dropped for it", p.Name)
		}
		if got := destsOf(p, packdecl.KindSkills); len(got) == 0 {
			if why, ok := noSkills[p.Name]; ok {
				t.Logf("pack %q has no skills destination, by exception: %s", p.Name, why)
				continue
			}
			t.Errorf("pack %q installs an agent but declares no `skills` destination — no "+
				"pack's skills reach it, and nothing errors because an absent destination is "+
				"indistinguishable from a jail whose packs ship no skills. If the agent truly "+
				"has no user-level skills dir, add it to noSkills with the reason.", p.Name)
		}
	}
}

// TestAgentSurfaceDestinationsAreDistinct: two agent packs sharing a destination would
// mean one agent's briefing is the other's, and `files`/`skills` collisions across packs
// are exactly what packload.Collisions exists to refuse. Cheap to assert, and it is the
// property that makes "the union of destinations" a safe model.
func TestAgentSurfaceDestinationsAreDistinct(t *testing.T) {
	for _, kind := range []packdecl.Kind{packdecl.KindBriefing, packdecl.KindSkills} {
		owner := map[string]string{}
		for _, p := range agentPacks(t) {
			for _, dest := range destsOf(p, kind) {
				if prev, dup := owner[dest]; dup {
					t.Errorf("%s destination %q is declared by BOTH %s and %s",
						kind, dest, prev, p.Name)
					continue
				}
				owner[dest] = p.Name
			}
		}
	}
}

// TestAgentSurfacesMatchTheVerifiedPaths pins each shipped destination against what the
// agent's own binary was OBSERVED to read, on 2026-08-31, by reading the installed
// binaries in a jail (`strings`). A destination the agent does not read is a briefing
// nobody sees — silent, permanent, and invisible to every other test in this repo, which
// can only check that yolo wrote the file it said it would.
//
// Evidence, per agent:
//
//   - claude 2.1.251 — "Skills: `~/.claude/skills/<name>/SKILL.md`"; CLAUDE.md is its
//     documented user memory file.
//   - copilot (@github/copilot) — skills: its own /skills help lists "Personal:
//     ~/.copilot/skills/, ~/.agents/skills/, or ~/.claude/skills/". Briefing: the
//     user-level instruction file is `~/.copilot/copilot-instructions.md`, resolved by
//     `HK(Ra(ctx), "copilot-instructions.md")` where Ra() is `~/.copilot` (its second
//     argument is unused). There is NO user-level AGENTS.md read anywhere in the CLI —
//     AGENTS.md is project-scoped only (conventionPaths ["."], plus a cwd walk-up that
//     never reaches $HOME from /workspace). The pack pointed at `~/.copilot/AGENTS.md`
//     until 2026-08-31, so copilot had never once received a briefing.
//   - opencode 1.18.18 — briefing: `join(config, "AGENTS.md")` with config =
//     `~/.config/opencode`. Skills: its path table gives "Global skills |
//     ~/.config/opencode/skill(s)/<name>/SKILL.md".
//   - agy — "Global Discovery: `~/.gemini/config/`", where a standalone AGENTS.md is
//     "always active for their directory scope", and "`~/.gemini/config/skills/<name>/`".
//   - pi 0.84.4 (JS, so this is the code itself) — skills:
//     `join(resolvedAgentDir, "skills")` with `getAgentDir()` =
//     `join(homedir(), CONFIG_DIR_NAME, "agent")` and CONFIG_DIR_NAME = ".pi"
//     (dist/core/skills.js:348, dist/config.js:425). Briefing:
//     `loadContextFileFromDir(resolvedAgentDir)` over candidates
//     ["AGENTS.override.md", "AGENTS.md", …] (dist/core/resource-loader.js:33,87).
//   - codex 0.151.0 — the only one verified by RUNNING it rather than reading it, because
//     the Rust binary builds both paths at runtime and carries no literal to grep (all it
//     ships is the crate path `codex-home/src/instructions/mod.rs` and the message
//     "Failed to read global AGENTS.md instructions from `"). `codex debug prompt-input`
//     renders the model-visible prompt with no API call: with CODEX_HOME pointed at a
//     scratch dir, a marker in $CODEX_HOME/AGENTS.md and one in
//     $CODEX_HOME/skills/<name>/SKILL.md BOTH appear in the output, and renaming the
//     AGENTS.md away removes its marker — the control that makes the first observation
//     mean something.
//
// The bar for an entry here is evidence of that kind, never a plausible-looking path.
// `~/.copilot/AGENTS.md` was plausible for as long as this repo existed.
func TestAgentSurfacesMatchTheVerifiedPaths(t *testing.T) {
	verified := map[string]struct{ briefing, skills string }{
		"claude":   {".claude/CLAUDE.md", ".claude/skills"},
		"copilot":  {".copilot/copilot-instructions.md", ".copilot/skills"},
		"opencode": {".config/opencode/AGENTS.md", ".config/opencode/skills"},
		"agy":      {".gemini/config/AGENTS.md", ".gemini/config/skills"},
		"pi":       {".pi/agent/AGENTS.md", ".pi/agent/skills"},
		"codex":    {".codex/AGENTS.md", ".codex/skills"},
	}
	if len(verified) != len(agentPacks(t)) {
		t.Errorf("%d agent packs but %d verified — an agent pack whose destinations nobody "+
			"checked is how `~/.copilot/AGENTS.md` survived; verify it or say why here",
			len(agentPacks(t)), len(verified))
	}
	for _, p := range agentPacks(t) {
		want, checked := verified[p.Name]
		if !checked {
			continue // reported by the count check above
		}
		if got := destsOf(p, packdecl.KindBriefing); len(got) != 1 || got[0] != want.briefing {
			t.Errorf("pack %q briefing → %v, but %s reads %q", p.Name, got, p.Name, want.briefing)
		}
		if got := destsOf(p, packdecl.KindSkills); len(got) != 1 || got[0] != want.skills {
			t.Errorf("pack %q skills → %v, but %s reads %q", p.Name, got, p.Name, want.skills)
		}
	}
}
