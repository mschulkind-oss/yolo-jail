package run

// keeperquit_test.go drives a session's quit (endSession) into each of its branches, and the fresh
// launch's keeper spawn (startKeeper), so each of their call sites fails a test when deleted.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTheLastSessionsQuitStreamsItsKeepersTeardown: a quit whose shared lock was the last one finds
// the keeper holding the session lock, and waits for it, streaming its log, before it returns
// (JL-D11), through endSession itself.
func TestTheLastSessionsQuitStreamsItsKeepersTeardown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-last-quit"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	logF, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	// The keeper: blocked on the exclusive lock, which it gets once the session's goes, and then its
	// teardown, its log's last line, and its end.
	go func() {
		f, _ := openSessionLock(cname)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		time.Sleep(100 * time.Millisecond)
		_, _ = logF.WriteString("12:00:00.000 keeper: done\n")
		_ = logF.Close()
		_ = f.Close()
		releaseLock(live)
	}()
	o := goldenOptions("/ws", t.TempDir())
	var errBuf bytes.Buffer
	o.Stderr = &errBuf
	o.sessionLock = session
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: "abc123\n"}
	}
	if rc := o.endSession(cname, "podman", 0, time.Now(), 0, true); rc != 0 {
		t.Errorf("rc %d", rc)
	}
	if !strings.Contains(errBuf.String(), "keeper: done") {
		t.Errorf("the last quit did not stream the teardown:\n%s", errBuf.String())
	}
	if probeKeeper(cname) != keeperGone {
		t.Error("the last quit returned before its keeper was gone")
	}
}

// TestTheLastSessionOfAnUnkeptJailReapsIt is JL-D30 through endSession: the keeper is gone (its
// start record beside a free liveness lock), no other session holds the jail, so the quitting
// session stops the jail and removes what the keeper left, and says so.
func TestTheLastSessionOfAnUnkeptJailReapsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	const cname = "yolo-unkept-quit"
	if err := writeKeeperRecord(cname, keeperRecord{PID: 999999}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPIDFile(cname), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	o := goldenOptions("/ws", t.TempDir())
	var errBuf bytes.Buffer
	o.Stderr = &errBuf
	o.sessionLock = session
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "stop" {
			stopped = true
		}
		if !stopped && len(argv) > 1 && argv[1] == "ps" {
			return ExecResult{Ran: true, Stdout: "abc123\n"}
		}
		return ExecResult{Ran: true}
	}
	if rc := o.endSession(cname, "podman", 0, time.Now(), 0, false); rc != 0 {
		t.Errorf("rc %d", rc)
	}
	if !stopped {
		t.Error("the unkept jail's last session did not stop it")
	}
	if !strings.Contains(errBuf.String(), "This jail's keeper is gone, and this was its last session") {
		t.Errorf("the reap did not say so:\n%s", errBuf.String())
	}
	if _, ok := readKeeperRecord(cname); ok {
		t.Error("the reap left the dead keeper's start record")
	}
	if rec, _ := readJailStop(cname); !strings.Contains(rec.Reason, "its keeper (pid 999999) was gone") {
		t.Errorf("the reap recorded %q", rec.Reason)
	}
	if probeKeeper(cname) != keeperGone || sessionLockHeld(t, cname) {
		t.Error("the reap left a lock held")
	}
}

// TestTheFreshLaunchHandsItsLaunchLockToItsKeeper is JL-D31 through startKeeper: the keeper is
// handed the launch lock as a descriptor, and the launch's own copy is closed without releasing it,
// so the lock stays held until the keeper lets it go, and Run's deferred release is a no-op.
func TestTheFreshLaunchHandsItsLaunchLockToItsKeeper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	var handed *os.File
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, lock *os.File) (func() int, error) {
		removeKeeperPlan(planPath)
		fd, err := syscall.Dup(int(lock.Fd()))
		if err != nil {
			return nil, err
		}
		handed = os.NewFile(uintptr(fd), "keeper-launch-lock")
		return func() int { return 0 }, nil
	}
	o := goldenOptions("/ws", t.TempDir())
	o.holdLaunchLock("yolo-handoff")
	if o.launchLock == nil {
		t.Fatal("no launch lock to hand over")
	}
	kp, err := o.startKeeper(&keeperPlan{Workspace: "/ws", Cname: "yolo-handoff", Runtime: "podman", RunCmd: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	kp.closeLifeline()
	_ = kp.progress.Close()
	if handed == nil {
		t.Fatal("the keeper was handed no launch lock")
	}
	if !o.launchLock.isClosed() {
		t.Error("the launch still holds its copy of the lock it handed over")
	}
	o.releaseLaunchLock() // Run's deferred release, which must not release the keeper's
	probe, err := os.OpenFile(filepath.Join(filepath.Dir(sessionLockPath("yolo-handoff")), "yolo-handoff.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("the launch lock was released at the hand-off, not by the keeper")
	}
	releaseLock(handed)
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Errorf("the keeper's release did not free the lock: %v", err)
	}
}
