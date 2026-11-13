package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// validateActiveProvider is the launch's check of `active_provider`, the selection key a readable
// user config writes (OQ-RC14; docs/reference/config/user-config.md#which-provider-each-agent-runs-on). The key is user-scope only (its
// schema rule, SchemaScopeUserOnly), so a TOML project file is refused by its own reader; this
// refuses the old-format workspace file, which has no schema. It also refuses the key beside the
// legacy `profile` key (the consumer boundary's own words, ConfigProviderConsumer) and a value
// that is not a table of agent name, or `default`, to a provider selector or a list of them.
// Whether a selector names a declared provider is the launch channel's check
// (checkProfileDeclarations), which knows the selected packs.
func validateActiveProvider(cfg *jsonx.OrderedMap, workspace string, errs *[]string) {
	if _, present := cfg.Get(ActiveProviderKey); !present {
		return
	}
	if _, err := ConfigActiveProviderSelection(cfg); err != nil {
		add(errs, err.Error()+". Write it as a table: [active_provider] with default = \"<provider>\" "+
			"and one <agent> = \"<provider>\" line per agent that differs, or run "+
			"`yolo config set active_provider.default <provider> --user`.")
	}
	if table, ok := activeProviderTable(cfg); ok {
		if _, star := table.Get("*"); star {
			add(errs, "config.active_provider.*: \"*\" names no agent. It was the old `profile` table's "+
				"every-agent key; write `default` instead (`yolo active-provider default <provider>`).")
		}
	}
	if _, legacy := cfg.Get(ProfileKey); legacy {
		add(errs, activeProviderWithProfileProblem+". Remove `profile`, or run `yolo config migrate` to "+
			"convert the old file.")
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace)
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(ActiveProviderKey); atWorkspace {
		userFile, _ := UserConfigFile()
		add(errs, "config."+ActiveProviderKey+": user-scope only — which provider each agent runs on "+
			"is your choice, not a project's. Move it to "+userFile+" (`yolo config set "+
			"active_provider.<agent> <provider> --user`), or remove it from the workspace config.")
	}
}

func activeProviderTable(cfg *jsonx.OrderedMap) (*jsonx.OrderedMap, bool) {
	raw, _ := cfg.Get(ActiveProviderKey)
	table, ok := raw.(*jsonx.OrderedMap)
	return table, ok
}
