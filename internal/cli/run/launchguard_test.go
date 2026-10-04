package run

// launchguard_test.go pins a fresh launch interrupted before its keeper exists
// (docs/design/jail-lifetime-last-session-wins.md JL-D75): a Ctrl-C while the image builds, a
// prompt waits or the launch lock is awaited must leave nothing behind that no container holds.
// The launch's pack tree is the one a nested jail showed left (3 of them, under
// AGENTS_DIR/<cname>/pack-trees): no signal arm was installed before the keeper's spawn, so the
// default action ended the process and no deferred discard ran.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// armExit is what a test sees of an arm's exit: its status, and the pack trees still on disk at
// that moment, after which a real exit would run nothing more.
type armExit struct {
	code int
	left []string
}

// seeArmExits swaps launchArmExit for one that reports each exit, with the trees of cname that
// remain, and returns the channel those reports arrive on.
func seeArmExits(t *testing.T, cname string) <-chan armExit {
	t.Helper()
	return seeArmExitsWith(t, cname, nil)
}

// seeArmExitsWith is seeArmExits that also runs atExit at each exit, before it reports, for what
// else a test must read at that moment. An arm takes launchArmExit when it is installed, so this
// is swapped in before the arm.
//
// It also gives the test a set of tracked nix children of its own (nixchildren.Isolate). An
// arm whose exit is faked here has run its teardown, whose first act is the real
// nixchildren.Stop (launchSignalArm.terminate), and a stop is permanent for the set it ran on:
// shared with the rest of the binary, it refused every later test's tracked nix
// (TestAGuardTestsNixStopEndsWithTheTest). A test runs that teardown and lives on only with the
// arm's exit faked, and this is where it is faked.
func seeArmExitsWith(t *testing.T, cname string, atExit func()) <-chan armExit {
	t.Helper()
	nixchildren.Isolate(t)
	exits := make(chan armExit, 4)
	saved := launchArmExit
	launchArmExit = func(code int) {
		if atExit != nil {
			atExit()
		}
		exits <- armExit{code: code, left: packTreesOf(cname)}
	}
	t.Cleanup(func() { launchArmExit = saved })
	return exits
}

// packTreesOf lists the pack trees under cname's root.
func packTreesOf(cname string) []string {
	entries, _ := os.ReadDir(paths.PackTreeRoot(cname))
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// catchSignal keeps sig from ending the test binary when no arm is installed to catch it, which is
// the defect's own shape: without this a red run would kill the whole package's run.
func catchSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	catch := make(chan os.Signal, 4)
	signal.Notify(catch, sig)
	t.Cleanup(func() { signal.Stop(catch) })
}

// TestASIGINTWhileTheImageBuildsLeavesNoPackTree drives Run() down the podman fresh-launch path
// and interrupts it at the image build, the longest wait before the keeper's spawn and the likeliest
// moment for a Ctrl-C. The launch must end through its own arm, 130, with its pack tree already gone
// when it exits: nothing after a real exit runs, so a tree still there at that moment stays for
// good, used by no container.
func TestASIGINTWhileTheImageBuildsLeavesNoPackTree(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cname := runtime.FromWorkspace(ws)
	// Nothing named podman is reachable, so a launch that went past the build could not start a
	// real container.
	t.Setenv("PATH", t.TempDir())
	exits := seeArmExits(t, cname)
	catchSignal(t, syscall.SIGINT)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0} // no container of this name, running or not
	}
	var mu sync.Mutex
	var staged []string
	var seen *armExit
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		mu.Lock()
		staged = packTreesOf(cname)
		mu.Unlock()
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
		select {
		case e := <-exits:
			mu.Lock()
			seen = &e
			mu.Unlock()
		case <-time.After(5 * time.Second):
		}
		return image.LoadResult{OK: false}
	}
	rc := Run(*o)

	mu.Lock()
	defer mu.Unlock()
	if len(staged) != 1 {
		t.Fatalf("the launch had %d pack trees staged at its image build, want 1, so this test says "+
			"nothing about the interrupt (rc %d)\nstdout:\n%s\nstderr:\n%s", len(staged), rc,
			stdout.String(), stderr.String())
	}
	if seen == nil {
		t.Fatalf("no signal arm ended the launch interrupted at its image build: the SIGINT took its "+
			"default action, which runs no deferred discard and leaves %s under %s", staged[0],
			paths.PackTreeRoot(cname))
	}
	if seen.code != 128+int(syscall.SIGINT) {
		t.Errorf("the interrupted launch exited %d, want %d", seen.code, 128+int(syscall.SIGINT))
	}
	if len(seen.left) != 0 {
		t.Errorf("the interrupted launch exited with its pack tree still under %s (%s), which no "+
			"container ever held and nothing will remove", paths.PackTreeRoot(cname), strings.Join(seen.left, ", "))
	}
}

