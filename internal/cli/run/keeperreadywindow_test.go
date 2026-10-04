package run

// keeperreadywindow_test.go pins a fresh launch interrupted between its keeper's "ready" and the
// moment its signal arm is retargeted to the first session's own teardown
// (docs/design/jail-lifetime-last-session-wins.md JL-D58, JL-D74). The arm still runs the
// pre-ready teardown there, which closes the lifeline, but the keeper stops reading the lifeline at
// ready: from then on the jail ends when its last session's count goes. The first session's count
// was this launch's own, held until the process exited, so the teardown used to wait out its whole
// bound (keeperUnwindWait, 20 s) for a keeper that could not drain until it gave up.
//
// WHICH TEARDOWN TAKES THAT SIGNAL IS A RACE, and either answer is a correct end: the arm's
// goroutine takes the signal while the launch's own goroutine reads the ready frame and retargets
// the arm, and either can go first.
// Each test here fixes one order: the first three signal before the retarget, and the last after it.

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestASIGINTBetweenReadyAndTheRetargetEndsTheJailWithoutWaitingOutTheBound drives every piece a
// fresh launch runs there: the first session counted before the spawn (holdSessionLock), startKeeper
// with TestMain's in-process keeper, the launch's arm around keeperPreReadyTeardown, the relay until
// ready, and a real SIGINT to this process before any retarget. The launch must exit 130 once its
// keeper has ended the jail as its last session's end, well inside the bound, rather than at the
// bound with the keeper still holding the jail.
func TestASIGINTBetweenReadyAndTheRetargetEndsTheJailWithoutWaitingOutTheBound(t *testing.T) {
	saved := keeperUnwindWait
	keeperUnwindWait = 8 * time.Second
	t.Cleanup(func() { keeperUnwindWait = saved })
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	cname := "yolo-ready-window-sigint"
	jail := newFakeJail(t, cname)
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Exec = jail.exec
	o.PIDAlive = func(int) bool { return false }
	o.RestoreTerminal = func() {}
	// The fresh launch counts its first session before the spawn (JL-D16).
	o.holdSessionLock(cname)
	t.Cleanup(o.releaseSessionLock)
	if o.sessionLock == nil {
		t.Fatal("the fixture could not count the first session, so the keeper would never drain on it")
	}
	cfg, err := encodeConfig(jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: o.Workspace, Cname: cname, Runtime: "podman",
		Config: cfg, SocketsDir: hostServiceSocketsDir(cname, false), RunCmd: jail.mainArgv(true),
		ImageRef: "the-image"}
	kp, err := o.startKeeper(plan)
	if err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 1)
	isolateNixSet(t)
	arm := armLaunchSignalsWith(o.keeperPreReadyTeardown(kp, cname, "podman"), func(code int) { codes <- code })
	// The arm that fired never disarms (its exit would have ended the process).
	t.Cleanup(func() { popLaunchArm(arm) })
	var out, errOut, jailOut, jailErr lockedBuffer
	if !kp.relay(&out, &errOut, &jailOut, &jailErr, keeperEvents{}) {
		t.Fatalf("the relay never saw the jail ready:\n%s", errOut.String())
	}
	// READY, and the arm not yet retargeted: the moment the launch's own goroutine is between
	// relayKeeper's return and arm.retarget.
	sent := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-codes:
		if code != 128+int(syscall.SIGINT) {
			t.Errorf("the launch exited %d, want %d", code, 128+int(syscall.SIGINT))
		}
	case <-time.After(keeperUnwindWait + 15*time.Second):
		t.Fatal("the launch's arm never exited")
	}
	took := time.Since(sent)
	select {
	case <-kp.exited:
	default:
		t.Errorf("the launch's arm exited after %s with its keeper still holding the jail: the keeper "+
			"waits for the first session's count, which only this process's exit let go", took.Round(time.Millisecond))
	}
	if took >= keeperUnwindWait {
		t.Errorf("the launch waited %s, its whole unwind bound (%s), for a keeper that could not end the jail "+
			"while the launch still counted itself in it", took.Round(time.Millisecond), keeperUnwindWait)
	}
	if n := jail.stopCount(); n != 1 {
		t.Errorf("the jail was stopped %d times, want once", n)
	}
	if rec, _ := readJailStop(cname); rec.Reason != lastSessionLeftReason {
		t.Errorf("the jail's stop recorded %q, want %q: a first session that never began still ends as the last session",
			rec.Reason, lastSessionLeftReason)
	}
}

