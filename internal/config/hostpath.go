package config

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostpath.go is the `host_path` key (docs/design/host-launch-environment.md §2.2): the folders a
// user adds to the PATH yolo's checks read at `yolo host`, for a launcher whose own PATH lacks
// them — a Waybar button, cron, a macOS hotkey launcher.
//
// # What it adds to, and why it only adds
//
// The maintainer's ruling HE-DIR1: yolo's checks at the host read the PATH yolo was started with,
// because it cannot otherwise know where a user keeps their tools, and `host_path` ADDS folders
// to it. So the value is a list of folders appended after that PATH (HE-D3), never a PATH that
// replaces it, and an empty list is the same as none. The resolver that does the appending is
// internal/hostpath; this file only reads and checks the list.
//
// # The grammar
//
// Each entry is a folder, absolute or starting with `~/` (expanded against paths.Home()). A
// relative folder, `~user/`, anything carrying `$` and anything carrying `:` are refused:
// relative would depend on the cwd, `~user` on a passwd lookup nobody asked for, `$` would read
// the launcher's environment a second time behind a key that looks fixed, and `:` would smuggle
// two folders into one entry. Refused by validateHostPath, and dropped by the reader, so an entry
// validation refuses reaches no PATH even where nothing validated the config first.
//
// # Why it is read from the USER config directly
//
// A folder on this list decides which binary `yolo host` runs for a name no floor entry covers,
// and a workspace config is agent-editable: a cloned repository naming a folder here would choose
// the program a host agent runs. So it is read from user scope alone, `host_wrappers`'
// construction, and a workspace spelling is a validation error that is never read.
const hostPathKey = "host_path"

// HostPathFolders is the user config's `host_path`, each folder expanded, in written order, with
// every entry validation refuses left out. Empty when the key is absent, empty or unreadable.
func HostPathFolders() []string {
	return hostPathFolders(UserScopeConfigOrEmpty(), paths.Home())
}

// hostPathFolders is the reading, split from the real-home lookup so the validator and the tests
// exercise one implementation.
func hostPathFolders(cfg *jsonx.OrderedMap, home string) []string {
	v, present := cfg.Get(hostPathKey)
	if !present || v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range list {
		s, ok := e.(string)
		if !ok || hostPathEntryProblem(s) != "" {
			continue
		}
		out = append(out, expandHostPathEntry(s, home))
	}
	return out
}

// expandHostPathEntry expands a leading `~/` against home and cleans the result.
func expandHostPathEntry(s, home string) string {
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return filepath.Clean(s)
}

// hostPathEntryProblem is why one `host_path` entry is refused, "" when it is accepted.
func hostPathEntryProblem(s string) string {
	switch {
	case s == "":
		return "an empty entry names no folder"
	case strings.Contains(s, "$"):
		return "it carries a `$`, and host_path expands no variable: an entry that did would read the " +
			"launcher's environment again, behind a key that looks fixed"
	case strings.ContainsRune(s, ':'):
		return "it carries a `:`, which separates PATH folders; write each folder as its own entry"
	case s == "~":
		return "name a folder under your home as `~/<folder>`, or write an absolute path"
	case strings.HasPrefix(s, "~/"):
		return ""
	case strings.HasPrefix(s, "~"):
		return "`~user/` is not expanded; write the folder as an absolute path, or `~/` for your own home"
	case !filepath.IsAbs(s):
		return "it is relative, and would name a different folder from every directory yolo is started " +
			"in; write it absolute, or starting with `~/`"
	}
	return ""
}

// hostPathProblems is every problem with a `host_path` value, one message per refused entry.
func hostPathProblems(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return []string{"expected a list of folders (got " + pyReprValue(v) + ")"}
	}
	var out []string
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			out = append(out, "entry "+itoa(i)+": expected a folder string (got "+pyReprValue(e)+")")
			continue
		}
		if prob := hostPathEntryProblem(s); prob != "" {
			out = append(out, "entry "+itoa(i)+" "+pyReprValue(s)+": "+prob)
		}
	}
	return out
}
