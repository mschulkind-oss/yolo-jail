package runcmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// flockNBSucceeds reports whether a non-blocking exclusive flock on f succeeds
// (releasing it immediately if so).
func flockNBSucceeds(f *os.File) bool {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true
}

// TestReapOrphanedJailsPolarity checks the reaping polarity: a live jail whose
// recorded owner PID is DEAD is reaped; one with a LIVE owner PID or NO owner
// file is left alone.
func TestReapOrphanedJailsPolarity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	// orphan-jail: owner pid 999999 (dead). live-jail: owner = our own pid.
	// no-owner-jail: no owner file.
	deadPID := 999999
	_ = os.WriteFile(ownerPIDFile("yolo-orphan"), []byte(strconv.Itoa(deadPID)+"\n"), 0o644)
	_ = os.WriteFile(ownerPIDFile("yolo-live"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)

	var stopped []string
	o := &Options{
		Getenv: func(string) string { return "" },
		Now:    time.Now,
		Stdout: discardBuf(),
		// liveYoloContainers → all three "live".
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			key := strings.Join(argv, " ")
			if strings.Contains(key, "ps -a --format") {
				return ExecResult{Ran: true, RC: 0, Stdout: "yolo-orphan running\nyolo-live running\nyolo-noowner running\n"}
			}
			if strings.Contains(key, "stop") {
				// record which container is stopped (argv: podman stop -t N name).
				stopped = append(stopped, argv[len(argv)-1])
				return ExecResult{Ran: true, RC: 0}
			}
			return ExecResult{Ran: false}
		},
	}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.Stdout = discardBuf()
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		key := strings.Join(argv, " ")
		if strings.Contains(key, "ps -a --format") {
			return ExecResult{Ran: true, RC: 0, Stdout: "yolo-orphan running\nyolo-live running\nyolo-noowner running\n"}
		}
		if strings.Contains(key, " stop ") || (len(argv) > 1 && argv[1] == "stop") {
			stopped = append(stopped, argv[len(argv)-1])
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: false}
	}

	o.reapOrphanedJails("podman")

	if len(stopped) != 1 || stopped[0] != "yolo-orphan" {
		t.Errorf("expected only yolo-orphan reaped, got %v", stopped)
	}
	// The orphan's owner-PID file is cleared by stopJail.
	if fileExists(ownerPIDFile("yolo-orphan")) {
		t.Error("orphan owner-PID file should be cleared")
	}
	// The live jail's owner file is untouched.
	if !fileExists(ownerPIDFile("yolo-live")) {
		t.Error("live jail owner-PID file must be left alone")
	}
}

// TestWriteAndClearOwnerPID checks the owner-PID file round trip.
func TestWriteAndClearOwnerPID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := &Options{Getpid: func() int { return 4242 }}
	fillDefaults(o)
	o.Getpid = func() int { return 4242 }
	o.writeOwnerPID("yolo-x")
	data, err := os.ReadFile(ownerPIDFile("yolo-x"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "4242" {
		t.Errorf("owner pid = %q, want 4242", data)
	}
	clearOwnerPID("yolo-x")
	if fileExists(ownerPIDFile("yolo-x")) {
		t.Error("owner-PID file not cleared")
	}
}

// TestPIDAlivePolarity: our own PID is alive; a very high unused PID is dead.
func TestPIDAlivePolarity(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if pidAlive(999999) {
		t.Error("pid 999999 should be dead")
	}
}

// TestWorkspaceLockExclusive: a second non-blocking acquire on a held lock fails
// (the race guard), and Close releases it.
func TestWorkspaceLockExclusive(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "x.lock")
	l1, err := acquireWorkspaceLock(lockPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A non-blocking flock from a second fd must fail while l1 holds it.
	f2, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	if flockNBSucceeds(f2) {
		t.Error("second non-blocking flock should fail while held")
	}
	l1.Close()
	if !flockNBSucceeds(f2) {
		t.Error("non-blocking flock should succeed after release")
	}
}
