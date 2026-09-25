package run

// trackingcleanup_test.go pins forgetGoneContainer (trackingcleanup.go): a launch drops its
// jail's tracking file once it has OBSERVED the container gone, which is what lets
// prune.PruneOrphanAgentStaging reap the jail's AGENTS_DIR entry and the home skeletons in it,
// as the maintainer's OQ-BH10 ruling assumed it did
// (docs/design/base-home-legacy-state.md#OQ-BH10). Before this, a --rm jail's tracking file
// outlived every normal exit, the reaper kept every tracked name, and a workspace in use
// gained one skeleton per fresh launch until someone ran `yolo ps`.

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// isExistingProbe reports whether argv is probeExistingContainer's podman query for cname.
func isExistingProbe(argv []string, cname string) bool {
	return slices.Equal(argv, []string{"podman", "ps", "-a", "-q", "--filter", "name=^/" + cname + "$"})
}

// trackingFixture isolates HOME and returns Options whose runtime answers probeExistingContainer
// with answer (every other command "did not run"), and the path of cname's tracking file,
// written as a fresh launch writes it.
func trackingFixture(t *testing.T, cname string, answer ExecResult) (*Options, string, *int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	ws := t.TempDir()
	o := goldenOptions(ws, home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	probes := 0
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if isExistingProbe(argv, cname) {
			probes++
			return answer
		}
		return ExecResult{Ran: false}
	}
	if err := runtimeWriteTracking(cname, ws); err != nil {
		t.Fatalf("writing the tracking file: %v", err)
	}
	tracking := filepath.Join(paths.ContainerDir(), cname)
	if _, err := os.Stat(tracking); err != nil {
		t.Fatalf("the fixture's tracking file is missing: %v", err)
	}
	return o, tracking, &probes
}

// TestANormalExitForgetsAGoneContainersTracking drives the real normal-exit teardown
// (teardownAfterExit) against a runtime that answers "no container of this name", and then
// the real reaper: the tracking file is gone, so the jail's AGENTS_DIR entry, skeleton and
// all, is reaped once past the age floor. It fails if the teardown's call is deleted.
func TestANormalExitForgetsAGoneContainersTracking(t *testing.T) {
	const cname = "yolo-tracking-gone"
	o, tracking, probes := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	skeleton := buildSkeletonForTest(t, cname, nil, nil, nil)
	entry := filepath.Join(paths.AgentsDir(), cname)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(entry, old, old); err != nil {
		t.Fatal(err)
	}

	// Control, before the teardown: the tracked name is kept, which is how the skeletons
	// used to pile up.
	if _, _, names := prune.PruneOrphanAgentStaging(paths.AgentsDir(),
		prune.TrackedContainerNames(paths.ContainerDir()), true, time.Hour, false, time.Now()); len(names) != 0 {
		t.Fatalf("control: the reaper would remove %v while the jail is still tracked", names)
	}

	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)

	if *probes == 0 {
		t.Fatal("the teardown never asked the runtime whether the container is gone")
	}
	if _, err := os.Lstat(tracking); !os.IsNotExist(err) {
		t.Fatalf("a normal exit left the tracking file %s (err %v): the reaper keeps every tracked "+
			"name, so this jail's AGENTS_DIR entry and every skeleton in it stay", tracking, err)
	}
	_, _, names := prune.PruneOrphanAgentStaging(paths.AgentsDir(),
		prune.TrackedContainerNames(paths.ContainerDir()), true, time.Hour, true, time.Now())
	if !slices.Contains(names, cname) {
		t.Errorf("the reaper did not remove %s once its tracking file was gone (removed %v)", entry, names)
	}
	if _, err := os.Lstat(skeleton); !os.IsNotExist(err) {
		t.Errorf("the skeleton %s survived the reap (err %v)", skeleton, err)
	}
}

