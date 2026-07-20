package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// deadPID returns the PID of a process that has already exited and been
// reaped, so relayKill's liveness probe short-circuits before it signals
// anything.
//
// Do not hardcode a low PID here. On a typical Linux host PID 123 is a live
// kernel thread (scsi_eh_*), which this test would then SIGTERM — and on a
// host where the test user owns that PID, `go test` would kill a real
// process.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a throwaway process: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait() // reaped: the PID is now dead, not a zombie
	return pid
}

// TestRelayKillFrozenClockTerminates pins the drain loop against a frozen
// o.Now. relayKill's SIGTERM→wait→SIGKILL escalation must time out on the real
// wall clock: o.Now is an injectable logical clock that tests freeze, and
// building the deadline from it produced a loop whose condition was
// permanently true, hanging until the target happened to die on its own.
//
// The target ignores SIGTERM so the drain loop is genuinely entered and has to
// reach its deadline, which is the path that used to spin forever.
func TestRelayKillFrozenClockTerminates(t *testing.T) {
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a throwaway process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	pidFile := filepath.Join(t.TempDir(), "yolo-broker-relay-deadbeef.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	frozen := time.Now()
	o := &Options{Now: func() time.Time { return frozen }}
	fillDefaults(o)
	o.Now = func() time.Time { return frozen }

	done := make(chan struct{})
	go func() {
		o.relayKill(pidFile, "")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("relayKill did not terminate under a frozen clock — the drain " +
			"deadline is being built from o.Now() instead of the wall clock")
	}

	if _, err := os.Stat(pidFile); err == nil {
		t.Error("relayKill should remove the PID file")
	}
}

// TestRelayReapOrphansCnameFold checks the run-path backstop reap decision: the
// current jail's just-ensured relay is spared even though its cname is not in the
// live-container set (Python folds `{cname}` into `live_jails`), a live sibling's
// relay is spared, and only an orphan whose hash matches no live jail (and older
// than the grace floor) is reaped.
func TestRelayReapOrphansCnameFold(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)

	currentName := "yolo-current-1111" // this jail — NOT in the live set yet
	liveSibling := "yolo-sibling-2222" // a running sibling jail
	orphanName := "yolo-orphan-3333"   // dead jail, relay leaked
	dead := deadPID(t)
	writePid := func(cname string) string {
		p := filepath.Join(base, "yolo-broker-relay-"+relayShortHash(cname)+".pid")
		if err := os.WriteFile(p, []byte(strconv.Itoa(dead)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Age past the grace floor so mtime alone doesn't spare it.
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		return p
	}
	currentPid := writePid(currentName)
	siblingPid := writePid(liveSibling)
	orphanPid := writePid(orphanName)

	o := &Options{Now: func() time.Time { return now }}
	fillDefaults(o)
	o.Now = func() time.Time { return now }

	// Live set contains only the sibling; the current jail is folded in by cname.
	live := map[string]struct{}{liveSibling: {}}
	reaped := o.relayReapOrphansIn(base, true, live, currentName)

	if !reflect.DeepEqual(reaped, []string{orphanPid}) {
		t.Errorf("reaped %v, want [%s]", reaped, orphanPid)
	}
	// The current jail's relay pid file survives (cname fold).
	if _, err := os.Stat(currentPid); err != nil {
		t.Error("current jail's relay must be spared")
	}
	// The live sibling's relay pid file survives.
	if _, err := os.Stat(siblingPid); err != nil {
		t.Error("live sibling's relay must be spared")
	}
	// The orphan's pid file is removed (apply=true).
	if _, err := os.Stat(orphanPid); err == nil {
		t.Error("orphan relay pid file should be removed")
	}
}

// TestRelayReapOrphansUnknownLivenessDeclines checks the fail-safe polarity: when
// liveness cannot be enumerated (known==false), the sweep reaps nothing — unknown
// must never read as "nothing live".
func TestRelayReapOrphansUnknownLivenessDeclines(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	orphan := filepath.Join(base, "yolo-broker-relay-"+relayShortHash("yolo-dead-9999")+".pid")
	if err := os.WriteFile(orphan, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	o := &Options{Now: func() time.Time { return now }}
	fillDefaults(o)
	o.Now = func() time.Time { return now }

	reaped := o.relayReapOrphansIn(base, false, nil, "yolo-current-0000")
	if len(reaped) != 0 {
		t.Errorf("unknown liveness reaped %v, want none", reaped)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Error("pid file must survive when liveness is unknown")
	}
}
