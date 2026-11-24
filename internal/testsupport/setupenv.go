package testsupport

import (
	"os"
	"slices"
	"strings"
)

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

// AmbientYoloDials names the YOLO_* variables, by exact name or by prefix, that a caller's shell
// carries from a launch and that change what a test's refusal or launch says: the hatches
// (YOLO_ALLOW_*), the host's program selection (YOLO_USE_*), and the jail marker and provider
// selection a `yolo host` session or a jail exports. Launch plumbing a fixture reads (the
// YOLO_SERVICE_*, YOLO_HOST_* and similar variables) is deliberately not here: some suites run
// children that need it.
var AmbientYoloDials = struct{ Exact, Prefix []string }{
	Exact:  []string{"YOLO_JAIL", "YOLO_PROVIDERS", "YOLO_PROFILES"},
	Prefix: []string{"YOLO_ALLOW_", "YOLO_USE_"},
}

// ScrubYoloEnv unsets AmbientYoloDials from the process environment and returns how many it
// removed, for a package's TestMain, AFTER its child dispatch (a re-exec'd helper reads the mode
// variables its parent set). A shell in a jail, or under `yolo host`, exports those; a test that
// reads one without setting it passes on CI and fails there. A test about a dial sets it itself
// (t.Setenv).
func ScrubYoloEnv() int {
	n := 0
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		drop := slices.Contains(AmbientYoloDials.Exact, name)
		for _, prefix := range AmbientYoloDials.Prefix {
			drop = drop || strings.HasPrefix(name, prefix)
		}
		if drop {
			_ = os.Unsetenv(name)
			n++
		}
	}
	return n
}
