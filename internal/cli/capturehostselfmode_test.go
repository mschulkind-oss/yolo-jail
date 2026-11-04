package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// capturehostselfmode_test.go pins slice 9 of docs/design/macos-user-model-plan.md at its CLI call
// sites: with `"setup": "seatbelt-self"` in the user config, `yolo capture`'s macos-user arm boots
// its jail as that setup and hands the act the run-as-you mode and the invoking user's identity,
// and the act's plan crosses into no account; and the host floor's Mac predicate asks no sandbox
// account. The mode is the selected setup's run_as (actMacosUserMode, named-setups-plan.md
// SUP-D13), the reading the launch makes.

// withSelfModeUserConfig adds `"setup": "seatbelt-self"` to the user config captureFixtureHome wrote.
func withSelfModeUserConfig(t *testing.T) {
	t.Helper()
	// Out of a jail, as on a Mac: in-jail, the user config is the generated snapshot.
	t.Setenv("YOLO_VERSION", "")
	p := paths.UserConfigPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(b), "{", `{"setup": "seatbelt-self", `, 1)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THE CAPTURE ARM PASSES THE MODE: it invokes the real closure on its dry-run path, through the
// real act, and reads both the options the act was handed and the plan it printed. Fails if the
// closure stops reading the selected setup, or drops Mode, HostHome or HostUser on the way to the
// act.
func TestCaptureHandsTheMacosUserActTheRunAsYouMode(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	withSelfModeUserConfig(t)
	t.Setenv("USER", "you")
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
	}))
	var got []macosuser.CaptureOptions
	orig := macCaptureAct
	macCaptureAct = func(d macosuser.Deps, o macosuser.CaptureOptions, dest string, dryRun bool) int {
		got = append(got, o)
		return orig(d, o, dest, dryRun)
	}
	t.Cleanup(func() { macCaptureAct = orig })

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("captureHost rc = %d, stderr = %s", rc, errw.String())
	}
	if seen.MacosUserRun == nil {
		t.Fatal("MacosUserRun is nil: the macos-user arm is unwired")
	}
	before := out.Len()
	_ = seen.MacosUserRun(jsonx.NewOrderedMap(), "", nil, nil, "", "", macosuser.HomeOverlay{},
		macosuser.HostContext{}, true, jsonx.NewOrderedMap(), []packload.BlockedTool{}, macosuser.JailDaemons{})
	plan := out.String()[before:]

	if len(got) != 1 {
		t.Fatalf("the macos-user capture act ran %d times, want 1", len(got))
	}
	home, _ := filepath.EvalSymlinks(os.Getenv("HOME"))
	if o := got[0]; o.Mode != macosuser.ModeSelf || o.HostHome != home || o.HostUser != "you" {
		t.Errorf("the act was handed mode %v, home %q, user %q; want self, %q, you", o.Mode, o.HostHome,
			o.HostUser, home)
	}
	if strings.Contains(plan, "/usr/bin/sudo") || strings.Contains(plan, macosuser.SandboxUser) {
		t.Errorf("the run-as-you capture plan crosses into the account:\n%s", plan)
	}
	if !strings.Contains(plan, "as you, inside the capture profile") ||
		!strings.Contains(plan, "all capture plan invariants hold") {
		t.Errorf("the run-as-you capture plan is not the run-as-you one, or is not viable:\n%s", plan)
	}
}

// With no setup the arm hands the act the account mode, and no identity, so the act's own HostUser
// (Deps.HostUser) places the account's staging tree as before.
func TestCaptureHandsTheMacosUserActTheAccountModeByDefault(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
	}))
	var got []macosuser.CaptureOptions
	orig := macCaptureAct
	macCaptureAct = func(_ macosuser.Deps, o macosuser.CaptureOptions, _ string, _ bool) int {
		got = append(got, o)
		return 0
	}
	t.Cleanup(func() { macCaptureAct = orig })
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("captureHost rc = %d, stderr = %s", rc, errw.String())
	}
	_ = seen.MacosUserRun(jsonx.NewOrderedMap(), "", nil, nil, "", "", macosuser.HomeOverlay{},
		macosuser.HostContext{}, true, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
	if len(got) != 1 || got[0].Mode != macosuser.ModeAccount || got[0].HostHome != "" || got[0].HostUser != "" {
		t.Errorf("the default capture was handed %+v; want the account mode and no identity", got)
	}
}

