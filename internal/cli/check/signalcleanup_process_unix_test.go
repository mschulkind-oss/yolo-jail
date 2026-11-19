//go:build darwin || linux

package check

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func configureSignalCleanupOwnedGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func verifySignalCleanupOwnedGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errors.New("signal cleanup child was not started")
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return err
	}
	if pgid != cmd.Process.Pid {
		return errors.New("signal cleanup child did not lead its fixture-owned process group")
	}
	return nil
}

func killSignalCleanupOwnedGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil || cmd.Process.Pid <= 1 {
		return errors.New("invalid signal cleanup process group owner")
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func waitSignalCleanupOwnedGroupExit(cmd *exec.Cmd, guard time.Duration) bool {
	if cmd.Process == nil || cmd.Process.Pid <= 1 {
		return false
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(guard)
	defer timer.Stop()
	for {
		err := syscall.Kill(-cmd.Process.Pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return false
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			return false
		}
	}
}

// TestSignalCleanupMissingMarkerTeardownOwnsStartedStandin exercises the same bounded
// teardown used when the 20-second staging marker is absent, without shortening that guard.
func TestSignalCleanupMissingMarkerTeardownOwnsStartedStandin(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(tmp, "nix-started")
	stopped := filepath.Join(tmp, "nix-stopped")
	exited := filepath.Join(tmp, "nix-exited")
	ready := filepath.Join(root, "child-ready")
	marker := filepath.Join(root, "intentionally-missing-stage-marker")
	lifetimeReader, lifetimeWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalExitRemovesSelectedPackTree$", "-test.v")
	configureSignalCleanupOwnedGroup(cmd)
	cmd.ExtraFiles = []*os.File{lifetimeReader}
	cmd.Env = testEnv(map[string]string{
		signalCleanupChildEnv:              "1",
		signalCleanupMissingMarkerChildEnv: "1",
		"HOME":                             filepath.Join(root, "home"),
		"TMPDIR":                           tmp,
		"CHECK_SIGNAL_NIX_STARTED":         started,
		"CHECK_SIGNAL_NIX_STOPPED":         stopped,
		"CHECK_SIGNAL_NIX_EXITED":          exited,
		"CHECK_SIGNAL_CHILD_READY":         ready,
	})
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		_ = lifetimeReader.Close()
		_ = lifetimeWriter.Close()
		t.Fatal(err)
	}
	_ = lifetimeReader.Close()
	waited := startSignalCleanupCmdWait(cmd)
	if err := verifySignalCleanupOwnedGroup(cmd); err != nil {
		result := stopSignalCleanupUnverifiedChild(cmd, waited, lifetimeWriter, started, exited)
		if !result.childJoined || (result.nixStarted && !result.nixExited) {
			t.Fatalf("group setup failed and unverified fixture teardown was incomplete; output withheld: %v", err)
		}
		t.Fatalf("could not establish fixture-owned process group: %v; output:\n%s", err, output.String())
	}
	teardownComplete := false
	defer func() {
		if teardownComplete {
			_ = lifetimeWriter.Close()
			return
		}
		result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, started, exited)
		if !result.safeToReadOutput() {
			t.Errorf("fallback teardown incomplete: child-joined=%t group-gone=%t; captured output withheld", result.childJoined, result.groupGone)
		}
	}()

	if !waitForSignalCleanupMarker(ready, 20*time.Second) {
		t.Fatalf("missing-marker child did not reach its stand-in readiness event")
	}
	if !fileExists(started) {
		t.Fatal("missing-marker child reported ready before its owned stand-in started")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("intentionally missing staging marker is unexpectedly present: %v", err)
	}

	result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, started, exited)
	teardownComplete = result.safeToReadOutput()
	if !result.nixExited {
		t.Fatalf("marker-failure teardown did not observe the owned stand-in exit: started=%t exited=false child-joined=%t group-gone=%t child-wait=%v captured-bytes=%d", result.nixStarted, result.childJoined, result.groupGone, result.waitErr, output.Len())
	}
	if !teardownComplete {
		t.Fatalf("marker-failure teardown did not join the complete owned process group: stand-in-exited=true child-joined=%t group-gone=%t captured-output-withheld", result.childJoined, result.groupGone)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("intentionally missing staging marker became present: %v", err)
	}
	if output.Len() == 0 {
		t.Fatal("joined child produced no captured output to validate the safe-read boundary")
	}
	t.Logf("missing-marker teardown: started=true exited=true child-joined=true group-gone=true captured-bytes=%d child-wait=%v", output.Len(), result.waitErr)
}
