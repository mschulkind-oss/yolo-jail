package config

// RenameTransform names a conversion operation for later migration code. Descriptors contain
// data only; they do not convert, alias, or activate any authored spelling.
type RenameTransform uint8

const (
	RenameUnspecified RenameTransform = iota
	RenameKey
	RenameMove
	RenameSplit
	RenameValueRewrite
	RenameFoldWhenEqual
	RenameProfileSelection
	RenameProfileVariant
	RenameUnmatchedFields
	RenameUnrepresentable
	RenameDropDefault
	RenameDropDerived
	RenameDrop
)

// RenameDescriptor describes one old key and its replacement(s) without implementing the
// transformation. Field-sensitive entries intentionally defer value handling to a converter.
type RenameDescriptor struct {
	OldPath      string
	Replacements []string
	Scope        SchemaScope
	Transform    RenameTransform
	TOMLRefusal  string
}

var configRenames = []RenameDescriptor{
	{OldPath: "agents", Replacements: []string{"packs"}, Scope: SchemaScopeUserOnly, Transform: RenameValueRewrite, TOMLRefusal: "Agent selection is now expressed by choosing the packs that install the programs you want; the old agent list is not accepted as an alias."},
	{OldPath: "repo_path", Replacements: nil, Scope: SchemaScopeBoth, Transform: RenameDrop, TOMLRefusal: "This setting was retired; yolo finds its repository from the flake bundle. Remove the key."},
	{OldPath: "agent_profiles", Replacements: []string{"active_provider"}, Scope: SchemaScopeUserOnly, Transform: RenameUnrepresentable, TOMLRefusal: "Profile selection now names a provider or provider/variant under active_provider. Choose the corresponding new provider names; the old profile mapping is not converted automatically."},
	{OldPath: "use_profiles", Replacements: []string{"active_provider"}, Scope: SchemaScopeUserOnly, Transform: RenameUnrepresentable, TOMLRefusal: "Profile selection now names a provider or provider/variant under active_provider. Choose the corresponding new provider names; the old profile mapping is not converted automatically."},
	{OldPath: "macos_log", Replacements: []string{"packs"}, Scope: SchemaScopeBoth, Transform: RenameUnrepresentable, TOMLRefusal: "The macOS log is delivered by the macos-log pack. Select that pack in user-scope packs if it is needed; the old mode cannot be treated as a TOML alias."},
	{OldPath: "mcp_presets", Replacements: []string{"packs", "mcp_servers.*"}, Scope: SchemaScopeFieldSensitive, Transform: RenameUnrepresentable, TOMLRefusal: "MCP server selection is no longer a built-in preset list: select chrome-devtools through packs, or declare a server under mcp_servers. Remove the retired preset key."},
	{OldPath: "host_processes", Replacements: []string{"the host-processes pack"}, Scope: SchemaScopeUserOnly, Transform: RenameDrop, TOMLRefusal: "Host process access is now supplied by the host-processes pack. Select that pack in user-scope packs; the old inline key is not a TOML alias."},
	{OldPath: "packages", Replacements: []string{"install.nix"}, Scope: SchemaScopeBoth, Transform: RenameMove},
	{OldPath: "mise_tools", Replacements: []string{"install.mise"}, Scope: SchemaScopeBoth, Transform: RenameMove},
	{OldPath: "gpu", Replacements: []string{"machine.gpu"}, Scope: SchemaScopeBoth, Transform: RenameMove},
	{OldPath: "resources", Replacements: []string{"machine.resources"}, Scope: SchemaScopeBoth, Transform: RenameMove},
	{OldPath: "network.ports", Replacements: []string{"machine.ports"}, Scope: SchemaScopeBoth, Transform: RenameMove},
	{OldPath: "network.forward_host_ports", Replacements: []string{"machine.forward"}, Scope: SchemaScopeBoth, Transform: RenameMove},
	{OldPath: "network.mode", Replacements: []string{"network.mode"}, Scope: SchemaScopeBoth, Transform: RenameKey},
	{OldPath: "network", Replacements: []string{"network.mode", "machine.ports", "machine.forward"}, Scope: SchemaScopeBoth, Transform: RenameSplit},
	{OldPath: "env_sources", Replacements: []string{"env.files", "env.set", "env.unset"}, Scope: SchemaScopeBoth, Transform: RenameSplit},
	{OldPath: "brokered", Replacements: []string{"allow.*", "requests.*"}, Scope: SchemaScopeFieldSensitive, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.grants.*", Replacements: []string{"allow.*.always", "requests.*"}, Scope: SchemaScopeFieldSensitive, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.grant_kinds", Replacements: []string{"allow.*.on_request"}, Scope: SchemaScopeUserOnly, Transform: RenameMove},
	{OldPath: "brokered.*.repos", Replacements: []string{"requests.github.repos"}, Scope: SchemaScopeWorkspaceOnly, Transform: RenameMove},
	{OldPath: "brokered.*.workspaces", Replacements: []string{"requests.github.repos"}, Scope: SchemaScopeFieldSensitive, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.grants.*.kind", Replacements: []string{"allow.*.always", "allow.*.on_request"}, Scope: SchemaScopeFieldSensitive, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.grants.*.repo", Replacements: []string{"requests.github.repos"}, Scope: SchemaScopeWorkspaceOnly, Transform: RenameMove},
	{OldPath: "brokered.*.grants.*.branch", Replacements: []string{"requests.git.push"}, Scope: SchemaScopeWorkspaceOnly, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.grants.*.tag", Replacements: []string{"requests.git.push_tags"}, Scope: SchemaScopeWorkspaceOnly, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.grants.*.workflow", Replacements: []string{"requests.github.dispatch_workflows"}, Scope: SchemaScopeWorkspaceOnly, Transform: RenameValueRewrite},
	{OldPath: "brokered.*.sets", Replacements: nil, Scope: SchemaScopeUserOnly, Transform: RenameUnrepresentable},
	{OldPath: "brokered.*.sets.*", Replacements: nil, Scope: SchemaScopeUserOnly, Transform: RenameUnrepresentable},
	{OldPath: "profile", Replacements: []string{"active_provider.default", "active_provider.*"}, Scope: SchemaScopeUserOnly, Transform: RenameProfileSelection},
	{OldPath: "profile.*", Replacements: []string{"active_provider.default", "active_provider.*"}, Scope: SchemaScopeUserOnly, Transform: RenameProfileSelection},
	{OldPath: "profiles.*", Replacements: []string{"providers.*", "providers.*.variants.*"}, Scope: SchemaScopeUserOnly, Transform: RenameProfileVariant},
	{OldPath: "profiles.*.provider", Replacements: []string{"providers.*"}, Scope: SchemaScopeUserOnly, Transform: RenameValueRewrite},
	{OldPath: "profiles.*.model", Replacements: []string{"providers.*.model", "providers.*.variants.*.model"}, Scope: SchemaScopeUserOnly, Transform: RenameValueRewrite},
	{OldPath: "profiles.*.via", Replacements: []string{"providers.*.via", "providers.*.variants.*.via"}, Scope: SchemaScopeUserOnly, Transform: RenameValueRewrite},
	{OldPath: "profiles.*.enforce_models", Replacements: []string{"providers.*.variants.*.enforce_models"}, Scope: SchemaScopeUserOnly, Transform: RenameMove},
	{OldPath: "profiles.*.subagents", Replacements: []string{"providers.*.variants.*.subagents"}, Scope: SchemaScopeUserOnly, Transform: RenameMove},
	{OldPath: "profiles.*.*", Replacements: []string{"providers.*.variants.*.*"}, Scope: SchemaScopeUserOnly, Transform: RenameUnmatchedFields},
	{OldPath: "providers.*.base_url", Replacements: []string{"providers.*.endpoints.*.base_url"}, Scope: SchemaScopeUserOnly, Transform: RenameValueRewrite},
	// Shared facts keep their name, a row's pointer at them and a row's sent id theirs
	// (docs/design/model-facts-migration.md MF-D2, MF-D3): identity rows, which the conversion
	// passes through (lower.go's model_facts case respells each entry's facts).
	{OldPath: "model_facts.*", Replacements: []string{"model_facts.*"}, Scope: SchemaScopeUserOnly, Transform: RenameKey},
	{OldPath: "providers.*.models.*.extends", Replacements: []string{"providers.*.models.*.extends"}, Scope: SchemaScopeBoth, Transform: RenameKey},
	{OldPath: "providers.*.models.*.wire_id", Replacements: []string{"providers.*.models.*.wire_id"}, Scope: SchemaScopeBoth, Transform: RenameKey},
	{OldPath: "providers.*.models.*.context_window", Replacements: []string{"providers.*.models.*.context", "providers.*.every_model.context"}, Scope: SchemaScopeBoth, Transform: RenameFoldWhenEqual},
	{OldPath: "providers.*.models.*.input", Replacements: []string{"providers.*.models.*.accepts", "providers.*.every_model.accepts"}, Scope: SchemaScopeBoth, Transform: RenameFoldWhenEqual},
	{OldPath: "providers.*.models.*.cost", Replacements: []string{"providers.*.models.*.price", "providers.*.every_model.price"}, Scope: SchemaScopeBoth, Transform: RenameFoldWhenEqual},
	// A row keyed by a name says its id with `model`; one keyed by its id says nothing (MF-D1).
	{OldPath: "providers.*.models.*.id", Replacements: []string{"providers.*.models.*.model"}, Scope: SchemaScopeBoth, Transform: RenameKey},
	{OldPath: "providers.*.models.*.vendor", Replacements: []string{"providers.*.models.<id>"}, Scope: SchemaScopeBoth, Transform: RenameDropDerived},
	{OldPath: "providers.*.models.*.max_tokens", Replacements: []string{"providers.*.models.*.max_tokens", "providers.*.every_model.max_tokens"}, Scope: SchemaScopeBoth, Transform: RenameFoldWhenEqual},
	{OldPath: "providers.*.models.*.reasoning", Replacements: []string{"providers.*.models.*.reasoning", "providers.*.every_model.reasoning"}, Scope: SchemaScopeBoth, Transform: RenameFoldWhenEqual},

	{OldPath: "loopholes.*.enabled", Replacements: []string{"off"}, Scope: SchemaScopeFieldSensitive, Transform: RenameValueRewrite},
	{OldPath: "loopholes.*.settings.*", Replacements: []string{"allow.*.*"}, Scope: SchemaScopeFieldSensitive, Transform: RenameValueRewrite},
	{OldPath: "journal", Replacements: []string{"packs"}, Scope: SchemaScopeUserOnly, Transform: RenameUnrepresentable, TOMLRefusal: "Journal access is now supplied by the journal pack. Select that pack in user-scope packs; the old mode is not a TOML alias."},
}

// RenameDescriptors returns a detached copy of the migration metadata in declaration order.
func RenameDescriptors() []RenameDescriptor {
	out := make([]RenameDescriptor, len(configRenames))
	for i, d := range configRenames {
		out[i] = d
		out[i].Replacements = append([]string(nil), d.Replacements...)
	}
	return out
}
