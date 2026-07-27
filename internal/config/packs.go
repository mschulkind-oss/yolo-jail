package config

// packs.go implements the `packs` config key: agent-configuration packs, fetched
// host-side and staged into the jail.
//
// SCOPE, and it is the whole security model of the feature: `packs` is USER-SCOPE
// ONLY, read from paths.UserConfigPath() DIRECTLY rather than from the merged
// config. Workspace scope is therefore inexpressible by construction — not
// validated-against, which is a weaker guarantee that has to be maintained
// correctly forever.
//
// The reason is the same one that retired host_claude_files: a workspace config
// travels with the repo and is agent-editable, so it must not be able to name
// content that enters the jail. A pack can carry skills and briefing prose an agent
// then follows, and (later) surface fragments; that is influence a committed,
// agent-writable file may not have. This mirrors LoadHostFiles' source-bearing half
// exactly (hostfiles.go), which is the only shape of this boundary that has held
// here.
//
// A repo that wants to configure its own agents does not need this key: it already
// has a git repo and can lay out whatever it likes in the workspace. Packs solve
// cross-machine, cross-person distribution, which is inherently user-level. That
// ruling is why there is no workspace half, no `pack_requests`, and no approval
// verb to promote one.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packsKey is the top-level config key.
const packsKey = "packs"

// knownPackKeys is the accepted key set of the object form.
var knownPackKeys = set(
	"source", "name", "agents", "only", "exclude", "allow_exec",
)

// PackEntry is one validated `packs` entry, lowered from either the string (sugar)
// or object form.
//
// The json tags define the YOLO_PACKS wire form (see MarshalPacks): the host CLI
// resolves entries and hands them to the entrypoint through that env var, so the
// entrypoint never re-reads config — the same contract YOLO_HOST_FILES uses, and
// the reason the in-jail side cannot widen the set.
type PackEntry struct {
	// Source is the pack address. Always set. Either a `file://` URL (a local path,
	// the only form phase 0 fetches) or a `git+<transport>://` URL.
	Source string `json:"source"`

	// Name is the pack's short name, used for the staging dir, `yolo pack ls`, and
	// provenance. Defaults to a slug derived from Source when not given.
	Name string `json:"name"`

	// Agents restricts the pack to a subset of the SELECTED agents. Empty means
	// every selected agent, which is the common case and the default.
	Agents []string `json:"agents,omitempty"`

	// Only and Exclude filter the pack tree by glob, applied in that order. `only`
	// is a documented first-line ergonomic, not an escape hatch: "give me just
	// these three skills" is the dominant demand once a shared corpus is large
	// enough that a blanket import stops being trusted.
	Only    []string `json:"only,omitempty"`
	Exclude []string `json:"exclude,omitempty"`

	// AllowExec permits staging files carrying the exec bit. Default false: a pack
	// is CONTENT, and an executable arriving through a content channel is a
	// different trust question than a skill file, so it must be opted into.
	AllowExec bool `json:"allowExec,omitempty"`
}

// Slug returns a filesystem-safe identifier for this pack's staging dir. It reuses
// HostFileEntry.Slug's escaping so the two staging namespaces cannot collide by
// accident and one escaping bug cannot exist in two forms.
func (p PackEntry) Slug() string {
	return HostFileEntry{Path: p.Name}.Slug()
}

// IsLocal reports whether Source is a file:// address, which needs no fetch.
func (p PackEntry) IsLocal() bool {
	return strings.HasPrefix(p.Source, "file://")
}

// LoadPacks returns the validated `packs` entries from the USER config only.
//
// It deliberately takes no merged config: reading the user file directly is what
// makes workspace scope inexpressible (see the file header). Callers pass the merged
// map to nothing here — if a future caller wants to, that is the change to refuse.
//
// A malformed user config is an ERROR, never a silently empty list: dropping a pack
// silently looks exactly like the feature not working, which is the failure this
// plumbing exists to avoid (the same call LoadHostFiles makes).
func LoadPacks(warn Warn) ([]PackEntry, error) {
	if warn == nil {
		warn = func(string) {}
	}
	userPath := paths.UserConfigPath()
	userCfg, err := LoadJSONCWithIncludes(userPath, userPath, true, warn, nil)
	if err != nil {
		return nil, err
	}
	v, present := userCfg.Get(packsKey)
	if !present || v == nil {
		return nil, nil
	}
	entries, problems := checkPacks(v)
	for _, p := range problems {
		warn(p + " — entry skipped")
	}
	return entries, nil
}

