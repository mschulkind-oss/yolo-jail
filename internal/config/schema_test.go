package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestSchemaKeysCensusEveryLegacyLiveAndNestedField(t *testing.T) {
	keys := SchemaKeys()
	got := map[string]SchemaKey{}
	for _, key := range keys {
		if key.Path == "" {
			t.Fatal("schema key has empty path")
		}
		if _, exists := got[key.Path]; exists {
			t.Errorf("schema path %q appears more than once", key.Path)
		}
		got[key.Path] = key
	}
	for _, path := range []string{
		"runtime", "packages", "mounts", "network", "network.mode", "network.ports",
		"resources.memory", "providers.*.endpoints.*.base_url", "providers.*.models.*.id",
		"providers.*.models.*.cost.cache_write", "model_facts.*.openrouter_routing",
		"profiles.*", "profile", "loopholes.*.settings.*", "brokered.*.grants.*",
		"host_files.*", "env_sources.*", "programs.overrides.*",
	} {
		if _, ok := got[path]; !ok {
			t.Errorf("legacy schema census omits %q", path)
		}
	}
	for path := range knownTopLevelConfigKeys {
		if _, ok := got[path]; !ok {
			t.Errorf("schema census omits top-level field %q", path)
		}
	}
	for prefix, fields := range map[string]map[string]struct{}{
		"network": knownNetworkKeys, "security": knownSecurityKeys,
		"security.blocked_tools.*": knownBlockedToolKeys, "packages.*": knownPackageKeys,
		"lsp_servers.*": knownLSPServerKeys, "mcp_servers.*": knownMCPServerKeys,
		"providers.*": knownProviderKeys, "providers.*.endpoints.*": knownEndpointKeys,
		"providers.*.models.*": knownModelKeys, "providers.*.models.*.cost": knownModelCostKeys,
		"model_facts.*": knownSharedModelKeys, "devices.*": knownDeviceKeys,
		"resources": knownResourcesKeys, "loopholes.*": knownHostServiceKeys,
		"loopholes.*.settings": knownLoopholeOverrideKeys, "gpu": knownGPUKeys,
		"prune": knownPruneKeys, "profiles.*": knownProfileKeys,
		"brokered.*": knownBrokeredSourceKeys, "brokered.*.grants.*": knownBrokeredGrantKeys,
		"brokered.*.sets.*": knownBrokeredSetKeys, "programs": knownProgramsKeys,
		"programs.overrides.*": knownProgramOverrideKeys, "host_files.*": knownHostFileKeys,
	} {
		for field := range fields {
			path := prefix + "." + field
			if _, ok := got[path]; !ok {
				t.Errorf("schema census omits nested field %q", path)
			}
		}
	}
	for _, path := range []string{
		"mounts.*", "mounts.*.host", "mounts.*.at", "mounts.*.mode", "mounts.*.optional",
		"programs.autoprune", "programs.overrides.*.path", "programs.overrides.*.prebuilt",
		"programs.overrides.*.run", "profiles.*.model", "profiles.*.*",
		"brokered.*.grants.*.kind", "brokered.*.grants.*.repo", "brokered.*.grants.*.branch",
		"brokered.*.sets.*.name", "brokered.*.sets.*.mode", "brokered.*.sets.*.window",
		"brokered.*.sets.*.max_window", "brokered.*.sets.*.uses", "brokered.*.sets.*.admit",
		"brokered.*.sets.*.admit.*", "host_files.*.path", "host_files.*.source",
		"host_files.*.content", "host_files.*.codec", "host_files.*.managed",
		"host_files.*.defaults", "host_files.*.mode",
	} {
		if _, ok := got[path]; !ok {
			t.Errorf("legacy config field census omits %q", path)
		}
	}
	for _, path := range []string{"providers", "mounts", "loopholes", "brokered", "host_files"} {
		key := got[path]
		if key.Scope != SchemaScopeFieldSensitive {
			t.Errorf("%s scope = %v, want field-sensitive", path, key.Scope)
		}
	}
	for _, path := range []string{"profiles.*.provider", "profiles.*.model", "profile.*", "programs.overrides.*.path"} {
		if got[path].Scope != SchemaScopeUserOnly {
			t.Errorf("user-only root scope not inherited by %s: got %v", path, got[path].Scope)
		}
	}
	if got["providers.*.models.*.cost.cache_read"].Type != SchemaTypeNumber ||
		got["resources.memory"].Type != SchemaTypeString || got["resources.cpus"].Type != SchemaTypeMixed ||
		got["gpu.enabled"].Type != SchemaTypeBoolean || got["profiles.*.model"].Type != SchemaTypeString {
		t.Errorf("legacy nested types are incomplete: cost=%v memory=%v cpus=%v gpu=%v model=%v",
			got["providers.*.models.*.cost.cache_read"].Type, got["resources.memory"].Type,
			got["resources.cpus"].Type, got["gpu.enabled"].Type, got["profiles.*.model"].Type)
	}
	if got["packs"].Scope != SchemaScopeUserOnly || got["runtime"].Scope != SchemaScopeBoth {
		t.Errorf("scope census lost whole-key distinctions: packs=%v runtime=%v", got["packs"].Scope, got["runtime"].Scope)
	}
}

