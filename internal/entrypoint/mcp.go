package entrypoint

import (
	"path/filepath"
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

// ${VAR} INTERPOLATION WAS REMOVED (2026-08-03), deliberately and by ruling. Do not add it
// back as a convenience; the reason is structural, not stylistic.
//
// yolo used to expand ${VAR} in an MCP server's `env`, `headers`, and `url` against e.Vars
// (hydrated from env_sources at boot). Two independent reasons it was wrong:
//
//  1. THE VALUE HAD NO LAYER. Every other value in a rendered surface has a provenance
//     answer — defaults / host / config-overlay:<pack> / managed / computed / retired:<layer>
//     — and the whole host-render story depends on being able to ask "who set this key?".
//     An interpolated secret entered the file without passing through any layer, so
//     `config diff` could not attribute it and the orphan-key prune could not tell yolo's
//     output from the user's. It was a value sneaking in the side door.
//  2. IT SOURCED CONFIG CONTENT FROM PROCESS ENV AT RENDER TIME. env_sources is a
//     jail-PROVISIONING input (what the container's environment contains). Using it as a
//     rendering input made the bytes written to a config file depend on the ambient
//     environment of whoever ran the render — the one input the confinement model
//     deliberately does not treat as configuration.
//
// It was also unnecessary, which is what made the tradeoff one-sided. hydrateEnvFromUserEnvFile
// does os.Setenv for every env_sources var before any generator runs (boot.go), so those
// variables are already in the environment of every process the entrypoint spawns — verified:
// a non-interactive `sh -c` and a bare execve'd python both see them, `env -i` clears them
// (proving process-env inheritance rather than a sourced rc file), and boot-time daemons carry
// them in /proc/<pid>/environ. So the consuming agent can resolve ${VAR} itself. yolo resolving
// first bought nothing and wrote a plaintext secret into a config file it does not own.
//
// Consequence to expect: yolo writes the literal ${VAR} and the consumer resolves it or does
// not. If it does not, that is the consumer's decision to own, not a gap for yolo to paper
// over. If a real need for declared secret references appears, the honest form is a LAYER with
// provenance resolved at launch — not a string substitution during render.

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

	return servers
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
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
