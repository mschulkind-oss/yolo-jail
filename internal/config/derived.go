package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EffectivePackages ports _effective_packages: config `packages` plus
// gpu.vaapi-implied extras (mesa, libva-utils) when gpu is AMD + enabled +
// vaapi. Returns a copy; does not mutate config. Order is package order then
// the appended extras (skipping any already present, by string identity —
// Python's `if pkg not in packages`, which is value equality; here the extras
// are strings, and `pkg not in packages` compares against ALL package entries,
// including dict entries, but a string never equals a dict, so string-only
// comparison is faithful).
func EffectivePackages(config *jsonx.OrderedMap) []any {
	packages := listCopy(getListOrNilFalsy(config, "packages"))

	gpu, _ := asMap(getMapOrEmpty(config, "gpu"))
	if gpu != nil && truthy(getOr(gpu, "enabled", nil)) &&
		truthy(getOr(gpu, "vaapi", nil)) && strEq(getOr(gpu, "vendor", nil), "amd") {
		for _, pkg := range vaapiPackages {
			if !containsAny(packages, pkg) {
				packages = append(packages, pkg)
			}
		}
	}
	return packages
}

// FilterMCPServersByEnv ports _filter_mcp_servers_by_env: drop MCP servers whose
// requires_env gate isn't satisfied by envMap. Non-dict input returns unchanged.
// Null entries (preset removals) pass through. Preserves insertion order.
func FilterMCPServersByEnv(mcpServers any, envMap map[string]string) any {
	m, ok := asMap(mcpServers)
	if !ok {
		return mcpServers
	}
	filtered := jsonx.NewOrderedMap()
	for _, name := range m.Keys() {
		cfg, _ := m.Get(name)
		if cm, ok := asMap(cfg); ok {
			required, _ := cm.Get("requires_env")
			if reqList, ok := asList(required); ok {
				drop := false
				for _, v := range reqList {
					if s, ok := asStr(v); ok && envMap[s] == "" {
						drop = true
						break
					}
				}
				if drop {
					continue
				}
			}
		}
		filtered.Set(name, cfg)
	}
	return filtered
}

