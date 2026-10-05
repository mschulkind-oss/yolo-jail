// Package ioprio is the one reading of `resources.io` and the one grading of the disk a
// path lands on, shared by every reader: config validation, the launcher and its briefing,
// the macos-user orchestrator, `yolo check`, and the two that apply it: yolo-entrypoint on
// Linux (ioprio_set) and the macos-user launcher on macOS (setiopolicy_np)
// (docs/design/io-priority.md).
//
// ONE PACKAGE, BECAUSE THE SHORTHAND HAS FIVE READERS. `"io": "low"` means
// `{"priority": "low"}` and nothing else (IO-D3), and a second hand-written reading of it —
// a launcher that took `{}` for a declaration, or a check that took `"normal"` for one —
// would put a disclosure line on a launch that declared nothing, or leave one off a launch
// that did. The same goes for the grading: the launch and `yolo check` must agree on which
// disk ignores the value, so both call Grade (IO-D5).
//
// It imports nothing of yolo's but jsonx, so config, the run pipeline, the check and the
// entrypoint can all depend on it without a cycle.
package ioprio

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Priority is a declared `resources.io.priority`: one of three words, never a raw class or
// level. The kernel accepts levels past 7 and reads them back as hint bits, so the enum is
// also what keeps a level the scheduler would misread from ever reaching ioprio_set.
type Priority string

const (
	// Normal makes no call: the jail keeps whatever class its launcher's process tree holds.
	// It is also what an unset key, `null` and `{}` mean.
	Normal Priority = "normal"
	// Low is class BE (best effort), level 7 on Linux, and IOPOL_UTILITY on macos-user.
	Low Priority = "low"
	// Idle is class IDLE on Linux, and IOPOL_THROTTLE on macos-user.
	Idle Priority = "idle"
)

// EnvVar carries the launcher's decision into the container environment. The entrypoint
// reads it and never the config, so an attach applies the value the jail was launched
// with (IO-D2). Absent means the launcher passed nothing.
const EnvVar = "YOLO_IO_PRIORITY"

// ReexecMarkerEnv marks the entrypoint's second image. The first image sets it on the
// re-exec and the second removes it before anything else reads the environment, so no
// child ever sees it and the entrypoint re-executes at most once (IO-D1).
const ReexecMarkerEnv = "YOLO_IO_PRIORITY_REEXEC"

// Declared reports whether p asks for anything. Only a declared priority is delivered,
// disclosed or graded: "normal" declares nothing to deliver.
func (p Priority) Declared() bool { return p == Low || p == Idle }

// valid reports whether s is one of the three words, spelled exactly.
func valid(s string) bool {
	switch Priority(s) {
	case Normal, Low, Idle:
		return true
	}
	return false
}

// expectedWords is how every refusal names the legal values.
const expectedWords = `"idle", "low" or "normal"`

// knownObjectKeys is the object form's closed key set. A `weight` key exists only if
// OQ-IO7 ships a cgroup half; until then it is an unknown key like any other.
var knownObjectKeys = map[string]bool{"priority": true}

