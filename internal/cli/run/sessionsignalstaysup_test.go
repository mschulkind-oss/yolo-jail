package run

// sessionsignalstaysup_test.go pins what a session's signal teardown (attachTeardown) says of the
// jail it leaves (docs/design/jail-lifetime-last-session-wins.md JL-D76). A SIGINT or a SIGTERM
// leaves the terminal that ran the session, so when other sessions keep the jail up the teardown
// prints the one line a session's quit prints then (noteJailStaysUp), without the count its own exec
// can still be in: once, on the terminal alone, as the quit's goes, once the terminal's modes are back
// and before its jail indicator is, so the line lands in the tab that ran the session. The last
// session's teardown says nothing of the jail staying up, and lets its count go as a quit does
// before it asks. A SIGHUP is a closed pane, which leaves no terminal to read the line, so it says
// nothing either and asks nothing.
//
// Each test drives the real signal arm with a real signal to this process, against TestMain's
// in-process keeper (startReadyWindowLaunch): the fresh launch's arm retargeted at ready, as
// runContainer retargets it, and an attach's arm of the same kind around the same teardown.

import (
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// staysUpSessionID is the session id each teardown here hangs up.
const staysUpSessionID = "00112233445566778899aabbccddeeff"

// signalArm sends this process sig and returns the exit the arm took, on codes.
func signalArm(t *testing.T, sig syscall.Signal, codes <-chan int) int {
	t.Helper()
	if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-codes:
		return code
	case <-time.After(keeperUnwindWait + 15*time.Second):
		t.Fatalf("the arm never exited on %s", sig)
		return 0
	}
}

// retargetToTheSession is runContainer at ready: the arm retargeted to the session's teardown,
// then the lifeline closed.
func (l *readyWindowLaunch) retargetToTheSession(t *testing.T) {
	t.Helper()
	if !l.arm.retarget(l.o.attachTeardown("podman", l.cname, staysUpSessionID)) {
		t.Fatal("the arm refused the retarget with no signal sent")
	}
	l.kp.closeLifeline()
}

// holdAnotherSession counts a second session in cname's jail for the rest of the test.
func holdAnotherSession(t *testing.T, cname string) *sessionLock {
	t.Helper()
	lock, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lock.release)
	return lock
}

// awaitKeeperEnd waits for the keeper to end the jail once its last session has gone.
func awaitKeeperEnd(t *testing.T, l *readyWindowLaunch, why string) {
	t.Helper()
	select {
	case <-l.kp.exited:
	case <-time.After(15 * time.Second):
		t.Fatal(why)
	}
	if n := l.jail.stopCount(); n != 1 {
		t.Errorf("the jail was stopped %d times, want once", n)
	}
}

// staysUpLine is the start of noteJailStaysUp's line for cname.
func staysUpLine(cname string) string { return "Jail " + cname + " stays up for" }

// TestASignalThatLeavesATerminalSaysTheJailStaysUpForTheOthersOnce: one of two sessions ended by
// a SIGINT or a SIGTERM says, once, that the jail stays up for the other, between putting its
// terminal's modes back and its jail indicator, and on the terminal alone, not in launch.log
// (terminalOnly, where the quit's line goes); the
// keeper keeps the jail up, and ends it once the other session leaves. For the fresh launch's first
// session, whose arm was retargeted at ready, and for an attach, whose arm runs the same teardown.
func TestASignalThatLeavesATerminalSaysTheJailStaysUpForTheOthersOnce(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run("the first session/"+sig.String(), func(t *testing.T) {
			cname := "yolo-staysup-first-" + strings.ToLower(strings.ReplaceAll(sig.String(), " ", "-"))
			var other *sessionLock
			l := startReadyWindowLaunch(t, cname, true, func() { other = holdAnotherSession(t, cname) })
			var launchLog lockedBuffer
			l.o.Stderr = teeLog{w: l.stderr, log: &launchLog}
			l.retargetToTheSession(t)
			moments := watchTerminal(l.o, l.arm, l.stderr)

			if code := signalArm(t, sig, l.codes); code != 128+int(sig) {
				t.Errorf("the session's arm exited %d, want %d", code, 128+int(sig))
			}
			assertStaysUpOnce(t, cname, l.stderr.String(), launchLog.String(), moments)
			select {
			case <-l.kp.exited:
				t.Fatal("the keeper ended a jail another session is still in")
			default:
			}
			if n := l.jail.stopCount(); n != 0 {
				t.Errorf("the jail was stopped %d times with another session in it", n)
			}
			other.release()
			awaitKeeperEnd(t, l, "the keeper did not end the jail once its other session left")
		})

		t.Run("an attach/"+sig.String(), func(t *testing.T) {
			cname := "yolo-staysup-attach-" + strings.ToLower(strings.ReplaceAll(sig.String(), " ", "-"))
			// The fresh launch is the other session: it counted itself before its spawn and holds
			// that count while it runs.
			l := startReadyWindowLaunch(t, cname, true, nil)
			a := goldenOptions(t.TempDir(), t.TempDir())
			a.Exec = l.jail.exec
			a.PIDAlive = func(int) bool { return false }
			attachErr := &lockedBuffer{}
			var launchLog lockedBuffer
			a.Stderr = teeLog{w: attachErr, log: &launchLog}
			a.holdSessionLock(cname)
			t.Cleanup(a.releaseSessionLock)
			if a.sessionLock == nil {
				t.Fatal("the attach could not count itself in the jail")
			}
			codes := make(chan int, 1)
			arm := armLaunchSignalsWith(a.attachTeardown("podman", cname, staysUpSessionID), func(code int) { codes <- code })
			t.Cleanup(func() { popLaunchArm(arm) })
			moments := watchTerminal(a, arm, attachErr)

			if code := signalArm(t, sig, codes); code != 128+int(sig) {
				t.Errorf("the attach's arm exited %d, want %d", code, 128+int(sig))
			}
			assertStaysUpOnce(t, cname, attachErr.String(), launchLog.String(), moments)
			if got := l.stderr.String(); strings.Contains(got, "stays up") {
				t.Errorf("the other session printed the attach's line:\n%s", got)
			}
			if n := l.jail.stopCount(); n != 0 {
				t.Errorf("the jail was stopped %d times with another session in it", n)
			}
			l.o.releaseSessionLock() // the fresh launch's session quits
			awaitKeeperEnd(t, l, "the keeper did not end the jail once its last session left")
		})
	}
}

