package testsupport

import (
	"testing"
	"time"
)

// noDeadlineBudget is the readiness budget when the test binary runs with no -timeout.
const noDeadlineBudget = 10 * time.Minute

// ReadinessBudget is how long a test fixture may wait for something it started to come up (a
// child process binding its socket, a goroutine publishing a file) or to go away, when the test
// is about WHAT happens rather than how long it takes.
//
// A fixed second loses on a loaded machine, where a child exec alone can be starved past it, and
// the test then fails for a reason it does not test. So the bound is the test binary's own
// deadline, less room for the failure report and cleanup: a wait that ends on its condition
// spends none of it, and one that never ends fails the test before -timeout panics the binary.
// It is never for a wait the test means to EXPIRE.
func ReadinessBudget(t *testing.T) time.Duration {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		return noDeadlineBudget
	}
	return BudgetWithin(time.Until(deadline))
}

// BudgetWithin is ReadinessBudget's bound for a caller holding only what is left of the
// binary's timeout (a TestMain, which has no t): remaining less thirty seconds, or half of
// remaining when that would leave less than half. A remaining of zero or less means no timeout.
func BudgetWithin(remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return noDeadlineBudget
	}
	if budget := remaining - 30*time.Second; budget > remaining/2 {
		return budget
	}
	return remaining / 2
}