func TestRenameDescriptorsHaveScopeAndReplacement(t *testing.T) {
	descriptors := RenameDescriptors()
	seen := map[string]bool{}
	for _, d := range descriptors {
		if d.OldPath == "" || seen[d.OldPath] {
			t.Errorf("empty or duplicate old path in rename table: %q", d.OldPath)
		}
		seen[d.OldPath] = true
		if d.Transform == RenameUnspecified {
			t.Errorf("%s has no transformation", d.OldPath)
		}
		if d.Transform != RenameDrop && d.Transform != RenameUnrepresentable && len(d.Replacements) == 0 {
			t.Errorf("%s has no replacement", d.OldPath)
		}
		if d.Scope == SchemaScopeFieldSensitive && d.Transform == RenameUnspecified {
			t.Errorf("%s omits its field-sensitive transformation", d.OldPath)
		}
		for _, replacement := range d.Replacements {
			if strings.TrimSpace(replacement) == "" {
				t.Errorf("%s has empty replacement", d.OldPath)
			}
		}
	}
	for _, path := range []string{"packages", "mise_tools", "profile", "providers.*.models.*.context_window", "brokered.*.grants.*", "loopholes.*.enabled"} {
		if !seen[path] {
			t.Errorf("rename table omits legacy path %q", path)
		}
	}
	if !renameHasTransform(descriptors, "providers.*.models.*.input", RenameFoldWhenEqual) {
		t.Error("shared model facts must be folded only when their values are equal")
	}
	for _, path := range []string{"providers.*.models.*.max_tokens", "providers.*.models.*.reasoning"} {
		if !renameHasTransform(descriptors, path, RenameFoldWhenEqual) {
			t.Errorf("%s must retain per-model values and fold only equal shared facts", path)
		}
	}
	if replacementContains(descriptors, "providers.*.models.*.max_tokens", "providers.*.models.*.max_tokens") == false ||
		replacementContains(descriptors, "providers.*.models.*.max_tokens", "providers.*.every_model.max_tokens") == false {
		t.Error("max_tokens rename must describe both row preservation and optional shared fold")
	}
	for _, path := range []string{"providers.*.models.*.context_window", "providers.*.models.*.cost"} {
		if !renameHasTransform(descriptors, path, RenameFoldWhenEqual) {
			t.Errorf("%s must retain row values and fold only equal shared facts", path)
		}
	}
	if !replacementContains(descriptors, "providers.*.models.*.context_window", "providers.*.models.*.context") ||
		!replacementContains(descriptors, "providers.*.models.*.context_window", "providers.*.every_model.context") {
		t.Error("context_window rename omits its row-preserving or shared-fact destination")
	}
	if !replacementContains(descriptors, "providers.*.models.*.cost", "providers.*.models.*.price") ||
		!replacementContains(descriptors, "providers.*.models.*.cost", "providers.*.every_model.price") {
		t.Error("cost rename omits its row-preserving or shared-fact destination")
	}
	if !replacementContains(descriptors, "providers.*.models.*.input", "providers.*.every_model.accepts") {
		t.Error("input rename omits its accepted shared-fact destination")
	}
	if replacementContains(descriptors, "network.mode", "machine.network") {
		t.Error("network.mode has no accepted rename to machine.network")
	}
	if !replacementContains(descriptors, "network.mode", "network.mode") {
		t.Error("unchanged network.mode must remain explicit in the rename contract")
	}
	for _, path := range []string{
		"profiles.*", "profiles.*.provider", "profiles.*.model", "profiles.*.via",
		"profiles.*.enforce_models", "profiles.*.subagents", "profiles.*.*", "profile.*",
		"brokered.*.repos", "brokered.*.grants.*.kind", "brokered.*.grants.*.repo",
		"brokered.*.grants.*.branch", "brokered.*.sets.*",
	} {
		if !seen[path] {
			t.Errorf("provider/profile/broker migration descriptor omits %q", path)
		}
	}
	if !renameHasTransform(descriptors, "profiles.*", RenameProfileVariant) ||
		!renameHasTransform(descriptors, "profile.*", RenameProfileSelection) ||
		!renameHasTransform(descriptors, "brokered.*.sets.*", RenameUnrepresentable) {
		t.Error("provider variants, native/default selections, and unrepresentable grants need explicit transforms")
	}
}

