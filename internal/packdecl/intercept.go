package packdecl

import (
	"fmt"
	"strings"
)

// intercept.go is the `intercept` kind's own validation (kinds.go, KindIntercept).

// interceptProblems checks an intercept's `forward` and refuses the refusal-shaped fields
// `blocked-tool` carries, which on an intercept no shim would print.
func interceptProblems(label string, c Contribution) []string {
	var problems []string
	if len(c.Forward) == 0 {
		problems = append(problems, fmt.Sprintf(
			"%s: kind \"intercept\" needs \"forward\", the argv the shim execs with the caller's "+
				"arguments appended (e.g. [\"yolo\", \"gh\", \"--\"])", label))
		return problems
	}
	first := c.Forward[0]
	switch {
	case !ValidBinName(first):
		problems = append(problems, fmt.Sprintf(
			"%s.forward[0] %q must be a bare program name, resolved on the jail's PATH", label, first))
	case first == c.Bin:
		problems = append(problems, fmt.Sprintf(
			"%s.forward[0] is %q, the name being intercepted: the shim is first on PATH, so it "+
				"would exec itself", label, first))
	}
	for i, w := range c.Forward {
		if w == "" || strings.ContainsAny(w, "\x00\r\n") {
			problems = append(problems, fmt.Sprintf(
				"%s.forward[%d]: an empty word or a control character — each word reaches the "+
					"forwarder as one argument", label, i))
		}
	}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"message", c.Message != ""}, {"suggestion", c.Suggestion != ""},
		{"replacement", c.Replacement != ""}, {"flags", len(c.Flags) > 0},
		{"allow_flags", len(c.AllowFlags) > 0},
	} {
		if f.set {
			problems = append(problems, fmt.Sprintf(
				"%s: kind \"intercept\" does not take %q — an intercept forwards every call; "+
					"a refusal is a \"blocked-tool\"", label, f.name))
		}
	}
	return problems
}
