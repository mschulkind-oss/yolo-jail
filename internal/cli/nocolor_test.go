package cli

import (
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// standInTerminal makes colorForWriter treat every *os.File as a terminal for the
// rest of the test — captureStdout hands a command a pipe, which the real probe
// correctly reports as not one, and a gate that never sees a terminal cannot be
// shown to honor anything.
func standInTerminal(t *testing.T) {
	t.Helper()
	saved := fileIsTerminal
	fileIsTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { fileIsTerminal = saved })
}

// noColorEntryPoints is every entry point in this package that decides color for
// os.Stdout itself, each driven by a cheap invocation that prints styled output.
// A new entry point that colors belongs here.
var noColorEntryPoints = []struct {
	name string
	// darwinOnlyEffect is set for the macos-* commands: on Linux they refuse (in
	// color), on darwin they would really provision an account.
	darwinOnlyEffect bool
	// setup runs after HOME and the cwd are fresh temp dirs, before the command.
	setup func(t *testing.T)
	run   func() int
}{
	{name: "--help", run: func() int { return Main([]string{"yolo", "--help"}) }},
	{name: "config-ref", run: func() int { return runConfigRef([]string{"config-ref"}) }},
	{name: "init", run: func() int { return runInit([]string{"init"}) }},
	{name: "host", run: func() int { return runHost([]string{"host", "wrappers", "status"}) }},
	// programsJail points every variable the command reads (the pack root, npm prefix,
	// GOPATH) at the fixture, never at the environment the suite runs in. The command
	// reads that root through entrypoint.LoadJailPacks, which switches the process to
	// the tolerant manifest decoder (packload.TolerateSkew); the restore keeps every
	// later test in this binary on the strict one (packupdate_test.go names the trap).
	{name: "programs", setup: func(t *testing.T) {
		programsJail(t)
		t.Cleanup(packload.OverrideSkewTolerance(false))
	},
		run: func() int { return runPrograms([]string{"programs", "ls"}) }},
	{name: "pack", run: func() int { return runPack([]string{"pack", "ls"}) }},
	// No boot baseline in a fresh cwd, which drift reports in color.
	{name: "config", run: func() int { return runConfig([]string{"config", "drift"}) }},
	// A configured pack that cannot resolve is reported in color, before any probe.
	{name: "check-deps", setup: func(t *testing.T) {
		writeUserConfig(t, `{"packs": ["file:///nonexistent-yjtest-pack"]}`)
	}, run: func() int { return runCheckDeps([]string{"check-deps", "--no-manifest"}) }},
	{name: "describe", run: func() int { return runDescribe([]string{"describe"}) }},
	{name: "apply", run: func() int { return runApply([]string{"apply"}) }},
	// The capture act prints its header before the jail, which the fixture fakes.
	{name: "capture", setup: func(t *testing.T) {
		captureFixtureHome(t, captureFixtureInstaller)
		withFakeCaptureJail(t, fakeCaptureJail(t, new(run.Options), probetoolEntries()))
	}, run: func() int { return runCapture([]string{"capture", "probetool"}) }},
	// The auto-capture trigger `yolo run` wires: the closure itself, driven the way
	// TestALaunchWiresTheAutoCaptureTrigger drives it, so its color is the one the
	// launch's own wiring decides.
	{name: "run auto-capture", setup: func(t *testing.T) {
		captureFixtureHome(t, captureFixtureInstaller)
		withFakeCaptureJail(t, fakeCaptureJail(t, new(run.Options), probetoolEntries()))
	}, run: func() int {
		var seen run.Options
		prev := launchRunPipeline
		launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
		defer func() { launchRunPipeline = prev }()
		if rc := runRun([]string{"run", "--", "true"}); rc != 0 || seen.AutoCapture == nil {
			return 1
		}
		seen.AutoCapture([]string{"probetool"}, capturedPlatform)
		return 0
	}},
	{name: "macos-setup", darwinOnlyEffect: true,
		run: func() int { return runMacosSetup([]string{"macos-setup"}) }},
	{name: "macos-teardown", darwinOnlyEffect: true,
		run: func() int { return runMacosTeardown([]string{"macos-teardown"}) }},
	{name: "macos-unshare", darwinOnlyEffect: true,
		run: func() int { return runMacosUnshare([]string{"macos-unshare"}) }},
	{name: "macos-fix-permissions", darwinOnlyEffect: true,
		run: func() int { return runMacosFixPermissions([]string{"macos-fix-permissions"}) }},
}

