package cli

import (
	"os"
	"testing"

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
