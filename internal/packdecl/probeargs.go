package packdecl

import (
	"fmt"
	"strings"
)

// probeArgsProblems validates Contribution.ProbeArgs, a program's VERSION PROBE arguments
// (docs/design/pi-extension-store-builds.md XB-D24, XB-D51). It is refused off a `program` for
// `update`'s reason: the generated launcher is its one reader, so anywhere else it would be a
// declaration that silently does nothing.
//
// A present-but-empty list is refused for platforms' reason (it declares a probe nothing can
// match, where omitting the key is the stated way to have none), an empty or blank word because
// the launcher compares it with the first argument and no invocation's first argument is one,
// and a duplicate because it can only be a typo for a second word.
func probeArgsProblems(label string, c Contribution) []string {
	if c.ProbeArgs == nil {
		return nil
	}
	field := label + ".probe_args"
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf(
			"%s: kind %q does not take \"probe_args\" — a version probe is an invocation of the "+
				"installed PROGRAM through its launcher, so only \"program\" has one", field, c.Kind)}
	}
	if len(c.ProbeArgs) == 0 {
		return []string{field + ": an empty list makes nothing a version probe — omit the key, " +
			"or name the first arguments the program answers at once (e.g. [\"--version\"])"}
	}
	var problems []string
	seen := map[string]bool{}
	for i, w := range c.ProbeArgs {
		entry := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case strings.TrimSpace(w) == "" || strings.TrimSpace(w) != w:
			problems = append(problems, fmt.Sprintf("%s: %q is not a word — the launcher compares "+
				"each entry with the invocation's first argument exactly, so it must be the "+
				"argument as typed (e.g. \"--version\")", entry, w))
		case seen[w]:
			problems = append(problems, fmt.Sprintf("%s: %q is listed twice", entry, w))
		}
		seen[w] = true
	}
	return problems
}

// tempCachesProblems validates Contribution.TempCaches, the directories a program keeps compiled
// code in under its temporary directory (XB-D52). It is refused off a `program` for `update`'s
// reason.
//
// Each entry must be ONE bare directory name: the launcher links `<tmpdir>/<name>` to
// `~/.local/state/yolo/compile-cache/tmp/<name>`, so a path with structure would put the link
// somewhere the program never looks and the target outside the cache. A present-but-empty list
// and a duplicate are refused for probeArgsProblems' reasons.
func tempCachesProblems(label string, c Contribution) []string {
	if c.TempCaches == nil {
		return nil
	}
	field := label + ".temp_caches"
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf(
			"%s: kind %q does not take \"temp_caches\" — the launcher of the installed PROGRAM "+
				"keeps them, so only \"program\" has one", field, c.Kind)}
	}
	if len(c.TempCaches) == 0 {
		return []string{field + ": an empty list keeps nothing — omit the key, or name the " +
			"directories the program caches compiled code in under its temporary directory " +
			"(e.g. [\"jiti\"])"}
	}
	var problems []string
	seen := map[string]bool{}
	for i, name := range c.TempCaches {
		entry := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") ||
			strings.TrimSpace(name) != name:
			problems = append(problems, fmt.Sprintf("%s: %q must be one bare directory name "+
				"(e.g. \"jiti\") — the launcher links <tmpdir>/<name> to a per-workspace cache, "+
				"so a path would link somewhere the program never looks", entry, name))
		case seen[name]:
			problems = append(problems, fmt.Sprintf("%s: %q is listed twice", entry, name))
		}
		seen[name] = true
	}
	return problems
}
