package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// runcmdnotch_test.go is docs/design/declaration-parity.md DP-B22: `yolo --at guest --
// <cmd>` did not select a notch, did not refuse — it CORRUPTED THE ARGV. cli.RewriteArgv
// leaves a non-host notch alone (only `host` has an exec verb to redirect to), and
// parseRunArgs had no `--at` case, so its default arm read the flag as the start of the
// command: a jail started and then failed inside itself with `--at: command not found`.
//
// The row's own note is that the existing test asserted RewriteArgv's output and never ran
// parseRunArgs — "the callee pinned, the call site not". These run the parser.

// TestParseRunArgsConsumesTheNotchFlag: both spellings land on Options.Notch, and NEITHER
// leaves a token in the inner command's argv.
func TestParseRunArgsConsumesTheNotchFlag(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantNotch string
		wantArgs  []string
	}{
		// The exact shape RewriteArgv produces for `yolo --at guest -- claude`: the flag
		// precedes the injected "run" token, because "run" is inserted at the `--`.
		{"separate value", "--at guest run -- claude", "guest", []string{"claude"}},
		{"glued value", "--at=guest run -- claude", "guest", []string{"claude"}},
		// `yolo run --at host -- bash` reaches the parser intact: RewriteArgv only
		// rewrites when nothing before `--` names a subcommand, and "run" does.
		{"explicit run subcommand", "run --at host -- bash", "host", []string{"bash"}},
		{"jail is carried like any other", "run --at jail -- bash", "jail", []string{"bash"}},
		// Absent flag: the field stays empty and the config decides.
		{"absent", "run -- bash", "", []string{"bash"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts run.Options
			parseRunArgs(strings.Fields(tc.in), &opts)
			if opts.Notch != tc.wantNotch {
				t.Errorf("parseRunArgs(%q).Notch = %q, want %q", tc.in, opts.Notch, tc.wantNotch)
			}
			if !reflect.DeepEqual(opts.Args, tc.wantArgs) {
				t.Errorf("parseRunArgs(%q).Args = %q, want %q — a consumed flag must not "+
					"reach the inner command", tc.in, opts.Args, tc.wantArgs)
			}
			// The failure this row is named for, asserted directly: the token the user
			// typed as a flag must never be the command yolo tries to run.
			for _, a := range opts.Args {
				if a == "--at" || strings.HasPrefix(a, "--at=") {
					t.Errorf("parseRunArgs(%q) put %q in the inner command's argv — this "+
						"is the `--at: command not found` defect", tc.in, a)
				}
			}
		})
	}
}

// TestParseRunArgsTakesTheNotchValueWhateverItLooksLike: `--at` is a value flag like
// `--network`, so the next token is its value even when it looks like something else.
// Without this the parser would have a second, softer reading of one flag — the ambiguity
// OQ-PT5 took away from -p/--profile for the same reason.
func TestParseRunArgsTakesTheNotchValueWhateverItLooksLike(t *testing.T) {
	var opts run.Options
	parseRunArgs(strings.Fields("run --at -h -- bash"), &opts)
	if opts.Notch != "-h" {
		t.Errorf("Notch = %q, want %q: --at consumes the next token as its value", opts.Notch, "-h")
	}
	if !reflect.DeepEqual(opts.Args, []string{"bash"}) {
		t.Errorf("Args = %q, want [bash]", opts.Args)
	}
	// A dangling `--at` at the end takes no value and starts no command, the way every
	// other value flag in this parser behaves — and it is a misuse runRun refuses
	// (TestValueFlagWithNoValueIsRefused).
	var dangling run.Options
	parsed := parseRunArgs([]string{"run", "--at"}, &dangling)
	if dangling.Notch != "" || len(dangling.Args) != 0 {
		t.Errorf("a dangling --at should consume nothing: Notch=%q Args=%q",
			dangling.Notch, dangling.Args)
	}
	if parsed.misuse == nil {
		t.Error("a dangling --at reported no misuse")
	}
}

// TestRunHelpIsNotAnswerableThroughTheNotchValue is the runHelpRequested half. `--at` had
// to join that scan's value-flag list when it joined the parser's, or the two copies would
// disagree about one token and `yolo run --at -h -- x` would print run's usage instead of
// launching — the drift TestValueTakingFlagsCoverRunHelpSkips exists to catch one direction
// of.
func TestRunHelpIsNotAnswerableThroughTheNotchValue(t *testing.T) {
	if runHelpRequested(strings.Fields("run --at -h -- bash")) {
		t.Error("`-h` as the --at VALUE was read as a help request; runHelpRequested must " +
			"skip it exactly as it skips --network's value")
	}
	// The control: a real help request is still answered.
	if !runHelpRequested(strings.Fields("run --at jail --help")) {
		t.Error("a genuine --help after a complete --at pair was not recognised")
	}
}
