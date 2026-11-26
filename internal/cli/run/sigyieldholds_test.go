package run

import (
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/sigyield"
)

// TestTheMacosUserArmHoldsItsSignalsFromTheTerminalArm: the macos-user arm decides each signal by the
// launch's phase, so while it is installed it holds them from the front door's terminal arm
// (internal/sigyield), which would otherwise end the launch on the first one; disarmed, it lets go.
func TestTheMacosUserArmHoldsItsSignalsFromTheTerminalArm(t *testing.T) {
	disarm := NewMacosUserArm().install(nil, nil)
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) && !sigyield.Held(s) {
			t.Errorf("%v is not held while the macos-user arm is installed", s)
		}
	}
	disarm()
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if sigyield.Held(s) {
			t.Errorf("%v is still held after the macos-user arm's disarm", s)
		}
	}
}

// TestThePodmanWaitHoldsItsCtrlC: the podman readiness wait catches a Ctrl-C to say which podman it
// left running and exit 130, so it holds SIGINT from the terminal arm until its catch is removed.
func TestThePodmanWaitHoldsItsCtrlC(t *testing.T) {
	_, stop := (&Options{}).readyInterrupt()
	if !sigyield.Held(os.Interrupt) {
		t.Error("SIGINT is not held while the podman wait catches it")
	}
	stop()
	if sigyield.Held(os.Interrupt) {
		t.Error("SIGINT is still held after the wait's catch is removed")
	}
}
