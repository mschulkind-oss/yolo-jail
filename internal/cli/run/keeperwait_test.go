package run

import (
	"testing"
	"time"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestWaitForKeeperReturnsForASessionAndWaitsForATeardown pins the suite's wait for a workspace's
// keeper (WaitForKeeper): a session still in the jail ends the wait at once, since the keeper is
// keeping the jail up for it; a keeper draining is waited for until it is gone; and one that
// neither drains nor holds a session is reported after the bound.
func TestWaitForKeeperReturnsForASessionAndWaitsForATeardown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}

	session, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := WaitForKeeper(ws, 10*time.Second); err != nil || time.Since(start) > 2*time.Second {
		t.Errorf("with a session in the jail: err=%v after %s, want at once", err, time.Since(start))
	}
	session.release()

	if err := WaitForKeeper(ws, 200*time.Millisecond); err == nil {
		t.Error("a keeper that never ends was not reported after the bound")
	}

	drain, ok := tryExclusiveSessionLock(cname)
	if !ok {
		t.Fatal("could not stand in for the keeper's drain")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		drain.release()
		releaseLock(live)
	}()
	start = time.Now()
	if err := WaitForKeeper(ws, 10*time.Second); err != nil || time.Since(start) < 100*time.Millisecond {
		t.Errorf("a draining keeper: err=%v after %s, want a wait until it was gone", err, time.Since(start))
	}
}
