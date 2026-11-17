package hostskills

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourcedClaimPack(t *testing.T, name string) *packload.Pack {
	t.Helper()
	return &packload.Pack{Name: name, Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Source: "git+https://example.invalid/repo//skills/chosen?ref=main", As: "same"}, {Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}}}}
}
func TestSourcedClaimsRefuseOtherPackAndConventionalLocalSkillsBeforeWriting(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "other-sourced-pack", true: "conventional-local"}[local], func(t *testing.T) {
			first := sourcedClaimPack(t, "first")
			second := sourcedClaimPack(t, "second")
			if local {
				second.Name = "local"
				second.Local = true
				second.Decl.Contributes[0] = packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills"}
				dir := filepath.Join(second.Root, "skills/same")
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("local"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			packs, _ := packload.ResolveDestinations([]*packload.Pack{first, second})
			home := t.TempDir()
			plan := ComposeHostSkillsFor(packs, home, nil)
			if _, err := RenderHostSkills(plan, ComposeRequest{}, false); err == nil || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), second.Name) {
				t.Fatalf("expected named collision before unresolved row read: %v", err)
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Fatal("collision wrote destination")
			}
		})
	}
}
func TestSourcedReservedNameWithholdsWithoutFailureOrDelivery(t *testing.T) {
	p := sourcedClaimPack(t, "selected")
	p.Decl.Contributes[1].Reserved = []string{"same"}
	packs, _ := packload.ResolveDestinations([]*packload.Pack{p})
	home := t.TempDir()
	plan := ComposeHostSkillsFor(packs, home, nil)
	results, err := RenderHostSkills(plan, ComposeRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, result := range results {
		if result.Action == ActionRefused {
			t.Fatalf("withholding refused: %+v", result)
		}
		if result.Action == ActionReserved {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("no withholding action: %+v", results)
	}
	if _, err := os.Lstat(filepath.Join(home, ".fixture/skills/same")); !os.IsNotExist(err) {
		t.Fatalf("reserved row delivered: %v", err)
	}
	if cols := Collisions(plan); len(cols) != 0 {
		t.Fatalf("reserved claim collided: %+v", cols)
	}
}
