package run

// armstackcontinue_test.go pins that the launch arms' one handler catches SIGCONT while an arm is
// installed, and routes it to no arm (armstack.go). Catching it is what makes a signal sent to a
// STOPPED launch reach it on macOS: XNU leaves a caught signal sent to a stopped process on a thread
// it does not wake, and only a caught SIGCONT aborts that thread's wait. The Nightly macOS
// Integration run lost the SIGINT TestASIGINTInTheReadyWindowLeavesTheJailUpForAnotherSession sent
// to its stopped launch so, and the launch ran its session for ten minutes and exited 0.
//
// Linux wakes the stopped task's pending signal itself, so the loss cannot be reproduced here; what
// this pins is the catch, which fails if SIGCONT leaves the handler's set, and that a SIGCONT never
// ends the launch, which fails if the router hands it to an arm.

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
)

func TestTheLaunchArmsCatchSIGCONTAndEndNothingOnIt(t *testing.T) {
	nixchildren.Isolate(t)
	exits := make(chan int, 4)
	arm := armLaunchSignalsWith(func() {}, func(code int) { exits <- code })
	t.Cleanup(func() { popLaunchArm(arm) })

	before := launchArmsResumed.Load()
	if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); launchArmsResumed.Load() == before; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the launch arms' handler never took a SIGCONT: on macOS a SIGINT or SIGTERM sent to a " +
				"stopped launch then waits on a thread nothing wakes, and the launch runs on as if never signalled")
		}
	}
	select {
	case code := <-exits:
		t.Fatalf("a SIGCONT ended the launch (exit %d): it is caught for the wakeup alone", code)
	case <-time.After(100 * time.Millisecond):
	}

	// The arm still takes the signal that ends the launch.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exits:
		if code != 128+int(syscall.SIGINT) {
			t.Errorf("the arm exited %d on a SIGINT after a SIGCONT, want %d", code, 128+int(syscall.SIGINT))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the arm did not take a SIGINT after a SIGCONT")
	}
}