func replacementContains(descriptors []RenameDescriptor, oldPath, replacement string) bool {
	for _, d := range descriptors {
		if d.OldPath != oldPath {
			continue
		}
		for _, got := range d.Replacements {
			if got == replacement {
				return true
			}
		}
	}
	return false
}

func renameHasTransform(descriptors []RenameDescriptor, path string, transform RenameTransform) bool {
	for _, d := range descriptors {
		if d.OldPath == path && d.Transform == transform {
			return true
		}
	}
	return false
}

func TestRemovalPoliciesKeepLegacyNullCasesPathSpecific(t *testing.T) {
	policies := map[string]RemovalPolicy{}
	for _, rule := range RemovalPolicies() {
		if _, exists := policies[rule.Path]; exists {
			t.Fatalf("duplicate removal policy for %q", rule.Path)
		}
		policies[rule.Path] = rule.Policy
	}
	if policies["profile"] != RemovalNull || policies["profile.*"] != RemovalNull {
		t.Errorf("profile null removal semantics missing: %#v", policies)
	}
	if policies["providers.*.models.*.*"] != RemovalTombstone || policies["providers.*.models.*"] != RemovalNull {
		t.Errorf("model row/tombstone distinction missing: %#v", policies)
	}
	if policies["env_sources"] != RemovalUnspecified {
		t.Errorf("mixed legacy env_sources null semantics were flattened: %v", policies["env_sources"])
	}
}

