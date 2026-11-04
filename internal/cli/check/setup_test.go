package check

import (
	"strings"
	"testing"
)

// setup_test.go pins `yolo check` predicting the setup a launch selects (named-setups-plan.md
// slice 4a): check calls the launch's own selection (config.SelectSetup, through checkSetup),
// so it refuses what the launch refuses, in the launch's words.

// withSetupEnv is a tweak giving Check's Getenv one variable on top of the fixture's.
func withSetupEnv(key, value string) func(*Options) {
	return func(o *Options) {
		inner := o.Getenv
		o.Getenv = func(k string) string {
			if k == key {
				return value
			}
			if k == "YOLO_VERSION" {
				return "" // a host-side check: a removed YOLO_RUNTIME refuses there
			}
			return inner(k)
		}
	}
}

// YOLO_SETUP=seatbelt-self on Linux: the launch refuses it (setup.Buildable), so check fails it
// in Merged Configuration with the same sentence, naming the setup this machine has.
func TestCheckRefusesASeatbeltSetupOnLinux(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfigWith(t, `{}`, false, nil, withSetupEnv("YOLO_SETUP", "seatbelt-self"))
	for _, want := range []string{
		`[FAIL] setup "seatbelt-self" uses Seatbelt, which only macOS has.`,
		"yolo --setup podman-self -- claude",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check under YOLO_SETUP=seatbelt-self on Linux does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Runtime available: macos-user") {
		t.Errorf("check graded a Seatbelt runtime on Linux:\n%s", got)
	}
}

// YOLO_RUNTIME=podman on the host: the launch refuses the removed variable naming YOLO_SETUP, and
// so does check.
func TestCheckReportsTheRemovedYoloRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfigWith(t, `{}`, false, nil, withSetupEnv("YOLO_RUNTIME", "podman"))
	for _, want := range []string{"[FAIL]", "YOLO_RUNTIME is removed", "YOLO_SETUP=podman-self", "unset YOLO_RUNTIME"} {
		if !strings.Contains(got, want) {
			t.Errorf("check under YOLO_RUNTIME=podman does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Runtime available: podman") {
		t.Errorf("check graded the runtime a removed variable named, though the launch refuses it:\n%s", got)
	}
}
