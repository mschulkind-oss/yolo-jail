package checkdiag

import "strings"

// Problem is a (title, detail) pair parsed from a module self-check's output.
type Problem struct {
	Title  string
	Detail string
}

// SplitSelfCheckProblems splits module self-check output into (title, detail)
// pairs. Self-checks print one or more "FAIL: …" entries, each optionally
// followed by continuation lines. Splits on "FAIL:" boundaries: the first line
// of each chunk (after the "FAIL:" prefix, stripped) is the title, the
// remaining non-blank lines joined by "\n" are the detail. Non-FAIL preamble is
// dropped.
func SplitSelfCheckProblems(output string) []Problem {
	var problems []Problem
	var current []string
	have := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, " \t\r\f\v")
		if strings.HasPrefix(line, "FAIL:") {
			if have {
				problems = append(problems, finalizeProblem(current))
			}
			current = []string{strings.TrimSpace(line[len("FAIL:"):])}
			have = true
		} else if have {
			current = append(current, line)
		}
	}
	if have {
		problems = append(problems, finalizeProblem(current))
	}
	return problems
}

func finalizeProblem(lines []string) Problem {
	title := lines[0]
	var detail []string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			detail = append(detail, line)
		}
	}
	return Problem{Title: title, Detail: strings.Join(detail, "\n")}
}
