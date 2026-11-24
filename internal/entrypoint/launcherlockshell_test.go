package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// lockHarness runs the shared lock functions in bash, as the launchers do.
func lockHarness(t *testing.T, fake string, lock string, script string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	body := "STALE_LOCK=600\n" + stampMtimeFn + lockShellFns + script
	cmd := exec.Command("bash", "-c", body, "harness", lock)
	cmd.Env = []string{"HOME=" + filepath.Dir(lock), "PATH=" + fake + ":" + os.Getenv("PATH")}
	return cmd
}

func backdate(t *testing.T, path string) {
	t.Helper()
	backdatePath(t, path, 2*time.Hour)
}

// lockTry runs one _lock_try and returns its status.
func lockTry(t *testing.T, lock string) int {
	t.Helper()
	c := lockHarness(t, "/nonexistent", lock, `_lock_try "$1" mine "$STALE_LOCK"; exit $?`)
	if ee, ok := c.Run().(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 0
}

// raceForLock starts n bash processes at once (a gate FIFO they all read), each trying the lock
// and, on a win, appending to a log WITHOUT releasing it. It returns the number of winners.
func raceForLock(t *testing.T, lock string, n int) int {
	t.Helper()
	fake := fakeSuccessfulMkdir(t)
	dir := filepath.Dir(lock)
	gate := filepath.Join(dir, "gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	log := filepath.Join(dir, "winners")
	var cmds []*exec.Cmd
	for i := 0; i < n; i++ {
		c := lockHarness(t, fake, lock, `read -r _ < `+shellQuoteForTest(gate)+`
if _lock_try "$1" "tok.$$.$RANDOM" "$STALE_LOCK"; then echo won >> `+shellQuoteForTest(log)+`; fi
`)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, c)
	}
	w, err := os.OpenFile(gate, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		_, _ = w.WriteString("go\n")
	}
	// The writer stays open until every claimant is done, so a late reader still finds its line
	// instead of blocking on a pipe nobody writes to.
	for _, c := range cmds {
		_ = c.Wait()
	}
	w.Close()
	data, _ := os.ReadFile(log)
	return strings.Count(string(data), "won")
}

// Exactly one of many simultaneous claimants wins a free lock, mkdir being no help.
func TestLockClaimHasOneWinnerWhenMkdirIsNot(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".yolo-update.lock")
	if got := raceForLock(t, lock, 8); got != 1 {
		t.Errorf("%d claimants won the lock, want exactly 1", got)
	}
}

// Stale recovery is race-safe: of many waiters reaching a dead holder's lock at once, exactly one
// takes it. A remove-then-create reclaim lets two.
func TestStaleLockReclaimHasOneWinner(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".yolo-update.lock")
	if err := os.WriteFile(lock, []byte("dead.1.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdate(t, lock)
	if got := raceForLock(t, lock, 8); got != 1 {
		t.Errorf("%d waiters reclaimed the stale lock, want exactly 1", got)
	}
	if tok, _ := os.ReadFile(lock); strings.Contains(string(tok), "dead") {
		t.Errorf("the dead holder's lock was not replaced: %q", tok)
	}
}

// A reaper that died after winning the reap claim must not freeze the lock for good.
func TestAnOrphanedReapClaimIsCleared(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".yolo-update.lock")
	if err := os.WriteFile(lock, []byte("dead.1.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdate(t, lock)
	st, _ := os.Stat(lock)
	reap := lock + ".reap.dead.1.2." + strconv.FormatInt(st.ModTime().Unix(), 10)
	if err := os.WriteFile(reap, []byte("dead-reaper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdate(t, reap)
	if rc := lockTry(t, lock); rc != 1 {
		t.Fatalf("the first try meets the orphaned claim and clears it, busy=1, got %d", rc)
	}
	if rc := lockTry(t, lock); rc != 0 {
		t.Errorf("the next try must reap the stale lock, got %d", rc)
	}
}

// A lock directory left by a previous version: busy while young, taken over once stale, and a
// lock whose parent is missing cannot be taken at all (2), which is not "busy".
func TestLockTakesOverAnOldFormatDirectory(t *testing.T) {
	root := t.TempDir()
	lock := filepath.Join(root, ".yolo-update.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, ".yolo-lock-owner"), []byte("old.1.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := lockTry(t, lock); rc != 1 {
		t.Errorf("a young old-format lock is held, got %d", rc)
	}
	backdate(t, lock)
	if rc := lockTry(t, lock); rc != 0 {
		t.Errorf("a stale old-format lock is taken over, got %d", rc)
	}
	if st, err := os.Stat(lock); err != nil || st.IsDir() {
		t.Errorf("the lock is now a file (err=%v)", err)
	}
	if rc := lockTry(t, filepath.Join(root, "missing", ".yolo-update.lock")); rc != 2 {
		t.Errorf("a lock whose store is missing cannot be taken at all (2), got %d", rc)
	}
}

// Neither launcher template, nor the refresh, takes or drops a lock with mkdir or rmdir.
func TestNoLauncherLockIsAMkdir(t *testing.T) {
	native := nativeAgentLauncher("probe", &packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://example.invalid/x"},
		"/s", "/r", "", true, launcherServers{}, nil)
	for _, src := range []string{native, prelaunchRefreshShellFn} {
		for _, bad := range []string{`mkdir "$LOCK_DIR"`, `mkdir "$REFRESH_LOCK"`, `rmdir "$LOCK_DIR"`, `rmdir "$REFRESH_LOCK"`} {
			if strings.Contains(src, bad) {
				t.Errorf("a launcher still takes or drops a lock with %s", bad)
			}
		}
	}
}

// The Go refresh takes the same lock a launcher does, and is exclusive without mkdir.
func TestAcquirePrefixLockIsExclusiveAndTokenReleased(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".yolo-update.lock")
	now := time.Now()
	a := acquirePrefixLock(lock, now)
	if a == "" {
		t.Fatal("a free lock must be taken")
	}
	if b := acquirePrefixLock(lock, now); b != "" {
		t.Errorf("a held lock was taken again: %q", b)
	}
	releasePrefixLock(lock, "someone-else")
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("a release by a non-owner removed the lock: %v", err)
	}
	// A stale lock is reaped, with a new token.
	if c := acquirePrefixLock(lock, now.Add(24*time.Hour)); c == "" || c == a {
		t.Errorf("a stale lock must be taken over with a new token, got %q", c)
	}
	releasePrefixLock(lock, a)
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("the first holder's late release removed the new holder's lock: %v", err)
	}
	// An old-format directory is held while young and taken over when stale.
	dir := filepath.Join(t.TempDir(), ".yolo-update.lock")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if acquirePrefixLock(dir, now) != "" {
		t.Error("a young old-format directory is held")
	}
	if tok := acquirePrefixLock(dir, now.Add(24*time.Hour)); tok == "" {
		t.Error("a stale old-format directory must be taken over")
	}
}
