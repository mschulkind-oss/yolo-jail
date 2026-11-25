package run

import (
	"runtime"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// A per-jail child the launch reaped carries its known exit status in the collected outcome; a
// signalled child, which has no exit code, and a child still alive at the deadline do not invent
// one. Driven through the actual selected start loop with the self-exec fixture child.
func TestPerJailStartupOutcomeKeepsTheReapedExitStatus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	// Every case ends on an event, never on the clock: the exit cases when the child exits, the
	// reason case when its accepted record ends the wait. The budget is only a failure guard.
	budget := readyFixtureBudget(t)
	for _, tc := range []struct {
		mode    string
		timeout time.Duration
		process hostservice.StartupProcessState
		known   bool
		status  int
	}{
		{"exit", budget, hostservice.StartupProcessExited, true, 2},
		{"exit0", budget, hostservice.StartupProcessExited, true, 0},
		{"signal", budget, hostservice.StartupProcessExited, false, 0},
		{"reason", budget, hostservice.StartupProcessAlive, false, 0},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			o, handles, _, output := runPerJailReasonFixture(t, tc.mode, tc.timeout, false, 0)
			if len(handles) != 0 || len(o.startupOutcomes) != 1 {
				t.Fatalf("selected start not observed: handles=%d outcomes=%+v\n%s", len(handles), o.startupOutcomes, output)
			}
			got := o.startupOutcomes[0]
			if got.ReasonRead.Kind != hostservice.StartupReasonReadRecord {
				t.Fatalf("the child's record was not read: %+v", got)
			}
			if got.Process != tc.process || got.ProcessExitStatusKnown != tc.known || got.ProcessExitStatus != tc.status {
				t.Fatalf("process evidence = %s known=%v status=%d, want %s known=%v status=%d (%+v)",
					got.Process, got.ProcessExitStatusKnown, got.ProcessExitStatus, tc.process, tc.known, tc.status, got)
			}
		})
	}
}

// A fronted per-jail service is ready only once its socket accepts a connect, so its outcome says
// accepted. (A legacy no-transport entry is ready on bare file existence and reports observed.)
// The reader is owner-cancelled only when readiness lands before the reader's own deadline, so
// the budget is the test's lifetime rather than a guess at how fast the child starts.
func TestPerJailFrontedReadinessIsAccepted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and binds an AF_UNIX socket")
	}
	o, handles, _, output := runPerJailReasonFixture(t, "ready", readyFixtureBudget(t), true, 0)
	if len(handles) != 1 || len(o.startupOutcomes) != 1 {
		t.Fatalf("ready fixture did not start: handles=%d outcomes=%+v\n%s", len(handles), o.startupOutcomes, output)
	}
	if got := o.startupOutcomes[0]; got.Kind != hostservice.StartupKindReady || got.Readiness != hostservice.StartupReadinessAccepted ||
		got.ReasonRead.Kind != hostservice.StartupReasonReadCancelled {
		t.Fatalf("fronted readiness = %s/%s read=%s, want ready/accepted/owner-cancelled: %+v",
			got.Kind, got.Readiness, got.ReasonRead.Kind, got)
	}
}