// TestANormalExitReleasesTheLaunchsOwnLockFirst is the ordering the normal-exit arm depends
// on. The launch's lock (acquireWorkspaceLock, the path runContainer takes) may still be held
// when the child exits — onStarted releases it only after awaitRunningContainer's poll, on a
// goroutine the proxy never joins — and while it is, the teardown's non-blocking take
// declines and the tracking file stays. Released first, from both goroutines at once as
// onStarted and the exit arm can, the same teardown drops it.
func TestANormalExitReleasesTheLaunchsOwnLockFirst(t *testing.T) {
	const cname = "yolo-tracking-own-lock"
	o, tracking, probes := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	lockDir := filepath.Join(paths.GlobalStorage(), "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireWorkspaceLock(filepath.Join(lockDir, cname+".lock"), o.Workspace, lockNotices{})
	if err != nil || lock == nil {
		t.Fatalf("taking the launch's lock: %v", err)
	}

	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)
	if *probes != 0 {
		t.Errorf("the runtime was asked while the launch's own lock was held")
	}
	if _, err := os.Stat(tracking); err != nil {
		t.Fatalf("control: the tracking file went while the launch's own lock was held: %v", err)
	}

	done := make(chan struct{})
	go func() { lock.Close(); close(done) }() // onStarted's release
	lock.Close()                              // the normal-exit arm's
	<-done
	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)
	if _, err := os.Lstat(tracking); !os.IsNotExist(err) {
		t.Errorf("the tracking file stayed after the launch released its lock (err %v)", err)
	}
}

// TestTrackingStaysUnlessTheContainerIsKnownGone: every answer short of "the runtime says no
// container of this name exists, and no other launch of this workspace holds its lock"
// leaves the tracking file where it is. A tracking file removed under a live or starting
// jail would let the reaper take the skeleton that jail's /home/agent is bound from.
func TestTrackingStaysUnlessTheContainerIsKnownGone(t *testing.T) {
	const cname = "yolo-tracking-kept"
	for _, c := range []struct {
		name   string
		answer ExecResult
	}{
		{"the probe did not run", ExecResult{Ran: false}},
		{"the probe failed", ExecResult{Ran: true, RC: 125, Stderr: "cannot connect"}},
		{"the probe timed out", ExecResult{Ran: true, Timeout: true}},
		{"the container is still there", ExecResult{Ran: true, RC: 0, Stdout: "abcd1234\n"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, tracking, probes := trackingFixture(t, cname, c.answer)
			o.forgetGoneContainer(cname, "podman", "")
			if *probes != 1 {
				t.Errorf("the runtime was asked %d times, want once", *probes)
			}
			if _, err := os.Stat(tracking); err != nil {
				t.Errorf("the tracking file went on %q: %v", c.name, err)
			}
		})
	}

	t.Run("another launch holds the workspace lock", func(t *testing.T) {
		// A fresh launch holds the lock from before it writes its tracking file until its
		// container runs, so the file may be ITS; the runtime is not even asked.
		o, tracking, probes := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
		lockDir := filepath.Join(paths.GlobalStorage(), "locks")
		if err := os.MkdirAll(lockDir, 0o755); err != nil {
			t.Fatal(err)
		}
		held, err := os.OpenFile(filepath.Join(lockDir, cname+".lock"), os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		o.forgetGoneContainer(cname, "podman", "")
		if *probes != 0 {
			t.Errorf("the runtime was asked while another launch held the lock")
		}
		if _, err := os.Stat(tracking); err != nil {
			t.Errorf("the tracking file went while another launch held the workspace lock: %v", err)
		}
	})

	t.Run("Apple Container", func(t *testing.T) {
		// `container ls --all` prints a header even when it lists nothing.
		for _, c := range []struct {
			stdout string
			gone   bool
		}{
			{"ID  IMAGE  STATE\n", true},
			{"ID  IMAGE  STATE\n" + cname + "  img  stopped\n", false},
		} {
			o, tracking, _ := trackingFixture(t, cname, ExecResult{})
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if slices.Equal(argv, []string{"container", "ls", "--all"}) {
					return ExecResult{Ran: true, RC: 0, Stdout: c.stdout}
				}
				return ExecResult{Ran: false}
			}
			o.forgetGoneContainer(cname, "container", "")
			_, err := os.Stat(tracking)
			if c.gone && !os.IsNotExist(err) {
				t.Errorf("listing %q: the tracking file stayed (err %v)", c.stdout, err)
			}
			if !c.gone && err != nil {
				t.Errorf("listing %q: the tracking file went: %v", c.stdout, err)
			}
		}
	})
}

