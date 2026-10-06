package packdecl

// needsmodellist.go is the `needs_model_list` field (Contribution.NeedsModelList carries the
// reasoning; docs/design/model-lists-and-pickers.md OQ-MM6 the ruling): the platforms on which a
// program has no model catalog and no default model of its own.

import (
	"fmt"
	"slices"
)

// NeedsModelList reports whether the program installing bin declares that it needs a model list
// on platform. False when the manifest installs no such program or platform is "".
func (m *Manifest) NeedsModelList(bin, platform string) bool {
	if platform == "" {
		return false
	}
	for _, c := range m.Contributions() {
		if c.Kind == KindProgram && c.Bin == bin {
			return slices.Contains(c.NeedsModelList, platform)
		}
	}
	return false
}

// needsModelListProblems refuses a `needs_model_list` no consumer could read as written: on a kind
// other than program, empty, or naming a platform twice or as "".
func needsModelListProblems(label string, c Contribution) []string {
	if c.NeedsModelList == nil {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"needs_model_list\" — it says what a "+
			"PROGRAM can start on, so only \"program\" has an answer", label, c.Kind)}
	}
	if len(c.NeedsModelList) == 0 {
		return []string{fmt.Sprintf("%s: \"needs_model_list\" is an empty list, which names no "+
			"platform — omit it instead", label)}
	}
	var problems []string
	seen := map[string]bool{}
	for _, p := range c.NeedsModelList {
		switch {
		case p == "":
			problems = append(problems, fmt.Sprintf("%s: \"needs_model_list\" holds an empty platform", label))
		case seen[p]:
			problems = append(problems, fmt.Sprintf("%s: \"needs_model_list\" names %q twice", label, p))
		}
		seen[p] = true
	}
	return problems
}
