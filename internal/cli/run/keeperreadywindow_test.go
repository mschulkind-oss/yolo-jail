package run

// keeperreadywindow_test.go pins a fresh launch interrupted between its keeper's "ready" and the
// moment its signal arm is retargeted to the first session's own teardown
// (docs/design/jail-lifetime-last-session-wins.md JL-D58, JL-D74). The arm still runs the
// pre-ready teardown there, which closes the lifeline, but the keeper stops reading the lifeline at
// ready: from then on the jail ends when its last session's count goes. The first session's count
// was this launch's own, held until the process exited, so the teardown used to wait out its whole
// bound (keeperUnwindWait, 20 s) for a keeper that could not drain until it gave up.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(jail.dir, "stop"), nil, 0o644) })
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
	arm := armLaunchSignalsWith(o.keeperPreReadyTeardown(kp, cname, "podman"), func(code int) { codes <- code })
	// The arm that fired never disarms (its exit would have ended the process).
	t.Cleanup(func() { popLaunchArm(arm) })
	var out, errOut, jailOut, jailErr lockedBuffer
	if !relayKeeper(kp.progress, &out, &errOut, &jailOut, &jailErr, keeperEvents{}) {
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