// TestEveryEndOfTheLaunchForgetsTheContainer pins the two call sites in runContainer that no
// unit test can drive: the signal arm (the onTerminate closure) and the runtime-never-started
// branch. Each must call forgetGoneContainer AFTER its own lock.Close(), or the non-blocking
// take inside finds this very process holding the lock and the file always stays. The
// normal-exit call is driven for real by TestANormalExitForgetsAGoneContainersTracking.
func TestEveryEndOfTheLaunchForgetsTheContainer(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")

	// requireAfterClose checks that body calls forgetGoneContainer, and only after a
	// lock.Close().
	requireAfterClose := func(what string, body *ast.BlockStmt) {
		t.Helper()
		var closePos, forgetPos token.Pos
		ast.Inspect(body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch skelCallee(call) {
			case "Close":
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && skelIdent(sel.X) == "lock" && closePos == token.NoPos {
					closePos = call.Pos()
				}
			case "forgetGoneContainer":
				forgetPos = call.Pos()
			}
			return true
		})
		if forgetPos == token.NoPos {
			t.Errorf("%s no longer calls forgetGoneContainer: a jail that ends there keeps its "+
				"tracking file, so the reaper keeps its AGENTS_DIR entry and every skeleton in it", what)
			return
		}
		if closePos == token.NoPos || forgetPos < closePos {
			t.Errorf("%s calls forgetGoneContainer before lock.Close(): the non-blocking take "+
				"finds this launch's own lock held, and the tracking file never goes", what)
		}
	}

	var onTerminate, runErr *ast.BlockStmt
	ast.Inspect(fd, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 && skelIdent(x.Lhs[0]) == "onTerminate" && len(x.Rhs) == 1 {
				if fl, ok := x.Rhs[0].(*ast.FuncLit); ok {
					onTerminate = fl.Body
				}
			}
		case *ast.IfStmt:
			if be, ok := x.Cond.(*ast.BinaryExpr); ok && skelIdent(be.X) == "runErr" && be.Op == token.NEQ {
				runErr = x.Body
			}
		}
		return true
	})
	if onTerminate == nil || runErr == nil {
		t.Fatalf("runContainer's onTerminate closure (found: %v) or its `if runErr != nil` branch "+
			"(found: %v) moved; re-anchor this pin, do not delete it", onTerminate != nil, runErr != nil)
	}
	requireAfterClose("runContainer's signal arm (onTerminate)", onTerminate)
	requireAfterClose("runContainer's runtime-never-started branch", runErr)

	// The normal-exit arm: a top-level lock.Close() after the runErr branch and before
	// teardownAfterExit. onStarted's own release waits on awaitRunningContainer's poll and the
	// proxy does not join it, so without this the teardown's non-blocking takes can meet this
	// launch's own lock (TestANormalExitReleasesTheLaunchsOwnLockFirst shows the effect).
	runErrIdx, closeIdx, teardownIdx := -1, -1, -1
	for i, st := range fd.Body.List {
		if is, ok := st.(*ast.IfStmt); ok && is.Body == runErr {
			runErrIdx = i
		}
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		switch skelCallee(call) {
		case "Close":
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && skelIdent(sel.X) == "lock" {
				closeIdx = i
			}
		case "teardownAfterExit":
			teardownIdx = i
		}
	}
	if teardownIdx < 0 {
		t.Fatal("runContainer no longer calls teardownAfterExit at its top level; re-anchor this pin, do not delete it")
	}
	if closeIdx < 0 || closeIdx < runErrIdx || closeIdx > teardownIdx {
		t.Errorf("runContainer's normal-exit arm does not release the lock before teardownAfterExit "+
			"(runErr branch at %d, lock.Close at %d, teardown at %d): a child that exits before its "+
			"container is seen running leaves its tracking file and skeleton behind", runErrIdx, closeIdx, teardownIdx)
	}

	// And the signal arm asks only after stopping the jail: before stopJail the container is
	// alive by definition, so the probe could only ever answer "still there".
	var stopPos, forgetPos token.Pos
	ast.Inspect(onTerminate, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch skelCallee(call) {
			case "stopJail":
				stopPos = call.Pos()
			case "forgetGoneContainer":
				forgetPos = call.Pos()
			}
		}
		return true
	})
	if stopPos == token.NoPos || forgetPos < stopPos {
		t.Error("the signal arm asks whether the container is gone before stopping it")
	}
}