// modesHandle stands in for a session's runtime client at its arm (sessionHandle): its Terminate is
// where the TTY proxy puts the terminal's modes back, which the arm calls before its teardown, and it
// records how much the terminal had then. There is no client to kill.
type modesHandle struct {
	at       *int
	terminal *lockedBuffer
}

func (h modesHandle) Terminate() bool { *h.at = len(h.terminal.String()); return true }

func (modesHandle) Kill() {}

// terminalMoments is how much a session's terminal had when its teardown put each half of it back:
// its modes (modesAt, the proxy's Terminate) and its jail indicator (indicatorAt, RestoreTerminal).
// -1 for a half never put back.
type terminalMoments struct{ modesAt, indicatorAt int }

// watchTerminal records o's terminal moments on terminal, through arm's session handle and o's
// RestoreTerminal.
func watchTerminal(o *Options, arm *launchSignalArm, terminal *lockedBuffer) *terminalMoments {
	m := &terminalMoments{modesAt: -1, indicatorAt: -1}
	o.RestoreTerminal = func() { m.indicatorAt = len(terminal.String()) }
	arm.attach(modesHandle{at: &m.modesAt, terminal: terminal})
	return m
}

// assertStaysUpOnce: the stays-up line is on the terminal exactly once, and not in launch.log. It
// comes once the terminal's modes are back, so it is not printed into a raw terminal, and before its
// jail indicator is, so it lands in the tab that ran the session rather than one already handed
// back to the shell (the order of keeperPreReadyTeardown's last words, and of
// TestTerminateClosureRestoresTheTerminalAfterTheReport).
func assertStaysUpOnce(t *testing.T, cname, terminal, launchLog string, m *terminalMoments) {
	t.Helper()
	if n := strings.Count(terminal, staysUpLine(cname)); n != 1 {
		t.Fatalf("the teardown said %d times that the jail stays up for its other session, want once; "+
			"it printed:\n%s", n, terminal)
	}
	at := strings.Index(terminal, staysUpLine(cname))
	if m.modesAt < 0 {
		t.Error("the arm never put the terminal's modes back")
	} else if at < m.modesAt {
		t.Errorf("the line was printed before the terminal's modes were put back:\n%s", terminal)
	}
	if m.indicatorAt < 0 {
		t.Error("the teardown never put the terminal's jail indicator back")
	} else if at+len(staysUpLine(cname)) > m.indicatorAt {
		t.Errorf("the line was printed after the terminal's jail indicator was put back, into a tab already "+
			"handed back to the shell:\n%s", terminal)
	}
	if strings.Contains(launchLog, "stays up") {
		t.Errorf("the line went to launch.log too, where the quit's never goes:\n%s", launchLog)
	}
	if !strings.Contains(terminal, "re-enters it") {
		t.Errorf("the line is not the quit's (noteJailStaysUp):\n%s", terminal)
	}
}

