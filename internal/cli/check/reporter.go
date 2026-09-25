package check

import (
	"fmt"
	"io"
	"strings"
)

// ANSI SGR sequences for the colored output. The contract is defined on the
// ANSI-STRIPPED text (goldens pin the stripped bytes), so these are cosmetic —
// a test asserts stripping them yields the Color=false output verbatim.
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
	// [SKIP] is DIM, deliberately: it is the one badge that reports an absence, and a
	// coloured one would compete for attention with the three that report a finding.
	ansiDimBadge = "\x1b[2m"
)

// reporter accumulates the pass/warn/fail counts and writes the report.
//
// It also RECORDS every graded finding, because it is the one sink they all pass
// through: ok/fail/warn are the only three functions that can produce a [PASS],
// [FAIL] or [WARN] line, and configWarn routes the config loaders' findings onto
// warn rather than to a channel of its own. That single-sink property — the same
// one that lets the summary count honestly — is what makes `--format json` a
// recording rather than a second traversal of the sections (see jsonreport.go).
type reporter struct {
	w       io.Writer
	color   bool
	passed  int
	warned  int
	failed  int
	skipped int
	// section is the header most recently printed, stamped onto each finding so
	// a consumer can attribute one without re-deriving the section order.
	section  string
	version  string
	findings []Finding
}

func newReporter(w io.Writer, color bool) *reporter {
	return &reporter{w: w, color: color}
}

func (r *reporter) line(s string) { fmt.Fprintln(r.w, s) }

func (r *reporter) blank() { fmt.Fprintln(r.w) }

// sectionHeader prints a bold section header and remembers it as the section
// subsequent findings belong to.
//
// It was named `section` until the reporter grew a field of that name. The
// rename is mechanical; the recording is the point.
func (r *reporter) sectionHeader(name string) {
	r.section = name
	r.line(r.style(name, ansiBold))
}

// dim prints a dim informational line: two leading spaces, a "- " marker.
func (r *reporter) dim(msg string) {
	r.line("  " + r.style("- "+msg, ansiDim))
}

// note renders a (possibly multi-line) remediation note: first line prefixed
// with "-> ", continuation lines aligned. The note text itself is dim.
func (r *reporter) note(text string) {
	if text == "" {
		return
	}
	for _, l := range NoteLines(text) {
		// NoteLines already prepends the indent+arrow; style the whole
		// line dim.
		r.line(r.style(l, ansiDim))
	}
}

// ok increments the pass count and prints " [PASS] msg".
func (r *reporter) ok(msg string) {
	r.passed++
	r.record("pass", msg, "")
	r.line("  " + r.style("[PASS]", ansiBoldGreen) + " " + msg)
}

// fail increments the fail count and prints " [FAIL] msg" + optional note.
func (r *reporter) fail(msg, note string) {
	r.failed++
	r.record("fail", msg, note)
	r.line("  " + r.style("[FAIL]", ansiWhiteOnRed) + " " + msg)
	r.note(note)
}

// warn increments the warn count and prints " [WARN] msg" + optional note.
func (r *reporter) warn(msg, note string) {
	r.warned++
	r.record("warn", msg, note)
	r.line("  " + r.style("[WARN]", ansiBlackOnYel) + " " + msg)
	r.note(note)
}

// skip prints " [SKIP] msg" and counts it SEPARATELY from the three grades.
//
// # Why this level exists
//
// A check that did not look must not be counted as a pass — the principle OQ-3 ruled in
// docs/reference/claude-oauth-interposition.md#oq-3, with the spelling delegated here. Nine sites
// across five section files used to call r.ok on an area they had DECLINED to examine
// ("Inside jail — loophole checks skipped"), so an all-green in-jail run included areas
// nobody checked, and the pass count said so with a straight face. It was one of the three
// layers that hid a daemon dying 2,549 times in one jail.
//
// # Why not a warning, and why not silence
//
// A WARNING would be a finding, and there is none: nothing is wrong with a host-side area
// a jail cannot see, so warning about it would train the reader to ignore the badge that
// means "act on this". SILENCE would be worse than the pass it replaces — a section that
// prints nothing is indistinguishable from one that was never wired, which is the exact
// class the jsonreport recording exists to make visible.
//
// So it is a fourth level: printed, recorded, counted in its own bucket, and absent from
// the pass tally. The summary shows it only when non-zero, so a host run — where nothing
// skips — reads exactly as it did before.
//
// # It does not touch the exit code
//
// Check() returns 1 on r.failed alone. A skip is not a failure and must never become one:
// the jail is working as designed, and refusing there would make `yolo check` unusable in
// the one place AGENTS.md makes mandatory for verification.
func (r *reporter) skip(msg, note string) {
	r.skipped++
	r.record("skip", msg, note)
	r.line("  " + r.style("[SKIP]", ansiDimBadge) + " " + msg)
	r.note(note)
}

