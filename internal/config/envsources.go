package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ParseDotenv ports _parse_dotenv: parse KEY=VALUE dotenv content into an
// ordered map. Comment/blank lines ignored; `export ` prefix stripped; matching
// single/double quote wrappers removed; malformed lines (no `=`, invalid var
// name) silently skipped. Returns keys in first-seen order (later assignment to
// an existing key updates value, keeps position — Python dict semantics).
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

// ResolveEnvSourcePath ports _resolve_env_source_path: ~ expansion, absolute
// paths pass through, relative paths resolve against the workspace root.
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

// ResolveEnvSources ports _resolve_env_sources: iterate env_sources in order —
// inline dicts apply directly; string entries read as dotenv files; later
// entries override earlier; missing/unreadable files warn (via warn) and skip.
// Returns the final env map as an OrderedMap (later-wins on key, position kept).
func ResolveEnvSources(workspace string, config *jsonx.OrderedMap, warn Warn) *jsonx.OrderedMap {
	if warn == nil {
		warn = func(string) {} // console.print warnings; discarded by default here
	}
	merged := jsonx.NewOrderedMap()
	entries := getListOrNilFalsy(config, "env_sources")
	for _, entry := range entries {
		if em, ok := asMap(entry); ok {
			for _, k := range em.Keys() {
				v, _ := em.Get(k)
				// Python: `if isinstance(k, str) and isinstance(v, str)`. Decoded
				// JSON keys are always strings, so only the value type gates.
				if vs, vok := asStr(v); vok {
					merged.Set(k, vs)
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
				merged.Set(k, v)
			}
		}
	}
	return merged
}

// uses. Only "~" and "~/..." are expanded (a "~user" form is left untouched
// unless we can resolve it, matching the common case). HOME resolution follows
// Python's expanduser: HOME if set (empty HOME -> unchanged tilde per CPython
// posixpath? — actually posixpath uses pwd when HOME unset). We reuse the same
// HOME/pwd logic internal/paths uses.
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
		// bare "~" or "~/..." — posixpath: userhome.rstrip('/') + path[i:],
		// or '/' when that is empty.
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

// homeForExpand $HOME if set (even
// empty), else the passwd entry (pwd.getpwuid). Empty HOME with "~/x" yields
// "/x" after the rstrip+`or "/"` in expandUser.
func homeForExpand() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		return h
	}
	if u, err := userHomeDir(); err == nil {
		return u
	}
	return ""
}
