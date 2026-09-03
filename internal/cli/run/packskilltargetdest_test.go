package run

// packskilltargetdest_test.go pins that a skills contribution which names no DESTINATION
// never becomes a mount target.
//
// The defect: packSkillTargets set `Dest: c.Into` for every `skills` contribution, and an
// ADDRESSED one (`{"kind":"skills","agents":["claude"]}`) has no `into` by design — it names
// who its content is FOR, and where that agent reads is the agent pack's business
// (briefing-audiences.md P4). So Dest was "", the mount destination collapsed to the home
// root, and podman refused the launch with `"/home/agent": duplicate mount destination` —
// every jail launch, for any pack carrying an addressed skills tree.
//
// Measured 2026-09-03: reproduced at 49bb2088 (before that day's work), so it shipped with
// briefing-audiences steps 1-2 on 2026-09-02. An addressed BRIEFING was unaffected; only
// skills mount.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestPackSkillTargetsSkipsContributionsWithNoDestination(t *testing.T) {
	packs := []*packload.Pack{{
		Name: "house",
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			// addressed: an audience, no destination
			{Kind: packdecl.KindSkills, From: "skills", Agents: []string{"claude"}},
			// an ordinary declaring destination, for contrast
			{Kind: packdecl.KindSkills, Into: ".claude/skills", Agent: "claude"},
		}},
	}}
	targets := packSkillTargets(packs)
	for _, tg := range targets {
		if tg.Dest == "" {
			t.Fatalf("a skills contribution with no `into` became a mount target with an EMPTY "+
				"destination — the bind resolves to the home ROOT and podman refuses the launch "+
				"with a duplicate /home/agent mount. Targets: %+v", targets)
		}
	}
	if len(targets) != 1 {
		t.Fatalf("want exactly the one declaring destination as a target, got %d: %+v",
			len(targets), targets)
	}
	if targets[0].Dest != ".claude/skills" {
		t.Errorf("wrong target survived: %+v", targets[0])
	}
}
