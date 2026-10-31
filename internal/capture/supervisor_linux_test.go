//go:build linux

package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A CANCELLED CAPTURE WHOSE SUPERVISOR NEVER ANSWERS FAILS CLOSED, within a bound: the installer
// stops its own supervisor, so descendant ownership is unknown, and the capture neither waits
// forever nor publishes a manifest. The wait is shrunk (supervisorCancelWait); the production
// one is the supervisor's own cleanup bound and a second.
func TestProgramCaptureDriverCancellationWithStoppedSupervisorFailsClosed(t *testing.T) {
	saved := supervisorCancelWait
	supervisorCancelWait = 200 * time.Millisecond
	t.Cleanup(func() { supervisorCancelWait = saved })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out")
	start := time.Now()
	_, err := Run(Options{Context: ctx, Home: t.TempDir(), Out: out, Artifacts: []string{".local/bin/probe"},
		Command: []string{"/bin/sh", "-c", `kill -STOP $PPID`}})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 7*time.Second {
		t.Fatalf("stopped ownership: %v after %v", err, time.Since(start))
	}
	if _, err := os.Stat(ManifestPath(out)); !os.IsNotExist(err) {
		t.Fatal("unknown ownership published manifest")
	}
}
