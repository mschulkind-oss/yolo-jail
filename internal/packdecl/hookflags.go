package packdecl

import (
	"fmt"
	"strings"
)

// hookFlagsProblems validates a hook's `flags`, the arguments boot_check passes its script
// (docs/reference/mcp-configuration.md, OQ-MP9). Any other hook is refused one, for
// probeArgsProblems' reason: nothing would read it. An empty word or one holding a NUL is
// refused because it cannot be an argument an author meant.
func hookFlagsProblems(label string, c Contribution) []string {
	if c.Flags == nil {
		return nil
	}
	field := label + ".flags"
	if c.Hook != HookBootCheck {
		return []string{fmt.Sprintf("%s: hook %q does not take \"flags\" — only %q runs "+
			"anything to pass them to; remove the key", field, c.Hook, HookBootCheck)}
	}
	var problems []string
	for i, w := range c.Flags {
		if w == "" || strings.ContainsRune(w, 0) {
			problems = append(problems, fmt.Sprintf("%s[%d]: %q is not an argument — write "+
				"each word the check script takes as one entry (e.g. \"--boot-check\")", field, i, w))
		}
	}
	return problems
}
