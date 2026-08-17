package nixdiag

import "strings"

// Problem is a (title, detail) pair parsed from a module self-check's output.
type Problem struct {
	Title  string
	Detail string
}

// Grade is the severity a self-check assigned to one of its own output lines.
type Grade int

const (
	// GradeFail is a "FAIL: …" line: the self-check's exit code is non-zero.
	GradeFail Grade = iota
	// GradeNote is a "NOTE: …" line: a warning that does not fail the check.
	GradeNote
	// GradeOK is an "OK: …" line: a passing observation the daemon wants
	// SURFACED, as opposed to the bare trailing "OK" summary, which is not a
	// graded line and is dropped.
	GradeOK
)

// GradedLine is one entry parsed from a module self-check's output: a grade, a
// title, and the continuation lines beneath it.
type GradedLine struct {
	Grade  Grade
	Title  string
	Detail string
}

// SplitSelfCheckLines parses a module self-check's stdout into graded entries.
//
// The self-check wire has always been a THREE-level line protocol — "FAIL: …"
// (fails the check), "NOTE: …" (warns), "OK: …" (a passing observation worth
// printing) — with any following non-prefixed lines being that entry's detail,
// and non-prefixed preamble before the first entry dropped. Core, however, only
// ever parsed one third of it (SplitSelfCheckProblems, FAIL only) and threw the
// rest away on the rc=0 path.
//
// That gap is why `yolo check` grew a claude-shaped section of its own: the
// broker's shared-credentials REMAINING LIFETIME could not be reported through
// the loophole's own doctor_cmd, because a passing self-check's output never
// reached the screen, so the grading was re-implemented in Go inside `check`
// against a schema (`claudeAiOauth.expiresAt`) core has no business knowing.
// Parsing the whole protocol is what lets that live where it belongs — in the
// loophole's own --self-check — and it does the same favour for every other
// loophole's notes, which were being swallowed identically.
//
// A bare trailing "OK"/"OK (…)" summary carries no colon and is deliberately NOT
// a graded line: callers render their own "self-check ok" header for that.
func SplitSelfCheckLines(output string) []GradedLine {
	var lines []GradedLine
	var current []string
	grade := GradeFail
	have := false
	flush := func() {
		if !have {
			return
		}
		title, detail := finalizeEntry(current)
		lines = append(lines, GradedLine{Grade: grade, Title: title, Detail: detail})
	}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, " \t\r\f\v")
		switch {
		case strings.HasPrefix(line, "FAIL:"):
			flush()
			grade, current, have = GradeFail, []string{strings.TrimSpace(line[len("FAIL:"):])}, true
		case strings.HasPrefix(line, "NOTE:"):
			flush()
			grade, current, have = GradeNote, []string{strings.TrimSpace(line[len("NOTE:"):])}, true
		case strings.HasPrefix(line, "OK:"):
			flush()
			grade, current, have = GradeOK, []string{strings.TrimSpace(line[len("OK:"):])}, true
		case strings.HasPrefix(line, "OK"):
			// The trailing summary ("OK", "OK (broker present; …)"): colon-less,
			// so not a graded entry — but it must still CLOSE the entry above it,
			// or a clean self-check would render its own summary as the preceding
			// line's remediation detail.
			flush()
			have = false
		case have:
			current = append(current, line)
		}
	}
	flush()
	return lines
}

// SplitSelfCheckProblems is the FAIL-only view of SplitSelfCheckLines, kept as
// its own name because the non-zero-exit render path wants exactly that subset.
func SplitSelfCheckProblems(output string) []Problem {
	var problems []Problem
	for _, l := range SplitSelfCheckLines(output) {
		if l.Grade == GradeFail {
			problems = append(problems, Problem{Title: l.Title, Detail: l.Detail})
		}
	}
	return problems
}

func finalizeEntry(lines []string) (title, detail string) {
	var body []string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			body = append(body, line)
		}
	}
	return lines[0], strings.Join(body, "\n")
}
