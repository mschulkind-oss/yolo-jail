package integration

// detachedwriters_test.go makes every launch this suite starts wait, at cleanup, for the
// detached processes that launch left writing into its workspace.
//
// THE FAILURE (CI run 36383731749): TestAttachRestartsAnOlderJailAtATerminal passed, and
// then Go's t.TempDir cleanup failed with `unlinkat <tempdir>: directory not empty`. A
// podman launch's teardown starts `yolo internal scratch-rm` detached, and the launcher
// exits without waiting for it (internal/cli/run/scratchremoval.go). The remover's one
// `scratch:` line in `<workspace>/.yolo/housekeeping.log` landed after the test's
// RemoveAll had read the temp dir, recreating the workspace under it. Reproduced here by
// delaying the remover two seconds: `<ws>/.yolo/{.gitignore,housekeeping.log}` reappeared
// after the cleanup, and the test passed only because the write lost the race the other
// way.
//
// The remover no longer creates a missing `.yolo` (paths.OpenExistingWorkspaceStateFile),
// which narrows the window but cannot close it: a `.yolo` still present when the note is
// opened gains a file while RemoveAll is walking it. So the suite also WAITS: each remover
// holds a shared flock on `<ws>/.yolo/scratch-rm.lock` for its whole life, taken by the
// launcher before the spawn (run.WaitForScratchRemovers). awaitDetachedWriters is the
// suite's one spelling of that wait, and TestEveryLaunchSiteAwaitsDetachedWriters makes a
// launch site without it a failure rather than a flake.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// detachedWriterWait bounds the wait for one workspace's removers: the remover's own
// minute-long wait for podman to release the volumes (run's scratchRemovalWait) plus its
// podman calls, with margin. A remover still running past it is reported, not waited out.
const detachedWriterWait = 2 * time.Minute

// awaitDetachedWriters registers, at a launch site, a cleanup that waits for every detached
// process launched for dir to finish writing into it.
//
// REGISTERED AT THE LAUNCH, not in writeProject, because cleanups run last-registered
// first: one registered here runs before the cleanup of the workspace it launched in
// (writeProject's removeWorkspaceTree) and before t.TempDir's RemoveAll, whichever helper
// made the directory — several tests launch in a bare t.TempDir().
func awaitDetachedWriters(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		if err := run.WaitForScratchRemovers(dir, detachedWriterWait); err != nil {
			t.Errorf("%v: its housekeeping line would land in the middle of this test's "+
				"temp-dir cleanup", err)
		}
	})
}

// THE CALL SITE, BY BEHAVIOR: a runCommand launch's cleanup waits for a remover still in
// flight. The "remover" is this test holding the shared lock the real one inherits,
// released a second after the launch returns; the launch is `yolo --version`, since what is
// pinned is the harness's wait and not the spawn (the unit tier pins that the launcher
// hands the child the lock: run.TestWaitForScratchRemoversWaitsForTheSpawnedChild). Delete
// the awaitDetachedWriters call from runCommand and the subtest returns before the release.
func TestALaunchesCleanupWaitsForItsDetachedWriters(t *testing.T) {
	requireJail(t)
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(ws, ".yolo", run.ScratchRemoverLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}

	var released atomic.Bool
	t.Run("launch", func(t *testing.T) {
		if res := runYoloCLI(t, ws, "--version"); res.rc != 0 {
			t.Fatalf("yolo --version: rc=%d\n%s", res.rc, res.combined())
		}
		go func() {
			time.Sleep(time.Second)
			released.Store(true)
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		}()
	})
	if !released.Load() {
		t.Error("the launch's cleanup returned while a detached writer still held the " +
			"workspace's in-flight lock: runCommand no longer calls awaitDetachedWriters")
	}
}

// THE CALL SITES, BY READING THEM. Every function in this package that execs the yolo
// binary under test must also wait for what that launch left behind: awaitDetachedWriters
// when it has a *testing.T, run.WaitForScratchRemovers directly when it has none (the
// warmup, and a launch run on a goroutine). A new launch helper that forgets fails here,
// under -short, instead of as one CI shard's cleanup flake a week later.
func TestEveryLaunchSiteAwaitsDetachedWriters(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var sites, missing []string
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			execsYolo, waits := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				// The PROGRAM argument only: runSuite's `go build -o yoloBin` builds it.
				prog := map[string]int{"exec.Command": 0, "exec.CommandContext": 1}
				switch name := callName(call.Fun); name {
				case "exec.Command", "exec.CommandContext":
					if i := prog[name]; i < len(call.Args) {
						if id, ok := call.Args[i].(*ast.Ident); ok && id.Name == "yoloBin" {
							execsYolo = true
						}
					}
				case "awaitDetachedWriters", "run.WaitForScratchRemovers":
					waits = true
				}
				return true
			})
			if !execsYolo {
				continue
			}
			site := name + ":" + fn.Name.Name
			sites = append(sites, site)
			if !waits {
				missing = append(missing, site)
			}
		}
	}
	sort.Strings(missing)
	if len(sites) < 5 {
		t.Fatalf("found only %d launch sites (%v); the scan no longer sees what it is pinning", len(sites), sites)
	}
	if len(missing) > 0 {
		t.Errorf("these functions launch yolo without waiting for its detached writers, so a "+
			"scratch remover can write into the workspace during t.TempDir's cleanup: %s\n"+
			"Add awaitDetachedWriters(t, dir) at the launch (detachedwriters_test.go).",
			strings.Join(missing, ", "))
	}
}

// callName is a call's function as written: "f" or "pkg.F".
func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}