// Parse reads one `resources.io` value and returns the priority it declares, plus every
// problem with it, each prefixed with path (the validator passes "config.resources.io").
// A value with problems yields Normal: nothing is applied from a value the validator
// refuses.
//
//   - nil (JSON null, or the key absent) is unset, which is Normal.
//   - a string is the shorthand for the priority alone.
//   - an object admits `priority` only; `{}` and `{"priority": null}` are unset.
func Parse(v any, path string) (Priority, []string) {
	switch t := v.(type) {
	case nil:
		return Normal, nil
	case string:
		if !valid(t) {
			return Normal, []string{fmt.Sprintf("%s: expected %s (got %q)", path, expectedWords, t)}
		}
		return Priority(t), nil
	case *jsonx.OrderedMap:
		var problems []string
		keys := append([]string(nil), t.Keys()...)
		sort.Strings(keys)
		for _, k := range keys {
			if !knownObjectKeys[k] {
				problems = append(problems, path+"."+k+": unknown key")
			}
		}
		p := Normal
		switch pv, _ := t.Get("priority"); pt := pv.(type) {
		case nil:
		case string:
			if valid(pt) {
				p = Priority(pt)
			} else {
				problems = append(problems, fmt.Sprintf("%s.priority: expected %s (got %q)", path, expectedWords, pt))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s.priority: expected a string, one of %s", path, expectedWords))
		}
		if len(problems) > 0 {
			return Normal, problems
		}
		return p, nil
	}
	return Normal, []string{path + `: expected a string (` + expectedWords + `) or an object {"priority": ...}`}
}

// FromResources is the priority a `resources` block declares, for every reader past
// validation. A block that fails validation never reaches a launch, so a problem here reads
// as Normal rather than as an error nobody could act on.
func FromResources(res *jsonx.OrderedMap) Priority {
	if res == nil {
		return Normal
	}
	v, _ := res.Get("io")
	p, problems := Parse(v, "resources.io")
	if len(problems) > 0 {
		return Normal
	}
	return p
}

// The Linux encoding: class << 13 | level (include/uapi/linux/ioprio.h).
const (
	classShift = 13
	classBE    = 2
	classIdle  = 3
	levelMask  = 0x7
)

// KernelValue is p's ioprio_set value on Linux, and false for Normal, which makes no call.
func (p Priority) KernelValue() (int, bool) {
	switch p {
	case Low:
		return classBE<<classShift | 7, true
	case Idle:
		return classIdle << classShift, true
	}
	return 0, false
}

// Describe renders a raw ioprio value the way `ionice` would name it: "be/7", "idle",
// "none/0". Unset reads "none/0" even for a thread BFQ serves as BE from its nice value,
// because ioprio_get returns the raw field.
func Describe(v int) string {
	class, level := v>>classShift, v&levelMask
	switch class {
	case 0:
		return fmt.Sprintf("none/%d", level)
	case 1:
		return fmt.Sprintf("rt/%d", level)
	case classBE:
		return fmt.Sprintf("be/%d", level)
	case classIdle:
		return "idle"
	}
	return fmt.Sprintf("class%d/%d", class, level)
}

// ClassName is the plain-words name the briefing and the boot log use for p.
func (p Priority) ClassName() string {
	switch p {
	case Low:
		return "best effort, level 7"
	case Idle:
		return "idle class"
	}
	return "unset"
}

// The macOS disk I/O policy, from xnu's <sys/resource.h>: setiopolicy_np(IOPOL_TYPE_DISK,
// IOPOL_SCOPE_PROCESS, <policy>) is what the macos-user launcher calls (io-priority.md §5.5,
// IO-D7). The numbers are the header's, not a mapping of ours, so they are spelled here once
// and both the darwin call and the Linux-tested mapping read them.
const (
	IopolTypeDisk     = 0 // IOPOL_TYPE_DISK
	IopolScopeProcess = 0 // IOPOL_SCOPE_PROCESS
	IopolDefault      = 0 // IOPOL_DEFAULT
	IopolImportant    = 1 // IOPOL_IMPORTANT (IOPOL_NORMAL is its older name)
	IopolPassive      = 2 // IOPOL_PASSIVE
	IopolThrottle     = 3 // IOPOL_THROTTLE
	IopolUtility      = 4 // IOPOL_UTILITY
	IopolStandard     = 5 // IOPOL_STANDARD
)

// DarwinPolicy is p's macOS disk policy, and false for Normal, which makes no call: "low" is
// IOPOL_UTILITY ("throttled to prevent a significant impact on the latency of IMPORTANT and
// STANDARD I/Os", getiopolicy_np(3)) and "idle" is IOPOL_THROTTLE ("for long-running I/O
// intensive background work"). Pure, so the mapping is tested on every platform; the call
// itself is darwin's (diskpolicy_darwin.go).
func (p Priority) DarwinPolicy() (int, bool) {
	switch p {
	case Low:
		return IopolUtility, true
	case Idle:
		return IopolThrottle, true
	}
	return 0, false
}

// DarwinPolicyName names a raw macOS disk policy the way <sys/resource.h> does, for the
// launch's warning and the briefing: "IOPOL_UTILITY", or "policy 9" for a number the header
// does not define.
func DarwinPolicyName(v int) string {
	switch v {
	case IopolDefault:
		return "IOPOL_DEFAULT"
	case IopolImportant:
		return "IOPOL_IMPORTANT"
	case IopolPassive:
		return "IOPOL_PASSIVE"
	case IopolThrottle:
		return "IOPOL_THROTTLE"
	case IopolUtility:
		return "IOPOL_UTILITY"
	case IopolStandard:
		return "IOPOL_STANDARD"
	}
	return fmt.Sprintf("policy %d", v)
}
