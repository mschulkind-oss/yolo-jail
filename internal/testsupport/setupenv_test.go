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

func TestScrubYoloEnvRemovesDialsAndKeepsLaunchPlumbingAndHarnessKnobs(t *testing.T) {
	for k, v := range map[string]string{
		"YOLO_ALLOW_MISSING_PROGRAMS": "1", "YOLO_USE_INSTALLED": "copilot", "YOLO_JAIL": "1",
		"YOLO_TEST_JAIL_TIMEOUT": "9", "YOLO_SERVICE_X_ENDPOINT": "e", "NOT_YOLO_X": "1",
	} {
		t.Setenv(k, v)
	}
	if n := ScrubYoloEnv(); n < 3 {
		t.Fatalf("removed %d, want at least the three dials set above", n)
	}
	for _, gone := range []string{"YOLO_ALLOW_MISSING_PROGRAMS", "YOLO_USE_INSTALLED", "YOLO_JAIL"} {
		if _, ok := os.LookupEnv(gone); ok {
			t.Errorf("%s survived the scrub", gone)
		}
	}
	for _, kept := range []string{"YOLO_TEST_JAIL_TIMEOUT", "YOLO_SERVICE_X_ENDPOINT", "NOT_YOLO_X"} {
		if _, ok := os.LookupEnv(kept); !ok {
			t.Errorf("%s was removed", kept)
		}
	}
}
