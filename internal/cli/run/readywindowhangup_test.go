package run

// readywindowhangup_test.go pins what the fresh launch's teardown before ready says on a SIGHUP
// (keeperPreReadyTeardown, awaitKeeperUnwind; docs/design/jail-lifetime-last-session-wins.md JL-D76).
// A SIGHUP is a closed pane or window, which leaves no terminal to read a line, so neither teardown
// a launch's arm can run says that the jail stays up: the session's said nothing on one already
// (TestAHangupSaysNothingOfTheJailStayingUp), and the one before ready, which takes a signal in the
// ready window when the arm wins the race to the retarget, now says nothing either. It still lets
// the first session's count go and returns as soon as the jail is known to stay up, as on a SIGINT.
//
// Each test drives the real signal arm with a real SIGHUP to this process, in the ready window,
// against TestMain's in-process keeper (startReadyWindowLaunch).

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestAHangupInTheReadyWindowSaysNothingOfTheJailStayingUp: a SIGHUP before the retarget, with
// another session in the jail, ends the launch at once, as a SIGINT there does, and the keeper keeps
// the jail up for the other session, but nothing is said of it: the pane that would read it is gone.
func TestAHangupInTheReadyWindowSaysNothingOfTheJailStayingUp(t *testing.T) {
	cname := "yolo-ready-window-hangup"
	var other *sessionLock
	l := startReadyWindowLaunch(t, cname, true, func() { other = holdAnotherSession(t, cname) })
	sent := time.Now()
	if code := signalArm(t, syscall.SIGHUP, l.codes); code != 128+int(syscall.SIGHUP) {
		t.Errorf("the launch exited %d, want %d", code, 128+int(syscall.SIGHUP))
	}
	if took := time.Since(sent); took >= keeperUnwindWait/2 {
		t.Errorf("the launch waited %s (its bound is %s) for a keeper that keeps the jail up for its other session",
			took.Round(time.Millisecond), keeperUnwindWait)
	}
	if got := l.stderr.String(); strings.Contains(got, "stays up") {
		t.Errorf("a hung-up launch said the jail stays up, to a pane that is gone:\n%s", got)
	}
	select {
	case <-l.kp.exited:
		t.Fatal("the keeper ended a jail another session is still in")
	default:
	}
	if n := l.jail.stopCount(); n != 0 {
		t.Errorf("the jail was stopped %d times with another session in it", n)
	}
	other.release()
	awaitKeeperEnd(t, l, "the keeper did not end the jail once its other session left: the hung-up launch's "+
		"teardown never let the first session's count go")
}

// TestAHangupInTheReadyWindowOfAnUncountedLaunchSaysNothingOfTheJailStayingUp: a first session the
// count could not hold leaves a jail its keeper never drains on the count (JL-P3). On a SIGINT the
// launch says so and how to end it; on a SIGHUP there is nobody to tell, so it says nothing and does
// not wait either.
func TestAHangupInTheReadyWindowOfAnUncountedLaunchSaysNothingOfTheJailStayingUp(t *testing.T) {
	cname := "yolo-ready-window-hangup-uncounted"
	l := startReadyWindowLaunch(t, cname, false, nil)
	sent := time.Now()
	if code := signalArm(t, syscall.SIGHUP, l.codes); code != 128+int(syscall.SIGHUP) {
		t.Errorf("the launch exited %d, want %d", code, 128+int(syscall.SIGHUP))
	}
	if took := time.Since(sent); took >= keeperUnwindWait/2 {
		t.Errorf("the launch waited %s (its bound is %s) for a keeper that never drains on the count",
			took.Round(time.Millisecond), keeperUnwindWait)
	}
	if got := l.stderr.String(); strings.Contains(got, "stays up") {
		t.Errorf("a hung-up launch said the jail stays up, to a pane that is gone:\n%s", got)
	}
	if n := l.jail.stopCount(); n != 0 {
		t.Errorf("the jail was stopped %d times", n)
	}
}
