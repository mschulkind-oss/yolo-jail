package run

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestHostSingletonsAreIsolatedHere pins TestMain's testsupport.IsolateHostSingletons
// call (journalbridge_test.go): without it this package.s launches
// spawn singletons on the machine-wide /tmp/yolo-<name>.* again.
func TestHostSingletonsAreIsolatedHere(t *testing.T) {
	if paths.HostSingletonDir == paths.DefaultHostSingletonDir {
		t.Fatalf("paths.HostSingletonDir is the machine-wide %q; TestMain must call testsupport.IsolateHostSingletons", paths.HostSingletonDir)
	}
}
