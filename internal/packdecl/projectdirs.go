package packdecl

// projectdirs.go is the `project_dirs` field of a `skills` destination: where the agent that
// reads the destination ALSO reads skills at project scope (docs/reference/agent-briefings.md, the workspace layer).
// The field's semantics are in Contribution.ProjectDirs; this file holds its validation and the
// one accessor every reader goes through.

import (
	"fmt"
	"path"
	"strings"
)

// ProjectSkillDirs is every project-scope skills directory this manifest's `skills` destinations
// declare, in declaration order, each spelled once.
//
// ONE accessor, for the reason SkillsSource is one: the launcher reads it twice (the source set
// and the skip rule), and two hand-rolled loops over Contributions() are how one of them comes to
// stop honoring a field the other still reads.
func (m *Manifest) ProjectSkillDirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range m.Contributions() {
		if c.Kind != KindSkills {
			continue
		}
		for _, d := range c.ProjectDirs {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// projectDirsProblems validates one contribution's `project_dirs`.
//
// Every entry names a directory INSIDE the workspace, relative to its root, and is refused
// otherwise: the field decides which workspace bytes the launcher copies, host-side, into every
// agent's home, so a spelling that could reach outside the tree is the one mistake this field
// cannot be allowed to make (P5 of the design). The launcher's reader refuses an escape at run
// time whatever this says — a symlink inside the workspace can escape where no spelling does —
// so this is the AUTHORING half: a pack that declares `../x` is told so by `yolo pack lint`
// rather than having a launch quietly mirror nothing.
func projectDirsProblems(label string, c Contribution) []string {
	if len(c.ProjectDirs) == 0 {
		return nil
	}
	var problems []string
	if c.Kind != KindSkills {
		return append(problems, fmt.Sprintf(
			"%s: kind %q does not take \"project_dirs\" — it names where an agent reads SKILLS "+
				"inside a workspace, so only a \"skills\" destination has an agent to read them",
			label, c.Kind))
	}
	if c.Agent == "" {
		return append(problems, fmt.Sprintf(
			"%s: \"project_dirs\" belongs on a skills DESTINATION (an entry with \"agent\" and "+
				"\"into\") — it states where THAT agent reads skills in a workspace, which content "+
				"a pack ships has no agent to be true of", label))
	}
	seen := map[string]bool{}
	for i, d := range c.ProjectDirs {
		field := fmt.Sprintf("%s.project_dirs[%d]", label, i)
		switch {
		case d == "":
			problems = append(problems, field+": empty path")
			continue
		case strings.HasPrefix(d, "/"):
			problems = append(problems, field+": must be relative to the workspace root, not absolute ("+d+")")
			continue
		case strings.Contains(d, `\`):
			problems = append(problems, field+": must use \"/\" separators ("+d+")")
			continue
		}
		clean := path.Clean(d)
		if clean == "." {
			problems = append(problems, field+": names the workspace root itself, which is not a "+
				"skills directory ("+d+")")
			continue
		}
		if clean == ".." || strings.HasPrefix(clean, "../") {
			problems = append(problems, field+": must stay inside the workspace, not climb out of it ("+d+")")
			continue
		}
		if clean != d {
			problems = append(problems, fmt.Sprintf("%s: write it as %q — the launcher compares "+
				"these spellings across packs, so one directory must have one spelling (%s)", field, clean, d))
			continue
		}
		if first := strings.SplitN(clean, "/", 2)[0]; first == ".git" || first == ".yolo" {
			problems = append(problems, fmt.Sprintf("%s: %q is %s, never skill content (%s)", field,
				first, map[string]string{".git": "git's metadata", ".yolo": "yolo's own state directory"}[first], d))
			continue
		}
		if seen[clean] {
			problems = append(problems, field+": declared twice ("+d+")")
			continue
		}
		seen[clean] = true
	}
	return problems
}
