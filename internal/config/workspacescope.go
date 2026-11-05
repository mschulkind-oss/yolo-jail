package config

// WorkspaceUserScopeOnlyKey reports keys whose complete top-level value is rejected in
// workspace scope by validation. Mixed keys (for example providers and mounts) deliberately
// return false; their field/value-specific scope rules are not represented as whole-key facts.
// Keep this predicate as the adapter seam shared by validation and applied guidance.
func WorkspaceUserScopeOnlyKey(key string) bool {
	switch key {
	case "adapters", "agent_updates", "cache_relocations", "host_apply_on_launch", "host_floor", "host_import_exclusions", "host_management", "host_path", "host_wrappers", "local_clones", "macos_user_shared_tmp", "on_request", "packs", "perf_logging", "profile", "profiles", "programs", "promotion_target", "resource_groups", "setup", "setups", "share_host_fonts", "shipped_skill_prefix", "update_check":
		return true
	default:
		return false
	}
}
