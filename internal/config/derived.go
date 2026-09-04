package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// PlatformLinux and PlatformDarwin are the values a package entry's `platforms`
// list may carry. They are Go GOOS spellings rather than nix system doubles
// ("x86_64-linux"), because the fact being declared is about the PLATFORM and not
// the architecture — an entry marked linux-only is linux-only on both arm64 and
// amd64 — and because darwinpkg.NativeSystem already derives the nix double from
// GOOS/GOARCH, so the config surface never has to name one.
const (
	PlatformLinux  = "linux"
	PlatformDarwin = "darwin"
)

// EffectivePackages returns config `packages` for `platform`, plus gpu.vaapi-implied
// extras (mesa, libva-utils) when gpu is AMD + enabled + vaapi. Returns a copy; does
// not mutate config. Order is package order then the appended extras (skipping
// any already present). Extras are strings and dedup compares against all
// package entries; a string never equals a dict entry, so string-only
// comparison is correct.
//
// PLATFORM FILTERING happens HERE, before anything materializes, which is the whole
// design of it (A2 piece 2). An entry whose object form carries `platforms` is dropped
// when the target is not in that list — so nix never evaluates it, never reports it
// skipped, and the aggregated "no build for this platform" error downstream can treat
// everything STILL missing as a genuine problem. Filtering after the build instead
// would mean maintaining a second list of "absences that are fine", which is the
// bookkeeping this avoids.
//
// A STRING ENTRY IS EVERY PLATFORM, and an object with no `platforms` likewise — so
// every config written before this existed means exactly what it meant before.
//
// `platform` is a GOOS value; an empty one disables filtering entirely, which is what
// a caller that genuinely wants the declared list (a config dump, a diff) wants.
func EffectivePackages(config *jsonx.OrderedMap, platform string) []any {
	packages := filterPackagesForPlatform(
		listCopy(getListOrNilFalsy(config, "packages")), platform)

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
	m.Set("replacement", "rg")
	m.Set("block_flags", []any{"--recursive", "-r", "-R", "-*[rR]*"})
	return m
}

func findDefaults() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("message", "find is blocked to prevent unintended recursive searches. Use fd for a faster, more intuitive alternative.")
	m.Set("suggestion", "Try: fd <pattern>")
	m.Set("replacement", "fd")
	return m
}

// NormalizeBlockedTools turns the security section's blocked_tools (default
// ["grep","find"]) into the list-of-dict form the
// entrypoint consumes. String entries get default_messages merged in; dict
// entries (with "name") merge defaults-under-user.
// NormalizeBlockedToolsWith is NormalizeBlockedTools plus the packs' own declarations.
//
// PACK ENTRIES COME FIRST and a user entry of the same name REPLACES one, because the
// user config is the more specific statement: someone who wrote their own `grep` block
// meant it to be theirs, not to be merged field-by-field with a pack's. Replacement
// rather than merge also keeps the result explainable — every emitted record came
// wholly from one place.
func NormalizeBlockedToolsWith(securitySection *jsonx.OrderedMap, packTools []packload.BlockedTool) []any {
	userNames := map[string]bool{}
	for _, e := range NormalizeBlockedTools(securitySection) {
		if m, ok := asMap(e); ok {
			if n, ok := asStr(getOr(m, "name", nil)); ok {
				userNames[n] = true
			}
		}
	}
	out := make([]any, 0, len(packTools))
	for _, t := range packTools {
		if userNames[t.Name] {
			continue // the user said it themselves; theirs wins whole
		}
		m := jsonx.NewOrderedMap()
		m.Set("name", t.Name)
		if t.Message != "" {
			m.Set("message", t.Message)
		}
		if t.Suggestion != "" {
			m.Set("suggestion", t.Suggestion)
		}
		if t.Replacement != "" {
			m.Set("replacement", t.Replacement)
		}
		if len(t.AllowFlags) > 0 {
			allow := make([]any, 0, len(t.AllowFlags))
			for _, f := range t.AllowFlags {
				allow = append(allow, f)
			}
			m.Set("allow_flags", allow)
		}
		if len(t.Flags) > 0 {
			flags := make([]any, 0, len(t.Flags))
			for _, f := range t.Flags {
				flags = append(flags, f)
			}
			m.Set("block_flags", flags)
		}
		out = append(out, m)
	}
	return append(out, NormalizeBlockedTools(securitySection)...)
}

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

// defaultBlockedList is EMPTY, and that is the 2026-09-04 ruling.
//
// It returned ["grep","find"] until then, and the default carried an assumption it
// could not state: that the image bakes `rg` and `fd`. True of the container backends,
// false of macos-user, which bakes nothing — so on the first working Mac launch the
// shims were generated, `grep -r` exited 127, and the suggestion named a binary that
// did not exist. A default that depends on an image is not a default, it is one
// backend's policy applied to all of them.
//
// The list moved to the `guardrails` pack, where the thing that blocks a tool is also
// the thing that can require its replacement, and selecting it is the opt-in. Core
// blocks nothing on its own; a user's own `security.blocked_tools` still works exactly
// as before and is unaffected by any of this.
func defaultBlockedList() []any { return []any{} }

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

// filterPackagesForPlatform drops entries whose `platforms` list excludes `platform`.
// An empty platform means "no filtering" (see EffectivePackages).
func filterPackagesForPlatform(packages []any, platform string) []any {
	if platform == "" {
		return packages
	}
	out := make([]any, 0, len(packages))
	for _, p := range packages {
		if declared, ok := packagePlatforms(p); ok && !containsStr(declared, platform) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// packagePlatforms returns an entry's declared `platforms` list, and whether it
// declared one at all. A string entry never declares one.
func packagePlatforms(entry any) ([]string, bool) {
	m, ok := asMap(entry)
	if !ok {
		return nil, false
	}
	raw, present := m.Get("platforms")
	if !present {
		return nil, false
	}
	list, ok := asList(raw)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := asStr(v); ok {
			out = append(out, s)
		}
	}
	return out, true
}

// PackagesExcludedOn returns the NAMES of entries `platforms` excludes on this
// platform — the set whose absence from a native build is EXPECTED rather than an
// error.
//
// It exists so the two halves of A2 read the same declaration: EffectivePackages
// drops these before the build, and the aggregated error consults this to explain
// why a name the user wrote is not present. Deriving the second list from the first
// by subtraction would work today and silently stop working the moment anything else
// filters a package.
func PackagesExcludedOn(config *jsonx.OrderedMap, platform string) []string {
	var out []string
	for _, p := range getListOrNilFalsy(config, "packages") {
		declared, ok := packagePlatforms(p)
		if !ok || containsStr(declared, platform) {
			continue
		}
		if m, ok := asMap(p); ok {
			if name, ok := asStr(getOr(m, "name", nil)); ok {
				out = append(out, name)
			}
		}
	}
	return out
}
