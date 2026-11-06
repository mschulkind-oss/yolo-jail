package integration

import (
	"os"
	goruntime "runtime"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// jailName is the name the jail of a launch from dir runs under when the launch names no setup
// of its own: the project key with the selected setup's name appended (named-setups-plan.md
// SUP-D2). The suite's launches select what $YOLO_SETUP names, as the harness's detectRuntime
// reads it, or the platform's default setup. A launch that names one (withEnv("YOLO_SETUP=…"))
// is jailNameOn's.
func jailName(dir string) string {
	if name := os.Getenv(setup.EnvVar); name != "" {
		return jailNameOn(dir, name)
	}
	return jailNameOn(dir, setup.Default(goruntime.GOOS).Name)
}

// jailNameOn is the jail name of a launch of the named setup from dir.
func jailNameOn(dir, setupName string) string {
	return naming.JailName(dir, setupName)
}

// jailNamesOf is every name a jail of dir may run under, for a teardown that must not miss one
// whichever setup a test launched: each built-in setup's, a user setup's the test names (extra),
// and the legacy name an older yolo keyed its jail by (named-setups-plan.md SUP-D10).
func jailNamesOf(dir string, extra ...string) []string {
	names := []string{naming.FromWorkspace(dir)}
	for _, s := range setup.BuiltIns() {
		names = append(names, jailNameOn(dir, s.Name))
	}
	for _, name := range extra {
		names = append(names, jailNameOn(dir, name))
	}
	return names
}
