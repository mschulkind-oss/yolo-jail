package config

import (
	"errors"
	"regexp"
	"strings"
)

// An edit is refused only for a problem it introduces. A config that already fails validation
// (a model the launch accepts with no price, say) would otherwise refuse every config verb, however
// unrelated its edit. So a caller validates the config before the edit too (EditProblems), and
// validates the planned config tolerating those problems: a problem in both is the result's
// Preexisting, a warning; one only the planned config has still refuses the edit.

// EditProblemsError is the refusal for a planned config the validators found problems in, one
// message per problem.
type EditProblemsError struct {
	Problems []string
}

func (e *EditProblemsError) Error() string {
	return "validate planned effective config: " + strings.Join(e.Problems, "; ")
}

// EditProblems is the problems err refused a config for, nil when err is not a validation
// finding (a parse error, or an unreadable pack) and so names nothing an edit could leave as it is.
func EditProblems(err error) []string {
	var problems *EditProblemsError
	if !errors.As(err, &problems) {
		return nil
	}
	return append([]string(nil), problems.Problems...)
}

// ValidateExplicitEditTolerating is ValidateExplicitEdit, except that a problem among preexisting
// (the problems the config had before this edit, EditProblems) is reported in the result's
// Preexisting rather than refusing it.
func ValidateExplicitEditTolerating(workspace string, target ExplicitEditTarget, input ConfigReadInput, preexisting []string) (ValidatedExplicitEditResult, error) {
	ctx, err := readExplicitEditContext(workspace, target, input)
	if err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	return validateEditContext(ctx, preexisting)
}

// Preexisting is each problem the config already had before the edit and still has after it,
// which did not refuse the edit. A caller prints each one as a warning.
func (r ValidatedExplicitEditResult) Preexisting() []string {
	return append([]string(nil), r.preexisting...)
}

// problemPosition is a location's line and column, which an edit to the same file may move
// without changing the problem.
var problemPosition = regexp.MustCompile(`:\d+(:\d+)?`)

// problemKey is the identity of a problem across an edit: its message without line and column.
func problemKey(problem string) string {
	return problemPosition.ReplaceAllString(problem, "")
}

// ResolvedProblems is each of before's problems that after no longer has: what an edit fixed
// rather than left as it was.
func ResolvedProblems(before, after []string) []string {
	remaining := make(map[string]bool, len(after))
	for _, problem := range after {
		remaining[problemKey(problem)] = true
	}
	var resolved []string
	for _, problem := range before {
		if !remaining[problemKey(problem)] {
			resolved = append(resolved, problem)
		}
	}
	return resolved
}

// splitPreexistingProblems is problems split into those not among preexisting (which refuse) and
// those that are.
func splitPreexistingProblems(problems, preexisting []string) (refusing, tolerated []string) {
	if len(preexisting) == 0 {
		return problems, nil
	}
	known := make(map[string]bool, len(preexisting))
	for _, problem := range preexisting {
		known[problemKey(problem)] = true
	}
	for _, problem := range problems {
		if known[problemKey(problem)] {
			tolerated = append(tolerated, problem)
		} else {
			refusing = append(refusing, problem)
		}
	}
	return refusing, tolerated
}