// TestAGoneContainersSkeletonGoesWithItsTracking pins OQ-BH16 (docs/design/base-home-legacy-state.md):
// on the same known-gone evidence that drops the tracking file, the launch also removes the
// skeleton IT built, so a workspace used alone no longer keeps one skeleton per launch until
// `yolo prune --apply`. Driven through the real normal-exit teardown. It removes only the
// named skeleton: an older launch's, beside it, is the reaper's, and another jail's is never
// reachable (the path guard takes only a direct child of this cname's skeleton root).
func TestAGoneContainersSkeletonGoesWithItsTracking(t *testing.T) {
	const cname = "yolo-skeleton-gone"
	o, _, _ := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	older := buildSkeletonForTest(t, cname, nil, nil, nil)
	mine := buildSkeletonForTest(t, cname, nil, nil, nil)
	otherJail := buildSkeletonForTest(t, "yolo-skeleton-other", nil, nil, nil)

	o.teardownAfterExit(nil, "", nil, "", cname, "podman", mine, 0)

	if _, err := os.Lstat(mine); !os.IsNotExist(err) {
		t.Errorf("the launch's own skeleton %s survived a known-gone exit (err %v)", mine, err)
	}
	for _, keep := range []string{older, otherJail} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("a skeleton this launch did not build was removed: %s (%v)", keep, err)
		}
	}

	// A path outside this cname's skeleton root is refused by the guard, whatever it names.
	o2, _, _ := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	o2.forgetGoneContainer(cname, "podman", otherJail)
	if _, err := os.Stat(otherJail); err != nil {
		t.Errorf("forgetGoneContainer removed another jail's skeleton %s (%v)", otherJail, err)
	}
}

// TestASkeletonStaysUnlessTheContainerIsKnownGone: "could not ask", "still there" and another
// launch holding the lock all leave the skeleton, as they leave the tracking file. Removing it
// under a live jail would detach that jail's /home/agent (the design's rule 2).
func TestASkeletonStaysUnlessTheContainerIsKnownGone(t *testing.T) {
	const cname = "yolo-skeleton-kept"
	for _, c := range []struct {
		name   string
		answer ExecResult
	}{
		{"the probe did not run", ExecResult{Ran: false}},
		{"the probe failed", ExecResult{Ran: true, RC: 125, Stderr: "cannot connect"}},
		{"the probe timed out", ExecResult{Ran: true, Timeout: true}},
		{"the container is still there", ExecResult{Ran: true, RC: 0, Stdout: "abcd1234\n"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, _, _ := trackingFixture(t, cname, c.answer)
			sk := buildSkeletonForTest(t, cname, nil, nil, nil)
			o.forgetGoneContainer(cname, "podman", sk)
			if _, err := os.Stat(sk); err != nil {
				t.Errorf("the skeleton went on %q: %v", c.name, err)
			}
		})
	}
}

// TestEveryEndHandsTheSkeletonOn pins that each of runContainer's three ends passes the
// skeleton it built (in.homeSkeleton) to the known-gone cleanup: the signal arm and the
// runtime-never-started branch to forgetGoneContainer, the normal exit to teardownAfterExit.
// A call that passed "" would still drop the tracking file and leave the skeleton to pile up.
func TestEveryEndHandsTheSkeletonOn(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	passesSkeleton := func(call *ast.CallExpr) bool {
		for _, a := range call.Args {
			if sel, ok := a.(*ast.SelectorExpr); ok && skelIdent(sel.X) == "in" && sel.Sel.Name == "homeSkeleton" {
				return true
			}
		}
		return false
	}
	found := map[string]int{}
	ast.Inspect(fd, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch name := skelCallee(call); name {
			case "forgetGoneContainer", "teardownAfterExit":
				if passesSkeleton(call) {
					found[name]++
				} else {
					t.Errorf("runContainer calls %s without in.homeSkeleton at %v", name, call.Pos())
				}
			}
		}
		return true
	})
	if found["forgetGoneContainer"] != 2 || found["teardownAfterExit"] != 1 {
		t.Errorf("want two forgetGoneContainer calls and one teardownAfterExit call handing on "+
			"in.homeSkeleton, got %v", found)
	}
}
