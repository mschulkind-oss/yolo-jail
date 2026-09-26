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
