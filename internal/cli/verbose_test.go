package cli

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The call-site pin for the global verbose strip: Main must consume the flag
// before subcommand resolution and publish the opt-in, while leaving every
// other argv byte alone — including a -v after `--`, which is the inner
// command's flag, not ours (the stripUserLayer boundary).
func TestApplyVerboseFlagStripsAndPublishes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"verbose before dashdash", []string{"--verbose", "--", "bash"}, []string{"--", "bash"}},
		{"short spelling", []string{"-v", "stop"}, []string{"stop"}},
		{"mid-argv", []string{"stop", "--verbose", "extra"}, []string{"stop", "extra"}},
		{"after dashdash is the inner command's", []string{"--", "vim", "-v"}, []string{"--", "vim", "-v"}},
		{"no flag", []string{"--", "bash"}, []string{"--", "bash"}},
		{"glued form is not ours", []string{"--verbose=1", "--"}, []string{"--verbose=1", "--"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.VerboseEnv, "")
			resetVerboseFlagTyped(t)
			got := applyVerboseFlag(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("applyVerboseFlag(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("applyVerboseFlag(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
			if tc.name == "no flag" || tc.name == "after dashdash is the inner command's" || tc.name == "glued form is not ours" {
				if os.Getenv(paths.VerboseEnv) != "" {
					t.Errorf("published %s without seeing the flag", paths.VerboseEnv)
				}
				return
			}
			if os.Getenv(paths.VerboseEnv) != "1" {
				t.Errorf("%s not published", paths.VerboseEnv)
			}
		})
	}
}

// routeDecision-level pin: a verbose flag must not turn a runnable argv into
// an unknown command — the reason the strip runs before RewriteArgv.
func TestVerboseFlagKeepsRouting(t *testing.T) {
	for _, argv := range [][]string{
		{"yolo", "--verbose", "--", "bash"},
		{"yolo", "-v"},
		{"yolo", "--verbose", "stop"},
	} {
		if d := routeDecision(applyVerboseFlag(argv[1:])); d != "run" && d != "dispatch:run" && d != "dispatch:stop" {
			t.Errorf("routing after verbose strip of %v = %q", argv, d)
		}
	}
}

// resetVerboseFlagTyped restores the process-scoped "the flag was typed" signal
// after a test that sets it. It is package state by design (the front door writes
// it once, before any subcommand runs), so a test that leaves it set would hand
// every later test in this package a printing launch it never asked for.
func resetVerboseFlagTyped(t *testing.T) {
	t.Helper()
	prev := verboseFlagTyped
	t.Cleanup(func() { verboseFlagTyped = prev })
	verboseFlagTyped = false
}

// THE CALL-SITE PIN for D12's trap: `--verbose` and an inherited YOLO_VERBOSE=1
// are the SAME env var downstream (the strip publishes the flag as that
// variable), and they must still behave differently — the typed flag prints the
// timing report, the environment records in silence. The distinguishing signal is
// Options.Verbose, so this drives the real front door and reads what the launch
// pipeline was handed.
//
// Delete `opts.Verbose = explicitVerbose()` from runRun, or the
// `verboseFlagTyped = true` from applyVerboseFlag, and the first case goes red;
// set the field off anything the environment can also say and the second does.
func TestTypedVerboseReachesTheLaunchButTheEnvVarDoesNot(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		env  string
		want bool
	}{
		{"typed --verbose", []string{"yolo", "--verbose", "--", "true"}, "", true},
		{"typed -v", []string{"yolo", "-v", "--", "true"}, "", true},
		{"inherited YOLO_VERBOSE", []string{"yolo", "--", "true"}, "1", false},
		{"neither", []string{"yolo", "--", "true"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.VerboseEnv, tc.env)
			resetVerboseFlagTyped(t)

			var seen run.Options
			prev := launchRunPipeline
			launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
			t.Cleanup(func() { launchRunPipeline = prev })

			captureBoth(t, func() {
				if rc := Main(tc.argv); rc != 0 {
					t.Errorf("Main(%v) = %d, want 0 with the pipeline stubbed", tc.argv, rc)
				}
			})
			if seen.Verbose != tc.want {
				t.Errorf("Options.Verbose = %v, want %v — the launch cannot tell a typed "+
					"--verbose from an exported %s, so one of them reports wrongly",
					seen.Verbose, tc.want, paths.VerboseEnv)
			}
			// The env var still reaches the pipeline for RECORDING, through the
			// Getenv seam every gate reads; only the printing half is in doubt here.
			if tc.env != "" && os.Getenv(paths.VerboseEnv) == "" {
				t.Error("the environment opt-in was lost; recording would be off too")
			}
		})
	}
}
