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
//
// THE SECOND DETACHED WRITER IS A HOST-WIDE DAEMON. A launch that needs one spawns it
// detached, and it outlives the launch by design, running under the test's temp HOME
// (homedaemons_test.go has the inventory). So the same cleanup also stops the daemons that
// run under the launch's HOME, and waits for them to exit, before that HOME is removed.
//
// AND SO ARE THE PREPARATION WORKERS. A host launch whose packs declare a program leaves the
// preparation scheduler and a shared-demand worker running under the test's HOME
// (preparationworkers_test.go has the inventory, and CI run 38856922948 the failure), so the
// same cleanup stops those too. Not by the locks they hold, which missed a worker the scheduler
// had just spawned (CI run 39096056377): it stops every process still running with the test's
// HOME, round after round until none is, and only then lets the removal run.
//
// AND SO IS AN ORPHANED NIX. A launch refused after it started its image-identity eval beside the
// fork-build slot leaves that `nix eval` running with no parent under the test's HOME, writing
// nix's fetcher cache there (homenix_test.go has the measurement, and CI run 38924394239 the
// failure), so the same cleanup interrupts those too.
//
// THE THIRD IS THE JAIL'S KEEPER (docs/design/jail-lifetime-last-session-wins.md §9). A fresh
// launch spawns it detached, and it ends the jail once the last session is gone. A session that
// quits waits for that teardown, but one this suite kills (a background run's cancel, at its
// cleanup) does not, and the keeper then tears the jail down after the test: its config capture
// and launch.log lines land in the workspace while the test's RemoveAll walks it (measured on
// TestAttachDeliversTheSelectedProfile, whose first session is cancelled). So the cleanup waits
// for the workspace's keeper first, before the daemons its teardown still talks to are stopped
// and before the scratch remover that teardown starts is waited for.

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
// process the launch left behind: the scratch removers writing into the workspace dir, and
// the host daemons running under home, the HOME the launch was given (launchHome).
//
// REGISTERED AT THE LAUNCH, not in writeProject, because cleanups run last-registered
// first: one registered here runs before the cleanup of the workspace it launched in
// (writeProject's removeWorkspaceTree) and before t.TempDir's RemoveAll, whichever helper
// made the directory — several tests launch in a bare t.TempDir(). The same holds for the
// home: requireJail and packHome make it before any launch, so its removal runs after this.
//
// AT THE END OF THE TEST, not of the launch: one wait covers every launch the test made.
//
// Only a TEST's home is swept. A launch whose HOME is the machine's — nothing isolated it, or
// ambientHome handed it back — may be sharing its daemons with the developer's real jails.
func awaitDetachedWriters(t *testing.T, dir, home string) {
	t.Helper()
	sweep := home != "" && hostHome != "" && home != hostHome
	t.Cleanup(func() {
		// Every name a jail of dir may run under: the test's launch may have selected any setup.
		kept := false
		for _, cname := range jailNamesOf(dir) {
			if err := run.WaitForKeeper(cname, detachedWriterWait); err != nil {
				t.Errorf("%v: its teardown would land in the middle of this test's temp-dir cleanup", err)
			}
			kept = kept || run.KeeperAlive(cname)
		}
		// NOT WHILE THE JAIL IS STILL KEPT for a session: WaitForKeeper returns at once then, and
		// the jail's host daemons are rightly alive. That session's own launch registered its
		// cleanup earlier, so it runs later, once the test has released the session, and sweeps
		// the home then. Measured: TestAttachDeliversTheSelectedProfile's attach cleanup ran
		// before the release of the background session it attached to, and reported that
		// session's live daemons as stragglers.
		if sweep && !kept {
			// The launch's own host daemons, SIGTERMed by its end and left to finish (HD-R1's
			// detach): waited for, never stopped by name (homedaemons_test.go).
			if left := awaitHomeHostDaemons(home, homeDaemonBound); len(left) > 0 {
				t.Errorf("host daemons this test's launch ran under %s were still alive %s after "+
					"it, past the upstream deadline a detached daemon is bounded by, and were "+
					"killed: %s", home, homeDaemonBound, strings.Join(left, ", "))
			}
			// A refused launch's image-identity eval, orphaned under the home (homenix_test.go),
			// FIRST, so it gets the interrupt nix is given rather than the general stop's SIGTERM.
			stoppedNix, err := stopHomeNixProcesses(home)
			if len(stoppedNix) > 0 {
				t.Logf("stopped the nix processes left running under this test's HOME %s: %s",
					home, strings.Join(stoppedNix, ", "))
			}
			if err != nil {
				t.Errorf("stopping the nix processes this test's launch left running under %s: %v",
					home, err)
			}
			// Then EVERYTHING else still running under the home — the preparation scheduler, its
			// workers and the capture jails they run among them — until nothing is
			// (preparationworkers_test.go).
			stopped, err := stopHomeProcesses(home)
			if len(stopped) > 0 {
				t.Logf("stopped the processes left running under this test's HOME %s: %s",
					home, strings.Join(stopped, ", "))
			}
			if err != nil {
				t.Errorf("stopping the processes this test's launch left running under %s: %v",
					home, err)
			}
		}
		if err := run.WaitForScratchRemovers(dir, detachedWriterWait); err != nil {
			t.Errorf("%v: its housekeeping line would land in the middle of this test's "+
				"temp-dir cleanup", err)
		}
	})
}