// THE FLOOR'S MAC PREDICATE IN THE RUN-AS-YOU MODE asks no sandbox account, and its privilege facts
// are the staging root's, in words that say the act runs as you. Fails if hostFloorMacProbes stops
// reading the selected setup's run_as, or macActBlocked asks the account in self mode.
func TestAMacFloorInSelfModeAsksNoSandboxAccount(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	withSelfModeUserConfig(t)
	// The production probes (this package's init disarms hostFloorMacProbes itself); building them
	// asks the machine nothing.
	if got := realHostFloorMacProbes().Mode; got != macosuser.ModeSelf {
		t.Fatalf("the floor's Mac probes read mode %v, want self", got)
	}
	p := macSetup{}.probes()
	p.Mode = macosuser.ModeSelf
	p.Privilege = func() (macosuser.Precondition, bool) { return macosuser.Precondition{}, false }
	if got := macCaptureBlocked(p, "captures it"); got != "" {
		t.Errorf("a run-as-you Mac with no sandbox account cannot capture: %s", got)
	}
	var staging macosuser.Precondition
	for _, c := range macosuser.LaunchPreconditions() {
		if c.ID == macosuser.PreconditionStagingRootOwner {
			staging = c
		}
	}
	p.Privilege = func() (macosuser.Precondition, bool) { return staging, true }
	if got := macBuildBlocked(p, "builds it"); !strings.Contains(got, "runs as you") ||
		strings.Contains(got, "sudo rule") {
		t.Errorf("a run-as-you Mac with a foreign staging root says %q", got)
	}
}

// THE CAPTURE ARM PASSES THE SHARED-/tmp HATCH (OQ-MM8, MMP-D21): with `"macos_user_shared_tmp":
// true` beside the run-as-you mode, the act is handed SharedTmp, so the capture profile opens /tmp
// as a launch's does. Fails if the closure stops reading the hatch or drops it on the way.
func TestCaptureHandsTheMacosUserActTheSharedTmpHatch(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	withSelfModeUserConfig(t)
	p := paths.UserConfigPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "{", `{"macos_user_shared_tmp": true, `, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
	}))
	var got []macosuser.CaptureOptions
	orig := macCaptureAct
	macCaptureAct = func(_ macosuser.Deps, o macosuser.CaptureOptions, _ string, _ bool) int {
		got = append(got, o)
		return 0
	}
	t.Cleanup(func() { macCaptureAct = orig })
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("captureHost rc = %d, stderr = %s", rc, errw.String())
	}
	_ = seen.MacosUserRun(jsonx.NewOrderedMap(), "", nil, nil, "", "", macosuser.HomeOverlay{},
		macosuser.HostContext{}, true, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
	if len(got) != 1 || got[0].Mode != macosuser.ModeSelf || !got[0].SharedTmp {
		t.Errorf("the capture was handed %+v; want the run-as-you mode with SharedTmp", got)
	}
}

// AN ACT ON A RUNTIME BOOTS THE SELECTED SETUP WHEN IT RUNS THERE (actSetup, SUP-D13): a capture
// or build jail filed under macos-user is handed the setup a launch selects, seatbelt-self from
// the user config, so it runs as you; with no setup selected it is handed the runtime's built-in,
// seatbelt-yolo-account; and an act on podman under that user config is handed podman-self, not
// the Seatbelt setup. Fails if runCaptureJail stops handing the jail actSetup's name.
func TestACaptureActBootsTheSelectedSetupOnItsRuntime(t *testing.T) {
	for _, tc := range []struct {
		self      bool
		runtime   string
		wantSetup string
	}{
		{true, "macos-user", "seatbelt-self"},
		{false, "macos-user", "seatbelt-yolo-account"},
		{true, "podman", "podman-self"},
	} {
		captureFixtureHome(t, captureFixtureInstaller)
		t.Setenv("YOLO_VERSION", "")
		if tc.self {
			withSelfModeUserConfig(t)
		}
		var seen run.Options
		withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 1 })
		var out, errw bytes.Buffer
		runCaptureJail(t.TempDir(), "probetool", []string{"true"}, nil,
			captureStreams{out: &out, errw: &errw}, false, captureAct{runtime: tc.runtime})
		if seen.SetupName != tc.wantSetup {
			t.Errorf("self=%v, an act on %s: the jail was handed setup %q, want %q",
				tc.self, tc.runtime, seen.SetupName, tc.wantSetup)
		}
	}
}
