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
//
// Also serves `headers` (see interpolatedDictFields) — the walk is "expand every string
// value of a dict", which is the same operation, so a second copy would only be a second
// place for the warning to go missing.
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
		resolved.Set(k, e.expandVars(s, &unresolved))
	}
	e.warnUnresolved(unresolved)
	return resolved
}

// interpolateString expands ${VAR} in ONE string, warning on anything unresolved — the
// single-value form of interpolateEnv, for the scalar fields (`url`).
//
// Same warn-don't-fail contract, and that matters most here: an unresolved ${VAR} in a url
// is a working-looking config whose server 401s, so the literal must never pass silently.
func (e *Env) interpolateString(s string) string {
	var unresolved []string
	out := e.expandVars(s, &unresolved)
	e.warnUnresolved(unresolved)
	return out
}

// expandVars replaces every ${VAR} in s with its value from e.Vars, appending the names it
// could not resolve to unresolved and leaving those references LITERAL. The literal is
// deliberate — substituting an empty string would turn a missing credential into a request
// that looks well-formed and fails at the server.
func (e *Env) expandVars(s string, unresolved *[]string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		if val, ok := e.Lookup(name); ok {
			return val
		}
		*unresolved = append(*unresolved, name)
		return match
	})
}

// warnUnresolved emits ONE sorted warning naming every variable a server entry referenced
// but yolo could not resolve, or nothing when they all resolved. The remedy is named because
// "undefined variable" without it sends people to their shell rather than to env_sources,
// which is the one place a secret is supposed to live.
func (e *Env) warnUnresolved(unresolved []string) {
	if len(unresolved) == 0 {
		return
	}
	e.warn("warning: MCP config references undefined variable(s): " +
		strings.Join(sortedUnique(unresolved), ", ") +
		" — left literal; define them in an `env_sources` file")
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

	// Expand ${VAR} in the interpolated fields, preserving each existing key's
	// position.
	for _, name := range servers.Keys() {
		v, _ := servers.Get(name)
		cfg, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		servers.Set(name, e.interpolateServer(cfg))
	}
	return servers
}

// interpolatedStringFields are the server fields whose STRING value gets ${VAR} expansion,
// beside the `env` dict that has always had it.
//
// `url` is here because the http/sse transports are otherwise unusable with any secret. The
// canonical remote-MCP form embeds the credential in the query string —
// "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}" — and with expansion limited
// to `env` that landed VERBATIM in the config and the server answered 401 with nothing said.
// A stdio server can route a secret through `env`; an http one has nowhere else to put it.
//
// This also brings yolo in line with Claude Code's own documented expansion set for
// .mcp.json (command, args, env, url, headers). `headers` is a dict, so it goes through the
// same walk `env` does (see interpolateServer); `command` and `args` are deliberately NOT
// interpolated here — see interpolateServer for why that is a separate decision.
var interpolatedStringFields = []string{"url"}

// interpolatedDictFields are the server fields that are dicts of strings, each value
// interpolated. `env` is the original; `headers` is the http-transport equivalent, and the
// one place other than `url` a remote server's credential can go.
var interpolatedDictFields = []string{"env", "headers"}

// interpolateServer expands ${VAR} in one server entry's interpolated fields, returning a new
// OrderedMap with every key in its original position.
//
// WHAT IS NOT INTERPOLATED, and why it is a deliberate line rather than an oversight:
// `command` and `args`. Every value yolo puts there today is a path yolo itself computed
// (the mcp-wrappers node shim, an npm bin), and a ${VAR} in an argv position is the one
// place an expansion becomes a command-injection surface rather than a convenience. Claude
// Code expands them in .mcp.json; yolo declining to is the more conservative half of the
// difference, and a stdio server's secret belongs in `env` regardless. If a concrete need
// appears, adding "command" to interpolatedStringFields is the whole change.
func (e *Env) interpolateServer(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	rebuilt := jsonx.NewOrderedMap()
	for _, k := range cfg.Keys() {
		v, _ := cfg.Get(k)
		if contains(interpolatedDictFields, k) {
			if dict, isMap := v.(*jsonx.OrderedMap); isMap {
				rebuilt.Set(k, e.interpolateEnv(dict))
				continue
			}
		}
		if contains(interpolatedStringFields, k) {
			if s, isStr := v.(string); isStr {
				rebuilt.Set(k, e.interpolateString(s))
				continue
			}
		}
		rebuilt.Set(k, v)
	}
	return rebuilt
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
