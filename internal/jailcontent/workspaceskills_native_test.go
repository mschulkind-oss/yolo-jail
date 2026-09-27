package jailcontent

// workspaceskills_native_test.go pins what the workspace layer does about the skills an agent
// reads NATIVELY — the ones the mirror never delivers, and still has to reckon with. The skip rule
// was applied to the winning copy's own directory only, so an agent that natively reads a
// DIFFERENT directory carrying the same name was sent the winner and saw two skills of that name;
// and an agent that natively reads a skill whose name yolo's built-in holds sees both, which the
// mirror cannot stop and the launch did not say.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// Two source dirs carry `lint` with different content. Whichever wins the name, an agent that
// reads the OTHER dir natively already sees that copy, so it is sent neither; an agent that reads
// neither gets the winner; and the collision says who reads the losing copy. In both orders.
func TestWorkspaceLayerNeverSendsANameAnAgentReadsNatively(t *testing.T) {
	for _, order := range [][]string{{".codex/skills", ".agents/skills"}, {".agents/skills", ".codex/skills"}} {
		t.Run(order[0]+" first", func(t *testing.T) {
			f := newWSFixture(t)
			f.file(t, ".codex/skills/lint/SKILL.md", "CODEX")
			f.file(t, ".agents/skills/lint/SKILL.md", "AGENTS")
			targets(t, map[string][]string{
				"codex": {".codex/skills"},
				"pi":    {".agents/skills"},
				"omp":   nil,
			})
			nativeTo := map[string]string{".codex/skills": "codex", ".agents/skills": "pi"}

			staging, rep := f.stage(t, order)

			for _, agent := range []string{"codex", "pi"} {
				if skillsOf(t, staging, agent)["lint"] {
					data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName(agent), "lint", "SKILL.md"))
					t.Errorf("%s reads a lint natively and was sent another (%q): two skills of one name", agent, data)
				}
			}
			want := map[string]string{".codex/skills": "CODEX", ".agents/skills": "AGENTS"}[order[0]]
			if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("omp"), "lint", "SKILL.md")); string(data) != want {
				t.Errorf("omp reads neither dir and should receive the winner %q, got %q", want, data)
			}
			wantColl := []WorkspaceSkillCollision{{Name: "lint", Winner: order[0], Losers: []string{order[1]},
				ReadNatively: []WorkspaceSkillNativeCopy{{Source: order[1], By: []string{nativeTo[order[1]]}}}}}
			if !reflect.DeepEqual(rep.Collisions, wantColl) {
				t.Errorf("collision report:\n got %+v\nwant %+v", rep.Collisions, wantColl)
			}
			if len(rep.HeldBack) != 0 {
				t.Errorf("the collision line already says who was held back: %+v", rep.HeldBack)
			}
		})
	}
}

// The name rule reaches an entry this reader REFUSED, too: the agent resolves the link in the
// jail, where its target may well be a skill, so a same-named skill from another dir is held back
// from it — and since no collision was recorded, the launch says so on a line of its own.
func TestWorkspaceLayerHoldsBackANameARefusedNativeEntryCarries(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, "node_modules/x/SKILL.md", "the host's per-side copy")
	f.link(t, ".agents/skills/x", "../../node_modules/x")
	f.file(t, ".claude/skills/x/SKILL.md", "claude's x")
	targets(t, map[string][]string{"pi": {".agents/skills"}, "codex": nil})

	staging, rep := f.stage(t, []string{".claude/skills", ".agents/skills"}, func(ws *WorkspaceSkills) {
		ws.PerSide = []string{"node_modules"}
	})

	if skillsOf(t, staging, "pi")["x"] {
		t.Error("pi reads .agents/skills/x natively; claude's x would be a second skill of that name")
	}
	if !skillsOf(t, staging, "codex")["x"] {
		t.Error("codex reads neither dir natively and should receive claude's x")
	}
	want := []WorkspaceSkillHeldBack{{Name: "x", From: ".claude/skills", Native: ".agents/skills", In: []string{"pi"}}}
	if !reflect.DeepEqual(rep.HeldBack, want) {
		t.Errorf("held-back report:\n got %+v\nwant %+v", rep.HeldBack, want)
	}
}

// OQ-WS2 says a workspace skill never shadows yolo's built-in, and in every destination the mirror
// delivers to it does not. An agent that reads the directory NATIVELY is another matter: it sees
// the repo's copy whatever yolo stages, next to the built-in yolo does stage. yolo cannot keep the
// two apart, so it says so — and still stages the built-in.
func TestWorkspaceLayerSaysWhatAnAgentReadsNativelyUnderATakenName(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/configuring-the-jail/SKILL.md", "the repo's own version")
	targets(t, map[string][]string{"pi": {".agents/skills"}, "codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})

	builtin, _ := builtinskills.FS.ReadFile("configuring-the-jail/SKILL.md")
	for _, agent := range []string{"pi", "codex"} {
		got, _ := os.ReadFile(filepath.Join(staging, SkillStagingName(agent), "configuring-the-jail", "SKILL.md"))
		if !bytes.Equal(got, builtin) {
			t.Errorf("%s: the staged configuring-the-jail must be yolo's built-in", agent)
		}
	}
	want := []WorkspaceSkillCompeting{{Name: "configuring-the-jail", Source: ".agents/skills",
		With: []string{"yolo's built-in skill"}, Readers: []string{"pi"}}}
	if !reflect.DeepEqual(rep.Competing, want) {
		t.Errorf("competing report:\n got %+v\nwant %+v", rep.Competing, want)
	}
	if len(rep.Shadowed) != 1 || !reflect.DeepEqual(rep.Shadowed[0].In, []string{"codex"}) {
		t.Errorf("the shadow is codex's alone — pi was never going to be sent a copy: %+v", rep.Shadowed)
	}
}
