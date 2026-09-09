package check

// jsonreport.go is `yolo check --format json`: the graded findings as data.
//
// WHY IT IS A RECORDING AND NOT A SECOND TRAVERSAL. `check` has ~25 sections
// across a dozen files, every one of them a side-effecting probe, and running
// them twice — once for text, once for JSON — would double a run that already
// includes a nix build and could report two different answers about the same
// machine. It does not have to: every graded line in the report goes through
// exactly one sink, the reporter's ok/fail/warn (and configWarn, which routes
// the config loaders' findings onto warn rather than to a channel of its own —
// see its comment for why that consolidation happened). So the reporter records
// what it grades, in the same single pass, and the two forms are one measurement
// rendered twice.
//
// WHAT IS DELIBERATELY NOT HERE: the dim informational lines and the section
// prose. Those are not findings — they are the report teaching a reader, and
// they carry no pass/warn/fail grade to act on. A consumer wanting them can read
// the text form, which is what it is for. The COUNTS are here, and they are the
// reporter's own, so the document's summary and the printed summary cannot
// disagree.

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
)

// Finding is one graded line of the report.
type Finding struct {
	// Section is the header the finding appeared under ("Container Runtime",
	// "Packs", …), stamped at record time so a consumer never has to re-derive
	// the section order.
	Section string `json:"section"`
	// Status is "pass", "warn" or "fail" — the three grades the badges carry.
	Status  string `json:"status"`
	Message string `json:"message"`
	// Note is the remediation text the human form renders as "-> …" lines,
	// unwrapped. Empty for most findings.
	Note string `json:"note,omitempty"`
}

// Report is one `yolo check --format json` document.
type Report struct {
	Version string `json:"version"`
	// Failed is the field that decides the exit code, and it is first among the
	// counts for that reason: exit is non-zero iff any section FAILED. Warnings
	// never fail the command.
	Passed   int       `json:"passed"`
	Warned   int       `json:"warned"`
	Failed   int       `json:"failed"`
	Findings []Finding `json:"findings"`
}

// record appends one graded finding. Called only from ok/fail/warn, which is
// what makes the recording exhaustive.
func (r *reporter) record(status, msg, note string) {
	r.findings = append(r.findings, Finding{
		Section: r.section,
		Status:  status,
		Message: msg,
		Note:    note,
	})
}

// report is the document the reporter has accumulated.
func (r *reporter) report() Report {
	f := r.findings
	if f == nil {
		// `[]`, never `null`: a consumer looping over the value should not have
		// to special-case a run that graded nothing.
		f = []Finding{}
	}
	return Report{
		Version:  r.version,
		Passed:   r.passed,
		Warned:   r.warned,
		Failed:   r.failed,
		Findings: f,
	}
}

// finish emits the JSON document when one was asked for, and returns rc
// unchanged.
//
// EVERY ONE of Check's five exits goes through it — including the four early
// ones, which stop the run at a config parse error or an accumulated failure —
// so `--format json` can never answer with an empty stream. That is the whole
// reason it takes the exit code and hands it straight back: a caller that
// remembers to emit at the happy path and forgets at an early return produces
// exactly the silence a machine consumer cannot diagnose.
func finish(out io.Writer, errw io.Writer, format string, r *reporter, rc int) int {
	if !outfmt.IsJSON(format) {
		return rc
	}
	enc, err := json.MarshalIndent(r.report(), "", "  ")
	if err != nil {
		fmt.Fprintf(errw, "yolo check: encoding the report failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, string(enc))
	return rc
}
