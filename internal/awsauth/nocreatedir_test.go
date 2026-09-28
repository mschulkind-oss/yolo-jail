package awsauth

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A daemon's broker (NoCreateDir) refuses a mint into a state dir that has gone, and leaves
// it gone: the daemon exits for that removal (hostservice.WatchStateDir), and a request
// landing before the exit must not bring back a directory a launch has just retired.
func TestANoCreateDirBrokerNeverRecreatesItsStateDir(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIA1"), &calls)
	dir := filepath.Join(filepath.Dir(b.StatePath), "state")
	b.StatePath, b.LockPath = filepath.Join(dir, StateFileName), filepath.Join(dir, LockFileName)
	b.NoCreateDir = true

	_, err := b.Fetch(context.Background(), "jail-a")
	if err == nil || !strings.Contains(err.Error(), "is gone") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("Fetch with a missing dir = %v, want an error saying %s is gone", err, dir)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the refused mint left %s behind (%v)", dir, err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("minted %d times with nowhere to cache the result, want 0", got)
	}

	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Fetch(context.Background(), "jail-a"); err != nil {
		t.Fatalf("Fetch with the dir present: %v", err)
	}
	if _, err := os.Stat(b.StatePath); err != nil {
		t.Errorf("no cache written into the existing dir: %v", err)
	}
}

// The default is unchanged: a broker that is not a daemon's creates what it needs.
func TestABrokerCreatesItsStateDirByDefault(t *testing.T) {
	var calls atomic.Int32
	b, now := testBroker(t, unnarrowed("p"), nil)
	b.Minter.Run = countingRunner(expiringIn(now, time.Hour, "ASIA1"), &calls)
	dir := filepath.Join(filepath.Dir(b.StatePath), "state")
	b.StatePath, b.LockPath = filepath.Join(dir, StateFileName), filepath.Join(dir, LockFileName)
	if _, err := b.Fetch(context.Background(), "jail-a"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := os.Stat(b.StatePath); err != nil {
		t.Fatalf("no cache written: %v", err)
	}
}
