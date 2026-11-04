package testsupport

import (
	"os"
	"testing"
)

// TestClearSetupSelectionEnvRemovesEveryName sets each variable as a jail would and checks
// that none survives the clear.
func TestClearSetupSelectionEnvRemovesEveryName(t *testing.T) {
	for _, name := range SetupSelectionEnv {
		t.Setenv(name, "podman")
	}
	ClearSetupSelectionEnv()
	for _, name := range SetupSelectionEnv {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q after ClearSetupSelectionEnv, want it unset", name, v)
		}
	}
}
