package checkcmd

import (
	"fmt"
	"io"
	"strings"
)

// ANSI SGR sequences for the Go-native colored output. Parity is defined on the
// ANSI-STRIPPED text (the plan's Stage-15 exit criteria: goldens pin the
// stripped bytes), so these are cosmetic — a test asserts stripping them yields
// the Color=false output verbatim.
const (
	ansiReset      = "\x1b[0m"
	ansiBold       = "\x1b[1m"
	ansiDim        = "\x1b[2m"
	ansiGreen      = "\x1b[32m"
	ansiRed        = "\x1b[31m"
	ansiYellow     = "\x1b[33m"
	ansiBoldGreen  = "\x1b[1;32m"
	ansiWhiteOnRed = "\x1b[1;97;41m"
	ansiBlackOnYel = "\x1b[1;30;43m"
)

// reporter accumulates the pass/warn/fail counts and writes the report. It is
// the Go analog of the nested ok/fail/warn/_print_note closures in check().
type reporter struct {
	w      io.Writer
	color  bool
	passed int
	warned int
	failed int
}

func newReporter(w io.Writer, color bool) *reporter {
	return &reporter{w: w, color: color}
}

func (r *reporter) line(s string) { fmt.Fprintln(r.w, s) }

func (r *reporter) blank() { fmt.Fprintln(r.w) }

// section prints a bold section header (console.print("[bold]NAME[/bold]")).
func (r *reporter) section(name string) {
	r.line(r.style(name, ansiBold))
}

// dim prints a dim informational line. The Python form is
// `console.print(" [dim]- <msg>[/dim]")` — two leading spaces, a "- " marker.
func (r *reporter) dim(msg string) {
	r.line("  " + r.style("- "+msg, ansiDim))
}

// note renders a (possibly multi-line) remediation note the way check()'s
// _print_note does: first line prefixed with "-> ", continuation lines aligned.
// The note text itself is dim.
func (r *reporter) note(text string) {
	if text == "" {
		return
	}
	for _, l := range NoteLines(text) {
		// NoteLines already prepends the indent+arrow; style the whole
		// line dim to match Python's [dim] wrapping of each note line.
		r.line(r.style(l, ansiDim))
	}
}

// ok increments the pass count and prints " [PASS] msg".
func (r *reporter) ok(msg string) {
	r.passed++
	r.line("  " + r.style("[PASS]", ansiBoldGreen) + " " + msg)
}

// fail increments the fail count and prints " [FAIL] msg" + optional note.
func (r *reporter) fail(msg, note string) {
	r.failed++
	r.line("  " + r.style("[FAIL]", ansiWhiteOnRed) + " " + msg)
	r.note(note)
}

// warn increments the warn count and prints " [WARN] msg" + optional note.
func (r *reporter) warn(msg, note string) {
	r.warned++
	r.line("  " + r.style("[WARN]", ansiBlackOnYel) + " " + msg)
	r.note(note)
}

// warningLine prints a non-counting "Warning: <msg>" line (yellow).
// bare console.print("[yellow]Warning: …[/yellow]") calls (e.g. env_sources file
// not found during the preflight) — informational, NOT a graded [WARN] badge,
// so it does NOT touch the warn count.
func (r *reporter) warningLine(msg string) {
	r.line(r.style("Warning: "+msg, ansiYellow))
}

// style wraps s in an ANSI SGR sequence when color is on; otherwise returns s
// unchanged. Combined-SGR sequences (e.g. "1;97;41") pass through verbatim.
func (r *reporter) style(s, sgr string) string {
	if !r.color || sgr == "" {
		return s
	}
	return sgr + s + ansiReset
}

// styledCount renders a colored "N label" fragment for the summary line.
func (r *reporter) styledCount(n int, label, sgr string) string {
	return r.style(fmt.Sprintf("%d %s", n, label), sgr)
}

// summaryFailOnly renders the Config-Files early-exit summary: just the fail
// count (check() line 1449-1451 — warnings are NOT shown here even if present).
func (r *reporter) summaryFailOnly() {
	r.section("Summary")
	r.line("  " + r.styledCount(r.failed, "failed", ansiRed))
	r.blank()
}

// summaryFailWarn renders the merged-validation early-exit summary: fail count
// plus warnings when any (check() line 1495-1499).
func (r *reporter) summaryFailWarn() {
	r.section("Summary")
	parts := []string{r.styledCount(r.failed, "failed", ansiRed)}
	if r.warned > 0 {
		parts = append(parts, r.styledCount(r.warned, "warnings", ansiYellow))
	}
	r.line("  " + strings.Join(parts, ", "))
	r.blank()
}

// summaryFinal renders the end-of-run summary: passed + optional failed +
// optional warnings (check() line 2095-2101).
func (r *reporter) summaryFinal() {
	r.section("Summary")
	parts := []string{r.styledCount(r.passed, "passed", ansiGreen)}
	if r.failed > 0 {
		parts = append(parts, r.styledCount(r.failed, "failed", ansiRed))
	}
	if r.warned > 0 {
		parts = append(parts, r.styledCount(r.warned, "warnings", ansiYellow))
	}
	r.line("  " + strings.Join(parts, ", "))
	r.blank()
}
