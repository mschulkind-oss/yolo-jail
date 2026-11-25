package packdecl

// persetupstate.go is the `per_setup_state` declaration (docs/design/unified-workspace-home.md
// §6.3, UH-D7): a pack's statement of the paths in one of its workspace state directories that a
// setup keeps for itself instead of sharing with the workspace's other setups.
//
// # What core does with it
//
// By default every child of a workspace-scope `state` directory is SHARED: the unified workspace
// home holds the one copy and each setup's own home links to it (internal/unifiedhome), so a
// session started under one setup is found under another. The paths listed here are the
// exceptions, and each one stays a real file or directory in the setup's own home: an install a
// setup builds for its own platform (`agent/npm`: a Linux and a macOS node_modules must not be one
// directory), and a database whose lock behavior across two kernels is unmeasured (copilot's
// `session-store.db`, omp's `*.db`, UH-D7). A pack with nothing to carve out declares nothing.
//
// # Why it is a TOP-LEVEL key
//
// An older yolo's tolerant decoder ignores an unknown top-level key but skips a whole
// contribution that holds an unknown field (DecodeTolerant, skew.go). Declared on the `state`
// contribution, it would make an older jail drop the pack's state directory; declared here, an
// older jail ignores it, shares and composes nothing, and keeps today's behavior. The same
// reason `session_homes` is top-level (sessionhome.go, SP-D13).
//
//	per_setup_state = [{ dir = ".pi", paths = ["agent/npm", "agent/git"] }]

import (
	"fmt"
	"strings"
)

// PerSetupState is one carve-out declaration.
type PerSetupState struct {
	// Dir is a state directory of this pack, home-relative ("~/" is not written), spelled
	// exactly as its `state` contribution's `at`. Required; declared once.
	Dir string `json:"dir"`
	// Paths are the names beneath Dir that stay in each setup's own home. Each is exact and
	// Dir-relative, with "/" between segments: no "..", no absolute path, no glob, no lock name,
	// no empty segment and no trailing "/". Required, non-empty.
	Paths []string `json:"paths"`
}

// PerSetupState returns the pack's carve-out declarations in declaration order, copied so a
// consumer cannot reach back into the manifest. THE accessor: no reader touches the field.
func (m *Manifest) PerSetupState() []PerSetupState {
	if len(m.PerSetupStateDecls) == 0 {
		return nil
	}
	out := make([]PerSetupState, len(m.PerSetupStateDecls))
	for i, d := range m.PerSetupStateDecls {
		out[i] = PerSetupState{Dir: d.Dir, Paths: append([]string(nil), d.Paths...)}
	}
	return out
}

// PerSetupStatePathProblem is "" when p is a valid carve-out path, else why not.
func PerSetupStatePathProblem(p string) string {
	switch {
	case p == "":
		return "must name a path in the state directory"
	case strings.HasPrefix(p, "/"):
		return "must be relative to the state directory, not absolute"
	case strings.HasSuffix(p, "/"):
		return "must not end in \"/\": a directory is named without it"
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return "must not contain an empty, \".\" or \"..\" segment"
		case strings.ContainsAny(seg, "*?[]\\"):
			return "must be an exact name: a glob is not read"
		case strings.HasSuffix(seg, ".lock"):
			return "is a lock name: a lock is a process's, never a setup's state to carve out"
		}
	}
	return ""
}

// validatePerSetupState reports every structural problem with `per_setup_state`, in the house
// `per_setup_state[i].field: …` style. Every rule is version-invariant, so it runs on the
// tolerant path too.
func (m *Manifest) validatePerSetupState() []string {
	var problems []string
	var stateDirs []string
	for _, c := range m.Contributions() {
		if c.Kind == KindState && c.At != "" {
			stateDirs = append(stateDirs, strings.TrimSuffix(c.At, "/"))
		}
	}
	seen := map[string]bool{}
	for i, d := range m.PerSetupStateDecls {
		label := fmt.Sprintf("per_setup_state[%d]", i)
		dir := strings.TrimSuffix(d.Dir, "/")
		switch {
		case dir == "":
			problems = append(problems, label+".dir: is required — the state directory the paths are in, home-relative")
		default:
			before := len(problems)
			problems = appendPathProblems(problems, label+".dir", dir)
			if len(problems) == before {
				switch {
				case !containsString(stateDirs, dir):
					problems = append(problems, fmt.Sprintf("%s.dir: %q is not one of this pack's state dirs %v — "+
						"declare the `state` contribution that holds it", label, dir, stateDirs))
				case seen[dir]:
					problems = append(problems, fmt.Sprintf("%s.dir: %q is declared twice — list every path in one declaration", label, dir))
				}
				seen[dir] = true
			}
		}
		if len(d.Paths) == 0 {
			problems = append(problems, label+".paths: is required — name at least one path, or declare nothing")
		}
		for j, p := range d.Paths {
			if why := PerSetupStatePathProblem(p); why != "" {
				problems = append(problems, fmt.Sprintf("%s.paths[%d]: %q %s", label, j, p, why))
			}
		}
	}
	return problems
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
