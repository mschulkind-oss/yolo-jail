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
// `program` contribution. The four that ship a loophole and no CLI (audio,
// host-processes, journal, cgroup-delegate) have no briefing or skills to deliver and
// are not the subject here.
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
//
// codex and pi are ABSENT rather than assumed: neither CLI was installed in the jail this
// was verified from, so their destinations are unconfirmed. Adding them here needs the
// same kind of evidence, not a plausible-looking path — the copilot bug is what a
// plausible-looking path costs.
func TestAgentSurfacesMatchTheVerifiedPaths(t *testing.T) {
	verified := map[string]struct{ briefing, skills string }{
		"claude":   {".claude/CLAUDE.md", ".claude/skills"},
		"copilot":  {".copilot/copilot-instructions.md", ".copilot/skills"},
		"opencode": {".config/opencode/AGENTS.md", ".config/opencode/skills"},
		"agy":      {".gemini/config/AGENTS.md", ".gemini/config/skills"},
	}
	for _, p := range agentPacks(t) {
		want, checked := verified[p.Name]
		if !checked {
			continue // codex, pi — unverified on purpose; see the doc comment
		}
		if got := destsOf(p, packdecl.KindBriefing); len(got) != 1 || got[0] != want.briefing {
			t.Errorf("pack %q briefing → %v, but %s reads %q", p.Name, got, p.Name, want.briefing)
		}
		if got := destsOf(p, packdecl.KindSkills); len(got) != 1 || got[0] != want.skills {
			t.Errorf("pack %q skills → %v, but %s reads %q", p.Name, got, p.Name, want.skills)
		}
	}
}