// guardFixture is a launch that has staged its pack tree and armed its guard, with no runtime
// container of its name, holding the launch lock as a fresh launch does from the attach decision on.
type guardFixture struct {
	o     *Options
	cname string
	tree  string
	exits <-chan armExit
}

func newGuardFixture(t *testing.T, cname string) *guardFixture {
	t.Helper()
	return newGuardFixtureWith(t, cname, nil)
}

// newGuardFixtureWith is newGuardFixture whose arm's exit runs atExit first (seeArmExitsWith).
func newGuardFixtureWith(t *testing.T, cname string, atExit func()) *guardFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	exits := seeArmExitsWith(t, cname, atExit)
	o := goldenOptions(ws, t.TempDir())
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0} // no container of this name, running or not
	}
	o.RestoreTerminal = func() {}
	tree, err := newPackTree(cname)
	if err != nil {
		t.Fatal(err)
	}
	o.packTree = tree
	o.holdLaunchLock(cname)
	t.Cleanup(o.releaseLaunchLock)
	o.armLaunchGuard(cname, "podman")
	t.Cleanup(func() { popLaunchArm(o.launchGuard.arm) })
	return &guardFixture{o: o, cname: cname, tree: tree, exits: exits}
}

// interrupt sends this process sig and returns the exit the arm that acted took.
func (f *guardFixture) interrupt(t *testing.T, sig syscall.Signal) armExit {
	t.Helper()
	catchSignal(t, sig)
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-f.exits:
		return e
	case <-time.After(10 * time.Second):
		t.Fatalf("no arm ended the launch on %v", sig)
		return armExit{}
	}
}

