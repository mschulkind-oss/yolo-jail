package testsupport

import (
	"testing"
	"time"
)

func TestBudgetWithinLeavesRoomBeforeTheDeadline(t *testing.T) {
	for _, tc := range []struct{ remaining, want time.Duration }{
		{10 * time.Minute, 10*time.Minute - 30*time.Second},
		{40 * time.Second, 20 * time.Second},
		{0, noDeadlineBudget},
		{-time.Second, noDeadlineBudget},
	} {
		if got := BudgetWithin(tc.remaining); got != tc.want {
			t.Errorf("BudgetWithin(%s) = %s, want %s", tc.remaining, got, tc.want)
		}
	}
}

func TestReadinessBudgetEndsBeforeTheTestDeadline(t *testing.T) {
	got := ReadinessBudget(t)
	if got <= time.Second {
		t.Fatalf("ReadinessBudget = %s, want more than the fixed second it replaces", got)
	}
	if deadline, ok := t.Deadline(); ok && !time.Now().Add(got).Before(deadline) {
		t.Errorf("ReadinessBudget = %s reaches past the test deadline %s", got, deadline)
	}
}
