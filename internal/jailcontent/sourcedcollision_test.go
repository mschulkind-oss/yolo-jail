package jailcontent

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcedBuiltinCollisionAtJailNotch(t *testing.T) {
	for _, prefix := range []string{"", "yolo"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			shippedPrefixHome(t, prefix)
			for _, name := range BuiltinSkillClaimNames(prefix) {
				reg := &packload.SourcedSkill{Row: 0, By: packdecl.Contribution{Kind: packdecl.KindSkills, Source: "git+https://example.invalid/r.git//skills/x?ref=main", As: name}}
				sources := []PackSkillSource{{Pack: "selected", Registration: reg}}
				targets := []SkillTarget{{Staging: "skills-selected", Dest: ".fixture/skills", Agent: "fixture"}}
				if err := SkillCollisionError(sources, targets); err == nil || !strings.Contains(err.Error(), "selected") || !strings.Contains(err.Error(), "yolo's built-in skill") {
					t.Errorf("builtin %q must refuse both claims: %v", name, err)
				}
				targets[0].Reserved = []string{name}
				if err := SkillCollisionError(sources, targets); err != nil {
					t.Fatalf("reserved row must be withheld instead: %v", err)
				}
			}
		})
	}
}

func TestSourcedWorkspaceNameIsShadowedAtRealJailComposition(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/same/SKILL.md", "workspace marker")
	targets(t, map[string][]string{"fixture": nil})
	marker := []byte("---\nname: upstream\n---\nsourced marker\n")
	row := &packload.SourcedSkill{By: packdecl.Contribution{Kind: packdecl.KindSkills, Source: "git+https://example.invalid/r//skills/x?ref=main", As: "same"}, Outcome: packsrc.SkillPayloadResult{State: packsrc.SkillReady}, Plan: &packsrc.SkillRowPlan{Marker: marker, Path: "skills/x"}, PayloadRoot: f.ws}
	old := PackSkillDirs()
	SetPackSkillDirs([]PackSkillSource{{Pack: "selected", Registration: row}})
	t.Cleanup(func() { SetPackSkillDirs(old) })
	root, report, err := PrepareSkillsWith("sourced-workspace-shadow", &WorkspaceSkills{Root: f.ws, Dirs: []string{".agents/skills"}})
	if err != nil {
		t.Fatalf("workspace claim became fatal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "skills-fixture/same/SKILL.md"))
	if err != nil || !strings.Contains(string(data), "sourced marker") || strings.Contains(string(data), "workspace marker") {
		t.Fatalf("workspace not shadowed: %s %v report=%+v", data, err, report)
	}
}
