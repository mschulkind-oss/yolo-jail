package config

import (
	"sort"
	"strings"
)

// SchemaScope describes which authored config scope may contain a key. It is descriptive
// metadata only; validation remains the authority for mixed and value-sensitive keys.
type SchemaScope uint8

const (
	SchemaScopeBoth SchemaScope = iota + 1
	SchemaScopeUserOnly
	SchemaScopeWorkspaceOnly
	SchemaScopeFieldSensitive
)

// SchemaType is the existing schema's value-shape category. It is not a decoder.
type SchemaType uint8

const (
	SchemaTypeAny SchemaType = iota + 1
	SchemaTypeObject
	SchemaTypeString
	SchemaTypeBoolean
	SchemaTypeNumber
	SchemaTypeList
	SchemaTypeObjectOrList
	SchemaTypeMixed
)

// SchemaKey describes one concrete or wildcard path in a config vocabulary. Wildcard path
// components name arbitrary keys/items; they are never literal accepted keys.
type SchemaKey struct {
	Path    string
	Scope   SchemaScope
	Type    SchemaType
	Removal RemovalPolicy
}

// SchemaKeys returns the deterministic legacy JSONC path census, including retired keys kept
// alive for targeted refusals. It does not enable TOML reads or alter legacy validation.
func SchemaKeys() []SchemaKey {
	paths := map[string]struct{}{}
	add := func(path string) { paths[path] = struct{}{} }
	addSet := func(prefix string, keys map[string]struct{}) {
		for key := range keys {
			add(prefix + "." + key)
		}
	}
	for path := range knownTopLevelConfigKeys {
		add(path)
	}
	for prefix, fields := range map[string]map[string]struct{}{
		"network": knownNetworkKeys, "security": knownSecurityKeys,
		"security.blocked_tools.*": knownBlockedToolKeys, "packages.*": knownPackageKeys,
		"lsp_servers.*": knownLSPServerKeys, "mcp_servers.*": knownMCPServerKeys,
		"providers.*": knownProviderKeys, "providers.*.endpoints.*": knownEndpointKeys,
		"providers.*.models.*": knownModelKeys, "providers.*.models.*.cost": knownModelCostKeys,
		"model_facts.*": knownSharedModelKeys, "devices.*": knownDeviceKeys,
		"resources": knownResourcesKeys, "loopholes.*": knownHostServiceKeys,
		"loopholes.*.settings": knownLoopholeOverrideKeys, "profiles.*": knownProfileKeys,
		"brokered.*": knownBrokeredSourceKeys, "brokered.*.grants.*": knownBrokeredGrantKeys,
		"brokered.*.sets.*": knownBrokeredSetKeys, "programs": knownProgramsKeys,
		"programs.overrides.*": knownProgramOverrideKeys, "host_files.*": knownHostFileKeys,
		"gpu": knownGPUKeys, "prune": knownPruneKeys,
	} {
		addSet(prefix, fields)
	}
	for key := range mountObjectKeys {
		add("mounts.*." + key)
	}
	for _, path := range []string{
		"mounts.*", "network.ports.*", "network.forward_host_ports.*", "env_sources.*",
		"env_sources.*.*", "mise_tools.*", "lsp_servers.*.args.*", "mcp_servers.*.args.*",
		"mcp_servers.*.env.*", "providers.*.options.*", "providers.*.models.*.input.*",
		"providers.*.models.*", "mcp_servers.*", "profiles.*", "profiles.*.*",
		"profiles.*.model", "profiles.*.subagents.default", "profiles.*.subagents.roles.*",
		"profile.*", "profile.*.*", "brokered.*.grants.*", "brokered.*.grants.*.*",
		"brokered.*.repos.*", "brokered.*.sets.*.admit.*", "loopholes.*.settings.*",
		"loopholes.*.env.*", "host_files.*", "host_wrappers.*", "programs.*",
		"programs.overrides.*", "cache_relocations.*", "writable_home_dirs.*",
		"include_if_found.*", "required_capabilities.*", "adapters.*", "local_clones.*",
		"host_import_exclusions.*",
		"packages.*.outputs.*", "packages.*.platforms.*",
	} {
		add(path)
	}
	return schemaKeys(paths, legacySchemaScope, legacySchemaType, removalPolicy)
}