// TestEveryCommandHonorsNoColor drives each entry point's OWN color decision with
// a terminal stood in for stdout: with NO_COLOR unset it must color (the control —
// without it, a command that never colored would pass the veto), and with
// NO_COLOR=1 it must not (https://no-color.org).
//
// This is the test that fails when an entry point stops consulting the gate —
// passes isTTYStdout(), or a literal true, where colorForWriter(os.Stdout) belongs.
// Each case runs the registry handler itself, so the call site is what is pinned.
func TestEveryCommandHonorsNoColor(t *testing.T) {
	for _, ep := range noColorEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			if ep.darwinOnlyEffect && goruntime.GOOS == "darwin" {
				t.Skip("on darwin this command provisions the sandbox account for real")
			}
			for _, tc := range []struct {
				noColor  string
				wantANSI bool
			}{{"", true}, {"1", false}} {
				t.Setenv("HOME", t.TempDir())
				t.Chdir(t.TempDir())
				if ep.setup != nil {
					ep.setup(t)
				}
				t.Setenv("NO_COLOR", tc.noColor)
				standInTerminal(t)
				out := captureStdout(t, func() { ep.run() })
				if hasANSI := strings.Contains(out, "\x1b["); hasANSI != tc.wantANSI {
					t.Errorf("NO_COLOR=%q: stdout carries ANSI = %v, want %v:\n%s",
						tc.noColor, hasANSI, tc.wantANSI, out)
				}
			}
		})
	}
}

// writeUserConfig writes the user-scope config under the (already temp) HOME.
func writeUserConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestColorForWriterIsTheGate pins the helper every entry point above shares: a
// non-file writer never colors, a terminal does, and NO_COLOR vetoes the terminal.
func TestColorForWriterIsTheGate(t *testing.T) {
	standInTerminal(t)
	t.Setenv("NO_COLOR", "")
	if colorForWriter(new(strings.Builder)) {
		t.Error("a non-file writer colored")
	}
	if !colorForWriter(os.Stdout) {
		t.Error("control: a terminal with NO_COLOR unset did not color")
	}
	t.Setenv("NO_COLOR", "1")
	if colorForWriter(os.Stdout) {
		t.Error("NO_COLOR=1 did not veto a terminal")
	}
}

// TestPsRealDepsHonorsNoColor pins `yolo ps`'s color decision where it is made:
// runPs hands psRun the deps psRealDeps builds, and running runPs itself would
// query the machine's real container runtime.
func TestPsRealDepsHonorsNoColor(t *testing.T) {
	standInTerminal(t)
	t.Setenv("NO_COLOR", "")
	if !psRealDeps(nil, nil, "").Color {
		t.Fatal("control: psRealDeps did not request color on a terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if psRealDeps(nil, nil, "").Color {
		t.Error("NO_COLOR=1: psRealDeps still requested color")
	}
}

// TestMacosLaunchDepsHonorNoColor pins the macos-user LAUNCH path's color, which
// macosLaunchDeps decides (no unit test can run the launch itself). The four
// macos-* commands are driven end to end above.
func TestMacosLaunchDepsHonorNoColor(t *testing.T) {
	standInTerminal(t)
	t.Setenv("NO_COLOR", "")
	if !macosLaunchDeps(nil, nil).Color {
		t.Fatal("control: macosLaunchDeps did not request color on a terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if macosLaunchDeps(nil, nil).Color {
		t.Error("NO_COLOR=1: macosLaunchDeps still requested color")
	}
}

// colorVerbs is every `yolo config` verb that prints in color. `dump` prints
// JSON, which never colors.
var colorVerbs = []string{"render", "ls", "diff", "reset", "capture", "promote", "drift"}

// TestEveryConfigVerbTakesTheOneColorDecision pins the `yolo config` dispatch:
// every verb that colors is handed THE decision configRunW makes once, through
// colorForWriter. TestEveryCommandHonorsNoColor drives `config drift` for real, and
// before this test the other six verbs each made their own call at their own case,
// so any one of them could stop consulting the gate and the suite stayed green.
//
// Each verb's body is swapped for a recorder, so the test measures what the
// dispatch passes and nothing a verb's fixture would need. It fails when the one
// decision stops going through the gate, and when a verb is dispatched outside
// configColorVerbs (its recorder is never called).
func TestEveryConfigVerbTakesTheOneColorDecision(t *testing.T) {
	for _, verb := range colorVerbs {
		t.Run(verb, func(t *testing.T) {
			saved := configColorVerbs[verb]
			if saved == nil {
				t.Fatalf("configColorVerbs has no %q: the verb is dispatched outside the one "+
					"color decision", verb)
			}
			t.Cleanup(func() { configColorVerbs[verb] = saved })
			for _, tc := range []struct {
				noColor   string
				wantColor bool
			}{{"", true}, {"1", false}} {
				t.Setenv("HOME", t.TempDir())
				t.Chdir(t.TempDir())
				t.Setenv("NO_COLOR", tc.noColor)
				standInTerminal(t)
				called, got := false, false
				configColorVerbs[verb] = func(_ configTarget, _ []string, _, _ io.Writer, color bool) int {
					called, got = true, color
					return 0
				}
				captureStdout(t, func() { runConfig([]string{"config", verb}) })
				if !called {
					t.Fatalf("`yolo config %s` never reached its configColorVerbs entry", verb)
				}
				if got != tc.wantColor {
					t.Errorf("NO_COLOR=%q: `yolo config %s` was handed color = %v, want %v",
						tc.noColor, verb, got, tc.wantColor)
				}
			}
		})
	}
}
