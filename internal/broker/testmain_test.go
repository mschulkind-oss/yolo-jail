package broker

import (
	"os"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// TestMain gives this package's host singletons a private directory instead of the
// machine-wide /tmp/yolo-<name>.* and stops the ones its tests started
// (testsupport.IsolateHostSingletons says why).
func TestMain(m *testing.M) {
	// A STRAY (strayscope_test.go): this binary re-run under a host-wide daemon's argv,
	// which only has to exist until the test that started it kills it. Bounded, so one
	// whose test died with it does not outlive the run by more than a minute.
	if os.Getenv(strayProcessEnv) == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	// The color gate (tty.Color) reads NO_COLOR from the process environment, so a suite
	// run from a shell that exports it would fail every test asserting ANSI on a
	// terminal. A test about NO_COLOR sets it itself (t.Setenv).
	_ = os.Unsetenv(tty.NoColorVar)
	release := testsupport.IsolateHostSingletons()
	code := m.Run()
	release()
	os.Exit(code)
}

// TestHostSingletonsAreIsolatedHere pins the TestMain call above: without it this
// package's tests spawn singletons on the machine-wide /tmp/yolo-<name>.* again.
func TestHostSingletonsAreIsolatedHere(t *testing.T) {
	if paths.HostSingletonDir == paths.DefaultHostSingletonDir {
		t.Fatalf("paths.HostSingletonDir is the machine-wide %q; this package's TestMain must call testsupport.IsolateHostSingletons", paths.HostSingletonDir)
	}
}