// launchHome is the HOME a launch with environment env runs with: the LAST HOME= entry, which
// is the one exec hands the child, or this process's own when env names none.
func launchHome(env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], "HOME="); ok {
			return v
		}
	}
	return os.Getenv("HOME")
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
// when it has a *testing.T, and when it has none (the warmup) BOTH halves directly —
// run.WaitForScratchRemovers for the workspace and awaitHomeHostDaemons for the home. Either
// half alone fails: any launch may start a host daemon, since selecting a pack is enough, and it may still be exiting.
// A new launch helper that forgets fails here, under -short, instead of as one CI shard's
// cleanup flake a week later.
//
// awaitDetachedWriters itself is read too, so deleting either half from its body fails here
// under -short, and not only in the container suite's behavioral tests
// (TestALaunchesCleanupWaitsForItsDetachedWriters above, and
// TestALaunchesCleanupWaitsForItsHomesHostDaemons in homedaemons_test.go).
func TestEveryLaunchSiteAwaitsDetachedWriters(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var sites, missing []string
	helperSeen := false
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
			execsYolo, awaits, waitsScratch, waitsDaemons := false, false, false, false
			stopsWorkers, stopsNix := false, false
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
				case "awaitDetachedWriters":
					awaits = true
				case "run.WaitForScratchRemovers":
					waitsScratch = true
				case "awaitHomeHostDaemons":
					waitsDaemons = true
				case "stopHomeProcesses":
					stopsWorkers = true
				case "stopHomeNixProcesses":
					stopsNix = true
				}
				return true
			})
			if fn.Name.Name == "awaitDetachedWriters" {
				helperSeen = true
				if !waitsScratch || !waitsDaemons {
					t.Errorf("awaitDetachedWriters must both wait for the scratch removers "+
						"(run.WaitForScratchRemovers: %v) and wait for the home's host daemons "+
						"(awaitHomeHostDaemons: %v)", waitsScratch, waitsDaemons)
				}
				if !stopsWorkers || !stopsNix {
					t.Errorf("awaitDetachedWriters must stop what a launch leaves running under the "+
						"test's HOME: every process left under it (stopHomeProcesses: %v) and "+
						"an orphaned nix (stopHomeNixProcesses: %v)", stopsWorkers, stopsNix)
				}
			}
			if !execsYolo {
				continue
			}
			site := name + ":" + fn.Name.Name
			sites = append(sites, site)
			if !awaits && !(waitsScratch && waitsDaemons) {
				missing = append(missing, site)
			}
		}
	}
	sort.Strings(missing)
	if !helperSeen {
		t.Error("awaitDetachedWriters was not found, so its body was not checked")
	}
	if len(sites) < 5 {
		t.Fatalf("found only %d launch sites (%v); the scan no longer sees what it is pinning", len(sites), sites)
	}
	if len(missing) > 0 {
		t.Errorf("these functions launch yolo without waiting for its detached writers, so a "+
			"scratch remover can write into the workspace, or a host daemon into the "+
			"home, during t.TempDir's cleanup: %s\n"+
			"Add awaitDetachedWriters(t, dir, launchHome(cmd.Env)) at the launch, or, with no "+
			"*testing.T, both run.WaitForScratchRemovers and awaitHomeHostDaemons "+
			"(detachedwriters_test.go).",
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
