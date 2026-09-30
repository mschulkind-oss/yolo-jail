package run

import (
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestNoLaunchTestOutlivesItsHousekeepingSlot: the housekeeping slot runs on its own goroutine
// (run.go's `running` hook) and reads HOME as it goes, so a test whose launch reaches the jail's
// start and returns while the slot still runs leaves it writing its debounce stamps under the NEXT
// test's HOME. That surfaced as "TempDir RemoveAll cleanup: directory not empty" in
// TestAssembleRunCmdForwardsTheHoldOptIn, which launches nothing: a `last-cache-reap` stamp landed
// in its HOME while its TempDir was being removed. dispatchOptions waits for the slot at cleanup,
// so a slot slowed past its launch's return has ended by the time its test has.
func TestNoLaunchTestOutlivesItsHousekeepingSlot(t *testing.T) {
	var reached, ended atomic.Bool
	t.Run("launch", func(t *testing.T) {
		writeUserPacks(t, packHome(t), `[]`)
		fakePodmanLaunch(t, func(o *Options) {
			exec := o.Exec
			o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
				if inHousekeepingSlot() && !reached.Swap(true) {
					time.Sleep(300 * time.Millisecond)
					ended.Store(true)
				}
				return exec(argv, dir, env, timeout)
			}
		})
	})
	switch {
	case !reached.Load():
		t.Error("the launch's test returned before its housekeeping slot made its first runtime call " +
			"(or the slot makes none, and this test needs another way to slow it)")
	case !ended.Load():
		t.Error("the launch's test returned while its housekeeping slot still ran: the slot writes " +
			"under whatever HOME the next test sets")
	}
}

// inHousekeepingSlot reports whether the calling goroutine is running the housekeeping slot.
func inHousekeepingSlot() bool {
	buf := make([]byte, 64<<10)
	return strings.Contains(string(buf[:goruntime.Stack(buf, false)]), ".runHousekeeping(")
}
