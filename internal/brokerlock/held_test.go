package brokerlock

import (
	"context"
	"testing"
)

// Held is true exactly while a lock is held, for both lock-taking functions, and is false again
// after fn returns, whether it succeeded or not.
func TestHeldIsTrueOnlyInsideALock(t *testing.T) {
	dir := t.TempDir()
	if Held() {
		t.Fatal("Held before any lock")
	}
	if err := WithTransaction(dir, func() error {
		if !Held() {
			t.Error("Held is false inside WithTransaction")
		}
		return context.Canceled
	}); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	if Held() {
		t.Fatal("Held after WithTransaction returned")
	}
	if err := WithInspectionContext(context.Background(), dir, func() error {
		if !Held() {
			t.Error("Held is false inside WithInspectionContext")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if Held() {
		t.Fatal("Held after WithInspectionContext returned")
	}
}
