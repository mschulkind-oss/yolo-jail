package packsrc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillPayloadOmittedIntermediateLinkSurvivesPublicationAndLaunchCopy(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/x/SKILL.md": "selected", "real": "support", "B": "case collision"})
	gitIn(t, repo, "checkout", "-b", "skills-main")
	for name, target := range map[string]string{"a": "b", "b": "real"} {
		if err := os.Symlink(target, filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "--all")
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
