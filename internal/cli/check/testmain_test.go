package check

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMain gives this package's host singletons a private directory instead of the
// machine-wide /tmp/yolo-<name>.* and stops the ones its tests started
// (testsupport.IsolateHostSingletons says why).
func TestMain(m *testing.M) {
	// `<test-binary> -settings-sleeper-child <socket> <settings>` is a fake host-wide daemon
	// for singletonsettings_test.go: it binds its socket and accepts until killed. The
	// settings path is in its argv only so the spawn records what it was handed.
	if len(os.Args) >= 4 && os.Args[1] == "-settings-sleeper-child" {
		os.Exit(settingsSleeperChildMain(os.Args[2]))
	}
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
