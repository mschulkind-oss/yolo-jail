package cli

// hostapplydetail.go is docs/design/report-tiers.md §4.5's DETAIL ON DEMAND: which lines the
// report prints by default, and which ones `--verbose` adds.
//
// THE RULE IS THE TIER, NOT THE EMITTER (§4.1). A tier-2 RUN FACT — a destination that would
// render, one already in sync, a dependency probe, a composed-from line — is *counted in the
// verdict* and itemized only on demand; a tier-3 LOSS OR BLOCKER is never hidden, because
// §4.4's contract is that grouping compresses the LINES and never the SET. The one tier-2
// exception the design names explicitly is a would-change CONFIG surface, which stays itemized:
// there are few of them and they are the auditor's core.
//
// WHY THIS IS THE DEFAULT AND NOT A FLAG (OQ-RO1): the operator is the common reader and the
// one who stops reading, the auditor is the one who asks for more, and the footer tells them
// how. A flag nobody knows to type changes nothing for the reader who needed the change.
//
// ⚠ THE GATE HONORS BOTH SPELLINGS, AND explicitVerbose() IS THE WRONG ONE (OQ-RO2). That
// accessor answers "was --verbose TYPED on this invocation", which is deliberately narrower:
// perf-logging D12 needs the distinction because an inherited YOLO_VERBOSE=1 must RECORD
// timings without PRINTING a table at every quit. A long report a user asked for in their own
// shell profile is not that — they asked for it, and this is the diagnostic D14 reserved the
// flag for — so the gate reads the published environment variable, which a typed flag sets too
// (applyVerboseFlag). Copying the typed-only half here is the easy mistake and it silently
// breaks `YOLO_VERBOSE=1 yolo host apply`.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// reportVerbose reports whether this invocation asked for the detail view — a typed
// `--verbose`/`-v` or an inherited `YOLO_VERBOSE`. See the ⚠ above for why it is not
// explicitVerbose().
func reportVerbose() bool { return os.Getenv(paths.VerboseEnv) != "" }

// detail prints a line that belongs to the `--verbose` view only.
//
// A FUNCTION RATHER THAN A PRINTER FIELD, deliberately: richtext.Printer is a line printer with
// color and nothing else — it "exists to end the strip-always duplication, not to know what a
// report is" (§2.2) — and every stage of the apply takes one by value. A wrapper type would
// have meant changing the signature of every reporting function in six files to carry a
// property that belongs to the FACT, not to the writer.
func detail(pr richtext.Printer, format string, args ...any) {
	if !reportVerbose() {
		return
	}
	pr.Printf(format, args...)
}

// reportDestination prints one destination's line at the tier it states: a change or a loss
// prints always, a settled run fact prints under `--verbose`.
//
// The TIER is passed rather than derived for §4.1's reason — the class of a fact is known where
// the fact is produced, and is unrecoverable from the formatted string that arrives here. Every
// call site already passes the same tier to survey.note, which is what keeps the count and the
// line agreeing about what this destination is.
func reportDestination(pr richtext.Printer, tier reportTier, wouldChange bool,
	format string, args ...any) {
	if tier == tierLoss || wouldChange {
		pr.Printf(format, args...)
		return
	}
	detail(pr, format, args...)
}
