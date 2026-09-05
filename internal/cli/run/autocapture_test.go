package run

import (
	"bytes"
	goruntime "runtime"
	"strings"
	"testing"
)

// autocapture_test.go pins the auto-capture TRIGGER at the call site, from Run's own
// entry point.
//
// THAT IS THE WHOLE POINT OF DRIVING Run. install-capture.md slice 7 asks for exactly one
// test — *"assert the launch triggers a capture for a pack whose program is uncaptured,
// and confirm it goes RED when the call site is deleted"* — because a unit test on the
// decision function alone stays green with the feature switched off wholesale, which is
// the shape AGENTS.md says this repo has shipped five times. Every assertion below is
// downstream of `o.autoCaptureInstallerPrograms(staged.packs)` actually being called:
// delete that line from run.go and the seam is never invoked.
//
// The podman arm, because the trigger is container-only by placement (it sits below the
// macos-user return). Run goes on to fail in runContainer with no real daemon behind the
// stubbed Exec, which is fine and deliberately not asserted on — what is being measured
// happens strictly before that.

// TestALaunchAutoCapturesAnUncapturedInstallerProgram is the slice's required test.
//
// claude is the pack because it is the one shipped pack whose program is `via:
// "installer"` (agy is the other; both would do). A launch that selects it must reach the
// trigger with claude in the bin list.
func TestALaunchAutoCapturesAnUncapturedInstallerProgram(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	// A store that exists and is empty — i.e. an ordinary launch, not the capture jail.
	o.CapturesDir = func() string { return t.TempDir() }

	var gotBins []string
	var gotPlatform string
	called := 0
	o.AutoCapture = func(bins []string, platform string) {
		called++
		gotBins, gotPlatform = bins, platform
	}

	Run(*o)

	if called != 1 {
		t.Fatalf("the launch invoked the auto-capture trigger %d times, want 1 — "+
			"OQ-PD18's default-on trigger is not wired into the run pipeline\n"+
			"stdout:\n%s\nstderr:\n%s", called, stdout.String(), stderr.String())
	}
	if len(gotBins) != 1 || gotBins[0] != "claude" {
		t.Errorf("trigger bins = %v, want [claude] — the trigger must ask the SELECTED "+
			"packs what they install via an installer URL", gotBins)
	}
	// THE PLATFORM IS THE JAIL'S. A host-side capture.Platform() would answer
	// darwin/arm64 for a Mac running this backend, which misses every entry the machine
	// holds and re-captures on every launch: a store that never hits while looking full.
	//
	// ⚠ A LINUX RUNNER CANNOT DISTINGUISH THE TWO ANSWERS, and this comment is the honest
	// statement of that: here GOOS is already "linux", so the wrong implementation passes.
	// What the assertion does pin is the GOARCH half and the exact spelling
	// capture.Manifest.Platform uses; the GOOS half is checkable only by reading
	// containerJailPlatform, which takes its "linux" from a literal and has no GOOS in it.
	if want := "linux/" + goruntime.GOARCH; gotPlatform != want {
		t.Errorf("trigger platform = %q, want %q (the JAIL's, not the host's)", gotPlatform, want)
	}
}

// TestACaptureJailDoesNotAutoCapture is the recursion guard: the throwaway jail `yolo
// capture` runs must not itself trigger a capture, or a capture would capture a capture.
//
// It reads the SAME switch that suppresses the store mount — Options.CapturesDir
// returning "" — so one suppression covers both halves and the two cannot drift apart.
// Delete the `o.CapturesDir() == ""` clause from autoCaptureInstallerPrograms and this
// goes red.
func TestACaptureJailDoesNotAutoCapture(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	// Exactly what internal/cli/capturehost.go's runCaptureJail sets.
	o.CapturesDir = func() string { return "" }

	called := 0
	o.AutoCapture = func([]string, string) { called++ }

	Run(*o)

	if called != 0 {
		t.Fatalf("the capture jail triggered %d auto-captures — a capture must not "+
			"recursively trigger a capture (install-capture.md slice 4(f), slice 7)", called)
	}
}

// TestAJailWithNoInstallerProgramsTriggersNothing: the trigger must be silent for a pack
// set that declares no `via: "installer"` program, rather than calling the seam with an
// empty list and making internal/cli decide.
//
// copilot is the fixture because it is the live npm-declared agent CLI, and because it is
// the one program-delivery.md §3.5 warns about by name: its installer takes
// PREFIX="${PREFIX:-/usr/local}" on its root branch and exits 1 under the jail's uid 0 +
// --read-only, so an auto-capture of it would download, fail and store nothing. It is not
// a candidate at all while it stays `via: "npm"`, and this is what says so.
func TestAJailWithNoInstallerProgramsTriggersNothing(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.CapturesDir = func() string { return t.TempDir() }

	var gotBins []string
	called := 0
	o.AutoCapture = func(bins []string, _ string) { called++; gotBins = bins }

	Run(*o)

	if called != 0 {
		t.Fatalf("an npm-only pack set triggered auto-capture with bins %v; only "+
			"`via: \"installer\"` programs have anything to capture", gotBins)
	}
}

// TestInstallerBinsSkipsARefusedInstaller pins the origin gate on the trigger's own
// input, one layer below the launch.
//
// A FETCHED pack's installerUrl is refused by packload.HonoredInstalls precisely so a git
// ref cannot make yolo execute a shell script. Reading InstallContributions directly here
// would run exactly what that gate exists to refuse — and, unlike `yolo capture`, would
// do it unasked on every launch. There is no fixture for a fetched pack in this package,
// so this drives the predicate against the loaded pack set the launch would hand it.
func TestInstallerBinsReadsTheGrantedInstallsOnly(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "copilot", "guardrails"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	staged, ok := o.stageRunPacks("autocapture-test")
	if !ok {
		t.Fatalf("staging failed\nstdout:\n%s", stdout.String())
	}
	got := installerBins(staged.packs)
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("installerBins = %v, want [claude]: copilot installs via npm and "+
			"guardrails installs no program at all", got)
	}
	// And the list must be free of duplicates and of empty names, which is what makes it
	// safe to hand straight to a per-program lock keyed by the bin.
	for _, b := range got {
		if strings.TrimSpace(b) == "" {
			t.Errorf("installerBins produced an empty bin name: %q", got)
		}
	}
}
