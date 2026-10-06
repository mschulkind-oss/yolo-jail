package packdecl

// agentfiles.go is the `agent_files` field on a program (Contribution.AgentFiles;
// docs/design/model-lists-and-pickers.md MM-D33 the decision).
//
// An AGENT FILE is a term coined in MM-D33: a file the jail's per-agent env writer puts beside an
// agent's per-agent env file (~/.config/yolo-agent-env/<bin>.sh), owner-only, rewritten on every
// entry and removed with it, whose CONTENT the agent's env derive composes under a variable this
// field names, and whose PATH the agent then receives under that same variable. It exists for a
// program that reads a whole document from a file it is pointed at and cannot read the
// document's secret from its environment: copilot's providers.json (COPILOT_PROVIDERS_CONFIG)
// carries its provider's key as literal text, and a surface derive never receives a key, so the
// one composition that holds the key (the env derive) composes the file and the one vehicle that
// already holds that key at rest (the per-agent env file's directory) keeps it.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// agentFileNameRe is an agent file's name: one plain file name, so `<bin>.<name>` stays inside
// the per-agent env directory and never collides with an env file, which ends in `.sh`.
var agentFileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// agentFilesProblems refuses an `agent_files` no consumer could read as written: on a kind other
// than program, a variable that is not an environment variable name, and a file name that is not
// one plain name, names a parent, or ends in `.sh` (an env file's suffix, which the in-jail
// readers take for one).
func agentFilesProblems(label string, c Contribution) []string {
	if c.AgentFiles == nil {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"agent_files\" — it names files a "+
			"PROGRAM's env derive composes for that program, so only \"program\" has a process "+
			"to hand their paths to", label, c.Kind)}
	}
	if len(c.AgentFiles) == 0 {
		return []string{fmt.Sprintf("%s: \"agent_files\" is an empty object, which names no file — "+
			"omit it instead", label)}
	}
	vars := make([]string, 0, len(c.AgentFiles))
	for v := range c.AgentFiles {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	var problems []string
	names := map[string]string{}
	for _, v := range vars {
		name := c.AgentFiles[v]
		if !ValidEnvName(v) {
			problems = append(problems, fmt.Sprintf("%s: \"agent_files\" key %q is not an environment "+
				"variable name — it is the variable the program reads the file's path from", label, v))
		}
		switch {
		case !agentFileNameRe.MatchString(name) || strings.Contains(name, ".."):
			problems = append(problems, fmt.Sprintf("%s: \"agent_files.%s\" is %q — want one plain "+
				"file name (letters, digits, '.', '_' and '-'), written beside the agent's env file as "+
				"<bin>.<name>", label, v, name))
		case strings.HasSuffix(name, ".sh"):
			problems = append(problems, fmt.Sprintf("%s: \"agent_files.%s\" is %q — a name ending in "+
				"\".sh\" is an agent's env file, which the jail sources", label, v, name))
		}
		if prior, dup := names[name]; dup {
			problems = append(problems, fmt.Sprintf("%s: \"agent_files\" names the file %q for both %s "+
				"and %s — one file holds one document", label, name, prior, v))
		}
		names[name] = v
	}
	return problems
}

// AgentFiles is the `agent_files` of the program installing bin: variable → file name, nil when
// the manifest installs no such program or the program declares none.
func (m *Manifest) AgentFiles(bin string) map[string]string {
	for _, c := range m.Contributions() {
		if c.Kind == KindProgram && c.Bin == bin {
			return c.AgentFiles
		}
	}
	return nil
}
