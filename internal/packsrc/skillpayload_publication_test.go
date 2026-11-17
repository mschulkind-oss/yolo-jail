package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillPayloadWriterRefusesPlantedPublicationAncestor(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/chosen/SKILL.md": "chosen"})
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	outside := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(outside, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
	if err := os.Symlink(outside, filepath.Join(s.Dir, "skill-payloads")); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	result := s.ResolveSkillPayloads([]SkillRequest{{Label: "selected/skills[0]", Source: "git+file://" + repo + "//skills/chosen?ref=main"}}, SkillResolveOptions{LockPath: lock})[0]
	if result.State != SkillRefused || !strings.Contains(result.Reason, "symlink") {
		t.Fatalf("planted ancestor accepted: %+v", result)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("record advanced: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside writes: %v %v", entries, err)
	}
}