// checkPacks validates a raw `packs` value, returning the entries it could lower
// and a problem string per entry it could not. A non-list value yields one problem
// and no entries.
func checkPacks(v any) ([]PackEntry, []string) {
	list, ok := asList(v)
	if !ok {
		return nil, []string{"config." + packsKey + ": expected a list of pack entries"}
	}
	var entries []PackEntry
	var problems []string
	seenName := map[string]int{}
	for i, raw := range list {
		entry, problem := checkPackEntry(raw, fmt.Sprintf("config.%s[%d]", packsKey, i))
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		// A duplicate name would make two packs share a staging dir, so the second
		// would silently overwrite the first.
		if prev, dup := seenName[entry.Name]; dup {
			problems = append(problems, fmt.Sprintf(
				"config.%s[%d]: duplicate pack name %q (already used by entry %d) — "+
					"give one an explicit \"name\"", packsKey, i, entry.Name, prev))
			continue
		}
		seenName[entry.Name] = i
		entries = append(entries, entry)
	}
	return entries, problems
}

// checkPackEntry lowers and validates ONE entry in either form.
func checkPackEntry(raw any, itemPath string) (PackEntry, string) {
	if s, isStr := asStr(raw); isStr {
		return lowerPackSource(s, "", itemPath)
	}
	m, isMap := raw.(*jsonx.OrderedMap)
	if !isMap {
		return PackEntry{}, itemPath + ": expected a source string or an object"
	}
	for _, k := range m.Keys() {
		if _, known := knownPackKeys[k]; !known {
			return PackEntry{}, itemPath + "." + k + ": unknown key"
		}
	}
	rawSource, has := m.Get("source")
	if !has {
		return PackEntry{}, itemPath + ": missing required \"source\""
	}
	sourceStr, ok := asStr(rawSource)
	if !ok {
		return PackEntry{}, itemPath + ".source: expected a string address"
	}
	name := ""
	if rawName, hasName := m.Get("name"); hasName {
		if name, ok = asStr(rawName); !ok {
			return PackEntry{}, itemPath + ".name: expected a string"
		}
	}
	entry, problem := lowerPackSource(sourceStr, name, itemPath)
	if problem != "" {
		return PackEntry{}, problem
	}
	for _, field := range []struct {
		key string
		dst *[]string
	}{{"agents", &entry.Agents}, {"only", &entry.Only}, {"exclude", &entry.Exclude}} {
		rawVal, hasVal := m.Get(field.key)
		if !hasVal || rawVal == nil {
			continue
		}
		items, isList := asList(rawVal)
		if !isList {
			return PackEntry{}, itemPath + "." + field.key + ": expected a list of strings"
		}
		for _, it := range items {
			s, isStr := asStr(it)
			if !isStr {
				return PackEntry{}, itemPath + "." + field.key + ": expected a list of strings"
			}
			*field.dst = append(*field.dst, s)
		}
	}
	// An `agents` entry naming an unknown agent is a typo, and silently staging
	// nothing is the least helpful possible response.
	for _, a := range entry.Agents {
		if _, valid := validAgentSet[a]; !valid {
			return PackEntry{}, fmt.Sprintf("%s.agents: unknown agent %q. Valid agents: %s",
				itemPath, a, joinSorted(validAgentSet))
		}
	}
	if rawExec, hasExec := m.Get("allow_exec"); hasExec && rawExec != nil {
		b, isBool := rawExec.(bool)
		if !isBool {
			return PackEntry{}, itemPath + ".allow_exec: expected true or false"
		}
		entry.AllowExec = b
	}
	return entry, ""
}

