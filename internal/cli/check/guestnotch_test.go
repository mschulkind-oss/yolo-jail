package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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

// TestCheckOnLinuxDoesNotResolveTheGuestNotchToMacosUser is the control: Linux has no Seatbelt,
// so the setup the removed `confinement: "guest"` key chooses is refused there, in the words a
// launch refuses it with (setup.Buildable), naming the setup this machine has. check names no
// macos-user runtime.
func TestCheckOnLinuxDoesNotResolveTheGuestNotchToMacosUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfig(t, `{"confinement": "guest", "packages": ["ripgrep"]}`, false)
	if strings.Contains(got, "macos-user'") || strings.Contains(got, "Runtime available: macos-user") {
		t.Errorf("check over a Linux guest named the macos-user runtime, which Linux lacks:\n%s", got)
	}
	for _, want := range []string{
		`[FAIL] setup "seatbelt-yolo-account" uses Seatbelt, which only macOS has.`,
		"yolo --setup podman-self -- claude",
		"The line above names the change that clears it, then: yolo check",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check over a Linux guest does not say %q, as the launch it predicts does:\n%s", want, got)
		}
	}
}

// TestCheckOnMacOSFailsAGuestWithAContradictingRuntime: the removed keys `confinement: "guest"`
// and `runtime: "podman"` name two different setups, which the launch refuses (config.SelectSetup),
// so check fails it in Merged Configuration, naming both keys and both setups — not as an
// unavailable runtime pointing back at the Container Runtime section.
func TestCheckOnMacOSFailsAGuestWithAContradictingRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfig(t, `{"confinement": "guest", "runtime": "podman"}`, true)
	for _, want := range []string{"[FAIL]",
		`the removed keys confinement = "guest" and runtime = "podman" in your config name two different setups`,
		"yolo --setup seatbelt-yolo-account -- claude", "yolo --setup podman-self -- claude"} {
		if !strings.Contains(got, want) {
			t.Errorf("check over a contradicting macOS guest does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Runtime available: podman") {
		t.Errorf("check reported the contradicting runtime as available, though the launch "+
			"refuses it:\n%s", got)
	}
	// THE NEXT STEP IS THE FAIL LINE'S OWN: installing or starting podman does not clear it,
	// since the launch refuses the pair whatever podman does, so runtimeFindingNote must not
	// say so.
	if strings.Contains(got, "Install or start") {
		t.Errorf("check tells the user to install or start the contradicting runtime, which "+
			"leaves the launch refused:\n%s", got)
	}
	if !strings.Contains(got, "The line above names the change that clears it, then: yolo check") {
		t.Errorf("the contradiction FAIL's note does not point back at the line's own two "+
			"fixes:\n%s", got)
	}
}

// TestCheckOnAMacWithNoContainerRuntimeStillFailsTheContradiction: on a Mac with neither podman
// nor Apple Container installed, the Container Runtime section fails first, and an unavailable
// runtime defers to it with a dimmed pointer. The contradiction is not that: its fix is in the
// config whatever is installed (resolveRuntimeForCheck marks it not unavailable), so the FAIL
// still prints. Deleting that branch turns the FAIL into the pointer and fails this.
func TestCheckOnAMacWithNoContainerRuntimeStillFailsTheContradiction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfigWith(t, `{"confinement": "guest", "runtime": "podman"}`, true, nil,
		func(o *Options) {
			look := o.LookPath
			o.LookPath = func(name string) (string, bool) {
				if name == "podman" || name == "container" {
					return "", false
				}
				return look(name)
			}
		})
	if !strings.Contains(got, "name two different setups") {
		t.Errorf("with no container runtime installed, check hid the guest/runtime contradiction:\n%s", got)
	}
	if strings.Contains(got, "No runtime available — see Container Runtime above") {
		t.Errorf("check sent the contradiction to the Container Runtime section, whose install "+
			"step does not clear it:\n%s", got)
	}
}

// TestCheckPredictsAMacosGuestServesAsMacosUser: the served-daemon prediction resolves the runtime
// the launch would (configuredRuntimeName, through the production Options with Getenv set), and a
// macOS guest with no `runtime` key is a macos-user launch — so a bound loophole, which that
// backend cannot bind, is predicted withheld there, and served on Linux, where the guest is no
// macos-user launch. Dropping the notch from configuredRuntimeName's resolver fails the macOS half.
func TestCheckPredictsAMacosGuestServesAsMacosUser(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	moduleRoot := isolatedModuleDir(t)
	sock := filepath.Join(t.TempDir(), "native")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeLoopholeManifest(t, moduleRoot, "snd-like",
		`"name":"snd-like","description":"d","transport":"none",`+
			`"host_bind_mounts":[{"host":"`+sock+`","container":"/run/snd-like/native","readonly":true}]`)
	merged := jsonx.NewOrderedMap()
	merged.Set("confinement", "guest")
	for _, tc := range []struct {
		isMacOS, served bool
	}{
		{true, false}, // macos-user binds nothing
		{false, true}, // a Linux guest predicts a container launch, which binds it
	} {
		o := &Options{IsMacOS: tc.isMacOS, Getenv: func(string) string { return "" }}
		if got := o.predictedServed(merged, nil).Serves("snd-like"); got != tc.served {
			t.Errorf("guest, macOS=%v: predicted the bound loophole served = %v, want %v",
				tc.isMacOS, got, tc.served)
		}
	}
}
