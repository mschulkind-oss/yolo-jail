package check

import (
	"strings"
	"testing"
)

// guestnotch_test.go pins `yolo check` predicting the guest notch's launch on macOS (env-manager
// plan Phase 7.1, EMP-D1): with no `runtime` key, a macOS `confinement: "guest"` resolves the
// macos-user backend, so check skips the container probes that backend does not need, grades
// its runtime, and reports the package profile a launch there materializes. And it predicts
// the launch's one refusal at that notch: an explicit runtime naming a container.

// TestCheckOnMacOSResolvesTheGuestNotchToMacosUser covers each reader of the resolved runtime
// in turn: the Container Runtime section's early read (configRuntime), Merged Configuration's
// runtime line (resolveRuntime), and Declared packages' mechanism (the runtime check resolved).
func TestCheckOnMacOSResolvesTheGuestNotchToMacosUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no GC root anywhere under it
	got := runCheckOverConfig(t, `{"confinement": "guest", "packages": ["ripgrep"]}`, true)
	for _, want := range []string{
		"Native runtime 'macos-user' — no container runtime needed",
		"Runtime available: macos-user",
		"Declared packages",
		"A run materializes it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check over a macOS guest does not say %q — the notch did not select the "+
				"macos-user backend there:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nothing materializes") {
		t.Errorf("check called a macOS guest's packages inert, though its launch builds the "+
			"profile:\n%s", got)
	}
}

// TestCheckOnLinuxDoesNotResolveTheGuestNotchToMacosUser is the control: Linux has no guest
// backend, so check names no macos-user runtime there and its packages stay inert.
func TestCheckOnLinuxDoesNotResolveTheGuestNotchToMacosUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfig(t, `{"confinement": "guest", "packages": ["ripgrep"]}`, false)
	if strings.Contains(got, "macos-user'") || strings.Contains(got, "Runtime available: macos-user") {
		t.Errorf("check over a Linux guest named the macos-user runtime, which Linux lacks:\n%s", got)
	}
	if !strings.Contains(got, "nothing materializes") {
		t.Errorf("a Linux guest has no package layer, so its packages are inert:\n%s", got)
	}
}

// TestCheckOnMacOSFailsAGuestWithAContradictingRuntime: the launch refuses guest + a container
// runtime on macOS, so check fails it in Merged Configuration, naming both inputs and the fix —
// not as an unavailable runtime pointing back at the Container Runtime section.
func TestCheckOnMacOSFailsAGuestWithAContradictingRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfig(t, `{"confinement": "guest", "runtime": "podman"}`, true)
	for _, want := range []string{"[FAIL]", "`confinement: \"guest\"` runs on the macos-user backend",
		"`runtime: \"podman\"` in the config", "Drop one"} {
		if !strings.Contains(got, want) {
			t.Errorf("check over a contradicting macOS guest does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Runtime available: podman") {
		t.Errorf("check reported the contradicting runtime as available, though the launch "+
			"refuses it:\n%s", got)
	}
}