// TestASignalAfterTheLaunchsRecordsTakesThemBackWithItsTree: a signal between the records a fresh
// launch writes just before its keeper's spawn (the tracking file, the live-tree record) and the
// spawn itself, the window of the reclaim offer's prompt. The guard takes both records back with
// the tree and the skeleton, the launch lock first so the tracking file's non-blocking take does
// not find this process holding it.
func TestASignalAfterTheLaunchsRecordsTakesThemBackWithItsTree(t *testing.T) {
	cname := "yolo-guard-recorded"
	var lockFreeAtExit bool
	f := newGuardFixtureWith(t, cname, func() { lockFreeAtExit = launchLockFree(t, launchLockPath(cname)) })
	skeleton := filepath.Join(paths.HomeSkeletonRoot(cname), "skel-1")
	if err := os.MkdirAll(skeleton, 0o755); err != nil {
		t.Fatal(err)
	}
	if !f.o.launchGuard.noteSkeleton(skeleton) {
		t.Fatal("the guard refused the skeleton of a launch no signal had ended")
	}
	var writeErr error
	if !f.o.launchGuard.record(func() {
		if writeErr = runtimeWriteTracking(cname, f.o.Workspace); writeErr == nil {
			writeErr = writeLivePackTree(cname, f.tree)
		}
	}) {
		t.Fatal("the guard refused the records of a launch no signal had ended")
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	tracking := filepath.Join(paths.ContainerDir(), cname)
	if !fileExists(tracking) {
		t.Fatalf("the fixture wrote no tracking file at %s", tracking)
	}
	e := f.interrupt(t, syscall.SIGINT)
	if e.code != 128+int(syscall.SIGINT) {
		t.Errorf("the launch exited %d, want %d", e.code, 128+int(syscall.SIGINT))
	}
	if len(e.left) != 0 {
		t.Errorf("the launch exited with its pack tree still there: %v", e.left)
	}
	for what, p := range map[string]string{"the home skeleton": skeleton,
		"the live-tree record": paths.LivePackTreeRecord(cname), "the tracking file": tracking} {
		if fileExists(p) {
			t.Errorf("the launch exited with %s (%s) still there, naming a jail that never started", what, p)
		}
	}
	if !lockFreeAtExit {
		t.Error("the launch exited still holding its launch lock")
	}
}

// TestALaunchTheGuardEndedSpawnsNoKeeper: once a signal's teardown has begun, the keeper's spawn
// is refused, so no keeper starts a container from a tree the teardown is removing.
func TestALaunchTheGuardEndedSpawnsNoKeeper(t *testing.T) {
	f := newGuardFixture(t, "yolo-guard-no-spawn")
	spawned := false
	saved := defaultKeeperSpawner
	defaultKeeperSpawner = func(*Options, string, *os.File, *os.File, *os.File, []*os.File) (func() int, error) {
		spawned = true
		return func() int { return 0 }, nil
	}
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	f.o.launchGuard.end()
	if _, err := f.o.startKeeper(&keeperPlan{Cname: f.cname}); !errors.Is(err, errLaunchEnded) {
		t.Errorf("startKeeper after the guard ended the launch returned %v, want errLaunchEnded", err)
	}
	if spawned {
		t.Error("a keeper was spawned for a launch its guard had already ended")
	}
}

// TestASignalDuringTheSpawnEndsTheJailThroughItsKeeper: a signal that lands while the keeper is
// being spawned waits for the spawn, then ends the jail as the keeper's own arm would, by closing
// the lifeline, and leaves the pack tree, which is the keeper's from its spawn, to the keeper.
func TestASignalDuringTheSpawnEndsTheJailThroughItsKeeper(t *testing.T) {
	f := newGuardFixture(t, "yolo-guard-mid-spawn")
	spawning, release := make(chan struct{}), make(chan struct{})
	saved := defaultKeeperSpawner
	defaultKeeperSpawner = func(_ *Options, _ string, _, lifeline, _ *os.File, _ []*os.File) (func() int, error) {
		fd, err := syscall.Dup(int(lifeline.Fd()))
		if err != nil {
			return nil, err
		}
		life := os.NewFile(uintptr(fd), "lifeline")
		close(spawning)
		<-release
		// The keeper: it unwinds once the lifeline reads EOF, as keeper.go's does before ready.
		return func() int {
			_, _ = io.Copy(io.Discard, life)
			_ = life.Close()
			return 1
		}, nil
	}
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	started := make(chan *keeperProcess, 1)
	go func() {
		kp, err := f.o.startKeeper(&keeperPlan{Cname: f.cname, Workspace: f.o.Workspace})
		if err != nil {
			t.Error(err)
		}
		started <- kp
	}()
	<-spawning
	catchSignal(t, syscall.SIGINT)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-f.exits:
		t.Fatalf("the guard ended the launch (%d) while its keeper was being spawned, leaving %v", e.code, e.left)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	kp := <-started
	if kp == nil {
		t.Fatal("the spawn under way when the signal landed was refused")
	}
	select {
	case e := <-f.exits:
		if e.code != 128+int(syscall.SIGINT) {
			t.Errorf("the launch exited %d, want %d", e.code, 128+int(syscall.SIGINT))
		}
		if len(e.left) != 1 {
			t.Errorf("the guard discarded the pack tree its keeper had just been handed (left %v)", e.left)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the guard never ended the launch after the spawn")
	}
	select {
	case <-kp.exited:
	default:
		t.Error("the launch exited before its keeper had unwound: the guard did not close the lifeline")
	}
}

// TestOnlyTheInnermostArmActsAndItTakesTheOuterLaunchsTreeToo is a launch run inside another in
// this process, as a capture jail's is: its arm, installed over the outer launch's guard, alone acts
// on a signal, and before its exit takes the outer launch's pack tree back, since that exit ends the
// outer launch too. Once the inner launch's arm is disarmed, the guard acts again.
func TestOnlyTheInnermostArmActsAndItTakesTheOuterLaunchsTreeToo(t *testing.T) {
	t.Run("the inner arm acts", func(t *testing.T) {
		f := newGuardFixture(t, "yolo-guard-outer")
		var mu sync.Mutex
		innerRan := false
		innerExit := make(chan armExit, 1)
		inner := armLaunchSignalsWith(func() { mu.Lock(); innerRan = true; mu.Unlock() },
			func(code int) { innerExit <- armExit{code: code, left: packTreesOf(f.cname)} })
		t.Cleanup(func() { popLaunchArm(inner) })
		catchSignal(t, syscall.SIGHUP)
		if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-innerExit:
			mu.Lock()
			ran := innerRan
			mu.Unlock()
			if !ran {
				t.Error("the inner arm exited without its own teardown")
			}
			if len(e.left) != 0 {
				t.Errorf("the inner launch's exit left the outer launch's pack tree behind: %v", e.left)
			}
		case e := <-f.exits:
			t.Fatalf("the outer launch's guard acted (%d) on a signal its inner launch's arm was installed for", e.code)
		case <-time.After(10 * time.Second):
			t.Fatal("no arm acted")
		}
		select {
		case e := <-f.exits:
			t.Errorf("the outer guard exited too (%d): two arms acted on one signal", e.code)
		case <-time.After(200 * time.Millisecond):
		}
	})
	t.Run("the guard acts once the inner arm is gone", func(t *testing.T) {
		f := newGuardFixture(t, "yolo-guard-resumed")
		inner := armLaunchSignalsWith(func() { t.Error("a disarmed inner arm ran its teardown") },
			func(int) { t.Error("a disarmed inner arm exited") })
		if !inner.disarm() {
			t.Fatal("disarm of an arm that never fired reported a teardown under way")
		}
		if e := f.interrupt(t, syscall.SIGTERM); e.code != 128+int(syscall.SIGTERM) || len(e.left) != 0 {
			t.Errorf("the guard, innermost again, exited %d leaving %v; want %d and no tree",
				e.code, e.left, 128+int(syscall.SIGTERM))
		}
	})
}

// TestWhatTheLaunchMakesWhileTheGuardTearsDownIsNotLeftBehind: the guard's teardown runs on the
// arm's goroutine while the launch's own goroutine goes on, so the launch can reach the steps that
// tell the guard about its skeleton and its records after the teardown has taken what it knew. The
// skeleton and the two records must still be gone at the exit, and the records never written.
func TestWhatTheLaunchMakesWhileTheGuardTearsDownIsNotLeftBehind(t *testing.T) {
	cname := "yolo-guard-late"
	var tracking, skeleton string // named once the fixture has set HOME
	type leftAtExit struct{ tracking, live, skeleton bool }
	var left leftAtExit
	f := newGuardFixtureWith(t, cname, func() {
		left = leftAtExit{fileExists(tracking), fileExists(paths.LivePackTreeRecord(cname)), fileExists(skeleton)}
	})
	tracking = filepath.Join(paths.ContainerDir(), cname)
	skeleton = filepath.Join(paths.HomeSkeletonRoot(cname), "skel-late")
	inTeardown, resume := make(chan struct{}), make(chan struct{})
	f.o.RestoreTerminal = func() { close(inTeardown); <-resume }
	catchSignal(t, syscall.SIGINT)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inTeardown:
	case <-time.After(10 * time.Second):
		t.Fatal("the guard's teardown never ran")
	}
	// The launch's goroutine, still running: it builds its skeleton and writes its records as
	// runContainer does.
	if err := os.MkdirAll(skeleton, 0o755); err != nil {
		t.Fatal(err)
	}
	if f.o.launchGuard.noteSkeleton(skeleton) {
		t.Error("the guard took the skeleton of a launch its teardown had already ended")
	} else {
		discardUnheldSkeleton(cname, skeleton)
	}
	if f.o.launchGuard.record(func() {
		_ = runtimeWriteTracking(cname, f.o.Workspace)
		_ = writeLivePackTree(cname, f.tree)
	}) {
		t.Error("the guard let a launch its teardown had already ended write its records")
	}
	close(resume)
	select {
	case <-f.exits:
	case <-time.After(10 * time.Second):
		t.Fatal("the guard never exited")
	}
	if left.tracking || left.live || left.skeleton {
		t.Errorf("the launch exited leaving what it made during its guard's teardown: tracking file %v, "+
			"live-tree record %v, skeleton %v", left.tracking, left.live, left.skeleton)
	}
}

