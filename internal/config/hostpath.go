package config

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostpath.go is the `host_path` key (docs/reference/host-agent-environment.md, the launch PATH): the folders a
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
	problem, _ := hostPathEntryVerdict(s)
	return problem
}

// hostPathEntryVerdict is THE rule for one `host_path` entry: why it is refused, in validation's
// words, and the same reason short enough for a miss line, with the fix where there is an obvious
// one. Both "" when it is accepted. One switch, so the validator, the reader and the miss line cannot
// disagree about which entries are refused.
func hostPathEntryVerdict(s string) (problem, short string) {
	switch {
	case s == "":
		return "an empty entry names no folder", "it names no folder"
	case strings.Contains(s, "$"):
		short = "host_path expands no variable"
		if rest, ok := homeVariableRest(s); ok {
			short += `, so write it "~/` + rest + `"`
		}
		return "it carries a `$`, and host_path expands no variable: an entry that did would read the " +
			"launcher's environment again, behind a key that looks fixed", short
	case strings.ContainsRune(s, ':'):
		return "it carries a `:`, which separates PATH folders; write each folder as its own entry",
			`a ":" separates PATH folders, so write each folder as its own entry`
	case s == "~":
		return "name a folder under your home as `~/<folder>`, or write an absolute path",
			`it names no folder under your home; write "~/<folder>"`
	case strings.HasPrefix(s, "~/"):
		return "", ""
	case strings.HasPrefix(s, "~"):
		return "`~user/` is not expanded; write the folder as an absolute path, or `~/` for your own home",
			`"~user/" is not expanded; write the folder as an absolute path`
	case !filepath.IsAbs(s):
		return "it is relative, and would name a different folder from every directory yolo is started " +
				"in; write it absolute, or starting with `~/`",
			`it is relative; write it absolute, or starting with "~/"`
	}
	return "", ""
}

// homeVariableRest is the rest of an entry spelled `$HOME/<rest>` or `${HOME}/<rest>`, when that rest
// would itself be accepted after `~/` — the one refusal whose fix yolo can write for the user.
func homeVariableRest(s string) (string, bool) {
	for _, prefix := range []string{"$HOME/", "${HOME}/"} {
		if rest, ok := strings.CutPrefix(s, prefix); ok && rest != "" {
			if p, _ := hostPathEntryVerdict("~/" + rest); p == "" {
				return rest, true
			}
		}
	}
	return "", false
}

// HostPathRefusal is one part of the user config's `host_path` the reader leaves out, and why. No
// host verb validates the config before it reads the key, so without this an entry validation
// refuses would vanish in silence: a miss in the folder the user meant would then tell them to add
// the folder they already listed. The miss line names each one (internal/hostpath).
type HostPathRefusal struct {
	// Entry is what was written, rendered as JSON: `"$HOME/.cargo/bin"`, or `7`.
	Entry string
	// Whole is whether Entry is the key's whole value — not a list at all — rather than one entry.
	Whole bool
	// Why is the reason, and the fix where there is an obvious one, short enough for one line.
	Why string
}

// HostPathRefusals is every part of the user config's `host_path` that HostPathFolders leaves out, in
// written order. Empty when the key is absent, empty, unreadable or wholly accepted.
func HostPathRefusals() []HostPathRefusal { return hostPathRefusals(UserScopeConfigOrEmpty()) }

func hostPathRefusals(cfg *jsonx.OrderedMap) []HostPathRefusal {
	v, present := cfg.Get(hostPathKey)
	if !present || v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		r := HostPathRefusal{Entry: renderHostPathEntry(v), Whole: true, Why: "host_path is a list of folders"}
		if _, isString := v.(string); isString {
			r.Why += ", so write it [" + r.Entry + "]"
		}
		return []HostPathRefusal{r}
	}
	var out []HostPathRefusal
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			out = append(out, HostPathRefusal{Entry: renderHostPathEntry(e), Why: "it is not a folder name"})
			continue
		}
		if _, short := hostPathEntryVerdict(s); short != "" {
			out = append(out, HostPathRefusal{Entry: renderHostPathEntry(s), Why: short})
		}
	}
	return out
}

// renderHostPathEntry writes a config value the way the user wrote it in the file: as JSON.
func renderHostPathEntry(v any) string {
	if out, err := jsonx.DumpsCompact(v); err == nil {
		return out
	}
	return pyReprValue(v)
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
