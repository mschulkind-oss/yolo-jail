package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ParseDotenv parses KEY=VALUE dotenv content into an ordered map.
// Comment/blank lines ignored; `export ` prefix stripped; matching
// single/double quote wrappers removed; malformed lines (no `=`, invalid var
// name) silently skipped. Returns keys in first-seen order (later assignment to
// an existing key updates value, keeps position).
func ParseDotenv(text string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, raw := range splitLines(text) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimLeft(line[len("export "):], " \t\n\r\v\f")
		}
		if !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		if !envVarNameRe.MatchString(key) {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 && value[0] == value[len(value)-1] &&
			(value[0] == '\'' || value[0] == '"') {
			value = value[1 : len(value)-1]
		}
		out.Set(key, value)
	}
	return out
}

// ResolveEnvSourcePath expands ~, passes absolute paths through, and resolves
// relative paths against the workspace root.
func ResolveEnvSourcePath(entry, workspace string) string {
	expanded := expandUser(entry)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	joined := filepath.Join(workspace, expanded)
	if r, err := resolve(joined); err == nil {
		return r
	}
	return joined
}

// ResolveEnvSources iterates env_sources in order — inline dicts apply
// directly; string entries read as dotenv files; later
// entries override earlier; missing/unreadable files warn (via warn) and skip.
// Returns the final env map as an OrderedMap (later-wins on key, position kept).
func ResolveEnvSources(workspace string, config *jsonx.OrderedMap, warn Warn) *jsonx.OrderedMap {
	merged, _ := resolveEnvSources(workspace, config, warn)
	return merged
}

// resolveEnvSources is the single ordered pass over `env_sources`, returning the
// assignments and the removals together.
//
// One pass, because the two answers are defined by the SAME ordering and computing them
// separately let them disagree. Later entries win for both: an assignment after a null
// sets the variable and drops the removal; a null after an assignment removes it and drops
// the assignment. That holds across entry KINDS too — a dotenv file listed after an inline
// null cancels that null, which a dict-only scan could not see.
func resolveEnvSources(workspace string, config *jsonx.OrderedMap, warn Warn) (*jsonx.OrderedMap, []string) {
	if warn == nil {
		warn = func(string) {} // discard warnings by default
	}
	merged := jsonx.NewOrderedMap()
	removed := map[string]bool{}
	var order []string
	remove := func(k string) {
		if !removed[k] {
			order = append(order, k)
		}
		removed[k] = true
		merged.Delete(k)
	}
	assign := func(k, v string) {
		removed[k] = false
		merged.Set(k, v)
	}

	for _, entry := range getListOrNilFalsy(config, "env_sources") {
		if em, ok := asMap(entry); ok {
			for _, k := range em.Keys() {
				v, _ := em.Get(k)
				if v == nil {
					// The REMOVAL spelling. Only an inline dict can express one: a
					// dotenv FILE has no syntax for "unset", and inventing one would
					// make yolo's dialect differ from everyone else's.
					remove(k)
					continue
				}
				// Decoded JSON keys are always strings, so only the value type gates:
				// apply the entry only when the value is a string.
				if vs, vok := asStr(v); vok {
					assign(k, vs)
				}
			}
			continue
		}
		if s, ok := asStr(entry); ok {
			path := ResolveEnvSourcePath(s, workspace)
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					warn("env_sources file not found, skipping: " + s + " (resolved to " + path + ")")
				} else {
					warn("env_sources file unreadable, skipping: " + s + ": " + err.Error())
				}
				continue
			}
			parsed := ParseDotenv(string(data))
			for _, k := range parsed.Keys() {
				v, _ := parsed.Get(k)
				vs, _ := v.(string)
				assign(k, vs)
			}
		}
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		if removed[k] {
			out = append(out, k)
		}
	}
	return merged, out
}

// EnvSourceRemovals returns the variable names an `env_sources` entry asks to be REMOVED
// — the keys whose value is JSON null.
//
// # Why a removal exists at all
//
// ResolveEnvSources returns a map of assignments, and a map cannot express "and delete
// this one". A null is therefore skipped there, which is right for the JAIL: a container
// starts from an empty environment, so "removed" is already the default state of anything
// yolo does not pass in.
//
// On the HOST it is the opposite. `yolo host -- claude` starts from the invoking shell's
// os.Environ(), which may well carry an AWS_PROFILE that has to go — the motivating case
// in docs/design/host-agent-environment.md §2.2, where the hand-written wrapper's first
// act is `unset AWS_PROFILE`. No config SURFACE can express a removal at all, which is
// one of the reasons the process-env channel has to exist (§1 P1).
//
// A removal is not an empty assignment: `AWS_PROFILE=` and no AWS_PROFILE behave
// differently in every AWS SDK, which is why this returns names to unset rather than
// pairs to set empty.
//
// It shares ONE pass with ResolveEnvSources (resolveEnvSources below) so the two can never
// disagree about ordering. That mattered: an earlier version walked only the inline dicts,
// so a dotenv FILE listed after a null did assign the variable in the map while the
// removal still fired — later-wins in one half and earlier-wins in the other, and the
// unset silently won.
func EnvSourceRemovals(workspace string, config *jsonx.OrderedMap, warn Warn) []string {
	_, removals := resolveEnvSources(workspace, config, warn)
	return removals
}

// expandUser expands a leading "~". Only "~" and "~/..." are expanded (a
// "~user" form is left untouched, matching the common case). HOME resolution
// uses $HOME when set, else the passwd entry — the same HOME/pwd logic
// internal/paths uses.
func expandUser(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	// Find end of the ~ component.
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i == 1 {
		// bare "~" or "~/..." — home with trailing slashes stripped + the rest,
		// or "/" when that is empty.
		home := strings.TrimRight(homeForExpand(), "/")
		res := home + p[i:]
		if res == "" {
			return "/"
		}
		return res
	}
	// "~user/..." — best effort: leave untouched (config env_sources use ~/…).
	return p
}

// homeForExpand returns $HOME if set (even empty), else the passwd entry. Empty
// HOME with "~/x" yields "/x" after the rstrip+`or "/"` in expandUser.
func homeForExpand() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		return h
	}
	if u, err := userHomeDir(); err == nil {
		return u
	}
	return ""
}
