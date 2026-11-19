package packsrc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillPayloadOmittedIntermediateLinkSurvivesPublicationAndLaunchCopy(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/x/SKILL.md": "selected", "real": "support"})
	gitIn(t, repo, "checkout", "-b", "skills-main")
	// B and b collide on a case-insensitive filesystem, so they are built in the
	// Git tree directly rather than in the working tree.
	indexSkillFixture(t, repo, "100644", "B", "case collision")
	indexSkillFixture(t, repo, "120000", "a", "b")
	indexSkillFixture(t, repo, "120000", "b", "real")
	gitIn(t, repo, "commit", "-m", "supporting links")
	store := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	t.Cleanup(func() { _ = RemoveSealedSkillTree(filepath.Dir(store.Dir), store.Dir) })
	rows := []SkillRequest{{Label: "pack selected skills[0]", Source: "git+file://" + repo + "//skills/x?ref=skills-main"}}
	result := store.ResolveSkillPayloads(rows, SkillResolveOptions{LockPath: filepath.Join(t.TempDir(), SkillsLockName)})[0]
	if result.State != SkillReady {
		t.Fatalf("publication: %+v", result)
	}
	omitted := map[string]bool{}
	for _, entry := range result.Plan.Omissions {
		omitted[entry.Path] = true
	}
	for _, name := range []string{"a", "b", "B"} {
		if !omitted[name] {
			t.Fatalf("missing omission %q: %+v", name, result.Plan.Omissions)
		}
	}
	base := t.TempDir()
	target := filepath.Join(base, "launch-payload")
	t.Cleanup(func() { _ = RemoveSealedSkillTree(base, target) })
	if err := CopySkillPayload(result.Payload, target); err != nil {
		t.Fatalf("READY could not deploy: %v", err)
	}
	plan, err := ReadSkillPayloadTreeBeneath(base, target, rows)
	if err != nil || len(plan.Omissions) != 0 {
		t.Fatalf("recorded physical plan: %+v %v", plan, err)
	}
	for _, name := range []string{"a", "b", "B"} {
		if _, err := os.Lstat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatalf("omitted link/name copied: %s %v", name, err)
		}
	}
}
