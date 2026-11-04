package testsupport

import "os"

// SetupSelectionEnv names the variables a launched session exports that choose, or used to
// choose, where an agent runs: YOLO_SETUP (named-setups.md), and the removed YOLO_RUNTIME and
// YOLO_CONFINEMENT a jail may still carry in its environment. Each one changes what a test's
// launch or read-only verb selects, and YOLO_RUNTIME refuses a host launch outright, so a
// package whose fixtures build their environment from os.Environ reads the machine it runs on
// unless its TestMain clears them.
var SetupSelectionEnv = []string{"YOLO_SETUP", "YOLO_RUNTIME", "YOLO_CONFINEMENT"}

// ClearSetupSelectionEnv removes SetupSelectionEnv from the process environment, for a
// package's TestMain: the tests then select as on a host with none of them set, in a jail and
// on CI alike. A test about one of them sets it itself (t.Setenv).
func ClearSetupSelectionEnv() {
	for _, name := range SetupSelectionEnv {
		_ = os.Unsetenv(name)
	}
}
