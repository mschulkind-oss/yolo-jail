package durable

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A LINKED `.yolo` IS REFUSED BY EVERY READER (DS-D3). The jail writes `.yolo` through the
// workspace bind, so it can replace it with a link to any host directory; a by-path open of
// `.yolo/durable` would then list and size that directory on the host's terminal. The scan
// and both measurements refuse it, and none of the other directory's names come back.
func TestALinkedYoloIsRefusedByTheScanAndTheMeasurements(t *testing.T) {
	ws := workspaceDir(t)
	other := workspaceDir(t)
	otherDurable, err := Ensure(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(otherDurable, "private-name", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDurable, "private-name", "f"), make([]byte, 99), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, ".yolo"), filepath.Join(ws, ".yolo")); err != nil {
		t.Fatal(err)
	}

	sc := ScanDir(ScanOptions{Workspace: ws})
	var linked *paths.LinkedStateDirError
	if !errors.As(sc.Err, &linked) || len(sc.Others) != 0 {
		t.Errorf("the scan followed a linked .yolo: err=%v others=%v", sc.Err, sc.Others)
	}
	if sz, err := Measure(ws, 0, nil); !errors.As(err, &linked) || sz.Bytes != 0 {
		t.Errorf("Measure followed a linked .yolo: %+v, %v", sz, err)
	}
	if sz, err := MeasureRel(ws, "private-name", 0, nil); !errors.As(err, &linked) || sz.Bytes != 0 {
		t.Errorf("MeasureRel followed a linked .yolo: %+v, %v", sz, err)
	}
	if err := Check(ws); !errors.As(err, &linked) {
		t.Errorf("Check(linked .yolo) = %v, want a LinkedStateDirError", err)
	}
	if got := Reason(Check(ws), ws); got != "`.yolo` is a symbolic link" {
		t.Errorf("Reason = %q", got)
	}
}

// Check makes nothing and says nothing is wrong when the dir is absent or real.
func TestCheckCreatesNothing(t *testing.T) {
	ws := workspaceDir(t)
	if err := Check(ws); err != nil {
		t.Errorf("Check(no .yolo) = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".yolo")); !os.IsNotExist(err) {
		t.Errorf("Check created .yolo: %v", err)
	}
	if _, err := Ensure(ws); err != nil {
		t.Fatal(err)
	}
	if err := Check(ws); err != nil {
		t.Errorf("Check(real dir) = %v", err)
	}
}

// A REGISTRATION OUTSIDE THE GONE ROOTS IS NEVER CALLED GONE. From a container jail a
// worktree the host made beside the repository cannot be stat'ed, and the report called it
// gone at every launch; only one below the workspace or the per-launch set is judged.
func TestASiblingHostWorktreeIsNotCalledGone(t *testing.T) {
	ws := workspaceDir(t)
	if _, err := Ensure(ws); err != nil {
		t.Fatal(err)
	}
	scratch := workspaceDir(t)
	now := time.Now()
	sibling := filepath.Join(filepath.Dir(ws), "not-visible-from-the-jail", "sib")
	writeRegistration(t, ws, "sib", sibling+"/.git", "ref: refs/heads/sib", now, false)
	inScratch := filepath.Join(scratch, "land")
	writeRegistration(t, ws, "land", inScratch+"/.git", "ref: refs/heads/land", now, false)
	inWorkspace := filepath.Join(ws, ".claude", "worktrees", "gone")
	writeRegistration(t, ws, "gone", inWorkspace+"/.git", "ref: refs/heads/gone", now, false)

	sc := ScanDir(ScanOptions{Workspace: ws, GoneRoots: []string{ws, scratch}})
	if strings.Join(sc.Prunable, ",") != inWorkspace+","+inScratch {
		t.Errorf("prunable = %v, want the workspace and per-launch registrations and not %s", sc.Prunable, sibling)
	}
}

// A WORKSPACE REACHED THROUGH A LINK still finds its durable worktrees: git records a
// worktree's path with every link resolved, so a comparison against the linked spelling
// found none (darwin's /tmp and /var/folders; AGENTS.md's PATH-RESOLUTION class).
func TestAWorkspaceReachedThroughALinkFindsItsWorktrees(t *testing.T) {
	real := workspaceDir(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dir, err := Ensure(link)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(dir, "worktrees", "land")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// What git writes: the resolved path.
	writeRegistration(t, link, "land", filepath.Join(real, ".yolo", "durable", "worktrees", "land", ".git"),
		"ref: refs/heads/land", time.Now(), true)
	sc := ScanDir(ScanOptions{Workspace: link, GoneRoots: []string{link}})
	if len(sc.Worktrees) != 1 || sc.Worktrees[0].Name() != "land" || len(sc.Prunable) != 0 {
		t.Errorf("scan through a linked workspace: worktrees=%+v prunable=%v", sc.Worktrees, sc.Prunable)
	}
}

// Every jail-chosen string the report hands out is Printable: an ESC, OSC or bidi override in
// a directory name or branch never reaches the host terminal.
func TestJailChosenNamesArePrintable(t *testing.T) {
	ws := workspaceDir(t)
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	evil := "a\x1b]8;;http:x\x07b\u202ec"
	if err := os.Mkdir(filepath.Join(dir, evil), 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(dir, "worktrees", evil)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRegistration(t, ws, "e", filepath.Join(wt, ".git"), "ref: refs/heads/"+evil, time.Now(), false)
	sc := ScanDir(ScanOptions{Workspace: ws})
	if len(sc.Worktrees) != 1 {
		t.Fatalf("scan: %+v", sc)
	}
	w := sc.Worktrees[0]
	for _, s := range append([]string{w.Name(), w.RelName(), w.Describe()}, sc.Others...) {
		if strings.ContainsAny(s, "\x1b\x07\u202e") {
			t.Errorf("%q reached the report unfiltered", s)
		}
	}
	if w.Name() != "a]8;;http:xbc" {
		t.Errorf("Name = %q", w.Name())
	}
}

// THE REMOVAL ADVICE KEEPS UNCOMMITTED WORK. The briefing and `yolo check` once said
// `git worktree remove -f -f`, which overrides the lock AND the refusal to delete a dirty
// tree. The advice's safe form, run against real git on a locked worktree holding a new
// file, refuses and keeps the file; only the form it names as discarding changes deletes it.
func TestTheRemovalAdviceRefusesADirtyWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if !strings.Contains(RemoveAdvice, "`git worktree unlock <path> && git worktree remove <path>`") ||
		!strings.Contains(RemoveAdvice, "`git worktree remove -f -f <path>` discards them") {
		t.Fatalf("RemoveAdvice = %q", RemoveAdvice)
	}
	ws := workspaceDir(t)
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", ws, "-c", "user.name=t", "-c", "user.email=t@t",
			"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		return cmd.Run()
	}
	wt := filepath.Join(dir, "worktrees", "land")
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "c"},
		{"worktree", "add", "-q", "--lock", wt}} {
		if err := git(args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "unlanded"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git("worktree", "unlock", wt); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := git("worktree", "remove", wt); err == nil {
		t.Error("`git worktree remove` removed a worktree holding uncommitted work")
	}
	if _, err := os.Stat(filepath.Join(wt, "unlanded")); err != nil {
		t.Errorf("the uncommitted file is gone: %v", err)
	}
}
