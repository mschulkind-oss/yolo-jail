package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// guestnotch_test.go pins `yolo check` predicting a Seatbelt setup's launch on macOS (env-manager
// plan Phase 7.1, EMP-D1; named setups): with YOLO_SETUP naming seatbelt-yolo-account, check skips
// the container probes the macos-user backend does not need, grades its runtime, and reports the
// package profile a launch there materializes. And the removed keys that once chose it are a
// Merged Configuration refusal naming the setup, never a selection (named-setups-plan.md slice 5).

// TestCheckOnMacOSResolvesTheSeatbeltSetupToMacosUser covers each reader of the resolved runtime
// in turn: the Container Runtime section's early read, Merged Configuration's runtime line, and
// Declared packages' mechanism (the runtime check resolved).
func TestCheckOnMacOSResolvesTheSeatbeltSetupToMacosUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no GC root anywhere under it
	got := runCheckOverConfigWith(t, `{"packages": ["ripgrep"]}`, true, nil, withSetup("seatbelt-yolo-account"))
	for _, want := range []string{
		"Native runtime 'macos-user' — no container runtime needed",
		"Runtime available: macos-user",
		"Declared packages",
		"A run materializes it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check under seatbelt-yolo-account on macOS does not say %q — the setup did not "+
				"select the macos-user backend there:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nothing materializes") {
		t.Errorf("check called a Seatbelt setup's packages inert, though its launch builds the "+
			"profile:\n%s", got)
	}
}

// TestCheckOnLinuxDoesNotResolveTheSeatbeltSetupToMacosUser is the control: Linux has no Seatbelt,
// so the setup is refused there, in the words a launch refuses it with (setup.Buildable), naming
// the setup this machine has. check names no macos-user runtime.
func TestCheckOnLinuxDoesNotResolveTheSeatbeltSetupToMacosUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// On the host: inside a session a YOLO_SETUP this OS cannot run is the outer session's, and
	// is ignored (SUP-D12), so only the host's check refuses it.
	got := runCheckOverConfigWith(t, `{"packages": ["ripgrep"]}`, false, nil, func(o *Options) {
		withSetup("seatbelt-yolo-account")(o)
		prev := o.Getenv
		o.Getenv = func(k string) string {
			if k == "YOLO_VERSION" {
				return ""
			}
			return prev(k)
		}
	})
	if strings.Contains(got, "macos-user'") || strings.Contains(got, "Runtime available: macos-user") {
		t.Errorf("check under a Seatbelt setup on Linux named the macos-user runtime, which Linux lacks:\n%s", got)
	}
	for _, want := range []string{
		`[FAIL] setup "seatbelt-yolo-account" uses Seatbelt, which only macOS has.`,
		"yolo --setup podman-self -- claude",
		"The line above names the change that clears it, then: yolo check",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check under a Seatbelt setup on Linux does not say %q, as the launch it predicts does:\n%s", want, got)
		}
	}
}

// TestCheckRefusesTheRemovedKeysNamingTheirSetup: `confinement: "guest"` in a workspace config
// chooses nothing now (a project cannot choose where its agent runs); check fails it in Merged
// Configuration naming the setup and the unset, and still reports the launch the selection makes,
// the OS default, not a macos-user one.
func TestCheckRefusesTheRemovedKeysNamingTheirSetup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfigWith(t, `{"confinement": "guest", "runtime": "podman"}`, true, nil,
		func(o *Options) {
			prev := o.Getenv
			o.Getenv = func(k string) string {
				if k == "YOLO_VERSION" {
					return "" // the host's check, where a removed key is an error
				}
				return prev(k)
			}
		})
	for _, want := range []string{"[FAIL]", "config.confinement: REMOVED", "config.runtime: REMOVED",
		"yolo config set setup seatbelt-yolo-account --user", "yolo config unset confinement --project",
		"yolo config unset runtime --project"} {
		if !strings.Contains(got, want) {
			t.Errorf("check over the removed keys does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Runtime available: macos-user") {
		t.Errorf("check took the removed confinement key as choosing the macos-user backend:\n%s", got)
	}
}

// TestCheckPredictsASeatbeltSetupServesAsMacosUser: the served-daemon prediction resolves the
// runtime the launch would (configuredRuntimeName, through the production Options with Getenv
// set), and seatbelt-yolo-account on a Mac is a macos-user launch — so a bound loophole, which
// that backend cannot bind, is predicted withheld there, and served on Linux, where the setup is
// refused and the prediction is the default container launch's. Dropping the setup from
// configuredRuntimeName's resolver fails the macOS half.
func TestCheckPredictsASeatbeltSetupServesAsMacosUser(t *testing.T) {
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
	for _, tc := range []struct {
		isMacOS, served bool
	}{
		{true, false}, // macos-user binds nothing
		{false, true}, // refused on Linux: the prediction is a container launch's, which binds it
	} {
		o := &Options{IsMacOS: tc.isMacOS, Getenv: setupGetenv("seatbelt-yolo-account")}
		if got := o.predictedServed(merged, nil).Serves("snd-like"); got != tc.served {
			t.Errorf("seatbelt-yolo-account, macOS=%v: predicted the bound loophole served = %v, want %v",
				tc.isMacOS, got, tc.served)
		}
	}
}
