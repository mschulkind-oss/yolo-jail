package awsauthdaemon

import (
	"testing"
	"time"
)

// testHangGuard bounds a test's wait on an event that either comes or never does: the time
// left before the test's own deadline, less a margin for reporting the failure and for the
// red path's cleanup, and a day when go test runs with -timeout 0. A wait under it passes the
// moment its event arrives, however loaded the machine; only a hang reaches the bound. It is
// never a budget the code has to beat (AGENTS.md, Testing: tests are event-driven, not timed).
func testHangGuard(t *testing.T) time.Duration {
	t.Helper()
	td, ok := t.Deadline()
	if !ok {
		return 24 * time.Hour
	}
	left := time.Until(td)
	margin := 30 * time.Second
	if left < 2*margin {
		margin = left / 2
	}
	return left - margin
}
