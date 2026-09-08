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
//
// PUBLISHING IS NOT THE WHOLE SIGNAL, though, and the gap is D12's: because the
// flag publishes itself as YOLO_VERBOSE, `--verbose` and a YOLO_VERBOSE=1
// inherited from a shell profile are indistinguishable to every Getenv reader
// downstream — and the timing surface now has to tell them apart, since an
// explicit flag PRINTS its report and a persistent setting only records. So the
// strip also records, in-process, that the flag was TYPED on this invocation
// (verboseFlagTyped below). A second env var could not carry that: it would be
// inherited by the next `yolo` too, which is the thing being distinguished.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// verboseFlags are the flag's spellings, in the order usage text prints them.
var verboseFlags = []string{"--verbose", "-v"}

// verboseFlagTyped records whether THIS invocation's argv carried the flag, as
// opposed to inheriting YOLO_VERBOSE from the environment. Process-scoped state,
// written once by applyVerboseFlag before any subcommand runs and read by the
// handlers that must distinguish the two (runRun, runStop — see D12 in
// docs/design/perf-logging.md).
var verboseFlagTyped bool

// explicitVerbose reports whether --verbose / -v was typed on this invocation.
// The one reader of verboseFlagTyped, so the "typed, not inherited" rule has a
// single spelling for every handler that needs it.
func explicitVerbose() bool { return verboseFlagTyped }

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
		// The in-process half: only a TYPED flag sets this, which is what makes
		// `yolo -v -- bash` print its timing report while an exported
		// YOLO_VERBOSE=1 records in silence.
		verboseFlagTyped = true
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
