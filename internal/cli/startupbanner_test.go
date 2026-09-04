package cli

// startupbanner_test.go pins the four properties the startup banner was asked
// for: it reaches STDERR through the real dispatch, it corrupts no
// machine-readable STDOUT, the escape hatch turns it off, and `run` does not
// print it twice.
//
// Every test here drives dispatchNative rather than emitStartupBanner. A test
// that exercised the renderer alone would stay green with the call in
// dispatchNative deleted — the shape AGENTS.md records this repo shipping five
// times — so the mutation to check against any change here is: delete
// `emitStartupBanner(os.Stderr, os.Getenv)` from dispatch.go and watch these go
// red.

import (
	"bytes"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/banner"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// captureStdio runs body with os.Stdout and os.Stderr pointed at temp FILES and
// returns what each received.
//
// Files rather than pipes for probeHelp's reason: `config-ref` prints a
// multi-kilobyte document, and a pipe whose buffer fills blocks the writer — a
// hang instead of a result.
func captureStdio(t *testing.T, body func()) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outF, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	body()
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outF.Close()
	_ = errF.Close()
	ob, _ := os.ReadFile(filepath.Join(dir, "stdout"))
	eb, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	return string(ob), string(eb)
}

// wantStartupBanner renders the banner the CURRENT environment should produce,
// terminator included, and fails the test if it comes back empty — an empty
// expectation would let every assertion built on it pass vacuously, which is the
// one way a coverage test can lie.
func wantStartupBanner(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	emitStartupBanner(&buf, os.Getenv)
	got := buf.String()
	if strings.TrimSpace(got) == "" {
		t.Fatalf("the startup banner rendered empty in this environment (%s=%q) — "+
			"every assertion in this file would pass for the wrong reason",
			"YOLO_NO_BANNER", os.Getenv("YOLO_NO_BANNER"))
	}
	return got
}

// sandbox points cwd and $HOME at empty temp trees, so a command that reads
// config reads the same (absent) config in every subtest and writes nothing into
// the real workspace.
func sandbox(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Chdir(t.TempDir())
}

// The banner reaches stderr through the REAL dispatch, on a command that has
// nothing else to say on stderr.
func TestStartupBannerReachesStderrThroughDispatch(t *testing.T) {
	sandbox(t)
	want := wantStartupBanner(t)

	var rc int
	stdout, stderr := captureStdio(t, func() {
		rc = dispatchNative("config-ref", []string{"config-ref"})
	})
	if rc != 0 {
		t.Fatalf("`yolo config-ref` exit = %d, want 0", rc)
	}
	if stderr != want {
		t.Errorf("stderr = %q, want the startup banner %q", stderr, want)
	}
	if !strings.HasPrefix(stderr, "yolo-jail ") {
		t.Errorf("stderr = %q — a bug report has to open with the version", stderr)
	}
	// Not a substring search for "yolo-jail": the reference text is full of
	// `yolo-jail.jsonc`. The whole banner LINE is what must not appear.
	if strings.Contains(stdout, strings.TrimSpace(want)) {
		t.Errorf("the banner leaked onto stdout — config-ref's stdout is reference text")
	}
}

// TestEveryRegisteredCommandGetsTheStartupBanner walks the dispatch registry, so
// a command added tomorrow inherits the banner or fails here. It probes with
// `--help`, the one argument every registered command answers cheaply and
// without side effects (subhelp_test.go's TestEveryRegisteredCommandAnswersHelp
// is what makes that true).
func TestEveryRegisteredCommandGetsTheStartupBanner(t *testing.T) {
	sandbox(t)
	want := wantStartupBanner(t)
	for _, sub := range slices.Sorted(maps.Keys(registry)) {
		t.Run(sub, func(t *testing.T) {
			_, _, stderr := probeHelp(t, sub, "--help")
			if stderr != want {
				t.Errorf("`yolo %s --help` stderr = %q, want the startup banner %q",
					sub, stderr, want)
			}
		})
	}
}

