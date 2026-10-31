package config

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

type explicitRawScope struct {
	value *jsonx.OrderedMap
	scope SchemaScope
}

func validateExplicitRawReadableScopes(ctx *editValidationContext) error {
	for _, source := range []explicitRawScope{
		{value: ctx.rawUser, scope: SchemaScopeUserOnly},
		{value: ctx.rawWorkspace, scope: SchemaScopeWorkspaceOnly},
	} {
		if source.value == nil {
			continue
		}
		for _, root := range []string{"active_provider", "allow", "requests"} {
			value, present := source.value.Get(root)
			if present {
				if err := validateRawReadableScopePath(value, []string{root}, source); err != nil {
					return err
				}
			}
		}
		providersValue, present := source.value.Get(providersKey)
		providers, ok := asMap(providersValue)
		if !present || !ok {
			continue
		}
		for _, name := range providers.Keys() {
			entryValue, _ := providers.Get(name)
			entry, ok := asMap(entryValue)
			if !ok {
				continue
			}
			for _, key := range entry.Keys() {
				// These names belong to the recognized legacy providers table. Its
				// historical scope and facts are validated by the legacy validator.
				if _, legacy := knownProviderKeys[key]; legacy {
					continue
				}
				fieldValue, _ := entry.Get(key)
				if err := validateRawReadableScopePath(fieldValue, []string{providersKey, name, key}, source); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateRawReadableScopePath(value any, path []string, source explicitRawScope) error {
	if rule, ok := readableSchemaRule(path); ok {
		if source.scope == SchemaScopeWorkspaceOnly && rule.Scope == SchemaScopeUserOnly {
			return fmt.Errorf("config.%s: user-scope only; move this key to the user config", strings.Join(path, "."))
		}
		if source.scope == SchemaScopeUserOnly && rule.Scope == SchemaScopeWorkspaceOnly {
			if len(path) > 0 && path[0] == "requests" {
				return fmt.Errorf("config.%s: workspace-only key; this file is user scope; move requests to the workspace config", strings.Join(path, "."))
			}
			return fmt.Errorf("config.%s: workspace-only key; this file is user scope", strings.Join(path, "."))
		}
	}
	switch current := value.(type) {
	case *jsonx.OrderedMap:
		for _, key := range current.Keys() {
			child, _ := current.Get(key)
			if err := validateRawReadableScopePath(child, append(append([]string(nil), path...), key), source); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := validateRawReadableScopePath(child, append(append([]string(nil), path...), "*"), source); err != nil {
				return err
			}
		}
	}
	return nil
}

func reportUnknownKeysWithReadableFG(config *jsonx.OrderedMap, errs *[]string) {
	allowed := make(map[string]struct{}, len(knownTopLevelConfigKeys)+3)
	for key := range knownTopLevelConfigKeys {
		allowed[key] = struct{}{}
	}
	for _, root := range []string{"active_provider", "allow", "requests"} {
		if _, recognized := readableSchemaRule([]string{root}); recognized {
			allowed[root] = struct{}{}
		}
	}
	reportUnknownKeys(config, allowed, "config", errs)
}

func validateProvidersForExplicit(config, readableEffective *jsonx.OrderedMap, ctx *editValidationContext, errs, warns *[]string) {
	validateProviderCredentialScopeConfig(ctx.rawWorkspace, errs)
	legacy := legacyProviderValidationProjection(config)
	validateProviderEntries(legacy, errs, warns, nil)
	semantic, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		Config: readableEffective, User: ctx.rawUser, Workspace: ctx.rawWorkspace,
		UserOrigins:      readableUserOrigins(ctx.rawUser, ctx.userOrigins),
		WorkspaceOrigins: readableWorkspaceOrigins(ctx.rawWorkspace, ctx.workspaceOrigins),
		Packs:            ctx.packs, Catalog: ctx.catalog,
	})
	if err != nil {
		add(errs, err.Error())
		return
	}
	ctx.semantic = &semantic
}

func legacyProviderValidationProjection(config *jsonx.OrderedMap) *jsonx.OrderedMap {
	projected := cloneValidationMap(config)
	if projected == nil {
		return projected
	}
	value, present := projected.Get(providersKey)
	providers, ok := asMap(value)
	if !present || !ok {
		return projected
	}
	decoded, err := packdecl.DecodeProviderTable(config)
	if err != nil {
		return projected
	}
	legacyModels := map[string]map[string]bool{}
	for _, declaration := range decoded.Declarations {
		if len(declaration.Models) == 0 {
			continue
		}
		legacyModels[declaration.Name] = map[string]bool{}
		for id := range declaration.Models {
			legacyModels[declaration.Name][id] = true
		}
	}
	for _, name := range decoded.Disabled {
		providers.Delete(name)
	}
	canonicalOnly := readableOnlyProviderKeys()
	for _, name := range providers.Keys() {
		raw, _ := providers.Get(name)
		entry, ok := asMap(raw)
		if !ok {
			continue
		}
		for key := range canonicalOnly {
			entry.Delete(key)
		}
		if modelsValue, present := entry.Get("models"); present {
			models, ok := asMap(modelsValue)
			if ok {
				for _, id := range models.Keys() {
					if !legacyModels[name][id] {
						models.Delete(id)
					}
				}
			}
		}
		providers.Set(name, entry)
	}
	projected.Set(providersKey, providers)
	return projected
}

// readableOnlyProviderKeys are the provider-level keys only the readable vocabulary spells (its
// schema's providers.*.<key> rows that the legacy knownProviderKeys lacks): a provider's starting
// `model`, `alias`, `via`, `variants` and `every_model` (config-user.md §5). The legacy shape check
// reads an entry without them; their shapes are the readable schema's and the provider
// declaration decoder's to check.
func readableOnlyProviderKeys() map[string]bool {
	out := map[string]bool{}
	for _, key := range ReadableSchemaKeys() {
		parts := strings.Split(key.Path, ".")
		if len(parts) < 3 || parts[0] != providersKey || parts[1] != "*" {
			continue
		}
		if _, legacyKey := knownProviderKeys[parts[2]]; !legacyKey {
			out[parts[2]] = true
		}
	}
	return out
}
