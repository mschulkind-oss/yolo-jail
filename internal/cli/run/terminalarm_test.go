package run

import (
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/sigyield"
)

// TestTheTerminalArmPutsTheTerminalBackOnASignalNoArmTakes: with only the front door's terminal arm
// installed (the pack refresh, a config prompt, the staging before the launch guard), a SIGINT runs
// the restore and ends the process 130. It used to end the process at the signal's default, with the
// kitty tab still wearing the jail's title and colors.
func TestTheTerminalArmPutsTheTerminalBackOnASignalNoArmTakes(t *testing.T) {
	exits := seeArmExits(t, "yolo-terminal-arm-none")
	catchSignal(t, syscall.SIGINT)
	restored := make(chan struct{}, 4)
	disarm := ArmTerminalRestore(func() { restored <- struct{}{} })
	defer disarm()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-exits:
		if e.code != 130 {
			t.Errorf("the arm ended the process %d, want 130", e.code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the terminal arm never ended the process")
	}
	select {
	case <-restored:
	default:
		t.Error("the arm ended the process without putting the terminal back")
	}
}

// TestTheTerminalArmStandsBackForASignalAnotherHandlerHolds: a signal a handler of its own holds
// (internal/sigyield; the host floor's refresh records a Ctrl-C so the launch can decide) is that
// handler's, and the arm acts only on the next signal nobody holds. Signals reach the arm in order,
// so the SIGTERM after a held SIGINT is the one it ends the process with.
func TestTheTerminalArmStandsBackForASignalAnotherHandlerHolds(t *testing.T) {
	exits := seeArmExits(t, "yolo-terminal-arm-held")
	catchSignal(t, syscall.SIGINT)
	catchSignal(t, syscall.SIGTERM)
	disarm := ArmTerminalRestore(func() {})
	defer disarm()
	release := sigyield.Hold(syscall.SIGINT)
	defer release()
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case e := <-exits:
		if e.code != 128+int(syscall.SIGTERM) {
			t.Errorf("the arm ended the process %d, want %d: it acted on the held SIGINT", e.code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the terminal arm never ended the process")
	}
}

// TestTheTerminalArmIsTheLastWordsOfAHolderThatEndsTheProcess: a holder that ends the process on its
// signal (nixchildren.StopOnSignal, the TTY proxy's own teardown) calls sigyield.LastWords on the way
// out, and that is the terminal's restore while the arm is installed, and nothing once it is gone.
func TestTheTerminalArmIsTheLastWordsOfAHolderThatEndsTheProcess(t *testing.T) {
	seeArmExits(t, "yolo-terminal-arm-words")
	restored := 0
	disarm := ArmTerminalRestore(func() { restored++ })
	sigyield.LastWords()
	if restored != 1 {
		t.Errorf("last words under the arm restored %d times, want 1", restored)
	}
	disarm()
	sigyield.LastWords()
	if restored != 1 {
		t.Errorf("last words after the disarm restored again (%d)", restored)
	}
}
