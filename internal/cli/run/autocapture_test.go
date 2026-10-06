package run

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
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
// macos-user return), and driven to the FRESH-LAUNCH path with fakePodmanLaunch, because
// that is where the trigger sits (OQ-PD25): below every attach decision, beside the fork
// builds. An attach is driven the way TestAnAttachTriggersNoForkBuild drives one.

// installerLaunchHome selects one local pack whose only program installs `via: "installer"` —
// the shape claude, codex and agy ship — and returns the home.
func installerLaunchHome(t *testing.T) string {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	dir := filepath.Join(t.TempDir(), "installerpack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"contributes":[{"kind":"program","bin":"probetool","via":"installer",` +
		`"url":"https://example.invalid/install.sh"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `[{"source":"file://`+dir+`","name":"installerpack"}]`)
	return home
}

// TestALaunchAutoCapturesAnUncapturedInstallerProgram is the slice's required test.
//
// The pack is a local one whose program is `via: "installer"`, the shape claude, codex and agy
// ship. A fresh launch that selects it must reach the trigger with that bin in the list.
func TestALaunchAutoCapturesAnUncapturedInstallerProgram(t *testing.T) {
	installerLaunchHome(t)

	var gotBins []string
	var gotPlatform string
	called := 0
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.AutoCapture = func(bins []string, platform string) {
			called++
			gotBins, gotPlatform = bins, platform
		}
	})

	if called != 1 {
		t.Fatalf("the launch invoked the auto-capture trigger %d times, want 1 — "+
			"OQ-PD18's default-on trigger is not wired into the run pipeline\n%s", called, printed)
	}
	if len(gotBins) != 1 || gotBins[0] != "probetool" {
		t.Errorf("trigger bins = %v, want [probetool] — the trigger must ask the SELECTED "+
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

// TestAnAttachTriggersNoAutoCapture: OQ-PD18 rules auto-capture ON FIRST LAUNCH, and an attach
// is never one — the jail it enters was started by a fresh launch, which had the miss in front of
// it first. Running it again on entry only repeats that launch's attempt on every terminal that
// joins: 14 s per attach on Apple Container (MEASURED, docs/research/macos-backend-performance.md
// §8), where its jail cannot start beside the running one (INFERRED from §7, which MEASURED that a
// second unsealed jail cannot). OQ-PD25 moved the trigger below the attach decision.
//
// Red before the move: the trigger sat above runContainer, so the attach below ran it.
func TestAnAttachTriggersNoAutoCapture(t *testing.T) {
	installerLaunchHome(t)
	called := 0
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		cname := yoloruntime.FromWorkspace(o.Workspace)
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.AutoCapture = func([]string, string) { called++ }
	})
	if !strings.Contains(printed, "Attaching to existing jail") {
		t.Fatalf("the fixture did not attach, so the attach path is unexercised:\n%s", printed)
	}
	if called != 0 {
		t.Errorf("an attach ran the auto-capture trigger %d times; it belongs to the fresh "+
			"launch that started the jail:\n%s", called, printed)
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
	installerLaunchHome(t)
	called := 0
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		// Exactly what internal/cli/capturehost.go's runCaptureJail sets.
		o.CapturesDir = func() string { return "" }
		o.AutoCapture = func([]string, string) { called++ }
	})

	if called != 0 {
		t.Fatalf("the capture jail triggered %d auto-captures — a capture must not "+
			"recursively trigger a capture (install-capture.md slice 4(f), slice 7)\n%s", called, printed)
	}
}

// TestAJailWithNoInstallerProgramsTriggersNothing: the trigger must be silent for a pack
// set that declares no `via: "installer"` program, rather than calling the seam with an
// empty list and making internal/cli decide.
//
// opencode is the fixture because it is a live npm-declared agent CLI. copilot was, until it
// moved to GitHub's installer (OQ-NI1); TestInstallerBinsReadsTheGrantedInstallsOnly now
// counts it among the candidates.
func TestAJailWithNoInstallerProgramsTriggersNothing(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["opencode"]`)

	var gotBins []string
	called := 0
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.AutoCapture = func(bins []string, _ string) { called++; gotBins = bins }
	})

	if called != 0 {
		t.Fatalf("an npm-only pack set triggered auto-capture with bins %v; only "+
			"`via: \"installer\"` programs have anything to capture\n%s", gotBins, printed)
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
	writeUserPacks(t, home, `["claude", "copilot", "opencode", "guardrails"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	staged, ok := o.stageRunPacks("autocapture-test")
	if !ok {
		t.Fatalf("staging failed\nstdout:\n%s", stdout.String())
	}
	got := installerBins(staged.packs)
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	if strings.Join(sorted, ",") != "claude,copilot" {
		t.Errorf("installerBins = %v, want claude and copilot: both install with their vendor's "+
			"installer, opencode installs via npm and guardrails installs no program at all", got)
	}
	// And the list must be free of duplicates and of empty names, which is what makes it
	// safe to hand straight to a per-program lock keyed by the bin.
	for _, b := range got {
		if strings.TrimSpace(b) == "" {
			t.Errorf("installerBins produced an empty bin name: %q", got)
		}
	}
}