// TestAGuardTestsNixStopEndsWithTheTest: a test that interrupts a guarded launch runs the real
// nixchildren.Stop, and a stop is permanent for the process it runs in. That is right for a
// launch a signal is ending, which exits next, and wrong for this test binary, which fakes that
// exit (seeArmExitsWith) and goes on to other tests: each later test that started a tracked nix was
// refused, so whether it passed depended on the order the tests ran in. The stop must end with the
// test that made it. The nix after it is the store-delivered extras build's own, a stand-in first
// on PATH that prints a store path.
func TestAGuardTestsNixStopEndsWithTheTest(t *testing.T) {
	t.Run("an interrupted launch stops its nix", func(t *testing.T) {
		f := newGuardFixture(t, "yolo-guard-real-nix-stop")
		if e := f.interrupt(t, syscall.SIGINT); e.code != 128+int(syscall.SIGINT) {
			t.Errorf("the launch exited %d, want %d", e.code, 128+int(syscall.SIGINT))
		}
	})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\necho /nix/store/fake-extras\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile, err := nixBuildImageExtrasProfile(t.TempDir())
	if err != nil {
		t.Fatalf("a nix started after an earlier test's interrupted launch was refused: %v", err)
	}
	if profile != "/nix/store/fake-extras" {
		t.Errorf("the extras build returned %q, want the stand-in's store path", profile)
	}
}
