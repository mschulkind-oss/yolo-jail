package run

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
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
// downstream of `o.autoCaptureInstallerPrograms(staged.packs, containerJailPlatform())` being called:
// delete that line from run.go and the seam is never invoked.
//
// The podman arm is driven to the FRESH-LAUNCH path with fakePodmanLaunch, because that is
// where its trigger sits (OQ-PD25): below every attach decision, beside the fork builds. An
// attach is driven the way TestAnAttachTriggersNoForkBuild drives one. The macos-user arm has a
// call of its own before its dispatch (install-capture.md hand-off H4), driven through
// runMacosUserCapturingCtx, with the store pick that follows it.

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
	ws := t.TempDir()
	_, printed := fakePodmanLaunchIn(t, ws, "", func(o *Options) {
		o.AutoCapture = func(bins []string, platform string, stderr io.Writer, _ bool) {
			called++
			gotBins, gotPlatform = bins, platform
			if _, err := io.WriteString(stderr, "AUTO-CAPTURE-STREAM-TEST\n"); err != nil {
				t.Errorf("write launch stderr: %v", err)
			}
		}
	})
	launchLogPath := filepath.Join(ws, ".yolo", "logs", LaunchLogName)
	logBytes, logErr := os.ReadFile(launchLogPath)
	if logErr != nil {
		t.Fatalf("read parent launch log %s: %v", launchLogPath, logErr)
	}
	if !strings.Contains(string(logBytes), "AUTO-CAPTURE-STREAM-TEST") ||
		!strings.Contains(printed, "AUTO-CAPTURE-STREAM-TEST") {
		t.Fatalf("auto-capture presentation did not reach terminal stderr and the parent launch log:\nterminal:\n%s\nlog:\n%s",
			printed, string(logBytes))
	}

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

