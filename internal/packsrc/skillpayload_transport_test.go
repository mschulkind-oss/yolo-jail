package packsrc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSkillPayloadPromisorCompletionFailureKeepsPairDueAndRetries(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/chosen/SKILL.md": "v1"})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	source := "git+file://" + repo + "//skills/chosen?ref=main"
	a, _ := Parse(source)
	t.Cleanup(func() { _ = RemoveSealedSkillTree(filepath.Dir(s.Dir), s.Dir) })
	now := time.Unix(10000, 0)
	opts := SkillResolveOptions{LockPath: lock, Now: func() time.Time { return now }}
	initial := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if initial.State != SkillReady {
		t.Fatalf("initial: %+v", initial)
	}
	before, err := os.ReadFile(s.stampPath(a))
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(s.stampPath(a))
	gitIn(t, repo, "config", "uploadpack.allowFilter", "true")
	gitIn(t, repo, "config", "uploadpack.allowAnySHA1InWant", "true")
	if err := os.WriteFile(filepath.Join(repo, "new-sparse-blob"), []byte("new supporting data"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "--all")
	gitIn(t, repo, "commit", "-m", "new sparse candidate")
	candidate := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, s.mirrorPath(a.Repo), "config", "remote.origin.promisor", "true")
	gitIn(t, s.mirrorPath(a.Repo), "config", "remote.origin.partialclonefilter", "blob:none")
	wrapper, events := filepath.Join(t.TempDir(), "git"), filepath.Join(t.TempDir(), "events")
	script := fmt.Sprintf("#!/bin/sh\nfor a do if [ \"$a\" = --stdin ]; then printf 'completion-failed\\n' >> %q; echo fixture-blob-completion-failed >&2; exit 1; fi; done\nexec %q \"$@\"\n", events, realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	s.Git = wrapper
	now = now.Add(2 * BranchRefreshInterval)
	failed := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if failed.State != SkillReady || failed.Commit != initial.Commit || !strings.Contains(failed.Warning, "fixture-blob-completion-failed") {
		t.Fatalf("latest-record fallback: %+v", failed)
	}
	if b, _ := os.ReadFile(events); string(b) != "completion-failed\n" {
		t.Fatalf("completion event: %q", b)
	}
	after, _ := os.ReadFile(s.stampPath(a))
	af, _ := os.Stat(s.stampPath(a))
	if !bytes.Equal(before, after) || !fi.ModTime().Equal(af.ModTime()) {
		t.Fatalf("failed completion paced pair: before %q/%s after %q/%s", before, fi.ModTime(), after, af.ModTime())
	}
	current, _ := loadSkillsLock(lock)
	if current.Pairs[skillPair(a.Repo, a.Ref)].Commit != initial.Commit {
		t.Fatal("record advanced on missing blobs")
	}
	if _, err := os.Lstat(s.SkillPayloadPath(a.Repo, candidate)); !os.IsNotExist(err) {
		t.Fatalf("failed candidate published: %v", err)
	}
	s.Git = realGit
	retry := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if retry.State != SkillReady || retry.Commit != candidate || !retry.Fetched {
		t.Fatalf("same-clock retry: %+v", retry)
	}
	stamped, _ := os.ReadFile(s.stampPath(a))
	if string(stamped) != strconv.FormatInt(now.Unix(), 10)+"\n" {
		t.Fatalf("completion success stamp: %q", stamped)
	}
	// A complete but invalid candidate keeps the recorded payload. It is no longer PACED by a stamp
	// (LPC-D1): the probe sees the remote tip differ from the record at every launch.
	if err := os.Remove(filepath.Join(repo, "skills/chosen/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills/chosen/data"), []byte("marker absent"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "--all")
	gitIn(t, repo, "commit", "-m", "complete invalid candidate")
	now = now.Add(2 * BranchRefreshInterval)
	invalid := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if invalid.State != SkillReady || invalid.Commit != candidate || !invalid.Fetched || invalid.Warning == "" {
		t.Fatalf("invalid fallback: %+v", invalid)
	}
	again := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if again.State != SkillReady || again.Commit != candidate {
		t.Fatalf("invalid candidate lost the recorded payload: %+v", again)
	}
}

func TestSkillPayloadRequiredPinFetchFailureDoesNotStampOrdinaryControlStillDoes(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/chosen/SKILL.md": "branch body"})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "config", "uploadpack.allowAnySHA1InWant", "true")
	if err := os.WriteFile(filepath.Join(repo, "skills/chosen/SKILL.md"), []byte("unreferenced pin body"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "--all")
	tree := gitIn(t, repo, "write-tree")
	pin := gitSkillInput(t, repo, "pin fixture\n", "commit-tree", tree, "-p", gitIn(t, repo, "rev-parse", "HEAD"))
	wrapper := filepath.Join(t.TempDir(), "git")
	script := fmt.Sprintf("#!/bin/sh\nfor a do if [ \"$a\" = %q ]; then echo fixture-required-pin-failed >&2; exit 1; fi; done\nexec %q \"$@\"\n", pin, realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	source := "git+file://" + repo + "//skills/chosen?ref=" + pin
	a, _ := Parse(source)
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv(), Git: wrapper}
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	t.Cleanup(func() { _ = RemoveSealedSkillTree(filepath.Dir(s.Dir), s.Dir) })
	opts := SkillResolveOptions{LockPath: lock, Now: func() time.Time { return time.Unix(10000, 0) }}
	failed := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if failed.State != SkillNotFetched || !strings.Contains(failed.Reason, "fixture-required-pin-failed") {
		t.Fatalf("required pin transport lost: %+v", failed)
	}
	if _, err := os.Lstat(s.stampPath(a)); !os.IsNotExist(err) {
		t.Fatalf("failed pin stamped: %v", err)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("failed pin recorded: %v", err)
	}
	s.Git = realGit
	retry := s.ResolveSkillPayloads([]SkillRequest{{Label: "pack/skills[0]", Source: source}}, opts)[0]
	if retry.State != SkillReady || retry.Commit != pin {
		t.Fatalf("pin retry: %+v", retry)
	}
	ordinary := &Store{Dir: t.TempDir(), Env: gitTestEnv(), Git: wrapper}
	out, err := ordinary.Refresh([]RefreshPack{{Name: "ordinary", Source: source}}, RefreshOptions{Force: true})
	if err != nil || len(out) != 1 || out[0].FetchErr != nil || out[0].Err == nil {
		t.Fatalf("ordinary ignored-pin behavior changed: %+v %v", out, err)
	}
	if _, err := os.Stat(ordinary.stampPath(a)); err != nil {
		t.Fatalf("ordinary ref success no longer stamps: %v", err)
	}
}
