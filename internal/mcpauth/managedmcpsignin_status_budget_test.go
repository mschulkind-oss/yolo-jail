package mcpauth

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStatusStrictReadBudgetAndClosedBusyClassification(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	lock, err := os.Open(filepath.Join(f.broker.Canonical.Dir, "records.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	oldNow, oldSleep := storeLockNow, storeLockSleep
	t.Cleanup(func() { storeLockNow, storeLockSleep = oldNow, oldSleep })
	start := f.clock.Now()
	now := start
	storeLockNow = func() time.Time { return now }
	storeLockSleep = func(step time.Duration) { now = now.Add(step) }
	reader := &CanonicalStore{Dir: f.broker.Canonical.Dir, Filename: "records.json", LockName: "records.lock", Wait: HeaderReadWait}
	if _, err := reader.ReadStatus(); !errors.Is(err, ErrStoreBusy) {
		t.Fatal("status busy classification depended on arbitrary error text")
	}
	if now.Sub(start) != 2*time.Second || f.as.callCount() != 1 {
		t.Fatal("status exceeded logical lock budget or requested provider work")
	}
}