func TestReadableSchemaKeysDescribeNewVocabularyWithoutActivatingIt(t *testing.T) {
	got := map[string]SchemaKey{}
	for _, key := range ReadableSchemaKeys() {
		if _, exists := got[key.Path]; exists {
			t.Errorf("duplicate readable path %q", key.Path)
		}
		got[key.Path] = key
	}
	for _, path := range []string{
		"install.nix", "install.mise.*", "machine.gpu", "machine.resources.memory",
		"machine.ports.*.host", "machine.forward.*.jail", "env.files.*", "env.set.*", "env.unset.*",
		"allow.*.always", "allow.*.on_request", "requests.git.push", "requests.github.repos",
		"active_provider.default", "active_provider.*", "providers.*.variants.*.model",
		"providers.*.every_model.accepts", "providers.*.models.*.context", "providers.*.models.*.price.input",
	} {
		if _, ok := got[path]; !ok {
			t.Errorf("readable schema omits %q", path)
		}
	}
	if got["mounts"].Scope != SchemaScopeFieldSensitive || got["host_files.*.source"].Scope != SchemaScopeFieldSensitive ||
		got["programs.overrides.*.path"].Scope != SchemaScopeUserOnly || got["network.mode"].Type != SchemaTypeString {
		t.Errorf("unchanged legacy path metadata was not retained: mounts=%#v source=%#v program=%#v network=%#v",
			got["mounts"], got["host_files.*.source"], got["programs.overrides.*.path"], got["network.mode"])
	}
	if _, ok := got["machine.network"]; ok {
		t.Error("schema invents machine.network instead of retaining network.mode")
	}
	if got["allow.*.always"].Scope != SchemaScopeUserOnly || got["requests.*.repos"].Scope != SchemaScopeWorkspaceOnly ||
		got["providers.*.variants.*.model"].Scope != SchemaScopeUserOnly {
		t.Errorf("readable schema scope contract is incomplete: allow=%v request=%v variant=%v",
			got["allow.*.always"].Scope, got["requests.*.repos"].Scope, got["providers.*.variants.*.model"].Scope)
	}
	for _, path := range []string{
		"network.mode", "mounts.*.host", "host_files.*.source", "programs.overrides.*.path",
		"providers.*.models.*.max_tokens", "providers.*.models.*.reasoning",
		"providers.*.models.*.accepts", "providers.*.models.*.images.max_width",
		"providers.*.models.*.extends", "providers.*.models.*.twin_of",
		"providers.*.every_model.api", "providers.*.variants.*.subagents.default",
	} {
		if _, ok := got[path]; !ok {
			t.Errorf("readable schema omits settled provider field %q", path)
		}
	}
	if got["providers.*.models.*.max_tokens"].Type != SchemaTypeNumber ||
		got["providers.*.models.*.reasoning"].Type != SchemaTypeBoolean ||
		got["providers.*.models.*.accepts"].Type != SchemaTypeList ||
		got["providers.*.models.*.price.input"].Type != SchemaTypeNumber {
		t.Errorf("new provider/model nested types are incomplete: %#v %#v %#v %#v",
			got["providers.*.models.*.max_tokens"], got["providers.*.models.*.reasoning"],
			got["providers.*.models.*.accepts"], got["providers.*.models.*.price.input"])
	}
	if got["providers.*"].Removal != RemovalOffString || got["providers.*.variants.*"].Removal != RemovalOffString ||
		got["providers.*"].Type != SchemaTypeMixed || got["providers.*.variants.*"].Type != SchemaTypeMixed {
		t.Errorf("provider off values must be mixed shapes and explicit removals: provider=%#v variant=%#v", got["providers.*"], got["providers.*.variants.*"])
	}
	if got["install.nix"].Removal != RemovalEmptyList || got["active_provider.*"].Removal != RemovalNativeString ||
		got["mcp_servers.*"].Removal != RemovalOffString {
		t.Errorf("readable removal contract is incomplete: %#v %#v %#v", got["install.nix"], got["active_provider.*"], got["mcp_servers.*"])
	}
}

