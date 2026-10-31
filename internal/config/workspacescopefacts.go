package config

import (
	"sort"
	"strings"
)

// WorkspaceScopeRestriction is a descriptive fact about the legacy JSONC workspace
// validator. Path uses config-key spellings and `*` for arbitrary map keys or list items;
// When describes the value condition where a rule is conditional. These facts do not
// participate in validation, and do not describe the future readable-config vocabulary.
type WorkspaceScopeRestriction struct {
	Path string
	When string
}

// legacyNullAcceptedWholeWorkspaceScopeKeys records which current whole-key validators
// ignore for scope when their value is null. Whole-key membership still comes from SchemaKeys
// and WorkspaceUserScopeOnlyKey; this set only supplies descriptive When values.
var legacyNullAcceptedWholeWorkspaceScopeKeys = map[string]struct{}{
	"agent_updates":         {},
	"host_apply_on_launch":  {},
	"host_floor":            {},
	"host_management":       {},
	"host_path":             {},
	"host_wrappers":         {},
	"local_clones":          {},
	"macos_user_mode":       {},
	"macos_user_shared_tmp": {},
	"perf_logging":          {},
	"promotion_target":      {},
	"share_host_fonts":      {},
	"shipped_skill_prefix":  {},
	"update_check":          {},
}

// legacyMixedWorkspaceScopeRestrictions records the existing value-sensitive legacy rules
// enforced by the deciders named in each row. Whole-key rows are derived above, not duplicated.
var legacyMixedWorkspaceScopeRestrictions = []WorkspaceScopeRestriction{
	{Path: "config.brokered.*.grant_kinds"},
	{Path: "config.brokered.*.sets"},
	{Path: "config.env_sources[*]", When: "file entry resolves outside the workspace"},
	{Path: "config.host_files[*]", When: "entry names a host source (including string shorthand)"},
	{Path: "config.loopholes.*", When: "entry declares a command (install)"},
	{Path: "config.loopholes.*.doctor_cmd", When: "entry is not backed by a manifest"},
	{Path: "config.loopholes.*.env"},
	{Path: "config.loopholes.*.settings.*", When: "manifest declares the setting user-scope"},
	{Path: "config.mounts[*]", When: "mount is read-write"},
	{Path: "config.providers", When: "providers value is null"},
	{Path: "config.providers.*", When: "provider value is null"},
	{Path: "config.providers.*.api_key_env_name"},
	{Path: "config.providers.*.endpoints"},
	{Path: "config.providers.*.endpoints.*.base_url"},
	{Path: "config.providers.*.platform"},
}

// WorkspaceScopeRestrictions returns a fresh, deterministic description of the current
// legacy JSONC workspace-scope rules. Whole-key restrictions are selected from the actual
// legacy schema census only when its user-only scope agrees with the validator predicate;
// mixed-key facts are the audited conditional deciders above. Validation never reads this
// list, so changing descriptive metadata cannot grant or remove permission.
func WorkspaceScopeRestrictions() []WorkspaceScopeRestriction {
	out := make([]WorkspaceScopeRestriction, 0, len(legacyMixedWorkspaceScopeRestrictions))
	for _, key := range SchemaKeys() {
		if key.Scope != SchemaScopeUserOnly || strings.ContainsAny(key.Path, ".*") || !WorkspaceUserScopeOnlyKey(key.Path) {
			continue
		}
		when := ""
		if _, nullAllowed := legacyNullAcceptedWholeWorkspaceScopeKeys[key.Path]; nullAllowed {
			when = "value is non-null"
		}
		out = append(out, WorkspaceScopeRestriction{Path: "config." + key.Path, When: when})
	}
	out = append(out, legacyMixedWorkspaceScopeRestrictions...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].When < out[j].When
		}
		return out[i].Path < out[j].Path
	})
	unique := out[:0]
	for _, rule := range out {
		if len(unique) > 0 && unique[len(unique)-1] == rule {
			continue
		}
		unique = append(unique, rule)
	}
	return append([]WorkspaceScopeRestriction(nil), unique...)
}
