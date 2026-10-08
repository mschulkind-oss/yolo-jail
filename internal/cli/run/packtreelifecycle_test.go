package run

// packtreelifecycle_test.go drives a FRESH container launch through Run to its two ends that no
// other unit test reaches, and asks what they leave of the launch's pack tree (packtree.go): a
// runtime that never started, whose tree must go, and a container the teardown cannot prove gone,
// whose tree must stay — Run's deferred discardUnheldPackTree included, which is the last thing
// that could still remove it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// freshLaunch drives Run down the podman fresh-launch path with no packs, a prebuilt jail prefix
// and a ready image, to the runtime's own start. path is the PATH the launch execs its runtime
// from. Every `ps` answers that no container of the name exists until the file started exists,
// and psAnswer from then on; started == "" is a runtime that cannot be run at all, so no `ps`
// answers, as when it is not on PATH.
func freshLaunch(t *testing.T, path, started, psAnswer string) (ws, cname, stdout, stderr string, rc int) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws = t.TempDir()
	cname = yoloruntime.FromWorkspace(ws)
	t.Setenv("PATH", path)
	var out, errOut bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &out, &errOut, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch {
		case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(strings.Join(argv, " "), "--format"):
			return ExecResult{Ran: true, RC: 0}
		case started == "" && len(argv) >= 3 && argv[1] == "ps" && argv[2] == "-q":
			// The attach decision's question is answered — nothing is running — because a
			// runtime that could not say refuses the launch there (probeRunningContainer),
			// and this helper is for reaching the runtime's own start.
			return ExecResult{Ran: true, RC: 0}
		case started == "":
			return ExecResult{Ran: false}
		case len(argv) >= 2 && argv[1] == "ps":
			if _, err := os.Stat(started); err != nil {
				return ExecResult{Ran: true, RC: 0}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: psAnswer}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	dir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	rc = Run(*o)
	return ws, cname, out.String(), errOut.String(), rc
}

// TestARuntimeThatNeverStartedLeavesNoPackTree: the fresh path hands its tree to the container
// just before the runtime runs (packTreeHeld), so when the runtime is not on PATH at all no
// container ever held it, and the runErr branch must take the tree and its live record with it.
// Nothing else can: Run's deferred discard leaves a held tree alone, and the teardown's
// forgetGoneContainer asks a runtime that cannot answer, which removes nothing.
func TestARuntimeThatNeverStartedLeavesNoPackTree(t *testing.T) {
	_, cname, stdout, stderr, rc := freshLaunch(t, t.TempDir(), "", "")
	if rc != 1 || !strings.Contains(stdout, "not found on PATH") {
		t.Fatalf("the launch did not reach the runtime's start and fail there, so this test says "+
			"nothing about that branch: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if trees := packTreesUnder(t, cname); len(trees) != 0 {
		t.Errorf("a runtime that never started left its pack tree: %v", trees)
	}
	if _, err := os.Stat(paths.LivePackTreeRecord(cname)); !os.IsNotExist(err) {
		t.Errorf("a runtime that never started left the live-tree record naming its tree (%v)", err)
	}
}

// TestATreeTheTeardownCannotProveGoneOutlivesRun: the container ran and exited, and the runtime
// still lists a container of the name, so the teardown (forgetGoneContainer) keeps the tree: a
// jail may still be using it. Run's own deferred discard must keep it too, since it is the last
// thing that runs and the only one left that could remove it.
func TestATreeTheTeardownCannotProveGoneOutlivesRun(t *testing.T) {
	// The runtime here always lists the container, so the keeper's confirmGone would poll its
	// whole bound (restartPollAttempts x restartPollInterval, 15 s) before leaving it unkept.
	saved := keeperGoneAttempts
	keeperGoneAttempts = 2
	t.Cleanup(func() { keeperGoneAttempts = saved })
	bin, started := t.TempDir(), filepath.Join(t.TempDir(), "started")
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\n: > '"+started+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, cname, stdout, stderr, rc := freshLaunch(t, bin+":/bin:/usr/bin", started, "abc123\n")
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("the launch never ran its runtime: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if strings.Contains(stdout, "not found on PATH") || strings.Contains(stdout, "Refusing") {
		t.Fatalf("the launch did not run its container: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	trees := packTreesUnder(t, cname)
	if len(trees) != 1 {
		t.Fatalf("a tree whose container the runtime still lists is gone after Run: %v\nstdout:\n%s", trees, stdout)
	}
	raw, err := os.ReadFile(paths.LivePackTreeRecord(cname))
	if err != nil || strings.TrimSpace(string(raw)) != filepath.Base(trees[0]) {
		t.Errorf("the live-tree record no longer names the kept tree %s: %q (%v)", trees[0], raw, err)
	}
}

// TestAHeldTreeIsNotDiscardedAtReturn is the same rule at the method: a tree the container holds
// is not Run's to remove.
func TestAHeldTreeIsNotDiscardedAtReturn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-tree-held"
	tree := newTreeForTest(t, cname, "claude")
	o := &Options{packTree: tree, packTreeHeld: true}
	o.discardUnheldPackTree(cname)
	if !isDir(tree) {
		t.Error("discardUnheldPackTree removed a tree a started container holds")
	}
	o.packTreeHeld = false
	o.discardUnheldPackTree(cname)
	if isDir(tree) {
		t.Error("discardUnheldPackTree left a tree no container holds")
	}
}
