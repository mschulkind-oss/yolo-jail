// Package tomlx provides TOML parsing parity for the load-bearing places the
// port needs it (go-port plan §3 internal/tomlx): mise.toml / mise.jail.toml
// venv discovery (feeds the venv-shadow mount set and a jail-made-venv rmtree),
// agents_md's pyproject.toml read, and codex's ~/.codex/config.toml (the one
// agent config that is TOML, not JSON).
//
// Backed by github.com/BurntSushi/toml (the de-facto standard, vendored) rather
// than hand-written — TOML is genuinely complex and a vetted parser is the
// happy path. The parity surface is the SHAPES the port reads (str vs dict
// entries, nested env._.python.venv, later-file-wins), pinned by a fixture
// corpus in the test.
//
// Source of truth: Python tomllib observed behavior over the real config shapes.
package tomlx

import (
	"os"

	"github.com/BurntSushi/toml"
)

// Decode parses TOML bytes into a generic map, matching tomllib.load's shape:
// tables -> map[string]any, arrays -> []any, and scalars to Go's bool/int64/
// float64/string/time. Returns an error on malformed TOML (tomllib raises).
func Decode(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// DecodeFile reads and parses a TOML file.
func DecodeFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// VenvValue extracts env._.python.venv from a parsed mise config, walking the
// nested tables the way the entrypoint's venv-discovery does
// (`env -> _ -> python -> venv`). Returns (value, true) if the whole chain is
// present, else (nil, false). Mirrors the Python venv_value walk in
// entrypoint/shell.py.
func VenvValue(cfg map[string]any) (any, bool) {
	var cur any = cfg
	for _, key := range []string{"env", "_", "python", "venv"} {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// MiseVenvPath resolves the venv path from a set of mise config files in
// priority order (highest-priority first, first hit wins), mirroring the
// entrypoint's shell.py discovery:
//   - for each file, read env._.python.venv;
//   - the FIRST file that has a value wins;
//   - a string value is the path directly;
//   - a table value must have create=true (else "no venv"), then path (default
//     ".venv"); a non-string/absent -> "no venv".
//
// Returns (path, true) when a venv should be created, or ("", false) when none.
func MiseVenvPath(files []string) (string, bool) {
	var v any
	found := false
	for _, f := range files {
		cfg, err := DecodeFile(f)
		if err != nil {
			continue
		}
		if val, ok := VenvValue(cfg); ok {
			v = val
			found = true
			break
		}
	}
	if !found {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case map[string]any:
		create, _ := t["create"].(bool)
		if !create {
			return "", false
		}
		path, ok := t["path"].(string)
		if !ok {
			path = ".venv"
		}
		return path, true
	default:
		return "", false
	}
}
