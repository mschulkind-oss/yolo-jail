package configmigrate

import (
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// legacyLowering is how a legacy spelling reaches TOML (RC-D50: piecemeal migration always
// works, so every legacy key needs a rule).
type legacyLowering string

const (
	// loweredAtRead: every read of a legacy file lowers it (config.projectLegacyB2Config), so a
	// TOML file and a legacy file merge in layer order and a mixed config crosses into a jail as
	// TOML; `yolo config migrate` writes the same spelling.
	loweredAtRead legacyLowering = "lowered at read and by migrate"
	// loweredByMigrate: `yolo config migrate` converts it exactly, and a read keeps the legacy
	// spelling. A mixed config holding one crosses into a jail as the migration's lowering of it
	// (LowerInheritedUserScope, RC-D55), and the launch refuses naming `yolo config migrate`
	// (config.mixedFormatRefusal) only where that conversion declines, as migrate does.
	loweredByMigrate legacyLowering = "lowered by migrate only"
	// retired: the key means nothing any more, in either spelling; a legacy file holding one is
	// refused with its own retirement message, so there is nothing to lower.
	retired legacyLowering = "retired"
)

// legacyLowerings classifies every rename descriptor (config.RenameDescriptors).
var legacyLowerings = map[string]legacyLowering{
	"agents": retired, "repo_path": retired, "agent_profiles": retired, "use_profiles": retired,
	"macos_log": retired, "mcp_presets": retired, "host_processes": retired, "journal": retired,

	"packages": loweredAtRead, "mise_tools": loweredAtRead, "gpu": loweredAtRead,
	"resources": loweredAtRead, "network.ports": loweredAtRead,
	"network.forward_host_ports": loweredAtRead, "network.mode": loweredAtRead,
	"network": loweredAtRead, "env_sources": loweredAtRead,

	"brokered": loweredByMigrate, "brokered.*.grants.*": loweredByMigrate,
	"brokered.*.grant_kinds": loweredByMigrate, "brokered.*.repos": loweredByMigrate,
	"brokered.*.workspaces": loweredByMigrate, "brokered.*.grants.*.kind": loweredByMigrate,
	"brokered.*.grants.*.repo": loweredByMigrate, "brokered.*.grants.*.branch": loweredByMigrate,
	"brokered.*.grants.*.tag": loweredByMigrate, "brokered.*.grants.*.actor": loweredByMigrate, "brokered.*.grants.*.workflow": loweredByMigrate,
	"brokered.*.sets": loweredByMigrate, "brokered.*.sets.*": loweredByMigrate,
	"profile": loweredByMigrate, "profile.*": loweredByMigrate, "profiles.*": loweredByMigrate,
	"profiles.*.provider": loweredByMigrate, "profiles.*.model": loweredByMigrate,
	"profiles.*.via": loweredByMigrate, "profiles.*.enforce_models": loweredByMigrate,
	"profiles.*.subagents": loweredByMigrate, "profiles.*.*": loweredByMigrate,
	"providers.*.base_url": loweredByMigrate, "model_facts.*": loweredByMigrate,
	"providers.*.models.*.extends": loweredByMigrate, "providers.*.models.*.wire_id": loweredByMigrate,
	"providers.*.models.*.context_window": loweredByMigrate, "providers.*.models.*.input": loweredByMigrate,
	"providers.*.models.*.cost": loweredByMigrate, "providers.*.models.*.id": loweredByMigrate,
	"providers.*.models.*.vendor": loweredByMigrate, "providers.*.models.*.max_tokens": loweredByMigrate,
	"providers.*.models.*.reasoning": loweredByMigrate,
	"loopholes.*.enabled":            loweredByMigrate, "loopholes.*.settings.*": loweredByMigrate,
}

// Every legacy key has a lowering rule. Fails when a rename descriptor is added without one, when
// a classification names a descriptor that no longer exists, and when a live legacy top-level key
// is neither spelled the same in TOML nor described by a rename.
func TestEveryLegacyKeyHasALoweringRule(t *testing.T) {
	described := map[string]bool{}
	for _, d := range config.RenameDescriptors() {
		described[d.OldPath] = true
		rule, ok := legacyLowerings[d.OldPath]
		if !ok {
			t.Errorf("legacy key %q has no lowering rule: lower it at read (projectLegacyB2Config) "+
				"and in migrate, or in migrate alone, and classify it here (RC-D50)", d.OldPath)
			continue
		}
		if rule == retired && d.TOMLRefusal == "" {
			t.Errorf("legacy key %q is classified retired, but its descriptor gives no retirement message", d.OldPath)
		}
	}
	var stale []string
	for path := range legacyLowerings {
		if !described[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("classified keys no rename descriptor names: %s", strings.Join(stale, ", "))
	}
	keys := config.ReadableSchemaKeys()
	for _, key := range config.TopLevelConfigKeys() {
		if described[key] || schemaNames(keys, []string{key}) {
			continue
		}
		within := false
		for path := range described {
			within = within || strings.HasPrefix(path, key+".")
		}
		if within {
			continue
		}
		t.Errorf("live legacy key %q has no TOML spelling and no rename descriptor, so nothing lowers it", key)
	}
}