// ReadableSchemaKeys returns the canonical vocabulary proposed by the accepted readable-config
// design. It is metadata for later consumers, not a reader switch: old spellings are not
// accepted as aliases by this declaration.
func ReadableSchemaKeys() []SchemaKey {
	paths := map[string]SchemaKey{}
	add := func(path string, scope SchemaScope, typ SchemaType) {
		if strings.HasPrefix(path, "providers") {
			scope = readableProviderScope(path)
		}
		paths[path] = SchemaKey{Path: path, Scope: scope, Type: typ, Removal: readableRemovalPolicy(path)}
	}
	// Keep unchanged legacy paths in the canonical schema, and omit only the paths whose
	// accepted redesign explicitly gives them a new name or representation.
	removedRoots := map[string]bool{}
	// `model_facts` is spelled anew below (docs/design/model-facts-migration.md MF-D2): its entries
	// take the row fact spellings, so none of its legacy paths carry over.
	for _, root := range []string{"packages", "mise_tools", "gpu", "resources", "env_sources", "brokered", "profile", "profiles", "loopholes", "model_facts"} {
		removedRoots[root] = true
	}
	for _, retired := range RetiredConfigKeys() {
		removedRoots[retired] = true
	}
	for _, legacy := range SchemaKeys() {
		root := strings.SplitN(legacy.Path, ".", 2)[0]
		if removedRoots[root] || root == "providers" && (legacy.Path == "providers" || strings.HasPrefix(legacy.Path, "providers.*.models")) {
			continue
		}
		if legacy.Path == "network.ports" || legacy.Path == "network.ports.*" ||
			legacy.Path == "network.forward_host_ports" || legacy.Path == "network.forward_host_ports.*" ||
			legacy.Path == "providers.*.base_url" {
			continue
		}
		scope := legacy.Scope
		if root == "providers" {
			scope = readableProviderScope(legacy.Path)
		}
		add(legacy.Path, scope, legacy.Type)
	}
	// Register rename destinations first so the readable schema is built from the same source
	// table migration and refusal code will consume. Explicit entries below add settled fields
	// and refine field-level scope/type metadata where a whole old key was mixed.
	for _, rename := range RenameDescriptors() {
		for _, path := range rename.Replacements {
			if strings.Contains(path, ".") && !strings.ContainsAny(path, " <>\n") {
				add(path, rename.Scope, SchemaTypeAny)
			}
		}
	}
	both, user, workspace, mixed := SchemaScopeBoth, SchemaScopeUserOnly, SchemaScopeWorkspaceOnly, SchemaScopeFieldSensitive
	entries := []SchemaKey{
		{Path: "devices", Scope: both, Type: SchemaTypeList},
		{Path: "devices.*", Scope: both, Type: SchemaTypeMixed},
		{Path: "devices.*.usb", Scope: both, Type: SchemaTypeString},
		{Path: "devices.*.description", Scope: both, Type: SchemaTypeString},
		{Path: "devices.*.cgroup_rule", Scope: both, Type: SchemaTypeString},
		{Path: "packs", Scope: user, Type: SchemaTypeList},
		{Path: "packs.*", Scope: user, Type: SchemaTypeMixed},
		{Path: "packs.*.git", Scope: user, Type: SchemaTypeString},
		{Path: "packs.*.ref", Scope: user, Type: SchemaTypeString},
		{Path: "pack_sources", Scope: user, Type: SchemaTypeObject},
		{Path: "pack_sources.*", Scope: user, Type: SchemaTypeMixed},
		{Path: "pack_sources.*.git", Scope: user, Type: SchemaTypeString},
		{Path: "pack_sources.*.ref", Scope: user, Type: SchemaTypeString},
		{Path: "install", Scope: both, Type: SchemaTypeObject},
		{Path: "install.nix", Scope: both, Type: SchemaTypeList},
		{Path: "install.nix.*", Scope: both, Type: SchemaTypeMixed},
		{Path: "install.nix.*.name", Scope: both, Type: SchemaTypeString},
		{Path: "install.nix.*.nixpkgs", Scope: both, Type: SchemaTypeString},
		{Path: "install.nix.*.version", Scope: both, Type: SchemaTypeString},
		{Path: "install.nix.*.url", Scope: both, Type: SchemaTypeString},
		{Path: "install.nix.*.hash", Scope: both, Type: SchemaTypeString},
		{Path: "install.nix.*.outputs", Scope: both, Type: SchemaTypeList},
		{Path: "install.nix.*.outputs.*", Scope: both, Type: SchemaTypeString},
		{Path: "install.nix.*.platforms", Scope: both, Type: SchemaTypeList},
		{Path: "install.nix.*.platforms.*", Scope: both, Type: SchemaTypeString},
		{Path: "install.mise", Scope: both, Type: SchemaTypeObject},
		{Path: "install.mise.*", Scope: both, Type: SchemaTypeString},
		{Path: "machine", Scope: both, Type: SchemaTypeObject},
		{Path: "machine.gpu", Scope: both, Type: SchemaTypeMixed},
		{Path: "machine.gpu.enabled", Scope: both, Type: SchemaTypeBoolean},
		{Path: "machine.gpu.devices", Scope: both, Type: SchemaTypeString},
		{Path: "machine.gpu.capabilities", Scope: both, Type: SchemaTypeString},
		{Path: "machine.gpu.vendor", Scope: both, Type: SchemaTypeString},
		{Path: "machine.gpu.mode", Scope: both, Type: SchemaTypeString},
		{Path: "machine.gpu.hsa_override_gfx_version", Scope: both, Type: SchemaTypeString},
		{Path: "machine.gpu.seccomp_unconfined", Scope: both, Type: SchemaTypeBoolean},
		{Path: "machine.gpu.vaapi", Scope: both, Type: SchemaTypeBoolean},
		{Path: "machine.resources.cpus", Scope: both, Type: SchemaTypeMixed},
		{Path: "machine.resources.io", Scope: both, Type: SchemaTypeMixed},
		{Path: "machine.resources.io.priority", Scope: both, Type: SchemaTypeString},
		{Path: "machine.resources.io.*", Scope: both, Type: SchemaTypeAny},
		// A project may request one; validateMountScope judges it against allow.mounts (RC-D24).
		{Path: "mounts.*.write", Scope: mixed, Type: SchemaTypeBoolean},
		{Path: "machine.resources", Scope: both, Type: SchemaTypeObject},
		{Path: "machine.resources.memory", Scope: both, Type: SchemaTypeString},
		{Path: "machine.resources.pids_limit", Scope: both, Type: SchemaTypeNumber},
		{Path: "machine.ports", Scope: both, Type: SchemaTypeList},
		{Path: "machine.ports.*", Scope: both, Type: SchemaTypeObject},
		{Path: "machine.ports.*.host", Scope: both, Type: SchemaTypeNumber},
		{Path: "machine.ports.*.jail", Scope: both, Type: SchemaTypeNumber},
		{Path: "machine.forward", Scope: both, Type: SchemaTypeList},
		{Path: "machine.forward.*", Scope: both, Type: SchemaTypeObject},
		{Path: "machine.forward.*.host", Scope: both, Type: SchemaTypeNumber},
		{Path: "machine.forward.*.jail", Scope: both, Type: SchemaTypeNumber},
		{Path: "env", Scope: both, Type: SchemaTypeObject},
		{Path: "env.files", Scope: both, Type: SchemaTypeList},
		{Path: "env.files.*", Scope: both, Type: SchemaTypeString},
		{Path: "env.set", Scope: both, Type: SchemaTypeObject},
		{Path: "env.set.*", Scope: both, Type: SchemaTypeString},
		{Path: "env.unset", Scope: both, Type: SchemaTypeList},
		{Path: "env.unset.*", Scope: both, Type: SchemaTypeString},
		{Path: "allow", Scope: user, Type: SchemaTypeObject},
		{Path: "allow.*", Scope: user, Type: SchemaTypeObject},
		{Path: "allow.*.always", Scope: user, Type: SchemaTypeList},
		{Path: "allow.*.always.*", Scope: user, Type: SchemaTypeString},
		{Path: "allow.*.on_request", Scope: user, Type: SchemaTypeList},
		{Path: "allow.*.on_request.*", Scope: user, Type: SchemaTypeString},
		{Path: "allow.*.*", Scope: user, Type: SchemaTypeAny},
		{Path: "requests", Scope: workspace, Type: SchemaTypeObject},
		{Path: "requests.*", Scope: workspace, Type: SchemaTypeObject},
		{Path: "requests.git.push", Scope: workspace, Type: SchemaTypeList},
		{Path: "requests.git.push.*", Scope: workspace, Type: SchemaTypeString},
		{Path: "requests.git.new_branches", Scope: workspace, Type: SchemaTypeBoolean},
		{Path: "requests.git.push_tags", Scope: workspace, Type: SchemaTypeList},
		{Path: "requests.git.push_tags.*", Scope: workspace, Type: SchemaTypeString},
		{Path: "requests.*.repos", Scope: workspace, Type: SchemaTypeList},
		{Path: "requests.*.repos.*", Scope: workspace, Type: SchemaTypeString},
		{Path: "requests.github.repos", Scope: workspace, Type: SchemaTypeList},
		{Path: "requests.github.repos.*", Scope: workspace, Type: SchemaTypeString},
		{Path: "requests.github.create_pr", Scope: workspace, Type: SchemaTypeBoolean},
		{Path: "requests.github.create_issue", Scope: workspace, Type: SchemaTypeBoolean},
		{Path: "requests.github.dispatch_workflows", Scope: workspace, Type: SchemaTypeList},
		{Path: "requests.github.dispatch_workflows.*", Scope: workspace, Type: SchemaTypeString},
		{Path: "active_provider", Scope: user, Type: SchemaTypeObject},
		{Path: "active_provider.default", Scope: user, Type: SchemaTypeMixed},
		{Path: "active_provider.*", Scope: user, Type: SchemaTypeMixed},
		{Path: "active_provider.default.*", Scope: user, Type: SchemaTypeString},
		{Path: "active_provider.*.*", Scope: user, Type: SchemaTypeString},
		{Path: "providers", Scope: mixed, Type: SchemaTypeObject},
		{Path: "providers.*", Scope: mixed, Type: SchemaTypeMixed},
		{Path: "providers.*.alias", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.region", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.model", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.via", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.variants", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.variants.*", Scope: user, Type: SchemaTypeMixed},
		{Path: "providers.*.variants.*.model", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.variants.*.via", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.variants.*.capabilities", Scope: user, Type: SchemaTypeList},
		{Path: "providers.*.variants.*.capabilities.*", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.variants.*.enforce_models", Scope: user, Type: SchemaTypeBoolean},
		{Path: "providers.*.variants.*.subagents", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.variants.*.subagents.default", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.variants.*.subagents.roles", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.variants.*.subagents.roles.*", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.variants.*.*", Scope: user, Type: SchemaTypeAny},
		{Path: "providers.*.every_model", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.every_model.api", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.every_model.thinking", Scope: both, Type: SchemaTypeList},
		{Path: "providers.*.every_model.thinking.*", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.every_model.accepts", Scope: both, Type: SchemaTypeList},
		{Path: "providers.*.every_model.accepts.*", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.every_model.max_tokens", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.reasoning", Scope: both, Type: SchemaTypeBoolean},
		{Path: "providers.*.every_model.context", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.base", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.every_model.openrouter_routing", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.every_model.description", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.every_model.name", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.every_model.price", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.every_model.price.*", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.price.input", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.price.output", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.price.cache_read", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.price.cache_write", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.images", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.every_model.images.max_width", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.images.max_height", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.images.max_bytes", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.images.jpeg_quality", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.every_model.*", Scope: both, Type: SchemaTypeAny},
		{Path: "providers.*.every_model.*.*", Scope: both, Type: SchemaTypeAny},
		{Path: "providers.*.models", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.models.*", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.models.*.name", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.api", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.vendor", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.accepts", Scope: both, Type: SchemaTypeList},
		{Path: "providers.*.models.*.accepts.*", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.max_tokens", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.reasoning", Scope: both, Type: SchemaTypeBoolean},
		{Path: "providers.*.models.*.thinking", Scope: both, Type: SchemaTypeList},
		{Path: "providers.*.models.*.thinking.*", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.context", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.description", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.price", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.models.*.price.*", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.price.input", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.price.output", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.price.cache_read", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.price.cache_write", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.twin_of", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.compact_at", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.images", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.models.*.images.max_width", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.images.max_height", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.images.max_bytes", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.images.jpeg_quality", Scope: both, Type: SchemaTypeNumber},
		{Path: "providers.*.models.*.base", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.extends", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.openrouter_routing", Scope: both, Type: SchemaTypeObject},
		{Path: "providers.*.models.*.openrouter_routing.*", Scope: both, Type: SchemaTypeAny},
		// A row keyed by a name says which model it is (MF-D1); a row may send another id
		// (MF-D3). docs/design/model-facts-migration.md.
		{Path: "providers.*.models.*.model", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.models.*.wire_id", Scope: both, Type: SchemaTypeString},
		// Facts written once and shared by a model's routes (MF-D2), in the row fact spellings,
		// without what only a route can say (packdecl.ModelFactsRouteOnlyKeys).
		{Path: "model_facts", Scope: user, Type: SchemaTypeObject},
		{Path: "model_facts.*", Scope: user, Type: SchemaTypeObject},
		{Path: "model_facts.*.name", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.description", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.vendor", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.api", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.thinking", Scope: user, Type: SchemaTypeList},
		{Path: "model_facts.*.thinking.*", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.accepts", Scope: user, Type: SchemaTypeList},
		{Path: "model_facts.*.accepts.*", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.max_tokens", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.reasoning", Scope: user, Type: SchemaTypeBoolean},
		{Path: "model_facts.*.context", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.base", Scope: user, Type: SchemaTypeString},
		{Path: "model_facts.*.openrouter_routing", Scope: user, Type: SchemaTypeObject},
		{Path: "model_facts.*.openrouter_routing.*", Scope: user, Type: SchemaTypeAny},
		{Path: "model_facts.*.price", Scope: user, Type: SchemaTypeObject},
		{Path: "model_facts.*.price.input", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.price.output", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.price.cache_read", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.price.cache_write", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.images", Scope: user, Type: SchemaTypeObject},
		{Path: "model_facts.*.images.max_width", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.images.max_height", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.images.max_bytes", Scope: user, Type: SchemaTypeNumber},
		{Path: "model_facts.*.images.jpeg_quality", Scope: user, Type: SchemaTypeNumber},
		{Path: "network.mode", Scope: both, Type: SchemaTypeString},
		{Path: "providers.*.api_key_env_name", Scope: user, Type: SchemaTypeMixed},
		{Path: "providers.*.platform", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.capabilities", Scope: user, Type: SchemaTypeList},
		{Path: "providers.*.capabilities.*", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.wire_api", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.endpoints", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.endpoints.*", Scope: user, Type: SchemaTypeObject},
		{Path: "providers.*.endpoints.*.base_url", Scope: user, Type: SchemaTypeString},
		{Path: "providers.*.endpoints.*.wire_api", Scope: user, Type: SchemaTypeString},
		{Path: "mounts", Scope: mixed, Type: SchemaTypeList},
		{Path: "mounts.*", Scope: mixed, Type: SchemaTypeMixed},
		{Path: "mounts.*.host", Scope: mixed, Type: SchemaTypeString},
		{Path: "mounts.*.at", Scope: mixed, Type: SchemaTypeString},
		{Path: "mounts.*.mode", Scope: mixed, Type: SchemaTypeString},
		{Path: "mounts.*.optional", Scope: mixed, Type: SchemaTypeBoolean},
		{Path: "host_files", Scope: mixed, Type: SchemaTypeObject},
		{Path: "host_files.*", Scope: mixed, Type: SchemaTypeMixed},
		{Path: "host_files.*.path", Scope: mixed, Type: SchemaTypeString},
		{Path: "host_files.*.source", Scope: mixed, Type: SchemaTypeString},
		{Path: "host_files.*.content", Scope: mixed, Type: SchemaTypeString},
		{Path: "host_files.*.codec", Scope: mixed, Type: SchemaTypeString},
		{Path: "host_files.*.managed", Scope: mixed, Type: SchemaTypeAny},
		{Path: "host_files.*.defaults", Scope: mixed, Type: SchemaTypeAny},
		{Path: "host_files.*.mode", Scope: mixed, Type: SchemaTypeString},
		{Path: "programs.autoprune", Scope: user, Type: SchemaTypeBoolean},
		{Path: "programs.overrides.*.path", Scope: user, Type: SchemaTypeString},
		{Path: "programs.overrides.*.prebuilt", Scope: user, Type: SchemaTypeBoolean},
		{Path: "programs.overrides.*.run", Scope: user, Type: SchemaTypeString},
		{Path: "mcp_servers", Scope: both, Type: SchemaTypeObject},
		{Path: "mcp_servers.*", Scope: both, Type: SchemaTypeObject},
		{Path: "mcp_servers.*.command", Scope: both, Type: SchemaTypeList},
		{Path: "mcp_servers.*.command.*", Scope: both, Type: SchemaTypeString},
		{Path: "mcp_servers.*.args", Scope: both, Type: SchemaTypeList},
		{Path: "mcp_servers.*.args.*", Scope: both, Type: SchemaTypeString},
		{Path: "mcp_servers.*.env", Scope: both, Type: SchemaTypeObject},
		{Path: "mcp_servers.*.env.*", Scope: both, Type: SchemaTypeString},
		{Path: "mcp_servers.*.requires_env", Scope: both, Type: SchemaTypeList},
		{Path: "mcp_servers.*.requires_env.*", Scope: both, Type: SchemaTypeString},
		{Path: "mcp_servers.*.provides", Scope: both, Type: SchemaTypeString},
	}
	for _, key := range entries {
		add(key.Path, key.Scope, key.Type)
	}
	out := make([]SchemaKey, 0, len(paths))
	for path, key := range paths {
		key.Removal = readableRemovalPolicy(path)
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func readableProviderScope(path string) SchemaScope {
	if path == "providers" || path == "providers.*" {
		return SchemaScopeFieldSensitive
	}
	for _, prefix := range []string{
		"providers.*.alias", "providers.*.model", "providers.*.via",
		"providers.*.base_url", "providers.*.wire_api", "providers.*.api_key_env_name",
		"providers.*.platform", "providers.*.endpoints", "providers.*.variants",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+".") {
			return SchemaScopeUserOnly
		}
	}
	return SchemaScopeBoth
}

func schemaKeys(paths map[string]struct{}, scope func(string) SchemaScope, typ func(string) SchemaType, removal func(string) RemovalPolicy) []SchemaKey {
	out := make([]SchemaKey, 0, len(paths))
	for path := range paths {
		out = append(out, SchemaKey{Path: path, Scope: scope(path), Type: typ(path), Removal: removal(path)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func legacySchemaScope(path string) SchemaScope {
	for _, root := range TopLevelConfigKeys() {
		if WorkspaceUserScopeOnlyKey(root) && (path == root || strings.HasPrefix(path, root+".")) {
			return SchemaScopeUserOnly
		}
	}
	if fieldSensitivePath(path) {
		return SchemaScopeFieldSensitive
	}
	return SchemaScopeBoth
}

func fieldSensitivePath(path string) bool {
	for _, root := range []string{"providers", "mounts", "loopholes", "brokered", "host_files"} {
		if path == root || strings.HasPrefix(path, root+".") {
			return true
		}
	}
	return false
}

func legacySchemaType(path string) SchemaType {
	switch path {
	case "runtime", "confinement", "repo_path", "host_path", "macos_user_mode", "programs.overrides.*.run",
		"providers.*.region", "providers.*.base_url", "providers.*.wire_api",
		"providers.*.endpoints.*.base_url", "providers.*.endpoints.*.wire_api",
		"providers.*.models.*.id", "providers.*.models.*.name", "providers.*.models.*.description",
		"providers.*.models.*.vendor", "providers.*.models.*.base", "providers.*.models.*.wire_id",
		"profiles.*.model", "profiles.*.provider", "profiles.*.via",
		"mounts.*.host", "mounts.*.at", "mounts.*.mode", "host_files.*.path", "host_files.*.source",
		"host_files.*.content", "host_files.*.codec", "host_files.*.mode",
		"devices.*.usb", "devices.*.description", "devices.*.cgroup_rule",
		"gpu.devices", "gpu.capabilities", "gpu.vendor", "gpu.mode", "gpu.hsa_override_gfx_version",
		"network.mode", "resources.memory",
		"brokered.*.grants.*.kind", "brokered.*.grants.*.repo", "brokered.*.grants.*.branch", "brokered.*.grants.*.tag",
		"brokered.*.sets.*.name", "brokered.*.sets.*.mode", "brokered.*.sets.*.window",
		"brokered.*.sets.*.max_window":
		return SchemaTypeString
	case "include_if_found.*", "packages.*.name", "packages.*.nixpkgs", "packages.*.version", "packages.*.url", "packages.*.hash",
		"packages.*.outputs.*", "packages.*.platforms.*", "providers.*.models.*.input.*":
		return SchemaTypeString
	case "packages", "env_sources", "include_if_found", "writable_home_dirs", "required_capabilities",
		"providers.*.capabilities", "providers.*.models.*.input",
		"brokered.*.repos", "brokered.*.grant_kinds", "brokered.*.grants", "network.ports", "network.forward_host_ports",
		"devices", "packages.*.outputs", "packages.*.platforms":
		return SchemaTypeList
	case "mounts.*.optional", "programs.autoprune", "programs.overrides.*.prebuilt", "profiles.*.enforce_models",
		"gpu.enabled", "gpu.seccomp_unconfined", "gpu.vaapi", "providers.*.models.*.reasoning":
		return SchemaTypeBoolean
	case "providers.*.models.*.context_window", "providers.*.models.*.max_tokens",
		"providers.*.models.*.cost.input", "providers.*.models.*.cost.output",
		"providers.*.models.*.cost.cache_read", "providers.*.models.*.cost.cache_write",
		"brokered.*.sets.*.uses", "resources.pids_limit":
		return SchemaTypeNumber
	case "network", "security", "providers", "profiles", "loopholes", "brokered", "host_files", "resources", "gpu", "mcp_servers.*",
		"providers.*.endpoints", "providers.*.models", "providers.*.options", "providers.*.models.*.cost",
		"model_facts", "model_facts.*", "profiles.*", "loopholes.*.settings", "programs", "programs.overrides", "programs.overrides.*",
		"brokered.*", "brokered.*.grants.*", "brokered.*.sets", "brokered.*.sets.*":
		return SchemaTypeObject
	case "host_files.*.managed", "host_files.*.defaults":
		return SchemaTypeAny
	case "profile", "packs", "mounts.*", "host_files.*", "devices.*", "packages.*", "resources.cpus", "resources.io":
		return SchemaTypeMixed
	case "profile.*", "profile.*.*", "profiles.*.*":
		return SchemaTypeMixed
	case "brokered.*.sets.*.admit":
		return SchemaTypeList
	default:
		return SchemaTypeAny
	}
}

func readableRemovalPolicy(path string) RemovalPolicy {
	for _, rule := range ReadableRemovalPolicies() {
		if rule.Path == path {
			return rule.Policy
		}
	}
	return RemovalUnspecified
}