// hostFact prints a [SKIP] for a finding that is TRUE BUT NOT ABOUT THIS READER: a fact
// about the host, observed from inside a jail, where it is neither actionable nor a
// statement about the jail's own health.
//
// It is the second half of OQ-3's ruling. The first half is the nine passes above; the
// other direction is a section that grades an invisible host fact as [FAIL] and tells an
// in-jail reader their setup is broken when it is merely not visible from where they are
// standing. Same badge, because the reader's action is the same in both cases — none —
// and a fifth level would be a vocabulary nobody could keep straight.
//
// The NOTE is mandatory here and the parameter says so by being required: a bare "[SKIP]
// GPU" tells a reader less than the [FAIL] it replaces. It has to say where the fact could
// be checked instead.
func (r *reporter) hostFact(msg, whereToCheck string) {
	r.skip(msg+" — a host fact, not visible from inside a jail", whereToCheck)
}

// configWarn is this reporter's config.Warn — the ONE sink every config loader
// and resolver in this package hands its non-fatal findings to. It routes them
// onto the graded [WARN] path, so the summary counts them like any other finding.
//
// IT USED TO BE A SECOND CHANNEL, and that was the whole defect: it printed an
// ungraded "Warning: <msg>" line and said in as many words that it did NOT touch
// the warn count. `yolo check` therefore had two diagnostic channels and a summary
// that aggregated one — measured, three bogus env_sources entries printed five
// Warning lines under a summary reading "2 warnings". See
// docs/design/reference-mismatch-diagnostics.md §3 and §7 step 1, which offered
// "make bare Warning: lines reach the summary, or route them through the
// reporter"; this is the latter, because the former would have meant a third count
// and a badge nobody else uses.
//
// THIS IS THE CONFIG-RESOLUTION HALF, and §3 names two producers: "config resolution
// and loophole discovery". The loophole half is NOT a config.Warn sink at all — the
// supersedes did-you-mean (the best mismatch diagnostic in the tree) goes through
// internal/loopholes' package-level warnf, straight to os.Stderr, from Discover. And
// `check` never reaches that emission site, because its loopholes section walks
// through ValidateSet, which deliberately bypasses Discover (ValidateLoopholes' doc
// comment says why). So that half is graded in the loopholes section instead
// (checkLoopholes, from Set.SupersessionProblems), onto r.warn directly rather than
// through here: it is a finding about the resolved loophole set, not a config
// loader's. Relocating the MATCH to the launch path, where it could refuse, is §7
// step 4, which needs OQ-RM2 ruled first.
//
// GRADING DOES NOT CHANGE THE EXIT CODE. Check() returns 1 on r.failed alone (its
// three gates at check.go), and r.warned is read only by summaryFailWarn and
// summaryFinal below. So a finding that used to scroll past now lands in the count
// and nothing starts refusing — which is why this step needed no ruling on OQ-RM1.
func (r *reporter) configWarn(msg string) { r.warn(msg, "") }

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
// count (warnings are NOT shown here even if present).
func (r *reporter) summaryFailOnly() {
	r.sectionHeader("Summary")
	r.line("  " + r.styledCount(r.failed, "failed", ansiRed))
	r.blank()
}

// summaryFailWarn renders the merged-validation early-exit summary: fail count
// plus warnings when any.
func (r *reporter) summaryFailWarn() {
	r.sectionHeader("Summary")
	parts := []string{r.styledCount(r.failed, "failed", ansiRed)}
	if r.warned > 0 {
		parts = append(parts, r.styledCount(r.warned, "warnings", ansiYellow))
	}
	r.line("  " + strings.Join(parts, ", "))
	r.blank()
}

// summaryFinal renders the end-of-run summary: passed + optional failed +
// optional warnings.
func (r *reporter) summaryFinal() {
	r.sectionHeader("Summary")
	parts := []string{r.styledCount(r.passed, "passed", ansiGreen)}
	if r.failed > 0 {
		parts = append(parts, r.styledCount(r.failed, "failed", ansiRed))
	}
	if r.warned > 0 {
		parts = append(parts, r.styledCount(r.warned, "warnings", ansiYellow))
	}
	// Only when non-zero, so a host run reads exactly as it did before this level
	// existed — nothing skips there, and a "0 skipped" would be noise on every line.
	if r.skipped > 0 {
		parts = append(parts, r.styledCount(r.skipped, "skipped", ansiDim))
	}
	r.line("  " + strings.Join(parts, ", "))
	r.blank()
}