// EffectiveMCPServerNames ports _effective_mcp_server_names: preset names, then
// config servers added (append if new) or removed (null drops from the list).
// Returns []any to preserve non-string preset entries exactly as Python's
// `list(mcp_presets or [])` does (they never match a server name, so they are
// inert but must not be dropped).
func EffectiveMCPServerNames(mcpServers, mcpPresets any) []any {
	var names []any
	if truthy(mcpPresets) {
		if presets, ok := asList(mcpPresets); ok {
			names = append(names, presets...)
		}
	}
	m, ok := asMap(mcpServers)
	if !ok {
		return names
	}
	for _, name := range m.Keys() {
		cfg, _ := m.Get(name)
		if cfg == nil {
			names = removeFirstAny(names, name)
			continue
		}
		if _, ok := asMap(cfg); ok && !containsAny(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// SelectedAgents ports selected_agents: agent names from config (default
// DEFAULT_AGENTS), filtered to VALID_AGENTS, de-duplicated, order preserved.
// An explicit empty list is honored.
func SelectedAgents(config *jsonx.OrderedMap) []string {
	rawVal, present := config.Get("agents")
	var raw []any
	if !present || rawVal == nil {
		for _, a := range agents.DefaultAgents {
			raw = append(raw, a)
		}
	} else if l, ok := asList(rawVal); ok {
		raw = l
	} else {
		// Python iterates `raw` directly; a non-list, non-None raw would raise
		// on iteration. selected_agents is only called after _validate_config,
		// so this is unreachable for real input; treat as empty (no agents).
		raw = nil
	}
	seen := map[string]struct{}{}
	var result []string
	for _, nv := range raw {
		name, ok := asStr(nv)
		if !ok {
			continue
		}
		if _, valid := validAgentSet[name]; !valid {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

var validAgentSet = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, a := range agents.ValidAgents {
		m[a] = struct{}{}
	}
	return m
}()

// MergeMiseTools ports _merge_mise_tools: {**DEFAULT_MISE_TOOLS,
// **config.mise_tools}. Returns an OrderedMap so the result serializes with
// stable key order (defaults first, then config keys not already present,
// updates keeping position — Python dict-unpacking semantics).
func MergeMiseTools(config *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, k := range defaultMiseToolsKeys {
		out.Set(k, defaultMiseToolsVals[k])
	}
	if userTools, ok := asMap(getMapOrEmpty(config, "mise_tools")); ok && userTools != nil {
		for _, k := range userTools.Keys() {
			v, _ := userTools.Get(k)
			out.Set(k, v)
		}
	}
	return out
}

// MergeMiseDisabledTools ports _merge_mise_disabled_tools: yolo-managed package
// managers (pnpm) plus user-supplied tools (comma/space separated), deduped,
// comma-joined. Non-string userValue is ignored.
func MergeMiseDisabledTools(userValue any) string {
	var tools []string
	for _, tool := range defaultMiseDisabledTools {
		if !containsStr(tools, tool) {
			tools = append(tools, tool)
		}
	}
	if s, ok := userValue.(string); ok {
		for _, tool := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
			if tool != "" && !containsStr(tools, tool) {
				tools = append(tools, tool)
			}
		}
	}
	return strings.Join(tools, ",")
}

// grepDefault / findDefault mirror the default_messages dict in
// _normalize_blocked_tools. Built as ordered maps so the normalized entries
// serialize with Python's dict insertion order (name, then merged defaults).
func grepDefaults() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("message", "grep's recursive mode is blocked. Use ripgrep (rg) for recursive searches; pipe filters and single-file greps pass through.")
	m.Set("suggestion", "Try: rg <pattern> [path]")
	m.Set("block_flags", []any{"--recursive", "-r", "-R", "-*[rR]*"})
	return m
}

func findDefaults() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("message", "find is blocked to prevent unintended recursive searches. Use fd for a faster, more intuitive alternative.")
	m.Set("suggestion", "Try: fd <pattern>")
	return m
}

// NormalizeBlockedTools ports _normalize_blocked_tools: turn security section's
// blocked_tools (default ["grep","find"]) into the list-of-dict form the
// entrypoint consumes. String entries get default_messages merged in; dict
// entries (with "name") merge defaults-under-user.
func NormalizeBlockedTools(securitySection *jsonx.OrderedMap) []any {
	if securitySection == nil {
		securitySection = jsonx.NewOrderedMap()
	}
	rawBlockedVal := getOr(securitySection, "blocked_tools", defaultBlockedList())
	if rawBlockedVal == nil {
		rawBlockedVal = defaultBlockedList()
	}
	rawBlocked, ok := asList(rawBlockedVal)
	if !ok {
		// Python would iterate the value; a non-list raw would raise. Only
		// reached with an already-validated config, so treat non-list as empty.
		return []any{}
	}

	defaults := map[string]func() *jsonx.OrderedMap{
		"grep": grepDefaults,
		"find": findDefaults,
	}

	var out []any
	for _, tool := range rawBlocked {
		if s, ok := asStr(tool); ok {
			merged := jsonx.NewOrderedMap()
			merged.Set("name", s)
			if mkDef, ok := defaults[s]; ok {
				def := mkDef()
				for _, k := range def.Keys() {
					v, _ := def.Get(k)
					merged.Set(k, v)
				}
			}
			out = append(out, merged)
			continue
		}
		if tm, ok := asMap(tool); ok {
			if _, hasName := tm.Get("name"); hasName {
				nameV, _ := tm.Get("name")
				merged := jsonx.NewOrderedMap()
				if name, ok := asStr(nameV); ok {
					if mkDef, ok := defaults[name]; ok {
						def := mkDef()
						for _, k := range def.Keys() {
							v, _ := def.Get(k)
							merged.Set(k, v)
						}
					}
				}
				// merged.update(tool): user fields override, unspecified inherit,
				// new keys appended in tool's order.
				for _, k := range tm.Keys() {
					v, _ := tm.Get(k)
					merged.Set(k, v)
				}
				out = append(out, merged)
			}
		}
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func defaultBlockedList() []any { return []any{"grep", "find"} }

// ---------------------------------------------------------------------------
// value-model utilities
// ---------------------------------------------------------------------------

// truthy reproduces Python truthiness for the config value types that gate
// branches (gpu.enabled/vaapi): bool, non-empty containers/strings, nonzero
// numbers. nil is falsy.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []any:
		return len(t) != 0
	case *jsonx.OrderedMap:
		return t.Len() != 0
	case float64:
		return t != 0
	default:
		if n, ok := jsonx.AsInt(v); ok {
			return n != 0
		}
		return true
	}
}

func strEq(v any, s string) bool {
	got, ok := asStr(v)
	return ok && got == s
}

// getListOrNilFalsy mirrors `list(config.get(key, []) or [])` — a present-but-
// falsy value (None, empty list) yields an empty list.
func getListOrNilFalsy(m *jsonx.OrderedMap, key string) []any {
	v, ok := m.Get(key)
	if !ok || !truthy(v) {
		return nil
	}
	if l, ok := asList(v); ok {
		return l
	}
	return nil
}

// getMapOrEmpty mirrors `config.get(key) or {}` for a dict-typed field: returns
// the map when present and truthy, else an empty OrderedMap. Returned as any so
// callers can asMap it.
func getMapOrEmpty(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !truthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

func listCopy(in []any) []any {
	if in == nil {
		return nil
	}
	out := make([]any, len(in))
	copy(out, in)
	return out
}

// containsAny reports whether s (a string) equals any element of list. Mirrors
// Python `s in list` for a string needle over a mixed list.
func containsAny(list []any, s string) bool {
	for _, v := range list {
		if vs, ok := v.(string); ok && vs == s {
			return true
		}
	}
	return false
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// string s (only string elements can match), preserving order.
func removeFirstAny(list []any, s string) []any {
	for i, v := range list {
		if vs, ok := v.(string); ok && vs == s {
			return append(list[:i:i], list[i+1:]...)
		}
	}
	return list
}