// readyWindowLaunch is a fresh launch at the moment the window above opens: its keeper, started
// in-process, has said ready to the relay, and the launch's arm still runs the pre-ready teardown.
// counted says whether the launch counted its first session (holdSessionLock) or went on uncounted
// (plan.Uncounted, JL-P3). The exit the arm takes is on codes, and what the teardown prints on
// stderr.
type readyWindowLaunch struct {
	o      *Options
	cname  string
	jail   *fakeJail
	kp     *keeperProcess
	arm    *launchSignalArm
	codes  chan int
	stderr *lockedBuffer
}

func startReadyWindowLaunch(t *testing.T, cname string, counted bool, others func()) *readyWindowLaunch {
	t.Helper()
	saved := keeperUnwindWait
	keeperUnwindWait = 8 * time.Second
	t.Cleanup(func() { keeperUnwindWait = saved })
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	jail := newFakeJail(t, cname)
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Exec = jail.exec
	o.PIDAlive = func(int) bool { return false }
	o.RestoreTerminal = func() {}
	stderr := &lockedBuffer{}
	o.Stderr = stderr
	if counted {
		o.holdSessionLock(cname)
		t.Cleanup(o.releaseSessionLock)
		if o.sessionLock == nil {
			t.Fatal("the fixture could not count the first session")
		}
	}
	if others != nil {
		others()
	}
	cfg, err := encodeConfig(jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: o.Workspace, Cname: cname, Runtime: "podman",
		Config: cfg, SocketsDir: hostServiceSocketsDir(cname, false), RunCmd: jail.mainArgv(true),
		ImageRef: "the-image", Uncounted: !counted}
	kp, err := o.startKeeper(plan)
	if err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 1)
	isolateNixSet(t)
	arm := armLaunchSignalsWith(o.keeperPreReadyTeardown(kp, cname, "podman"), func(code int) { codes <- code })
	t.Cleanup(func() { popLaunchArm(arm) })
	var out, errOut, jailOut, jailErr lockedBuffer
	if !kp.relay(&out, &errOut, &jailOut, &jailErr, keeperEvents{}) {
		t.Fatalf("the relay never saw the jail ready:\n%s", errOut.String())
	}
	return &readyWindowLaunch{o: o, cname: cname, jail: jail, kp: kp, arm: arm, codes: codes, stderr: stderr}
}

// interrupt sends this process SIGINT in the window and returns how long the arm took to exit.
func (l *readyWindowLaunch) interrupt(t *testing.T) time.Duration {
	t.Helper()
	sent := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-l.codes:
		if code != 128+int(syscall.SIGINT) {
			t.Errorf("the launch exited %d, want %d", code, 128+int(syscall.SIGINT))
		}
	case <-time.After(keeperUnwindWait + 15*time.Second):
		t.Fatal("the launch's arm never exited")
	}
	return time.Since(sent)
}

// TestASIGINTInTheReadyWindowLeavesAJailOthersAreInWithoutWaiting: the same window with a second
// session already in the jail, one that attached once the container ran. Letting the first
// session's count go is then one session leaving a jail that stays up for another: the keeper keeps
// it, and the launch must say so and exit at once, as any session's quit does, rather than wait out
// its bound for a keeper that is not ending anything. Once the other session leaves, the keeper ends
// the jail as for any last session.
func TestASIGINTInTheReadyWindowLeavesAJailOthersAreInWithoutWaiting(t *testing.T) {
	cname := "yolo-ready-window-others"
	var other *sessionLock
	l := startReadyWindowLaunch(t, cname, true, func() {
		lock, _, err := takeSessionLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		other = lock
		t.Cleanup(other.release)
	})
	took := l.interrupt(t)
	if took >= keeperUnwindWait/2 {
		t.Errorf("the launch waited %s (its bound is %s) for a keeper that keeps the jail up for its other session",
			took.Round(time.Millisecond), keeperUnwindWait)
	}
	select {
	case <-l.kp.exited:
		t.Fatal("the keeper ended a jail another session is still in")
	default:
	}
	if n := l.jail.stopCount(); n != 0 {
		t.Errorf("the jail was stopped %d times with another session in it", n)
	}
	if got := l.stderr.String(); !strings.Contains(got, "stays up") {
		t.Errorf("the launch did not say its jail stays up for its other session; it printed:\n%s", got)
	}
	other.release()
	select {
	case <-l.kp.exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the keeper did not end the jail once its other session left")
	}
	if n := l.jail.stopCount(); n != 1 {
		t.Errorf("the jail was stopped %d times, want once", n)
	}
}

