package agentcfg

// hostcommands.go keeps a COMMAND THE JAIL CANNOT RUN out of the user's host layer: a value at
// one of the surface's declared command pointers (manifest.Surface.HostCommands) that names a
// path under the host's home which this jail cannot see.
//
// WHY. A host layer is the user's own file, composed into the jail's copy unchanged, and a
// command in it was written for the host: Claude Code's `hooks` carry
// `bash '/Users/<user>/.claude/hooks/<tool>.sh'` for whatever tool installed them. In a jail
// that path does not exist, so every such hook fails at every event and the agent reports the
// failure. The jail cannot fix the command, and running it can only fail, so the jail's copy
// leaves out the object holding it — the hook handler, the statusLine — and the caller says
// so. The user's file is never touched.
//
// THE MIRROR OF HC-D14 (entrypoint.JailPathsIn): `yolo host apply` refuses to write a
// jail-only path into the real home, and this keeps a host-only path out of the jail.
//
// WHAT COUNTS AS HOST-ONLY is deliberately narrow, because a false positive deletes a working
// hook: an absolute path, lying under the host's home, that the jail cannot stat. A path under
// /tmp, /usr or the jail's own home is never a candidate, and neither is one a `mounts` or
// `host_files` entry made reachable at the same spelling.

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
)

// HostCommandWildcard is the pointer token that matches every key of an object and every
// element of an array, so one pattern can name each hook handler of each event.
const HostCommandWildcard = "*"

// CommandDrop is one object the jail's copy left out: the pointer of the object removed, the
// command it held, and the host-only path that decided it. A REPORT, printed by the caller.
type CommandDrop struct {
	Pointer string
	Command string
	Path    string
}

// DropHostOnlyCommands is the host layer's bytes without each object holding a command that
// names a host-only path, and what it dropped, in pointer order. patterns are the surface's
// HostCommands pointers, "*" matching any key or index; hostHome is the host's home directory,
// as the launcher reported it; reachable says whether the jail can stat a path.
//
// The OBJECT holding the command goes, not the command alone: a hook handler without its
// `command` is a handler the agent cannot run either, and one without a `type` it may refuse.
// Every array or object that removal empties goes too, so an event whose only hook was
// host-only leaves no empty list behind. The document root never goes.
//
// It returns the input untouched when hostHome is empty (a launcher older than the variable),
// when nothing is dropped, and when the bytes do not decode as an object in codecName (the
// compose that follows reports that), so a surface with no host-only command reads exactly the
// bytes it always did.
func DropHostOnlyCommands(codecName string, hostBytes []byte, patterns []string, hostHome string,
	reachable func(string) bool) ([]byte, []CommandDrop) {
	hostHome = filepath.Clean(hostHome)
	if len(hostBytes) == 0 || len(patterns) == 0 || hostHome == "." || hostHome == "/" {
		return hostBytes, nil
	}
	c, ok := codec.LookupCodec(codecName)
	if !ok {
		return hostBytes, nil
	}
	decoded, err := c.Decode(hostBytes)
	if err != nil {
		return hostBytes, nil
	}
	if _, isObj := decoded.(map[string]any); !isObj {
		return hostBytes, nil
	}
	var drops []CommandDrop
	remove := map[string]bool{}
	for _, pattern := range patterns {
		steps, err := jsonptr.Parse(pattern)
		if err != nil || len(steps) < 2 {
			continue
		}
		for _, m := range matchPointer(decoded, steps, nil) {
			command, isString := m.value.(string)
			if !isString {
				continue
			}
			bad := hostOnlyPath(command, hostHome, reachable)
			if bad == "" {
				continue
			}
			holder := jsonptr.Format(m.steps[:len(m.steps)-1])
			if remove[holder] {
				continue
			}
			remove[holder] = true
			drops = append(drops, CommandDrop{Pointer: holder, Command: command, Path: bad})
		}
	}
	if len(drops) == 0 {
		return hostBytes, nil
	}
	kept, _ := removePointers(decoded, nil, remove)
	out, err := c.Encode(kept)
	if err != nil {
		return hostBytes, nil
	}
	sort.SliceStable(drops, func(i, j int) bool { return drops[i].Pointer < drops[j].Pointer })
	return out, drops
}

// pointerMatch is one concrete place a pattern matched: its tokens and the value there.
type pointerMatch struct {
	steps []string
	value any
}

// matchPointer is every concrete match of steps in v, "*" expanding to each key or index.
// Object keys are visited in sorted order so the drops come out the same on every boot.
func matchPointer(v any, steps, at []string) []pointerMatch {
	if len(steps) == 0 {
		return []pointerMatch{{steps: append([]string(nil), at...), value: v}}
	}
	step, rest := steps[0], steps[1:]
	var out []pointerMatch
	switch t := v.(type) {
	case map[string]any:
		if step != HostCommandWildcard {
			if child, ok := t[step]; ok {
				out = append(out, matchPointer(child, rest, with(at, step))...)
			}
			return out
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, matchPointer(t[k], rest, with(at, k))...)
		}
	case []any:
		for i, child := range t {
			idx := strconv.Itoa(i)
			if step != HostCommandWildcard && step != idx {
				continue
			}
			out = append(out, matchPointer(child, rest, with(at, idx))...)
		}
	}
	return out
}

// removePointers is v without every value whose pointer is in remove, and without every
// object or array those removals emptied; ok is false when v itself goes. A container that was
// already empty is kept: only an emptying this pass caused is pruned. v is never mutated.
func removePointers(v any, at []string, remove map[string]bool) (any, bool) {
	if len(at) > 0 && remove[jsonptr.Format(at)] {
		return nil, false
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			if kept, ok := removePointers(child, with(at, k), remove); ok {
				out[k] = kept
			}
		}
		if len(out) == 0 && len(t) > 0 && len(at) > 0 {
			return nil, false
		}
		return out, true
	case []any:
		out := make([]any, 0, len(t))
		for i, child := range t {
			if kept, ok := removePointers(child, with(at, strconv.Itoa(i)), remove); ok {
				out = append(out, kept)
			}
		}
		if len(out) == 0 && len(t) > 0 && len(at) > 0 {
			return nil, false
		}
		return out, true
	}
	return v, true
}

// absPathInCommand finds the absolute paths a shell command spells: a "/" that starts the
// command or follows whitespace, a quote, "=", ":" or "(", running to the next delimiter.
// A heuristic over shell text, and deliberately a generous one, because hostOnlyPath only
// acts on a match that is under the host's home AND absent from the jail.
var absPathInCommand = regexp.MustCompile("(?:^|[\\s'\"=:(])(/[^\\s'\"`;|&<>()$]+)")

// hostOnlyPath is the first path in command under hostHome that the jail cannot reach, or "".
func hostOnlyPath(command, hostHome string, reachable func(string) bool) string {
	for _, m := range absPathInCommand.FindAllStringSubmatch(command, -1) {
		p := filepath.Clean(m[1])
		if p != hostHome && !strings.HasPrefix(p, hostHome+"/") {
			continue
		}
		if reachable != nil && reachable(p) {
			continue
		}
		return p
	}
	return ""
}

// with is at extended by one token, in a fresh backing array, so sibling branches of a walk
// never write over each other's prefix.
func with(at []string, token string) []string {
	return append(at[:len(at):len(at)], token)
}
