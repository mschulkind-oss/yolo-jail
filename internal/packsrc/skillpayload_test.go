package packsrc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillPayloadGroupedRollbackAndReadOnlyRepair(t *testing.T) {
	remote := gitRepo(t, map[string]string{"initial": "x"})
	gitIn(t, remote, "checkout", "-b", "skills-main")
	for _, p := range []string{"nested/one/SKILL.md", "nested/two/SKILL.md", "nested/three/SKILL.md", "sibling/SKILL.md", "shared/data", "VERSION", "LICENSE", ".fixture-plugin/plugin.json", "hooks/start"} {
		writeSkillFixture(t, remote, p, "original "+p+"\n", 0644)
	}
	writeSkillFixture(t, remote, "nested/one/scripts/probe.sh", "#!/bin/sh\nexit 0\n", 0755)
	if err := os.Symlink("shared", filepath.Join(remote, "safe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/outside", filepath.Join(remote, "escaping")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, remote, "add", "--all")
	gitIn(t, remote, "commit", "-m", "skills")
	c1 := gitIn(t, remote, "rev-parse", "HEAD")
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	now := time.Unix(1800000000, 0)
	opts := SkillResolveOptions{LockPath: lock, Now: func() time.Time { return now }}
	rows := []SkillRequest{}
	for i, p := range []string{"one", "two", "three"} {
		rows = append(rows, SkillRequest{Label: "pack-" + string(rune('a'+i)) + "/" + p, Source: "git+file://" + remote + "//nested/" + p + "?ref=skills-main"})
	}
	result := s.ResolveSkillPayloads(rows, opts)
	if len(result) != 1 || result[0].State != SkillReady || result[0].Commit != c1 {
		t.Fatalf("initial: %+v", result)
	}
	first := result[0]
	if len(first.Plan.Omissions) != 1 || first.Plan.Omissions[0].Path != "escaping" {
		t.Fatalf("omissions: %+v", first.Plan)
	}
	if got, err := os.ReadFile(filepath.Join(first.Payload, "sibling/SKILL.md")); err != nil || string(got) != "original sibling/SKILL.md\n" {
		t.Fatalf("support bytes: %q %v", got, err)
	}
	if fi, err := os.Stat(filepath.Join(first.Payload, "nested/one/scripts/probe.sh")); err != nil || fi.Mode().Perm() != 0555 {
		t.Fatalf("executable: %v %v", fi, err)
	}
	for _, fi := range []string{"VERSION", "nested/one/SKILL.md"} {
		st, _ := os.Stat(filepath.Join(first.Payload, fi))
		if st.Mode().Perm()&0222 != 0 {
			t.Fatal("mutable payload")
		}
	}
	writeSkillFixture(t, remote, "VERSION", "new\n", 0644)
	if err := os.Remove(filepath.Join(remote, "nested/three/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, remote, "add", "--all")
	gitIn(t, remote, "commit", "-m", "invalid later row")
	c2 := gitIn(t, remote, "rev-parse", "HEAD")
	opts.Force = true
	result = s.ResolveSkillPayloads(rows, opts)
	if result[0].Commit != c1 || result[0].State != SkillReady || !strings.Contains(result[0].Warning, "three") || !strings.Contains(result[0].Warning, c2) {
		t.Fatalf("rollback: %+v", result)
	}
	a, _ := Parse(rows[0].Source)
	if _, err := os.Stat(s.SkillPayloadPath(a.Repo, c2)); !os.IsNotExist(err) {
		t.Fatalf("invalid candidate published: %v", err)
	}
	// B can publish c2 for its own valid group; A must refuse against that shared record.
	result = s.ResolveSkillPayloads(rows[:1], opts)
	if result[0].Commit != c2 || result[0].State != SkillReady {
		t.Fatalf("B: %+v", result)
	}
	before, _ := os.ReadFile(lock)
	reader := s.ReadSkillPayloads(rows, lock)
	if reader[0].State != SkillRefused || !strings.Contains(reader[0].Reason, "three") {
		t.Fatalf("A reader: %+v", reader)
	}
	after, _ := os.ReadFile(lock)
	if !bytes.Equal(before, after) {
		t.Fatal("reader wrote record")
	}
	// Delete only payload: valid readers skip and writer repairs locally when not due.
	payload := result[0].Payload
	unsealSkillFixture(t, payload)
	if err := os.RemoveAll(payload); err != nil {
		t.Fatal(err)
	}
	reader = s.ReadSkillPayloads(rows[:1], lock)
	if reader[0].State != SkillNotMaterialized || !strings.Contains(reader[0].Diagnostic(rows[0].Label), "objects present and valid; published payload absent") {
		t.Fatalf("deleted payload: %+v", reader)
	}
	if s.ReadSkillPayloads(rows, lock)[0].State != SkillRefused {
		t.Fatal("payload absence masked invalid row")
	}
	stamp, _ := os.ReadFile(s.stampPath(a))
	gitIn(t, s.mirrorPath(a.Repo), "update-ref", "-d", "refs/heads/skills-main")
	if err := os.Rename(remote, remote+"-gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(remote+"-gone", remote) })
	opts.Force = false
	result = s.ResolveSkillPayloads(rows[:1], opts)
	if result[0].State != SkillReady || result[0].Commit != c2 || result[0].Fetched {
		t.Fatalf("local repair: %+v", result)
	}
	stampAfter, _ := os.ReadFile(s.stampPath(a))
	if !bytes.Equal(stamp, stampAfter) {
		t.Fatal("repair wrote stamp")
	}
	t.Cleanup(func() { unsealSkillFixture(t, filepath.Join(s.Dir, "skill-payloads")) })
}

func writeSkillFixture(t *testing.T, root, p, text string, mode os.FileMode) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}
func unsealSkillFixture(t *testing.T, root string) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(p, fi.Mode().Perm()|0700)
		}
		return nil
	})
}