func TestAutoCaptureColorFollowsLaunchStderr(t *testing.T) {
	for _, tc := range []struct {
		noColor   string
		wantColor bool
	}{{"", true}, {"1", false}} {
		t.Run("NO_COLOR="+tc.noColor, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColor)
			installerLaunchHome(t)
			gotColor := false
			_, _ = fakePodmanLaunch(t, func(o *Options) {
				o.Color = true
				o.Getenv = func(key string) string {
					if key == "NO_COLOR" {
						return tc.noColor
					}
					return os.Getenv(key)
				}
				o.IsTTYStdout = func() bool { return false }
				o.IsTTYStderr = func() bool { return true }
				o.AutoCapture = func(_ []string, _ string, _ io.Writer, color bool) {
					gotColor = color
				}
			})
			if gotColor != tc.wantColor {
				t.Errorf("AutoCapture color=%v, want %v for stderr TTY and NO_COLOR=%q",
					gotColor, tc.wantColor, tc.noColor)
			}
		})
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
				return ExecResult{Ran: true, RC: 0, Stdout: podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.AutoCapture = func([]string, string, io.Writer, bool) { called++ }
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
		o.AutoCapture = func([]string, string, io.Writer, bool) { called++ }
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
		o.AutoCapture = func(bins []string, _ string, _ io.Writer, _ bool) { called++; gotBins = bins }
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

// macosUserCaptureSeams installs the two seams a macos-user launch reaches for the store — the
// trigger and the pick — recording each call in order, and returns the record. The pick answers
// with an entry only once the trigger has run, as a real store would after a first capture.
type macosUserCaptureCalls struct {
	order                     []string
	capturePlatform, pickPlat string
	captureBins, pickBins     []string
	pickDir                   string
}

func macosUserCaptureSeams(rec *macosUserCaptureCalls) func(*Options) {
	return func(o *Options) {
		// A Mac set up for this backend, so the arm's preflight admits the launch (the run of
		// this test on Linux has no macOS to ask).
		o.MacosUserLaunchProbes = setUpMacProbes
		o.CapturesDir = func() string { return "/Users/matt/.local/share/yolo-jail/captures" }
		o.AutoCapture = func(bins []string, platform string, _ io.Writer, _ bool) {
			rec.order = append(rec.order, "capture")
			rec.captureBins, rec.capturePlatform = bins, platform
		}
		o.MacosUserCaptures = func(dir string, bins []string, platform string, _ map[string]string) ([]macosuser.CaptureEntry, []string) {
			rec.order = append(rec.order, "pick")
			rec.pickDir, rec.pickBins, rec.pickPlat = dir, bins, platform
			if len(rec.order) < 2 || rec.order[0] != "capture" {
				return nil, nil
			}
			return []macosuser.CaptureEntry{{Bin: "probetool", Key: "0123456789abcdef",
				Source: dir + "/entries/0123456789abcdef"}}, []string{"1111111111111111"}
		}
	}
}

// TestAMacosUserLaunchAutoCapturesAndStagesForDarwin is the macos-user arm's half of hand-off H4,
// from Run's own entry point: the launch triggers a capture of its uncaptured installer program
// for the SANDBOX'S platform — darwin, not the container jail's linux — and then hands the
// backend the store's pick for that program, picked after the capture so a first launch stages
// what it just captured.
//
// Red with either call deleted from the arm: no capture is triggered, or the backend is handed
// no entry to stage and every launcher downloads.
func TestAMacosUserLaunchAutoCapturesAndStagesForDarwin(t *testing.T) {
	installerLaunchHome(t)
	var rec macosUserCaptureCalls
	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), macosUserCaptureSeams(&rec))

	want := "darwin/" + goruntime.GOARCH
	if rec.capturePlatform != want || len(rec.captureBins) != 1 || rec.captureBins[0] != "probetool" {
		t.Fatalf("the macos-user launch triggered auto-capture with %v for %q, want [probetool] for "+
			"%q (the sandbox's platform)\n%s", rec.captureBins, rec.capturePlatform, want, out)
	}
	if rec.pickPlat != want || rec.pickDir != "/Users/matt/.local/share/yolo-jail/captures" ||
		len(rec.pickBins) != 1 || rec.pickBins[0] != "probetool" {
		t.Errorf("the store pick was asked (%q, %v, %q), want (the CapturesDir store, [probetool], %q)",
			rec.pickDir, rec.pickBins, rec.pickPlat, want)
	}
	if strings.Join(rec.order, ",") != "capture,pick" {
		t.Errorf("the seams ran in the order %v, want the capture first, so a first launch stages "+
			"what it captured", rec.order)
	}
	if len(ctx.Captures) != 1 || ctx.Captures[0].Key != "0123456789abcdef" ||
		len(ctx.CapturesKept) != 1 || ctx.CapturesKept[0] != "1111111111111111" {
		t.Errorf("the backend was handed captures %+v kept %v, want the store's pick", ctx.Captures,
			ctx.CapturesKept)
	}
}

// A dry run captures nothing — it launches nothing, and a capture is a launch — but still asks the
// store, so its plan names what a real launch would stage.
func TestAMacosUserDryRunCapturesNothingAndNamesTheStore(t *testing.T) {
	installerLaunchHome(t)
	var rec macosUserCaptureCalls
	seams := macosUserCaptureSeams(&rec)
	runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
		seams(o)
		o.DryRun = true
	})
	for _, step := range rec.order {
		if step == "capture" {
			t.Fatalf("a --dry-run triggered an auto-capture (%v); a capture runs a vendor installer", rec.order)
		}
	}
	if len(rec.order) != 1 || rec.order[0] != "pick" {
		t.Errorf("a --dry-run did not ask the store what it would stage (%v)", rec.order)
	}
}

// A capture's own launch on macos-user — internal/cli's runCaptureJail, whose CapturesDir is ""
// — neither captures nor stages, so its installer cannot be satisfied by the store it is filling.
func TestAMacosUserCaptureLaunchNeitherCapturesNorStages(t *testing.T) {
	installerLaunchHome(t)
	var rec macosUserCaptureCalls
	seams := macosUserCaptureSeams(&rec)
	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
		seams(o)
		o.CapturesDir = func() string { return "" }
	})
	if len(rec.order) != 0 || len(ctx.Captures) != 0 {
		t.Errorf("a capture's own macos-user launch ran %v and staged %+v; it must do neither",
			rec.order, ctx.Captures)
	}
}