// TestASIGINTInTheReadyWindowOfAnUncountedLaunchDoesNotWaitForADrain: a first session the count
// could not hold has a keeper that never drains on the count (JL-P3), so after ready nothing the
// launch could wait for ends the jail: the launch says how to end it and exits at once.
func TestASIGINTInTheReadyWindowOfAnUncountedLaunchDoesNotWaitForADrain(t *testing.T) {
	cname := "yolo-ready-window-uncounted"
	l := startReadyWindowLaunch(t, cname, false, nil)
	took := l.interrupt(t)
	if took >= keeperUnwindWait/2 {
		t.Errorf("the launch waited %s (its bound is %s) for a keeper that never drains on the count",
			took.Round(time.Millisecond), keeperUnwindWait)
	}
	if n := l.jail.stopCount(); n != 0 {
		t.Errorf("the jail was stopped %d times", n)
	}
	if got := l.stderr.String(); !strings.Contains(got, stopRemedy("podman", cname)) {
		t.Errorf("the launch did not say how to end the jail it leaves up; it printed:\n%s", got)
	}
}

// TestASIGINTAfterTheReadyRetargetEndsOnlyThatSession is the same window resolved the other way:
// the launch's own goroutine read the ready frame and retargeted the arm to the session's teardown,
// as runContainer does at ready (arm.retarget with attachTeardown, then the lifeline's close), before
// the arm took the signal. The signal is then a session's: it hangs that session up in the jail and
// exits 130 at once, and the jail stays up for the other session, with its keeper. That teardown
// says so as the pre-ready one does (JL-D76), which sessionsignalstaysup_test.go pins for this order,
// so integration/keeperreadywindow_test.go requires the line in either order. Once this launch's
// count goes (its teardown lets it go to ask, as its exit would) and the other session leaves, the
// keeper ends the jail.
func TestASIGINTAfterTheReadyRetargetEndsOnlyThatSession(t *testing.T) {
	cname := "yolo-ready-window-retargeted"
	var other *sessionLock
	l := startReadyWindowLaunch(t, cname, true, func() {
		lock, _, err := takeSessionLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		other = lock
		t.Cleanup(other.release)
	})
	const sessionID = "00112233445566778899aabbccddeeff"
	if !l.arm.retarget(l.o.attachTeardown("podman", cname, sessionID)) {
		t.Fatal("the arm refused the retarget with no signal sent")
	}
	l.kp.closeLifeline()
	took := l.interrupt(t)
	if took >= keeperUnwindWait/2 {
		t.Errorf("the session's teardown took %s (the pre-ready teardown's bound is %s), and it waits on nothing",
			took.Round(time.Millisecond), keeperUnwindWait)
	}
	hangup := entrypoint.HangupSessionArg + " " + sessionID
	l.jail.mu.Lock()
	calls := append([]string(nil), l.jail.calls...)
	l.jail.mu.Unlock()
	hungUp := false
	for _, c := range calls {
		hungUp = hungUp || strings.HasSuffix(c, hangup)
	}
	if !hungUp {
		t.Errorf("the session's teardown did not hang up session %s in the jail; the runtime saw:\n%s",
			sessionID, strings.Join(calls, "\n"))
	}
	if n := l.jail.stopCount(); n != 0 {
		t.Errorf("the jail was stopped %d times with another session in it", n)
	}
	// The launch's exit lets its count go; the other session's count still keeps the jail up.
	l.o.releaseSessionLock()
	select {
	case <-l.kp.exited:
		t.Fatal("the keeper ended a jail another session is still in")
	case <-time.After(300 * time.Millisecond):
	}
	other.release()
	select {
	case <-l.kp.exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the keeper did not end the jail once its last session left")
	}
	if n := l.jail.stopCount(); n != 1 {
		t.Errorf("the jail was stopped %d times, want once", n)
	}
}
