package packdecl

import (
	"fmt"
	"regexp"
)

// herdrAgentName is the shape of a herdr agent name: one lower-case word, the spelling herdr's
// own detector lists (`omp`, `claude`, `copilot`).
var herdrAgentName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// herdrAgentProblems validates Contribution.HerdrAgent, the name herdr knows a program by. It is
// refused off a `program` for `update`'s reason (the launcher's herdr registration is its one
// reader, so anywhere else it would silently do nothing), and an empty or malformed value is
// refused by name, because the launcher would hand it to `herdr pane report-agent --agent` and
// herdr would reject the report on every launch.
func herdrAgentProblems(label string, c Contribution) []string {
	if c.HerdrAgent == nil {
		return nil
	}
	field := label + ".herdr_agent"
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf(
			"%s: kind %q does not take \"herdr_agent\" — it names a PROGRAM to herdr, so only "+
				"\"program\" has one; remove the key", field, c.Kind)}
	}
	name := *c.HerdrAgent
	if name == "" {
		return []string{field + ": an empty name tells herdr nothing — omit the key to report the " +
			"program's own name, or set the name herdr recognizes (e.g. herdr_agent = \"omp\")"}
	}
	if !herdrAgentName.MatchString(name) {
		return []string{fmt.Sprintf("%s: %q is not a herdr agent name — use one lower-case word of "+
			"letters, digits, \".\", \"_\" or \"-\" (e.g. \"omp\"), or omit the key to report the "+
			"program's own name", field, name)}
	}
	return nil
}
