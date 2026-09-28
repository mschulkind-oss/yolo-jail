package openauthclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A pi process rotating its OAuth token holds auth.json.lock through proper-lockfile with
// `stale: 30000`, so the lock's mtime is refreshed every 15 s and a live holder's lock can be
// anywhere up to that old (pi 0.87.1 dist/core/auth-storage.js, acquireLockAsync). A 15 s old
// lock is therefore LIVE by pi's rule, and yolo must wait for it rather than break it: breaking
// it lets yolo's write and pi's rotated token overwrite one another.
func TestWritePiAuthWaitsForALivePiLockFifteenSecondsOld(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	lockPath := authPath + ".lock"
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	refreshed := time.Now().Add(-15 * time.Second)
	if err := os.Chtimes(lockPath, refreshed, refreshed); err != nil {
		t.Fatal(err)
	}
	piLock, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- WritePiAuth(authPath, json.RawMessage(`{"access_token":"broker-access","expires_at":4102444800000,"generation":3}`))
	}()

	// While "pi" holds the lock, yolo must neither return nor touch the lock or the file.
	select {
	case err := <-done:
		t.Fatalf("WritePiAuth returned (%v) while pi held a live 15 s old lock; it broke the lock", err)
	case <-time.After(300 * time.Millisecond):
	}
	if now, err := os.Stat(lockPath); err != nil || !os.SameFile(piLock, now) {
		t.Fatalf("pi's live lock was replaced or removed while pi held it: %v", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("yolo wrote auth.json under pi's live lock: %v", err)
	}

	// pi finishes its rotation under the lock, then releases it.
	if err := os.WriteFile(authPath, []byte(`{"anthropic":{"type":"oauth","access":"rotated","refresh":"rotated-refresh","expires":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WritePiAuth did not finish after pi released its lock")
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["anthropic"]["refresh"] != "rotated-refresh" {
		t.Fatalf("pi's rotated token was lost: %s", data)
	}
	if got["openai-codex"]["access"] != "broker-access" {
		t.Fatalf("yolo's broker view was not written: %s", data)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("yolo left its lock behind: %v", err)
	}
}

// A lock whose mtime pi stopped refreshing more than 30 s ago was left by a pi that died holding
// it (proper-lockfile's directory survives SIGKILL). pi itself reclaims it, so yolo must too.
func TestWritePiAuthReclaimsALockAbandonedPastPisStaleWindow(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	lockPath := authPath + ".lock"
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	abandoned := time.Now().Add(-31 * time.Second)
	if err := os.Chtimes(lockPath, abandoned, abandoned); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := WritePiAuth(authPath, json.RawMessage(`{"access_token":"access","expires_at":4102444800000,"generation":4}`)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("reclaiming an abandoned lock took %s", elapsed)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock remained after the write: %v", err)
	}
}

// The wait for a live holder is bounded, and the refusal names the lock and pi's rule.
func TestAcquirePiAuthLockGivesUpOnALiveHolderWithAClearMessage(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	lockPath := authPath + ".lock"
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	release, err := acquirePiAuthLock(authPath, piLockRule{stale: 30 * time.Second, wait: 200 * time.Millisecond})
	if err == nil {
		release()
		t.Fatal("acquired a lock a live pi holds")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("bounded wait ran %s", elapsed)
	}
	for _, want := range []string{lockPath, "held by a running pi", "30s without a refresh"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the live holder's lock was removed: %v", err)
	}
}

// The bound WritePiAuth uses must outlast pi's stale window, or a lock abandoned just before yolo
// started would make yolo give up instead of reclaiming it.
func TestPiAuthLockRuleMatchesPisStaleWindow(t *testing.T) {
	if piAuthLockRule.stale < 30*time.Second {
		t.Fatalf("stale = %s; pi holds a live lock for up to 30s between refreshes", piAuthLockRule.stale)
	}
	if piAuthLockRule.wait <= piAuthLockRule.stale {
		t.Fatalf("wait %s does not outlast stale %s", piAuthLockRule.wait, piAuthLockRule.stale)
	}
}
