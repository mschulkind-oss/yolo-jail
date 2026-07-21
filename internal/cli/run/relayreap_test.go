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

// stubDeadPIDs makes every liveness probe answer "dead", so the reap sweep
// exercises its decision logic without relayKill ever signalling a real PID.
//
// Spawning a throwaway process and reusing its PID as a known-dead one does NOT
// work: the OS is free to recycle that PID immediately, and macOS — which wraps
// at PID_MAX 99999, four orders of magnitude below Linux's default 4194304 —
// does so readily on a busy `go test ./...` runner. When that happened in CI the
// sweep took the live branch and SIGTERMed an unrelated process; before
// relayKill's deadline was moved onto the wall clock, the drain loop then spun
// against this test's frozen o.Now and hung the whole package until the 10m
// test timeout fired. Never hand a real PID to kill-path code under test.
func stubDeadPIDs(o *Options) {
	o.PIDAlive = func(int) bool { return false }
}

// arbitraryPID is a placeholder written into the fixture pid files. It is never
// signalled (stubDeadPIDs reports it dead), so its value is irrelevant — which
// is precisely the property this fixture needs.
const arbitraryPID = 424242

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
	// Shrink the drain grace so this test isn't 3s of real sleep. The regression
	// it guards is the clock SOURCE (deadline built from the real wall clock, not
	// the frozen o.Now); the grace MAGNITUDE is orthogonal, and the 30s detector
	// below still catches a reintroduced frozen-clock spin regardless of grace.
	o.RelayKillGrace = 200 * time.Millisecond

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
// live-container set (cname is folded into the live set), a live sibling's
// relay is spared, and only an orphan whose hash matches no live jail (and older
// than the grace floor) is reaped.
func TestRelayReapOrphansCnameFold(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)

	currentName := "yolo-current-1111" // this jail — NOT in the live set yet
	liveSibling := "yolo-sibling-2222" // a running sibling jail
	orphanName := "yolo-orphan-3333"   // dead jail, relay leaked
	writePid := func(cname string) string {
		p := filepath.Join(base, "yolo-broker-relay-"+relayShortHash(cname)+".pid")
		if err := os.WriteFile(p, []byte(strconv.Itoa(arbitraryPID)+"\n"), 0o644); err != nil {
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
	stubDeadPIDs(o)

	// Live set contains only the sibling; the current jail is folded in by cname.
	live := map[string]struct{}{liveSibling: {}}

	// Bounded, like TestRelayKillFrozenClockTerminates: a regression in the
	// kill path must FAIL this test, not hang the package until go test's 10m
	// timeout — the opaque way the original frozen-clock spin presented in CI.
	var reaped []string
	done := make(chan struct{})
	go func() {
		reaped = o.relayReapOrphansIn(base, true, live, currentName)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("relayReapOrphansIn did not terminate — the reap/kill path is spinning")
	}

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
	if err := os.WriteFile(orphan, []byte(strconv.Itoa(arbitraryPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	o := &Options{Now: func() time.Time { return now }}
	fillDefaults(o)
	o.Now = func() time.Time { return now }
	stubDeadPIDs(o)

	reaped := o.relayReapOrphansIn(base, false, nil, "yolo-current-0000")
	if len(reaped) != 0 {
		t.Errorf("unknown liveness reaped %v, want none", reaped)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Error("pid file must survive when liveness is unknown")
	}
}
