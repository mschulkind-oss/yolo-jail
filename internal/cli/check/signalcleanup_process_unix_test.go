//go:build darwin || linux

package check

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

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
	teardownComplete := false
	defer func() {
		if teardownComplete {
			_ = lifetimeWriter.Close()
			return
		}
		result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, started, exited)
		if !result.safeToReadOutput() {
			t.Errorf("fallback fixture teardown incomplete: standin-exited=%t child-joined=%t; captured output withheld", result.nixExited, result.childJoined)
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
		t.Fatalf("marker-failure teardown did not observe the owned stand-in exit: started=%t child-joined=%t child-wait=%v; captured output withheld", result.nixStarted, result.childJoined, result.waitErr)
	}
	if !teardownComplete {
		t.Fatalf("marker-failure teardown did not join child and stand-in: standin-exited=true child-joined=%t; captured output withheld", result.childJoined)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("intentionally missing staging marker became present: %v", err)
	}
	if output.Len() == 0 {
		t.Fatal("joined child produced no captured output to validate the safe-read boundary")
	}
	t.Logf("missing-marker teardown: started=true exited=true child-joined=true captured-bytes=%d child-wait=%v", output.Len(), result.waitErr)
}