// setUpMacProbes answers every macos-user launch precondition as a Mac `yolo macos-setup` ran on
// does, for a workspace shared with the sandbox.
func setUpMacProbes() macosuser.LaunchProbes {
	return macosuser.LaunchProbes{
		IsMacOS:           func() bool { return true },
		Geteuid:           func() int { return 501 },
		Which:             func(string) bool { return true },
		SandboxUserExists: func() bool { return true },
		PathIsDir:         func(string) bool { return true },
		RunBash:           func(string) int { return 0 },
		StagingRoot: func(string) macosuser.StagingRootFacts {
			return macosuser.StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o755}
		},
		RunAsGrant: func() bool { return true },
	}
}

// A LAUNCH THE BACKEND IS ABOUT TO REFUSE CAPTURES NOTHING. The arm's trigger runs before the
// backend's dispatch, so before its preconditions; it used to run whatever they would say, and a
// Mac where `yolo macos-setup` never ran recorded the capture act's refusal as a failed capture
// (OQ-PD26), holding every later launch's capture off for a day after the setup, and a launch
// running as root paid the capture before its refusal. Red on HEAD before the preflight: the
// trigger was called in every case below.
func TestAMacosUserLaunchTheBackendWillRefuseAutoCapturesNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		unmet func(*macosuser.LaunchProbes)
	}{
		{"the sandbox account was never made", func(p *macosuser.LaunchProbes) {
			p.SandboxUserExists = func() bool { return false }
		}},
		{"running as root", func(p *macosuser.LaunchProbes) { p.Geteuid = func() int { return 0 } }},
		{"the workspace is not shared with the sandbox", func(p *macosuser.LaunchProbes) {
			p.RunBash = func(string) int { return 1 }
		}},
		{"not macOS", func(p *macosuser.LaunchProbes) { p.IsMacOS = func() bool { return false } }},
		{"the staging root is missing", func(p *macosuser.LaunchProbes) {
			p.StagingRoot = func(string) macosuser.StagingRootFacts { return macosuser.StagingRootFacts{} }
		}},
		{"another account owns the staging root", func(p *macosuser.LaunchProbes) {
			p.StagingRoot = func(string) macosuser.StagingRootFacts {
				return macosuser.StagingRootFacts{Exists: true, IsDir: true, UID: 602, Mode: 0o755}
			}
		}},
		{"the staging root is writable by others", func(p *macosuser.LaunchProbes) {
			p.StagingRoot = func(string) macosuser.StagingRootFacts {
				return macosuser.StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o775}
			}
		}},
		{"sudo cannot run commands as the sandbox account without a password", func(p *macosuser.LaunchProbes) {
			p.RunAsGrant = func() bool { return false }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			installerLaunchHome(t)
			var rec macosUserCaptureCalls
			seams := macosUserCaptureSeams(&rec)
			runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
				seams(o)
				o.MacosUserLaunchProbes = func() macosuser.LaunchProbes {
					p := setUpMacProbes()
					c.unmet(&p)
					return p
				}
			})
			for _, step := range rec.order {
				if step == "capture" {
					t.Fatalf("a launch whose backend refuses it (%s) auto-captured first (%v)", c.name, rec.order)
				}
			}
		})
	}
}

// WITH NO PROBE SEAM THE ARM ASKS THIS MACHINE (macosuser.RealLaunchProbes), and no machine a unit
// test runs on admits a macos-user launch of a fresh temp workspace — not Linux (not macOS), not a
// CI Mac with no sandbox account, not a Mac set up for the backend (the workspace carries no
// sandbox-group ACE). So nil must not mean "admit": the capture is not triggered.
func TestAMacosUserLaunchWithNoProbeSeamAsksThisMachine(t *testing.T) {
	installerLaunchHome(t)
	var rec macosUserCaptureCalls
	seams := macosUserCaptureSeams(&rec)
	runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
		seams(o)
		o.MacosUserLaunchProbes = nil
	})
	for _, step := range rec.order {
		if step == "capture" {
			t.Fatalf("with no probe seam the arm auto-captured (%v); nil must mean this machine's "+
				"answer, which refuses a fresh temp workspace", rec.order)
		}
	}
}
