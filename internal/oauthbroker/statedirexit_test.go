package oauthbroker

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

// THE CALL SITE, in the real daemon process: Main exits once its state dir — BrokerDir, the
// CA and leaf it minted at startup and the refresh lock — is removed, and the dir stays
// removed. Delete Main's hostservice.WatchStateDir call and the child serves on past the
// deadline.
//
// Why exit rather than carry on (internal/hostservice/statedir.go): the CA and leaf are
// minted only when a daemon STARTS, so a live broker whose dir a launch retired is one the
// next claude launch adopts and never gets a CA from.
//
// The child runs the production poll interval, so this waits a few seconds by design.
func TestMainExitsWhenItsStateDirIsRemoved(t *testing.T) {
	sock, state, out, done, stop := startSingletonProc(t)
	defer stop()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("the singleton never bound %s: %v\ndaemon output:\n%s", sock, err, out())
	}
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the daemon exited with %v for a removed state dir, want a clean exit\n%s", err, out())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("the daemon is still serving 15s after its state dir was removed\n%s", out())
	}
	if _, err := os.Lstat(state); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the state dir came back after the daemon exited (%v)", err)
	}
}

// The refresh lock never recreates the state dir it lives in. It used to MkdirAll it, so the
// first refresh after a retirement moved the dir away brought it back — the recreation the
// daemon's exit exists to prevent. With the dir gone the refresh fails, and says so, rather
// than running unlocked.
func TestTheRefreshLockDoesNotRecreateItsDirectory(t *testing.T) {
	dir := t.TempDir() + "/state"
	RefreshLockPath = dir + "/refresh.lock"
	defer func() { RefreshLockPath = "" }()
	ran := false
	result := withRefreshLock(func() RefreshResult { ran = true; return errResult("ok", true) })
	if ran {
		t.Error("the refresh ran without its lock")
	}
	if code, _ := result.Get("error"); code != "creds_unreadable" {
		t.Errorf("result = %v, want the creds_unreadable error", result)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refresh lock recreated %s (%v)", dir, err)
	}
}
