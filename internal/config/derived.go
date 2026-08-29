package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EffectivePackages returns config `packages` plus gpu.vaapi-implied extras
// (mesa, libva-utils) when gpu is AMD + enabled + vaapi. Returns a copy; does
// not mutate config. Order is package order then the appended extras (skipping
// any already present). Extras are strings and dedup compares against all
// package entries; a string never equals a dict entry, so string-only
// comparison is correct.
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

// FilterMCPServersByEnv drops MCP servers whose requires_env gate isn't
// satisfied by envMap. Non-dict input returns unchanged.
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

// FilterMCPServersByCapabilities drops MCP servers whose declared `provides` capability
// is already natively provided by the target agent/mode (nativeCaps).
func FilterMCPServersByCapabilities(mcpServers any, nativeCaps []string) any {
	m, ok := asMap(mcpServers)
	if !ok {
		return mcpServers
	}
	capSet := make(map[string]bool, len(nativeCaps))
	for _, c := range nativeCaps {
		capSet[c] = true
	}
	filtered := jsonx.NewOrderedMap()
	for _, name := range m.Keys() {
		cfg, _ := m.Get(name)
		if cm, ok := asMap(cfg); ok {
			if provV, ok := cm.Get("provides"); ok {
				if s, ok := asStr(provV); ok && capSet[s] {
					continue // suppressed because agent has native capability
				}
			}
		}
		filtered.Set(name, cfg)
	}
	return filtered
}

// EffectiveMCPServerNames returns preset names, then config servers added
// (append if new) or removed (null drops from the list). Returns []any to
// preserve non-string preset entries (they never match a server name, so they
// are inert but must not be dropped).
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

// SelectedAgents is a TRANSITIONAL SHIM and always returns the empty set. It ignores
// its argument, which is retained only to keep the call sites unchanged while they are
// converted.
//
// It used to read the `agents` config key, which is DELETED: config now carries one
// list of packs, a pack that installs an agent is just a pack, and NOTHING IN CORE knows
// what an agent is. That last clause named `internal/agents` until 2026-08-17 and was
// wrong twice over by then — the package is `internal/jailcontent`, and the registry that
// made the exemption true left it long before the name did (packs/*/pack.json holds it
// now). There is consequently no selection to read and nothing to fall back on — a user
// with no packs gets no agents, and is TOLD so at launch (that warning is the whole
// discoverability story, since with zero agents no briefing file is written to put a
// note in).
//
// It survives as a shim only so the registry-side callers keep compiling while they are
// converted to read packs instead; deleting it is that change's job, not this one. ONE
// call site remains — run.go's macos-user path. (The comment said four; prepare.go,
// check/entrypoint.go and run.go's container path were converted without it being
// updated, which is why the count is stated as a fact to re-grep rather than trusted.)
//
// Returning a non-nil empty slice is no longer LOAD-BEARING, and the comment that used
// to say it was has been corrected rather than deleted, because the claim is the kind a
// future reader would otherwise re-derive wrongly. It once mattered: ResolveAgents
// treated nil as "unspecified" and substituted DefaultAgents, so nil here resurrected
// claude in every caller. That fallback is GONE — nil and empty both yield no agents
// through ResolveAgents and SharedDirsFor, and every YOLO_AGENTS encoder on the way out
// (run.jsonDumpsStrings, check.jsonDumpStrings, macosuser.BuildRunPlan) builds its list
// with make(…, len(x)), so nil already serializes as `[]`, not `null`. Non-nil is now
// merely the tidier of two equivalent returns; nothing depends on it.
func SelectedAgents(*jsonx.OrderedMap) []string { return []string{} }

// MergeMiseTools merges config.mise_tools over the defaults. Returns an
// OrderedMap so the result serializes with stable key order: defaults first,
// then config keys not already present, updates keeping position.
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

// MergeMiseDisabledTools combines yolo-managed package managers (pnpm) with
// user-supplied tools (comma/space separated), deduped, comma-joined.
// Non-string userValue is ignored.
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

// grepDefaults / findDefaults supply the default block messages for
// NormalizeBlockedTools. Built as ordered maps so the normalized entries
// serialize in insertion order (name, then merged defaults).
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

// NormalizeBlockedTools turns the security section's blocked_tools (default
// ["grep","find"]) into the list-of-dict form the
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
		// Only reached with an already-validated config, so treat a non-list
		// value as empty.
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

// truthy reports truthiness for the config value types that gate branches
// (gpu.enabled/vaapi): bool, non-empty containers/strings, nonzero numbers.
// nil is falsy.
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

// getListOrNilFalsy returns the list at key, treating a present-but-falsy value
// (nil, empty list) as no list.
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

// getMapOrEmpty returns the map at key when present and truthy, else an empty
// OrderedMap. Returned as any so callers can asMap it.
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

// containsAny reports whether s equals any string element of list (a mixed
// list; non-string elements never match).
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

// removeFirstAny removes the first element equal to string s (only string
// elements can match), preserving order.
func removeFirstAny(list []any, s string) []any {
	for i, v := range list {
		if vs, ok := v.(string); ok && vs == s {
			return append(list[:i:i], list[i+1:]...)
		}
	}
	return list
}