func TestReadableSchemaMatchesWorkspaceProviderScopeAndSelectionReaders(t *testing.T) {
	readable := schemaKeyMap(ReadableSchemaKeys())
	for path, want := range map[string]SchemaScope{
		"providers":                    SchemaScopeFieldSensitive,
		"providers.*":                  SchemaScopeFieldSensitive,
		"providers.*.models":           SchemaScopeBoth,
		"providers.*.models.*.api":     SchemaScopeBoth,
		"providers.*.models.*.vendor":  SchemaScopeBoth,
		"providers.*.options":          SchemaScopeBoth,
		"providers.*.region":           SchemaScopeBoth,
		"providers.*.capabilities":     SchemaScopeBoth,
		"providers.*.model":            SchemaScopeUserOnly,
		"providers.*.via":              SchemaScopeUserOnly,
		"providers.*.endpoints":        SchemaScopeUserOnly,
		"providers.*.platform":         SchemaScopeUserOnly,
		"providers.*.api_key_env_name": SchemaScopeUserOnly,
		"mounts.*.write":               SchemaScopeFieldSensitive, // a project may request one (RC-D24)
		"active_provider.default":      SchemaScopeUserOnly,
	} {
		if got := readable[path].Scope; got != want {
			t.Errorf("ReadableSchemaKeys()[%q].Scope = %v, want %v", path, got, want)
		}
	}
	if got := readable["active_provider.default"].Type; got != SchemaTypeMixed {
		t.Errorf("active_provider.default type = %v, want mixed string/list selection", got)
	}

	selection, ok := ProfileSelectionOf([]any{"bedrock", "codex"})
	if !ok || !reflect.DeepEqual(selection.Default, []string{"bedrock", "codex"}) {
		t.Fatalf("the executable profile selector no longer accepts the ordered default set: %#v, %v", selection, ok)
	}

	for _, tc := range []struct {
		name, body string
		refused    bool
	}{
		{"model facts", `{"providers":{"acme":{"models":{"m":{"id":"m","input":["text"]}}}}}`, false},
		{"provider options", `{"providers":{"acme":{"options":{"context_window":"65536"}}}}`, false},
		{"provider region", `{"providers":{"acme":{"region":"us-east-1"}}}`, false},
		{"provider capabilities", `{"providers":{"acme":{"capabilities":["web_search"]}}}`, false},
		{"provider endpoint", `{"providers":{"acme":{"endpoints":{"openai":{"base_url":"https://example.test/v1"}}}}}`, true},
		{"provider platform", `{"providers":{"acme":{"platform":"aws-bedrock"}}}`, true},
		{"provider credential variable", `{"providers":{"acme":{"api_key_env_name":"ACME_KEY"}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := providerScopeErrors(t, tc.body, `{}`)
			routed := strings.Contains(strings.Join(errs, "\n"), "user-scope only")
			if routed != tc.refused {
				t.Fatalf("ValidateConfig scope refusal = %v for %s; errors: %v", routed, tc.name, errs)
			}
		})
	}
}

func TestReadableSchemaModelFieldsMatchExecutableManifestModelTypes(t *testing.T) {
	readable := schemaKeyMap(ReadableSchemaKeys())
	modelType := reflect.TypeOf(packdecl.ProviderModel{})
	for i := 0; i < modelType.NumField(); i++ {
		field := modelType.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := "providers.*.models.*." + name
		key, ok := readable[path]
		if !ok {
			t.Errorf("readable user schema omits executable model field %q", path)
		} else if want := schemaTypeForGo(field.Type); key.Type != want {
			t.Errorf("readable user schema type for %q = %v, manifest model shape is %v", path, key.Type, want)
		}
	}
	for _, nested := range []struct {
		typ  reflect.Type
		path string
	}{
		{reflect.TypeOf(packdecl.ProviderPrice{}), "providers.*.models.*.price"},
		{reflect.TypeOf(packdecl.ProviderPrice{}), "providers.*.every_model.price"},
		{reflect.TypeOf(packdecl.ProviderImage{}), "providers.*.models.*.images"},
		{reflect.TypeOf(packdecl.ProviderImage{}), "providers.*.every_model.images"},
	} {
		assertReflectedSchemaFields(t, readable, nested.path, nested.typ)
	}

	// The legacy validator's supported shared-fact set is the source inventory. These names
	// normalize to the typed D1 ProviderModel fields rather than being reflected as row keys.
	sharedNames := map[string]string{
		"name": "name", "description": "description", "reasoning": "reasoning",
		"input": "accepts", "cost": "price", "context_window": "context",
		"max_tokens": "max_tokens", "base": "base", "openrouter_routing": "openrouter_routing",
	}
	modelFields := make(map[string]reflect.StructField, modelType.NumField())
	for i := 0; i < modelType.NumField(); i++ {
		field := modelType.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			modelFields[name] = field
		}
	}
	for supported := range knownSharedModelKeys {
		manifestName, ok := sharedNames[supported]
		if !ok {
			t.Errorf("no D1 model field mapping for supported shared fact %q", supported)
			continue
		}
		field, ok := modelFields[manifestName]
		if !ok {
			t.Errorf("D1 ProviderModel omits supported shared fact %q (field %q)", supported, manifestName)
			continue
		}
		path := "providers.*.every_model." + manifestName
		key, ok := readable[path]
		if !ok {
			t.Errorf("readable shared schema omits supported fact %q at %q", supported, path)
		} else if want := schemaTypeForGo(field.Type); key.Type != want {
			t.Errorf("readable shared schema type for %q = %v, D1 model shape is %v", path, key.Type, want)
		}
	}
	// These typed D1 facts extend the legacy shared validator inventory. Check their actual
	// ProviderModel field types; row-only fields such as wire_id, twin_of, and compact_at do
	// not enter this shared census.
	for _, name := range []string{"api", "thinking", "accepts"} {
		field, ok := modelFields[name]
		if !ok {
			t.Errorf("D1 ProviderModel omits shared typed field %q", name)
			continue
		}
		path := "providers.*.every_model." + name
		key, ok := readable[path]
		if !ok {
			t.Errorf("readable shared schema omits D1 typed field %q", path)
		} else if want := schemaTypeForGo(field.Type); key.Type != want {
			t.Errorf("readable shared schema type for %q = %v, D1 model shape is %v", path, key.Type, want)
		}
	}
}

func TestReadableSchemaContainsLegacyValidatorFieldsAndExactMixedTypes(t *testing.T) {
	legacy := schemaKeyMap(SchemaKeys())
	for _, path := range []string{
		"include_if_found", "include_if_found.*", "providers.*.models.*.input", "providers.*.models.*.input.*",
		"packages.*.name", "packages.*.nixpkgs", "packages.*.version", "packages.*.url", "packages.*.hash",
		"packages.*.outputs", "packages.*.platforms", "gpu.enabled", "gpu.devices", "gpu.capabilities",
		"gpu.vendor", "gpu.mode", "gpu.hsa_override_gfx_version", "gpu.seccomp_unconfined", "gpu.vaapi",
	} {
		if _, ok := legacy[path]; !ok {
			t.Errorf("legacy schema census omits executable validator path %q", path)
		}
	}
	for _, path := range []string{
		"packages.*.outputs.*", "packages.*.platforms.*",
	} {
		if _, ok := legacy[path]; !ok {
			t.Errorf("legacy schema census omits executable package element path %q", path)
		}
	}
	if legacy["include_if_found"].Type != SchemaTypeList || legacy["include_if_found.*"].Type != SchemaTypeString {
		t.Errorf("include_if_found shape = %v / %v, want list of strings", legacy["include_if_found"].Type, legacy["include_if_found.*"].Type)
	}
	for _, field := range []string{"outputs", "platforms"} {
		if legacy["packages.*."+field].Type != SchemaTypeList || legacy["packages.*."+field+".*"].Type != SchemaTypeString {
			t.Errorf("legacy package %s shape = %v / %v, want list of strings", field,
				legacy["packages.*."+field].Type, legacy["packages.*."+field+".*"].Type)
		}
	}
	if legacy["providers.*.models.*.input"].Type != SchemaTypeList || legacy["providers.*.models.*.input.*"].Type != SchemaTypeString {
		t.Errorf("legacy model input shape = %v / %v, want list of strings", legacy["providers.*.models.*.input"].Type, legacy["providers.*.models.*.input.*"].Type)
	}
	readable := schemaKeyMap(ReadableSchemaKeys())
	for _, path := range []string{
		"install.nix", "install.nix.*", "install.nix.*.outputs.*", "install.nix.*.platforms.*",
		"machine.gpu", "mounts.*.write", "machine.resources.cpus", "machine.resources.io",
		"machine.resources.io.priority",
	} {
		if _, ok := readable[path]; !ok {
			t.Errorf("readable schema omits validator-backed or accepted path %q", path)
		}
	}
	for path, want := range map[string]SchemaType{
		"install.nix": SchemaTypeList, "install.nix.*": SchemaTypeMixed,
		"install.nix.*.outputs.*": SchemaTypeString, "install.nix.*.platforms.*": SchemaTypeString,
		"machine.gpu": SchemaTypeMixed, "machine.resources.cpus": SchemaTypeMixed,
		"machine.resources.io": SchemaTypeMixed, "machine.resources.io.priority": SchemaTypeString,
		"mounts.*.write": SchemaTypeBoolean,
	} {
		if got := readable[path].Type; got != want {
			t.Errorf("readable schema type for %q = %v, want %v", path, got, want)
		}
	}
	for field := range knownPackageKeys {
		legacyPath := "packages.*." + field
		readablePath := "install.nix.*." + field
		if _, ok := legacy[legacyPath]; !ok {
			t.Errorf("legacy schema omits executable package field %q", legacyPath)
		}
		key, ok := readable[readablePath]
		if !ok {
			t.Errorf("readable install schema omits validator field %q", readablePath)
			continue
		}
		if want := legacySchemaType(legacyPath); key.Type != want {
			t.Errorf("readable install schema type for %q = %v, legacy validator shape is %v", readablePath, key.Type, want)
		}
	}
	for field := range knownGPUKeys {
		legacyPath := "gpu." + field
		readablePath := "machine." + legacyPath
		if _, ok := legacy[legacyPath]; !ok {
			t.Errorf("legacy schema omits executable GPU field %q", legacyPath)
		}
		key, ok := readable[readablePath]
		if !ok {
			t.Errorf("readable machine GPU schema omits validator field %q", readablePath)
			continue
		}
		if want := legacySchemaType(legacyPath); key.Type != want {
			t.Errorf("readable GPU schema type for %q = %v, legacy validator shape is %v", readablePath, key.Type, want)
		}
	}
}

func TestReadableSchemaRemovalPoliciesCoverAcceptedEmptyListsAndNativeSelection(t *testing.T) {
	readable := schemaKeyMap(ReadableSchemaKeys())
	for _, key := range readable {
		if key.Type == SchemaTypeList && key.Removal != RemovalEmptyList {
			t.Errorf("list path %q has removal policy %v, want empty-list clearing", key.Path, key.Removal)
		}
	}
	if got := readable["env.set.*"].Removal; got != RemovalUnsetList {
		t.Errorf("removing a set environment variable through env.unset has policy %v, want unset-list", got)
	}
	for path, want := range map[string]RemovalPolicy{
		"env.unset":               RemovalEmptyList,
		"env.set.*":               RemovalUnsetList,
		"packs":                   RemovalEmptyList,
		"mounts":                  RemovalEmptyList,
		"env.files":               RemovalEmptyList,
		"install.nix":             RemovalEmptyList,
		"machine.ports":           RemovalEmptyList,
		"machine.forward":         RemovalEmptyList,
		"allow.*.always":          RemovalEmptyList,
		"allow.*.on_request":      RemovalEmptyList,
		"requests.git.push":       RemovalEmptyList,
		"requests.git.push_tags":  RemovalEmptyList,
		"requests.github.repos":   RemovalEmptyList,
		"active_provider.default": RemovalNativeString,
		"active_provider.*":       RemovalNativeString,
	} {
		if got := readable[path].Removal; got != want {
			t.Errorf("readable removal policy for %q = %v, want %v", path, got, want)
		}
	}
}

func TestReadableSchemaScopeAdapterIsNotWholeKeyAuthorization(t *testing.T) {
	for _, key := range []string{"providers", "mounts", "loopholes", "brokered", "host_files"} {
		if WorkspaceUserScopeOnlyKey(key) {
			t.Errorf("mixed key %q was authorized as wholly user-only", key)
		}
	}
	if !WorkspaceUserScopeOnlyKey("packs") || WorkspaceUserScopeOnlyKey("runtime") {
		t.Fatal("whole-key scope adapter did not retain its narrow result")
	}
}

func assertReflectedSchemaFields(t *testing.T, schema map[string]SchemaKey, path string, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		t.Fatalf("%s is not a struct: %s", path, typ)
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fieldPath := path + "." + name
		key, ok := schema[fieldPath]
		if !ok {
			t.Errorf("schema omits reflected manifest field %q", fieldPath)
			continue
		}
		if want := schemaTypeForGo(field.Type); key.Type != want {
			t.Errorf("schema type for reflected field %q = %v, want %v", fieldPath, key.Type, want)
		}
	}
}

func schemaKeyMap(keys []SchemaKey) map[string]SchemaKey {
	out := make(map[string]SchemaKey, len(keys))
	for _, key := range keys {
		out[key.Path] = key
	}
	return out
}

func schemaTypeForGo(typ reflect.Type) SchemaType {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.String:
		return SchemaTypeString
	case reflect.Bool:
		return SchemaTypeBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		return SchemaTypeNumber
	case reflect.Slice, reflect.Array:
		return SchemaTypeList
	case reflect.Struct, reflect.Map:
		return SchemaTypeObject
	default:
		return SchemaTypeAny
	}
}