// THE REGRESSION WORTH PINNING HARDEST. Four commands have machine-readable
// stdout that a banner would corrupt: `config dump` and `describe --json` print
// the canonical snapshot JSON the startup config-change diff validates against,
// `config drift` is documented for agents down to its 0/3/4 exit codes, and
// `config-ref` prints reference text.
//
// Each row runs the command BOTH ways — through dispatchNative, and by calling
// the same body with its own writers — and requires the two stdouts to be
// byte-identical. That is stronger than "stdout has no banner in it": it also
// catches a banner that lands on stdout in some other spelling, and it catches
// the exit code changing underneath.
func TestMachineReadableStdoutIsByteIdenticalThroughDispatch(t *testing.T) {
	cases := []struct {
		name   string
		argv   []string
		direct func(out, errw io.Writer) int
	}{
		{
			name:   "config-ref",
			argv:   []string{"config-ref"},
			direct: func(out, _ io.Writer) int { return configRefRun(out, false) },
		},
		{
			name:   "config dump",
			argv:   []string{"config", "dump"},
			direct: func(out, errw io.Writer) int { return configDump(nil, out, errw) },
		},
		{
			name:   "config drift",
			argv:   []string{"config", "drift"},
			direct: func(out, errw io.Writer) int { return configDrift(nil, out, errw, false) },
		},
		{
			name:   "describe --json",
			argv:   []string{"describe", "--json"},
			direct: func(out, errw io.Writer) int { return describeMain([]string{"--json"}, out, errw, false) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sandbox(t)
			bannerLine := wantStartupBanner(t)

			var wantOut, wantErr bytes.Buffer
			wantRC := tc.direct(&wantOut, &wantErr)

			var gotRC int
			gotOut, gotErr := captureStdio(t, func() {
				gotRC = dispatchNative(tc.argv[0], tc.argv)
			})

			if gotOut != wantOut.String() {
				t.Errorf("`yolo %s` stdout changed under the banner\n--- through dispatch ---\n%q\n"+
					"--- direct ---\n%q", tc.name, gotOut, wantOut.String())
			}
			if gotRC != wantRC {
				t.Errorf("`yolo %s` exit = %d through dispatch, %d direct", tc.name, gotRC, wantRC)
			}
			// The banner is the ONLY thing dispatch adds, and it goes first — a
			// caller reading stderr for the command's own diagnostics still finds
			// them, one line down.
			if want := bannerLine + wantErr.String(); gotErr != want {
				t.Errorf("`yolo %s` stderr = %q, want the banner followed by the command's own "+
					"stderr %q", tc.name, gotErr, want)
			}
		})
	}
}

// The escape hatch. Named for the repo's YOLO_NO_* shape, off by default, and it
// silences the banner without changing anything else the command writes.
func TestStartupBannerHatchSuppressesIt(t *testing.T) {
	sandbox(t)
	t.Setenv("YOLO_NO_BANNER", "1")

	var rc int
	stdout, stderr := captureStdio(t, func() {
		rc = dispatchNative("config-ref", []string{"config-ref"})
	})
	if rc != 0 {
		t.Fatalf("`yolo config-ref` exit = %d, want 0", rc)
	}
	if stderr != "" {
		t.Errorf("%s=1 left stderr = %q, want silence", "YOLO_NO_BANNER", stderr)
	}
	if stdout == "" {
		t.Error("the hatch silenced the command itself, not just the banner")
	}
}

// NO DOUBLE-PRINT FOR `run`. The launch line the run pipeline writes once it
// knows the runtime and the container name deliberately carries neither the
// version nor the platform — the startup banner above it already did — so `yolo
// run` prints each of those fields exactly once.
//
// Asserted here as a property of the two RENDERERS side by side rather than by
// launching a container: run.LaunchBanner's own test owns the same invariant
// from inside that package (TestLaunchBannerDoesNotRepeatTheStartupFields), and
// this is the half that can see both lines at once.
func TestRunDoesNotDoublePrintTheStartupFields(t *testing.T) {
	sandbox(t)
	startup := strings.TrimSpace(wantStartupBanner(t))
	// Both spellings a launch can take: a fresh launch, and an attach to a jail
	// baked at a different version (the one case the launch line carries a
	// version-shaped field at all).
	for _, launch := range []string{
		run.LaunchBanner("podman", "yolo-x-abc", "0.6.0", "", []string{"pids=32768"}),
		run.LaunchBanner("podman", "yolo-x-abc", "0.6.0", "0.5.0", nil),
	} {
		for _, field := range []string{"yolo-jail", banner.Platform()} {
			if strings.Contains(launch, field) {
				t.Errorf("the launch line %q repeats %q, which the startup banner %q already printed",
					launch, field, startup)
			}
		}
	}
}
