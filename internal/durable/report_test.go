package durable

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMeasureFollowsNoLinkAndStopsAtItsBudget(t *testing.T) {
	ws := workspaceDir(t)
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "big"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "worktrees", "land", "src"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "worktrees", "land", "src", "a"), make([]byte, 100), 0o644))
	must(os.WriteFile(filepath.Join(dir, "notes"), make([]byte, 20), 0o644))
	must(os.Symlink(outside, filepath.Join(dir, "linkdir")))
	must(os.Symlink(filepath.Join(outside, "big"), filepath.Join(dir, "linkfile")))

	sz, err := Measure(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sz.Bytes != 120 || sz.Partial {
		t.Errorf("Measure = %+v, want 120 bytes, complete", sz)
	}

	// A clock that jumps past the deadline at once: the walk stops and says so.
	calls := 0
	base := time.Now()
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return base
		}
		return base.Add(time.Hour)
	}
	sz, err = Measure(dir, time.Second, clock)
	if err != nil || !sz.Partial {
		t.Errorf("a walk past its budget = %+v, %v; want partial", sz, err)
	}

	if _, err := Measure(filepath.Join(ws, "absent"), 0, nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an absent dir gave %v", err)
	}
}

// writeRegistration fabricates git's admin files for one worktree, as `git worktree add`
// leaves them: gitdir names the worktree's .git file, HEAD the checkout.
func writeRegistration(t *testing.T, ws, id, gitdir, head string, mtime time.Time, locked bool) {
	t.Helper()
	admin := filepath.Join(ws, ".git", "worktrees", id)
	if err := os.MkdirAll(admin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"gitdir": gitdir + "\n", "HEAD": head + "\n", "index": ""} {
		p := filepath.Join(admin, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if locked {
		if err := os.WriteFile(filepath.Join(admin, "locked"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The scan reads the admin files, in either frame's spelling: a worktree made in the jail
// records /workspace/…, which the host maps to its own path through an alias.
func TestScanDirCountsDurableWorktreesFromTheAdminFiles(t *testing.T) {
	ws := workspaceDir(t)
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, wt := range []string{"land", "fix-footer"} {
		if err := os.MkdirAll(filepath.Join(dir, "worktrees", wt), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "worktrees", wt, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Made in the jail: the jail's spelling.
	writeRegistration(t, ws, "land", "/workspace/.yolo/durable/worktrees/land/.git",
		"1a2b3c4d5e6f", now.Add(-2*24*time.Hour), true)
	// Made on the host: the host's spelling.
	writeRegistration(t, ws, "fix-footer", filepath.Join(dir, "worktrees", "fix-footer", ".git"),
		"ref: refs/heads/fix-footer", now.Add(-41*24*time.Hour), false)
	// Outside the durable dir, and gone: the /tmp incident class.
	writeRegistration(t, ws, "tmpland", "/tmp/definitely-gone-"+filepath.Base(ws)+"/land/.git",
		"ref: refs/heads/x", now, false)

	sc := ScanDir(ScanOptions{Workspace: ws, Durable: dir, Aliases: map[string]string{"/workspace": ws}})
	if sc.Err != nil || sc.NotGit {
		t.Fatalf("scan: %+v", sc)
	}
	if len(sc.Worktrees) != 2 {
		t.Fatalf("worktrees = %+v, want land and fix-footer", sc.Worktrees)
	}
	oldest, newest := sc.Worktrees[0], sc.Worktrees[1]
	if oldest.Name() != "fix-footer" || oldest.Describe() != "fix-footer" || oldest.Locked {
		t.Errorf("oldest = %+v", oldest)
	}
	if newest.Name() != "land" || newest.Describe() != "detached at 1a2b3c4d" || !newest.Locked {
		t.Errorf("newest = %+v", newest)
	}
	if len(sc.Others) != 1 || sc.Others[0] != "notes" {
		t.Errorf("others = %v", sc.Others)
	}
	if len(sc.Prunable) != 0 {
		t.Errorf("the host frame checked registrations outside the durable dir: %v", sc.Prunable)
	}

	sc = ScanDir(ScanOptions{Workspace: ws, Durable: dir, Aliases: map[string]string{"/workspace": ws}, CheckGone: true})
	if len(sc.Prunable) != 1 || !strings.HasSuffix(sc.Prunable[0], "/land") || !strings.HasPrefix(sc.Prunable[0], "/tmp/") {
		t.Errorf("prunable = %v, want the one /tmp registration", sc.Prunable)
	}

	lines := LaunchLines(sc, Size{Bytes: 1_200_000_000}, nil, WalkBudget, now)
	want := []string{
		"Durable dir: 2 worktrees and 1 other entry, 1.2 GB, oldest idle 41 days (fix-footer) — `yolo check` lists them; yolo deletes nothing here.",
		"Durable dir: 1 worktree registration points at paths that no longer exist (" + sc.Prunable[0] + ") — `git worktree prune -n -v` lists what a prune would drop.",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("launch lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	partial := LaunchLines(sc, Size{Bytes: 2_000_000_000, Partial: true}, nil, WalkBudget, now)
	if !strings.Contains(partial[0], "≥ 2.0 GB (size walk stopped at 2s)") {
		t.Errorf("a partial walk: %q", partial[0])
	}
}

// Silence when there is nothing to say, and the directory count when there is no repository.
func TestLaunchLinesDegenerateCases(t *testing.T) {
	ws := workspaceDir(t)
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if lines := LaunchLines(ScanDir(ScanOptions{Workspace: ws, Durable: dir}), Size{}, nil, WalkBudget, now); len(lines) != 0 {
		t.Errorf("an empty durable dir printed %q", lines)
	}
	if err := os.MkdirAll(filepath.Join(dir, "worktrees", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc := ScanDir(ScanOptions{Workspace: ws, Durable: dir})
	if !sc.NotGit || sc.WorktreeDirs != 1 {
		t.Fatalf("no repository: %+v", sc)
	}
	lines := LaunchLines(sc, Size{Bytes: 5}, nil, WalkBudget, now)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Durable dir: 1 directory under worktrees/, 5 B — ") {
		t.Errorf("no repository: %q", lines)
	}
}

// Against real git: a worktree made with `git worktree add --lock` into the durable dir is a
// durable worktree, and the branch and lock are read back from git's own admin files.
func TestScanDirReadsWhatRealGitWrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	ws := workspaceDir(t)
	dir, err := Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws, "-c", "user.name=t", "-c", "user.email=t@t",
			"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "c")
	git("worktree", "add", "-q", "--lock", "-b", "topic", filepath.Join(dir, "worktrees", "land"))
	sc := ScanDir(ScanOptions{Workspace: ws, Durable: dir, CheckGone: true})
	if len(sc.Worktrees) != 1 {
		t.Fatalf("scan: %+v", sc)
	}
	w := sc.Worktrees[0]
	if w.Name() != "land" || w.Branch != "topic" || !w.Locked || w.LastActive.IsZero() {
		t.Errorf("worktree = %+v", w)
	}
	if len(sc.Prunable) != 0 {
		t.Errorf("a live worktree was called prunable: %v", sc.Prunable)
	}
}
