package packdecl

import (
	"fmt"
	"slices"
	"strings"
)

// guard.go is the `guard` field of a blocked-tool contribution (Contribution.Guard): its
// closed vocabulary and its validation.

// GuardWorkspaceState is the one path set a guard protects today: the workspace's `.yolo`
// and everything under it except the durable dir (`.yolo/durable`, $YOLO_DURABLE_DIR), plus
// every directory that contains `.yolo`, the workspace itself included, since removing or
// moving a parent takes `.yolo` with it. The workspace is the launch's own, on every setup:
// `/workspace` in a container jail, the real path under macos-user, and the directory
// `yolo host --` was started in.
//
// For `rm`, every operand is judged by that rule. For `mv`, every source is; the destination
// is refused only when the move would replace `.yolo` or a parent of it, or would overwrite
// something that already exists inside `.yolo` outside the durable dir. A move that only adds
// a new name inside `.yolo` passes: it destroys nothing the launch wrote.
const GuardWorkspaceState = "workspace-state"

// KnownGuards is the closed list of path sets a guard may name.
var KnownGuards = []string{GuardWorkspaceState}

// GuardBins are the tools a guard can stand in front of: the shim parses each one's own
// options (which take a value, where `--` ends them, which operand is the destination), so a
// tool whose grammar it does not know cannot be guarded.
var GuardBins = []string{"mv", "rm"}

// guardProblems checks a blocked-tool contribution that sets `guard`.
func guardProblems(label string, c Contribution) []string {
	if c.Guard == "" {
		return nil
	}
	var problems []string
	if !slices.Contains(KnownGuards, c.Guard) {
		problems = append(problems, fmt.Sprintf("%s.guard %q is not a path set yolo knows: use one of %s",
			label, c.Guard, strings.Join(KnownGuards, ", ")))
	}
	if c.Bin != "" && !slices.Contains(GuardBins, c.Bin) {
		problems = append(problems, fmt.Sprintf("%s.guard: a guard reads its tool's own arguments, "+
			"and it knows those of %s only, not %q", label, strings.Join(GuardBins, " and "), c.Bin))
	}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"flags", len(c.Flags) > 0}, {"allow_flags", len(c.AllowFlags) > 0},
		{"replacement", c.Replacement != ""},
	} {
		if f.set {
			problems = append(problems, fmt.Sprintf("%s: a blocked tool with \"guard\" does not take %q — "+
				"a guard refuses by the paths an argument names, not by flags, and points at no other tool",
				label, f.name))
		}
	}
	return problems
}
