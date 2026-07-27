package entrypoint

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// (empty) merged with the dict in YOLO_LSP_SERVERS. Returns an OrderedMap so
// insertion order (defaults then overrides) is preserved for byte-parity.
func LoadLSPServers(e *Env) *jsonx.OrderedMap {
	servers := jsonx.NewOrderedMap() // DEFAULT_LSP_SERVERS == {}
	extraJSON := e.Getenv("YOLO_LSP_SERVERS")
	if extraJSON == "" {
		return servers
	}
	decoded, err := jsonx.Decode([]byte(extraJSON))
	if err != nil {
		return servers
	}
	extra, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return servers
	}
	for _, k := range extra.Keys() {
		v, _ := extra.Get(k)
		servers.Set(k, v)
	}
	return servers
}

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolateEnv expand ${VAR} in the
// values of an env dict against e.Vars. Undefined vars are left literal and a
// single sorted warning is emitted. Non-string values pass through untouched.
// Returns a new OrderedMap preserving key order.
func (e *Env) interpolateEnv(env *jsonx.OrderedMap) *jsonx.OrderedMap {
	resolved := jsonx.NewOrderedMap()
	var unresolved []string
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		s, isStr := v.(string)
		if !isStr {
			resolved.Set(k, v)
			continue
		}
		out := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
			name := match[2 : len(match)-1]
			if val, ok := e.Lookup(name); ok {
				return val
			}
			unresolved = append(unresolved, name)
			return match
		})
		resolved.Set(k, out)
	}
	if len(unresolved) > 0 {
		names := sortedUnique(unresolved)
		e.warn("warning: MCP env references undefined variable(s): " + strings.Join(names, ", "))
	}
	return resolved
}

func (e *Env) chromeDevtoolsArgs() []any {
	npmBin := e.NpmBin()
	return []any{
		filepath.Join(npmBin, "chrome-devtools-mcp"),
		"--headless",
		"--isolated",
		"--executablePath",
		"/usr/bin/chromium",
		"--chrome-arg=--no-sandbox",
		"--chrome-arg=--disable-dev-shm-usage",
		"--chrome-arg=--disable-setuid-sandbox",
		"--chrome-arg=--disable-gpu",
		"--chrome-arg=--disable-software-rasterizer",
	}
}

// LoadMCPServers presets (opt-in via
// YOLO_MCP_PRESETS) merged with YOLO_MCP_SERVERS (overrides / additions /
// null-removals), requires_env gating, then ${VAR} interpolation of env values.
// Returns an OrderedMap whose key order follows insertion order.
func (e *Env) LoadMCPServers() *jsonx.OrderedMap {
	mcpWrappers := e.McpWrappersBin()
	npmBin := e.NpmBin()

	presets := map[string]*jsonx.OrderedMap{
		"chrome-devtools": func() *jsonx.OrderedMap {
			m := jsonx.NewOrderedMap()
			m.Set("command", filepath.Join(mcpWrappers, "node"))
			m.Set("args", e.chromeDevtoolsArgs())
			return m
		}(),
		"sequential-thinking": func() *jsonx.OrderedMap {
			m := jsonx.NewOrderedMap()
			m.Set("command", filepath.Join(mcpWrappers, "node"))
			m.Set("args", []any{filepath.Join(npmBin, "mcp-server-sequential-thinking")})
			return m
		}(),
	}

	servers := jsonx.NewOrderedMap()

	// Expand requested presets (order follows the YOLO_MCP_PRESETS list).
	if presetsJSON := e.Getenv("YOLO_MCP_PRESETS"); presetsJSON != "" {
		if decoded, err := jsonx.Decode([]byte(presetsJSON)); err == nil {
			if arr, ok := decoded.([]any); ok {
				for _, n := range arr {
					if name, isStr := n.(string); isStr {
						if p, exists := presets[name]; exists {
							servers.Set(name, p)
						}
					}
				}
			}
		}
	}

	// Merge custom servers (overrides, additions, null-removals).
	if extraJSON := e.Getenv("YOLO_MCP_SERVERS"); extraJSON != "" {
		if decoded, err := jsonx.Decode([]byte(extraJSON)); err == nil {
			if extra, ok := decoded.(*jsonx.OrderedMap); ok {
				for _, name := range extra.Keys() {
					cfg, _ := extra.Get(name)
					if cfg == nil {
						servers.Delete(name)
					} else if _, isMap := cfg.(*jsonx.OrderedMap); isMap {
						servers.Set(name, cfg)
					}
				}
			}
		}
	}

	// Conditional loading: requires_env gate. Iterate a snapshot of the keys,
	// mutating servers as we go.
	for _, name := range append([]string(nil), servers.Keys()...) {
		v, _ := servers.Get(name)
		cfg, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		reqVal, has := cfg.Get("requires_env")
		if !has {
			continue
		}
		required, ok := reqVal.([]any)
		if !ok {
			continue
		}
		var missing []string
		for _, rv := range required {
			if s, isStr := rv.(string); isStr {
				if val, present := e.Lookup(s); !present || val == "" {
					missing = append(missing, s)
				}
			}
		}
		if len(missing) > 0 {
			e.warn("notice: MCP server '" + name + "' skipped — required env not set: " + strings.Join(missing, ", "))
			servers.Delete(name)
		} else {
			// Strip requires_env, preserving other keys' order.
			stripped := jsonx.NewOrderedMap()
			for _, k := range cfg.Keys() {
				if k == "requires_env" {
					continue
				}
				kv, _ := cfg.Get(k)
				stripped.Set(k, kv)
			}
			servers.Set(name, stripped)
		}
	}

	// Expand ${VAR} in env values, preserving the position of the existing
	// "env" key.
	for _, name := range servers.Keys() {
		v, _ := servers.Get(name)
		cfg, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		envVal, has := cfg.Get("env")
		if !has {
			continue
		}
		envMap, ok := envVal.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		interpolated := e.interpolateEnv(envMap)
		// Rebuild {**cfg, "env": interpolated}: same key order as cfg, with env
		// updated in place (env already exists so its slot is kept).
		rebuilt := jsonx.NewOrderedMap()
		for _, k := range cfg.Keys() {
			if k == "env" {
				rebuilt.Set(k, interpolated)
			} else {
				kv, _ := cfg.Get(k)
				rebuilt.Set(k, kv)
			}
		}
		servers.Set(name, rebuilt)
	}
	return servers
}

func sortedUnique(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// LoadMCPPresetNames returns the enabled MCP preset names from YOLO_MCP_PRESETS, in
// config order. Empty when none are enabled.
//
// Split out so the bootstrap script's npm install can be gated on the SAME
// declaration that builds the server table (D6), rather than hardcoding a package
// list beside it and letting the two drift.
func (e *Env) LoadMCPPresetNames() []string {
	presetsJSON := e.Getenv("YOLO_MCP_PRESETS")
	if presetsJSON == "" {
		return nil
	}
	decoded, err := jsonx.Decode([]byte(presetsJSON))
	if err != nil {
		return nil
	}
	arr, ok := decoded.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range arr {
		if name, isStr := v.(string); isStr {
			out = append(out, name)
		}
	}
	return out
}
