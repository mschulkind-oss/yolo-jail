package mcpauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHeaderStrictReadBudgetIsTwoLogicalSeconds(t *testing.T) {
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
	t.Cleanup(func() { storeLockNow = oldNow; storeLockSleep = oldSleep })
	start := f.clock.Now()
	now := start
	storeLockNow = func() time.Time { return now }
	storeLockSleep = func(step time.Duration) { now = now.Add(step) }
	if token, err := f.broker.Header(context.Background(), f.server, nil); token != "" || err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatal("held canonical lock did not produce busy refusal")
	}
	if now.Sub(start) != HeaderReadWait || HeaderReadWait != 2*time.Second {
		t.Fatalf("strict read budget=%s", now.Sub(start))
	}
	if f.as.callCount() != 1 {
		t.Fatal("busy read requested provider work")
	}
}

func TestHeaderMissingStateCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	b := &OnceBroker{Canonical: &CanonicalStore{Dir: dir, Filename: "records.json", LockName: "records.lock"}}
	if token, err := b.Header(context.Background(), OnceServer{Name: "fixture", Identity: strings.Repeat("a", 64)}, nil); token != "" || !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("missing state header=%q err=%v", token, err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("header created missing canonical state")
	}
}

func TestImmediateMaintenanceBypassesTransientBackoffAtMostOncePerThirtySeconds(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.clock.now = f.clock.now.Add(time.Hour + time.Minute)
	f.as.fail, f.as.status = "temporarily_unavailable", 503
	for range 3 {
		if err := f.broker.Maintain(context.Background(), f.server, nil, true); err != nil {
			t.Fatal(err)
		}
	}
	if f.as.callCount() != 3 {
		t.Fatalf("repeated immediate bypass: calls=%d", f.as.callCount())
	}
	f.clock.now = f.clock.now.Add(30 * time.Second)
	if err := f.broker.Maintain(context.Background(), f.server, nil, true); err != nil {
		t.Fatal(err)
	}
	if f.as.callCount() != 4 {
		t.Fatal("logical 30-second event did not allow next immediate bypass")
	}
}

func TestTwoImmediateBrokersRereadShortLivedUnexpiredRecord(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	other := f.newBroker(f.broker.Canonical.Dir)
	f.clock.now = f.clock.now.Add(time.Hour + time.Minute)
	f.as.lifeSec = 60
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, broker := range []*OnceBroker{f.broker, other} {
		go func(b *OnceBroker) { <-start; done <- b.Maintain(context.Background(), f.server, nil, true) }(broker)
	}
	close(start)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("two immediate brokers did not settle")
		}
	}
	if f.as.callCount() != 2 {
		t.Fatal("loser repeated short-lived token redemption instead of rereading valid record")
	}
}