// TestASignalEndedSessionStatesNoCountItsOwnExecIsIn: the jail's count of its sessions is its live
// exec sessions (jailSessionCount), and a session a signal ends can still be one of them when its
// teardown speaks: its exec client is killed only once the teardown has returned, and in the ready
// window the hangup can reach the jail before the first session's exec has named itself, which then
// ends nothing. A nested jail measured that: a first session whose arm was retargeted at ready said
// its jail stays up for 2 other sessions, with one other session in and its own exec still running
// there. So the line names the jail's other sessions without a number. The runtime here answers as
// it did there: two execs, the other session's and this session's own.
func TestASignalEndedSessionStatesNoCountItsOwnExecIsIn(t *testing.T) {
	cname := "yolo-staysup-count"
	var other *sessionLock
	l := startReadyWindowLaunch(t, cname, true, func() { other = holdAnotherSession(t, cname) })
	l.jail.reportExecs(2)
	l.retargetToTheSession(t)
	if code := signalArm(t, syscall.SIGINT, l.codes); code != 128+int(syscall.SIGINT) {
		t.Errorf("the session's arm exited %d, want %d", code, 128+int(syscall.SIGINT))
	}
	got := l.stderr.String()
	if counted := regexp.MustCompile(`stays up for \d`); counted.MatchString(got) {
		t.Errorf("the line states a count of the jail's other sessions, which this session's own exec is "+
			"still in:\n%s", got)
	}
	if !strings.Contains(got, staysUpLine(cname)+" its other sessions;") {
		t.Errorf("the line does not name the jail's other sessions without a number:\n%s", got)
	}
	other.release()
	awaitKeeperEnd(t, l, "the keeper did not end the jail once its other session left")
}

// TestASignalToTheLastSessionSaysNothingOfTheJailStayingUp: the last session's SIGINT says nothing
// of the jail staying up. Its teardown lets the session's count go before it looks, as a quit does,
// so the keeper ends the jail then, without waiting for the process's exit, which the fake exit
// here never lets go of anything.
func TestASignalToTheLastSessionSaysNothingOfTheJailStayingUp(t *testing.T) {
	cname := "yolo-staysup-last"
	l := startReadyWindowLaunch(t, cname, true, nil)
	l.retargetToTheSession(t)
	if code := signalArm(t, syscall.SIGINT, l.codes); code != 128+int(syscall.SIGINT) {
		t.Errorf("the session's arm exited %d, want %d", code, 128+int(syscall.SIGINT))
	}
	if got := l.stderr.String(); strings.Contains(got, "stays up") {
		t.Errorf("the last session's teardown said the jail stays up:\n%s", got)
	}
	awaitKeeperEnd(t, l, "the keeper did not end the jail after its last session's SIGINT: the teardown "+
		"never let the session's count go, so it cannot have asked whether others remain")
}

// TestTheArmNamesTheSignalItEndsTheProcessOn: the teardown an arm runs reads the signal the arm
// took (signalEndingTheProcess), and once a test's exit has returned nothing is ending the process,
// so a teardown called directly afterwards, as other tests here call one, reads no signal and says
// nothing of the jail.
func TestTheArmNamesTheSignalItEndsTheProcessOn(t *testing.T) {
	var seen syscall.Signal
	var ending bool
	codes := make(chan int, 1)
	arm := armLaunchSignalsWith(func() { seen, ending = signalEndingTheProcess() }, func(code int) { codes <- code })
	t.Cleanup(func() { popLaunchArm(arm) })
	if code := signalArm(t, syscall.SIGTERM, codes); code != 128+int(syscall.SIGTERM) {
		t.Errorf("the arm exited %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	if !ending || seen != syscall.SIGTERM {
		t.Errorf("the teardown read %v (ending %v), want the arm's SIGTERM", seen, ending)
	}
	arm.awaitExit()
	if sig, ending := signalEndingTheProcess(); ending {
		t.Errorf("after the arm's exit returned, the process still reads as ending on %v", sig)
	}
}

// TestAHangupSaysNothingOfTheJailStayingUp: a SIGHUP is a closed pane, with no terminal left to read
// the line, so the teardown neither prints it nor asks whether others remain, though another
// session is in the jail. Its count stays held until the exit, as before, which keeps the
// teardown inside a multiplexer's SIGKILL budget (sessionHangupTimeout).
func TestAHangupSaysNothingOfTheJailStayingUp(t *testing.T) {
	cname := "yolo-staysup-hangup"
	var other *sessionLock
	l := startReadyWindowLaunch(t, cname, true, func() { other = holdAnotherSession(t, cname) })
	l.retargetToTheSession(t)
	if code := signalArm(t, syscall.SIGHUP, l.codes); code != 128+int(syscall.SIGHUP) {
		t.Errorf("the session's arm exited %d, want %d", code, 128+int(syscall.SIGHUP))
	}
	if got := l.stderr.String(); strings.Contains(got, "stays up") {
		t.Errorf("a hung-up session said the jail stays up, to a pane that is gone:\n%s", got)
	}
	if l.o.sessionLock == nil {
		t.Error("a hung-up session's teardown let its count go to ask whether others remain, which a closed " +
			"pane has no terminal to be told")
	}
	l.o.releaseSessionLock() // the exit
	other.release()
	awaitKeeperEnd(t, l, "the keeper did not end the jail once its sessions left")
}
