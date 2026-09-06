package cli

// verbose.go is the front-door half of the global `--verbose` / `-v` flag: the
// future gate for launcher diagnostics, wired in v1 to the same timing-span
// surface `--timing` drives (docs/design/perf-logging.md D1).
//
// IT IS A GLOBAL FLAG, stripped before subcommand resolution, exactly the
// `--user-layer` pattern (userlayer.go): a diagnostics gate belongs at the
// process level — `yolo --verbose -- bash`, `yolo stop --verbose`, and whatever
// command grows a diagnostic next should all share one spelling rather than
// each subcommand parser growing its own forgetter.
//
// Published through the environment (os.Setenv) rather than a threaded
// parameter, for the same reason userlayer.go publishes: every Getenv-seam
// reader in the process sees it, subcommands need no signature changes, and a
// nested `yolo` this process execs inherits the verbosity deliberately. The
// run pipeline reads it through paths.VerboseEnv; `YOLO_VERBOSE=1` typed
// directly is the same opt-in without flag surgery.
//
// The scan stops at `--`, so a `-v` meant for the COMMAND the jail runs
// (`yolo -- vim -v`) is left alone — the same boundary stripUserLayer states.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// verboseFlags are the flag's spellings, in the order usage text prints them.
var verboseFlags = []string{"--verbose", "-v"}

// applyVerboseFlag strips the global verbose flags from args (up to `--`) and
// publishes the opt-in to the process environment. Returns the remaining args.
func applyVerboseFlag(args []string) []string {
	found := false
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if isVerboseFlag(a) {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	if found {
		// Publishing cannot usefully fail here (unset variables are always
		// settable), and a diagnostics gate may never refuse a command.
		_ = os.Setenv(paths.VerboseEnv, "1")
	}
	return rest
}

func isVerboseFlag(a string) bool {
	for _, f := range verboseFlags {
		if a == f {
			return true
		}
	}
	return false
}
