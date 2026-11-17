package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

func TestSkillPayloadWaitingWriterReloadsSharedRecord(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/a/SKILL.md": "a1", "skills/b/SKILL.md": "b1"})
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	t.Cleanup(func() { unsealSkillFixture(t, s.Dir) })
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	a := SkillRequest{Label: "A/skills[0]", Source: "git+file://" + repo + "//skills/a?ref=main"}
	b := SkillRequest{Label: "B/skills[0]", Source: "git+file://" + repo + "//skills/b?ref=main"}
	initial := s.ResolveSkillPayloads([]SkillRequest{a, b}, SkillResolveOptions{LockPath: lock})[0]
	if initial.State != SkillReady {
		t.Fatalf("initial: %+v", initial)
	}
	if err := os.Remove(filepath.Join(repo, "skills/a/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeSkillFixture(t, repo, "skills/a/data", "a without marker", 0644)
	c2 := commitFile(t, repo, "skills/b/SKILL.md", "b2")
	// B validates the same commit via its separate pin pair, before taking the
	// actual branch mirror lock. Publication below is the short leaf operation
	// while that lock is held; A's public writer must reload AFTER waiting.
	pin := b
	pin.Source = strings.Replace(pin.Source, "ref=main", "ref="+c2, 1)
	staged := s.ResolveSkillPayloads([]SkillRequest{pin}, SkillResolveOptions{LockPath: lock})[0]
	if staged.State != SkillReady {
		t.Fatalf("B candidate: %+v", staged)
	}
	addr, _ := Parse(a.Source)
	unlock, err := s.lockMirror(addr.Repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	waiting := make(chan struct{}, 1)
	done := make(chan SkillPayloadResult, 1)
	go func() {
		done <- s.ResolveSkillPayloads([]SkillRequest{a}, SkillResolveOptions{LockPath: lock, Waiting: func(string) { waiting <- struct{}{} }})[0]
	}()
	select {
	case <-waiting:
	case <-time.After(30 * time.Second):
		t.Fatal("writer did not reach held mirror lock")
	}
	if err := s.saveSkillRecord(lock, SkillRecord{Repo: addr.Repo, Ref: addr.Ref, Commit: c2, Kind: "branch"}, nil); err != nil {
		t.Fatal(err)
	}
	unlock()
	unlock = nil
	select {
	case got := <-done:
		if got.State != SkillRefused || got.Commit != c2 || !strings.Contains(got.Reason, "SKILL.md") {
			t.Fatalf("stale result: %+v", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("writer did not complete after mirror release")
	}
	if got := s.ReadSkillPayloads([]SkillRequest{b}, lock)[0]; got.State != SkillReady || got.Commit != c2 {
		t.Fatalf("B reader: %+v", got)
	}
	if got := s.ReadSkillPayloads([]SkillRequest{pin}, lock)[0]; got.State != SkillReady || got.Commit != c2 {
		t.Fatalf("pin: %+v", got)
	}
}

func TestSkillPayloadReaderIsPureForMissingAndValidObjects(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/a/SKILL.md": "a"})
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	t.Cleanup(func() { unsealSkillFixture(t, s.Dir) })
	lock := filepath.Join(s.Dir, SkillsLockName)
	row := SkillRequest{Label: "fixture", Source: "git+file://" + repo + "//skills/a?ref=main"}
	before, err := treedigest.Of(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ReadSkillPayloads([]SkillRequest{row}, lock)[0]; got.State != SkillNotFetched {
		t.Fatalf("never fetched: %+v", got)
	}
	after, _ := treedigest.Of(s.Dir)
	if before != after {
		t.Fatal("never-fetched reader wrote store")
	}
	res := s.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: lock})[0]
	if res.State != SkillReady {
		t.Fatalf("initial: %+v", res)
	}
	before, _ = treedigest.Of(s.Dir)
	if got := s.ReadSkillPayloads([]SkillRequest{row}, lock)[0]; got.State != SkillReady {
		t.Fatalf("valid reader: %+v", got)
	}
	after, _ = treedigest.Of(s.Dir)
	if before != after {
		t.Fatal("valid reader changed mirror, record, stamp or payload")
	}
	addr, _ := Parse(row.Source)
	if err := os.RemoveAll(s.mirrorPath(addr.Repo)); err != nil {
		t.Fatal(err)
	}
	before, _ = treedigest.Of(s.Dir)
	if got := s.ReadSkillPayloads([]SkillRequest{row}, lock)[0]; got.State != SkillNotFetched {
		t.Fatalf("missing objects: %+v", got)
	}
	after, _ = treedigest.Of(s.Dir)
	if before != after {
		t.Fatal("missing-object reader wrote or lazily fetched")
	}
}
