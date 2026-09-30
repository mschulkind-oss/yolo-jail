package cli

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestGitConfigTripwireIsArmedHere pins TestMain's testsupport.ArmGitConfigTripwire call
// (hostdepstub_test.go): without it a fixture that reads the machine's git configuration
// passes again on every machine that does not sign its commits.
func TestGitConfigTripwireIsArmedHere(t *testing.T) {
	if !testsupport.GitConfigTripwireArmed() {
		t.Fatal("the git configuration tripwire is not armed; TestMain must call testsupport.ArmGitConfigTripwire")
	}
}

// TestHostSingletonsAreIsolatedHere pins TestMain's testsupport.IsolateHostSingletons
// call (hostdepstub_test.go): without it this package's host-daemon and broker tests
// spawn singletons on the machine-wide /tmp/yolo-<name>.* again.
func TestHostSingletonsAreIsolatedHere(t *testing.T) {
	if paths.HostSingletonDir == paths.DefaultHostSingletonDir {
		t.Fatalf("paths.HostSingletonDir is the machine-wide %q; TestMain must call testsupport.IsolateHostSingletons", paths.HostSingletonDir)
	}
}