// lowerPackSource validates an address and derives the default name.
func lowerPackSource(source, name, itemPath string) (PackEntry, string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return PackEntry{}, itemPath + ".source: must not be empty"
	}
	scheme, _, hasScheme := strings.Cut(source, "://")
	if !hasScheme {
		return PackEntry{}, itemPath + ".source: expected a URL with a scheme, " +
			"e.g. file:///path/to/pack or git+ssh://git@host/org/repo//sub?ref=main"
	}
	switch {
	case scheme == "file":
	case strings.HasPrefix(scheme, "git+"):
	default:
		return PackEntry{}, itemPath + ".source: unsupported scheme " + scheme +
			":// (expected file:// or git+ssh:// / git+https://)"
	}
	if _, err := url.Parse(source); err != nil {
		return PackEntry{}, itemPath + ".source: not a valid URL: " + err.Error()
	}
	if name == "" {
		name = defaultPackName(source)
	}
	if problem := checkPackName(name, itemPath); problem != "" {
		return PackEntry{}, problem
	}
	return PackEntry{Source: source, Name: name}, ""
}

// defaultPackName derives a short name from an address: the last non-empty path
// segment, with any `?ref=` query and a trailing `.git` stripped.
func defaultPackName(source string) string {
	s := source
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	// A git subpath uses `//` to separate repo from directory; the segment after it
	// is the more specific (and more useful) name.
	if i := strings.LastIndex(s, "//"); i > 0 && !strings.HasSuffix(s[:i], ":") {
		if sub := strings.Trim(s[i+2:], "/"); sub != "" {
			s = sub
		}
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// checkPackName rejects a name that would escape or collide in the staging dir.
func checkPackName(name, itemPath string) string {
	switch {
	case name == "":
		return itemPath + ": could not derive a pack name from the source — add an explicit \"name\""
	case name == "." || name == "..":
		return itemPath + ".name: must not be \".\" or \"..\""
	case strings.ContainsAny(name, "/\\:"):
		return itemPath + ".name: must not contain a path separator or ':'"
	}
	return ""
}

// validatePacks reports `packs` problems for `yolo check`.
//
// It reads the USER config, not the merged map, for the same reason LoadPacks does.
// A `packs` key found in the WORKSPACE config is a hard error naming the fix: it is
// not merely ignored, because a silently-inert key looks exactly like a broken
// feature.
func validatePacks(workspace string, errs *[]string) {
	userPath := paths.UserConfigPath()
	if userCfg, err := LoadJSONCWithIncludes(userPath, userPath, false, func(string) {}, nil); err == nil && userCfg != nil {
		if v, present := userCfg.Get(packsKey); present && v != nil {
			_, problems := checkPacks(v)
			for _, p := range problems {
				add(errs, p)
			}
		}
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(packsKey); atWorkspace {
		add(errs, "config."+packsKey+": user-scope only — move it to "+
			"~/.config/yolo-jail/config.jsonc. A workspace config travels with the repo "+
			"and is agent-editable, so it cannot decide which packs stage content "+
			"(skills and briefing prose an agent then follows) into the jail. A repo "+
			"that wants to configure its own agents can just commit the files.")
	}
}

// MarshalPacks renders resolved entries as the compact JSON that travels in
// YOLO_PACKS. Deterministic (entries are already in config order) so an unchanged
// config yields an unchanged argv.
func MarshalPacks(entries []PackEntry) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("marshalling packs for the entrypoint: %w", err)
	}
	return string(b), nil
}

// UnmarshalPacks decodes the YOLO_PACKS wire form. The entrypoint uses this; it
// never reads config, so it cannot widen the set.
func UnmarshalPacks(wire string) ([]PackEntry, error) {
	if strings.TrimSpace(wire) == "" {
		return nil, nil
	}
	var entries []PackEntry
	if err := json.Unmarshal([]byte(wire), &entries); err != nil {
		return nil, fmt.Errorf("decoding YOLO_PACKS: %w", err)
	}
	return entries, nil
}

// PacksForAgent returns the entries that apply to one agent, in config order
// (later entries win on same-named content — the whole mental model).
func PacksForAgent(entries []PackEntry, agent string) []PackEntry {
	var out []PackEntry
	for _, e := range entries {
		if len(e.Agents) == 0 {
			out = append(out, e)
			continue
		}
		if containsString(e.Agents, agent) {
			out = append(out, e)
		}
	}
	return out
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
