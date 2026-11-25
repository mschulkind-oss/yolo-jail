package cli

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
)

// writeConfigEditPreexisting warns of the problems the config already had, which the edit left as
// they are and did not refuse. One problem is one line. Several are one header and then each
// distinct explanation and fix once, with the model rows it applies to listed under it, so an
// unrelated one-key edit over a config with many alike problems is not a wall of repeated text.
func writeConfigEditPreexisting(errw io.Writer, result configedit.TargetedResult) {
	problems := result.Preexisting
	switch len(problems) {
	case 0:
		return
	case 1:
		fmt.Fprintf(errw, "warning: already in your config, and left as it is by this edit: %s\n", problems[0])
		return
	}
	fmt.Fprintf(errw, "warning: your config already has %d problems that this edit left as they are:\n", len(problems))
	for _, group := range groupPreexistingProblems(problems) {
		if len(group.places) == 1 {
			fmt.Fprintf(errw, "  - %s\n", group.original[0])
			continue
		}
		if group.places == nil {
			for _, problem := range group.original {
				fmt.Fprintf(errw, "  - %s\n", problem)
			}
			continue
		}
		fmt.Fprintf(errw, "  - %s\n", group.body)
		for _, place := range group.places {
			fmt.Fprintf(errw, "      at %s\n", place)
		}
	}
}

// modelGapProblemShape is the shape of a model-row problem from config validation
// (config.modelGapProblem): an optional "<file:line:col>: " location, then
// `provider <name> model "<id>": `, then the problem and its fix, which are the same text for every
// row with the same kind of problem. Only a message of exactly this shape is grouped.
var modelGapProblemShape = regexp.MustCompile(`^(?:(\S+): )?(provider \S+ model "(?:[^"\\]|\\.)*"): (.+)$`)

// preexistingGroup is the problems that share one body. places is nil for a problem of no
// recognized shape, which is never merged with another.
type preexistingGroup struct {
	body     string
	places   []string
	original []string
}

// groupPreexistingProblems groups problems by the text left after a model row's location is
// removed, in order of first appearance. A problem of any other shape is its own group, untouched.
func groupPreexistingProblems(problems []string) []preexistingGroup {
	var groups []preexistingGroup
	index := map[string]int{}
	for _, problem := range problems {
		m := modelGapProblemShape.FindStringSubmatch(problem)
		if m == nil || strings.TrimSpace(m[3]) == "" {
			groups = append(groups, preexistingGroup{original: []string{problem}})
			continue
		}
		place := m[2]
		if m[1] != "" {
			place = m[1] + ": " + m[2]
		}
		at, seen := index[m[3]]
		if !seen {
			at = len(groups)
			index[m[3]] = at
			groups = append(groups, preexistingGroup{body: m[3], places: []string{}})
		}
		groups[at].places = append(groups[at].places, place)
		groups[at].original = append(groups[at].original, problem)
	}
	return groups
}
