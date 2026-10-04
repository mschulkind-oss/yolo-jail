package run

import (
	"syscall"
	"testing"
	"time"
)

// TestAPassASignalCutsShortLeavesItsClassDue: a signal's arm stops every nix the process has
// running and refuses any it would start after (nixchildren), so a housekeeping pass still running
// in that teardown has each of its `nix store delete`s refused at once and runs to its end having
// done nothing. Its debounce must not be stamped then, or one interrupted launch costs a day of not
// reclaiming: the slot's rule is a stamp on completion only. done() is called from inside the arm's
// teardown, where that pass finishes.
func TestAPassASignalCutsShortLeavesItsClassDue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	due, done := o.classDebounce("store-outputs")
	if !due {
		t.Fatal("expected due")
	}
	codes := make(chan int, 1)
	isolateNixSet(t)
	arm := armLaunchSignalsWith(done, func(code int) { codes <- code })
	t.Cleanup(func() { popLaunchArm(arm) })
	if code := signalArm(t, syscall.SIGTERM, codes); code != 128+int(syscall.SIGTERM) {
		t.Errorf("the arm exited %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	arm.awaitExit()
	if stillDue, _ := o.classDebounce("store-outputs"); !stillDue {
		t.Fatal("a pass a signal cut short stamped its debounce: the next launch must retry, not " +
			"wait out a day on deletes the stop refused")
	}
}
