package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ProviderConsumerComposition selects the readable consumer path or retains the exact legacy
// profile selection. A non-nil Readable result means the caller must use the typed provider
// selection; otherwise Legacy is precisely the legacy `profile` key, lowered (legacyProfileSelection).
type ProviderConsumerComposition struct {
	Readable *ProviderSemanticResult
	Legacy   ProfileSelection
}

// activeProviderWithProfileProblem is the refusal for a config holding both selection keys: the
// consumer boundary below and the launch's validation (validateActiveProvider) say it in one voice.
const activeProviderWithProfileProblem = "config: active_provider and legacy profile selectors cannot be composed together; preserve the legacy reader for old files or select through active_provider"

// ConfigProviderConsumer is the error-bearing configuration boundary for F-owned selection
// consumers. It chooses active_provider only when explicitly authored, refuses mixed old/new
// selectors, and otherwise preserves the legacy profile selection unchanged.
func ConfigProviderConsumer(cfg *jsonx.OrderedMap, declarations []packdecl.ProviderContribution,
	packs []*packload.Pack, catalog map[string]map[string]packdecl.ProviderModel) (ProviderConsumerComposition, error) {
	return configProviderConsumer(cfg, declarations, packs, catalog, false)
}

// configProviderConsumer is ConfigProviderConsumer; reportModelGaps is ProviderSemanticInput's.
func configProviderConsumer(cfg *jsonx.OrderedMap, declarations []packdecl.ProviderContribution,
	packs []*packload.Pack, catalog map[string]map[string]packdecl.ProviderModel, reportModelGaps bool) (ProviderConsumerComposition, error) {
	if cfg != nil {
		if _, active := cfg.Get("active_provider"); active {
			if _, legacy := cfg.Get(ProfileKey); legacy {
				return ProviderConsumerComposition{}, fmt.Errorf("%s", activeProviderWithProfileProblem)
			}
			readable, err := composeConfiguredProviderSemantics(cfg, declarations, packs, catalog, reportModelGaps)
			if err != nil {
				return ProviderConsumerComposition{}, err
			}
			return ProviderConsumerComposition{Readable: &readable}, nil
		}
	}
	return ProviderConsumerComposition{Legacy: legacyProfileSelection(cfg)}, nil
}

// ClearNativeProviderEnvForReceiver clears only provider-owned environment declarations when
// the semantic selection explicitly chose native. Callers pass their composed environment
// names through decided so values already set or removed by the launcher remain authoritative.
func ClearNativeProviderEnvForReceiver(result ProviderSemanticResult, packs []*packload.Pack,
	receiver string, environ []string, decided func(string) bool) ([]string, []string) {
	if !result.Selection.NativeReceivers[receiver] {
		return environ, nil
	}
	variables := packload.ProviderEnvFor(packs, receiver, "")
	return packload.ClearProviderEnv(environ, variables, decided)
}

// ComposeConfiguredProviderSemantics is the explicit consumer boundary for readable
// provider declarations and active_provider. It parses only the named selection key,
// derives eligible receivers from selected pack declarations, and delegates all selection,
// model composition, and legacy derive projection to the shared semantic implementation.
// It does not alter the launch selection (ConfigActiveSelection) or activate runtime environment changes.
func ComposeConfiguredProviderSemantics(cfg *jsonx.OrderedMap,
	declarations []packdecl.ProviderContribution, packs []*packload.Pack,
	catalog map[string]map[string]packdecl.ProviderModel) (ProviderSemanticResult, error) {
	return composeConfiguredProviderSemantics(cfg, declarations, packs, catalog, false)
}

func composeConfiguredProviderSemantics(cfg *jsonx.OrderedMap,
	declarations []packdecl.ProviderContribution, packs []*packload.Pack,
	catalog map[string]map[string]packdecl.ProviderModel, reportModelGaps bool) (ProviderSemanticResult, error) {
	selection, err := ConfigActiveProviderSelection(cfg)
	if err != nil {
		return ProviderSemanticResult{}, err
	}
	return ComposeProviderSemantics(ProviderSemanticInput{
		Declarations:    declarations,
		Selection:       selection,
		Receivers:       ProviderReceivers(packs),
		Catalog:         catalog,
		ReportModelGaps: reportModelGaps,
	})
}

// ConfigActiveProviderSelection lowers one explicit active_provider object. The old
// profile config readers remain separate; this function is error-bearing so callers cannot
// silently treat a malformed new selection as legacy profile input or as no selection.
func ConfigActiveProviderSelection(cfg *jsonx.OrderedMap) (ActiveProviderSelection, error) {
	var selection ActiveProviderSelection
	if cfg == nil {
		return selection, nil
	}
	raw, exists := cfg.Get("active_provider")
	if !exists {
		return selection, nil
	}
	entries, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return selection, fmt.Errorf("config.active_provider: expected an object")
	}
	for _, receiver := range entries.Keys() {
		value, _ := entries.Get(receiver)
		path := "config.active_provider." + receiver
		selectors, err := activeProviderSelectors(path, value)
		if err != nil {
			return ActiveProviderSelection{}, err
		}
		if receiver == "default" {
			selection.Default = selectors
			continue
		}
		if selection.Named == nil {
			selection.Named = make(map[string][]string)
		}
		selection.Named[receiver] = selectors
	}
	return selection, nil
}

func activeProviderSelectors(path string, raw any) ([]string, error) {
	if selector, ok := raw.(string); ok {
		if selector == "" {
			return nil, fmt.Errorf("%s: expected a non-empty provider, provider/variant, or native selector", path)
		}
		return []string{selector}, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a provider selector string or list of selector strings", path)
	}
	selectors := make([]string, len(values))
	for i, value := range values {
		selector, ok := value.(string)
		if !ok || selector == "" {
			return nil, fmt.Errorf("%s[%d]: expected a non-empty provider, provider/variant, or native selector", path, i)
		}
		selectors[i] = selector
	}
	return selectors, nil
}

// LegacyProviderConsumerNormalization is the all-or-nothing projection of the old effective
// provider/profile boundary into readable provider declarations. Canonical and ProfileSelectors
// are populated only when every provider and profile field is representable.
type LegacyProviderConsumerNormalization struct {
	Canonical        []packdecl.ProviderContribution
	ProfileSelectors map[string]string
	Differences      []ProviderModelDifference
}

// NormalizeLegacyProviderConsumers accepts the complete effective tables emitted by
// packload.ComposeProviders and packload.ResolveProfiles. It decodes every legacy provider
// field before lowering provider/profile semantics; effective-only fields without a canonical
// representation are named as differences rather than silently discarded.
func NormalizeLegacyProviderConsumers(composed *jsonx.OrderedMap,
	profiles map[string]packload.ResolvedProfile,
	offline map[string]map[string]packdecl.ProviderModel) LegacyProviderConsumerNormalization {
	declarations, facts, differences := decodeEffectiveLegacyProviders(composed)
	if offline == nil {
		offline = facts
	} else {
		offline = mergeOfflineProviderFacts(facts, offline)
	}
	result := normalizeLegacyProviderDeclarations(declarations, profiles, offline)
	result.Differences = append(result.Differences, differences...)
	if len(result.Differences) != 0 {
		result.Canonical = nil
		result.ProfileSelectors = nil
	}
	return result
}

func decodeEffectiveLegacyProviders(composed *jsonx.OrderedMap) ([]packdecl.ProviderContribution, map[string]map[string]packdecl.ProviderModel, []ProviderModelDifference) {
	if composed == nil {
		return nil, map[string]map[string]packdecl.ProviderModel{}, nil
	}
	var declarations []packdecl.ProviderContribution
	facts := map[string]map[string]packdecl.ProviderModel{}
	var differences []ProviderModelDifference
	for _, name := range composed.Keys() {
		path := "providers." + name
		raw, _ := composed.Get(name)
		entry, ok := raw.(*jsonx.OrderedMap)
		if !ok {
			differences = append(differences, ProviderModelDifference{Path: path, Reason: "effective provider entry is not an object and has no canonical provider declaration"})
			continue
		}
		declaration := packdecl.ProviderContribution{Name: name}
		var routingByAlias *jsonx.OrderedMap
		for _, key := range entry.Keys() {
			value, _ := entry.Get(key)
			switch key {
			case "api_key_env_name":
				envNames, valid := packdecl.EnvNamesFromValue(value)
				if !valid {
					differences = append(differences, ProviderModelDifference{Path: path + ".api_key_env_name", Reason: "effective value is not a declared variable name or list of names"})
				} else {
					declaration.APIKeyEnvName = envNames
				}
			case "platform":
				declaration.Platform = effectiveString(&differences, path+".platform", value)
			case "region":
				declaration.Region = effectiveString(&differences, path+".region", value)
			case "capabilities":
				declaration.Capabilities = effectiveStringList(&differences, path+".capabilities", value)
			case "options":
				options, valid := effectiveMap(value)
				if !valid {
					differences = append(differences, ProviderModelDifference{Path: path + ".options", Reason: "effective value is not an option declaration map"})
					continue
				}
				declaration.Options = make(map[string]packdecl.OptionDefault, options.Len())
				for _, option := range options.Keys() {
					v, _ := options.Get(option)
					if v == nil {
						declaration.Options[option] = packdecl.OptionDefault{}
					} else if text, isString := v.(string); isString {
						declaration.Options[option] = packdecl.OptionDefault{Defaulted: true, Value: text}
					} else {
						differences = append(differences, ProviderModelDifference{Path: path + ".options." + option, Reason: "effective option default is neither a string nor a declared no-default value"})
					}
				}
			case "endpoints":
				endpoints, valid := effectiveMap(value)
				if !valid {
					differences = append(differences, ProviderModelDifference{Path: path + ".endpoints", Reason: "effective value is not an endpoint map"})
					continue
				}
				declaration.Endpoints = make(map[string]packdecl.ProviderEndpoint, endpoints.Len())
				for _, protocol := range endpoints.Keys() {
					endpointPath := path + ".endpoints." + protocol
					rawEndpoint, _ := endpoints.Get(protocol)
					endpoint, valid := effectiveMap(rawEndpoint)
					if !valid {
						differences = append(differences, ProviderModelDifference{Path: endpointPath, Reason: "effective endpoint is not an object"})
						continue
					}
					var decoded packdecl.ProviderEndpoint
					for _, endpointKey := range endpoint.Keys() {
						v, _ := endpoint.Get(endpointKey)
						switch endpointKey {
						case "base_url":
							decoded.BaseURL = effectiveString(&differences, endpointPath+".base_url", v)
						case "wire_api":
							decoded.WireAPI = effectiveString(&differences, endpointPath+".wire_api", v)
						default:
							differences = append(differences, ProviderModelDifference{Path: endpointPath + "." + endpointKey, Reason: "effective endpoint field has no canonical mapping"})
						}
					}
					declaration.Endpoints[protocol] = decoded
				}
			case "models":
				models, valid := effectiveMap(value)
				if !valid {
					differences = append(differences, ProviderModelDifference{Path: path + ".models", Reason: "effective value is not a model alias map"})
					continue
				}
				declaration.Models = make(map[string]string, models.Len())
				for _, alias := range models.Keys() {
					v, _ := models.Get(alias)
					if id, isString := v.(string); isString {
						declaration.Models[alias] = id
					} else {
						differences = append(differences, ProviderModelDifference{Path: path + ".models." + alias, Reason: "effective alias binding is not a wire-id string"})
					}
				}
			case "model_options":
				modelOptions, valid := effectiveMap(value)
				if !valid {
					differences = append(differences, ProviderModelDifference{Path: path + ".model_options", Reason: "effective value is not a model-fact map"})
					continue
				}
				declaration.ModelOptions = packdecl.ModelFacts{}
				for _, alias := range modelOptions.Keys() {
					v, _ := modelOptions.Get(alias)
					if v == nil {
						declaration.ModelOptions[alias] = nil
						continue
					}
					row, valid := effectiveMap(v)
					if !valid {
						differences = append(differences, ProviderModelDifference{Path: path + ".model_options." + alias, Reason: "effective model facts are not an object"})
						continue
					}
					declaration.ModelOptions[alias] = map[string]*string{}
					for _, fact := range row.Keys() {
						factValue, _ := row.Get(fact)
						if factValue == nil {
							declaration.ModelOptions[alias][fact] = nil
						} else if text, isString := factValue.(string); isString {
							copied := text
							declaration.ModelOptions[alias][fact] = &copied
						} else {
							differences = append(differences, ProviderModelDifference{Path: path + ".model_options." + alias + "." + fact, Reason: "effective flat model fact is neither a string nor a tombstone"})
						}
					}
				}
			case "models_only":
				if value == true {
					differences = append(differences, ProviderModelDifference{Path: path + ".models_only", Reason: "legacy model-list presence enforcement has no provider declaration field"})
				}
			case "model_routing":
				routingByAlias, ok = effectiveMap(value)
				if !ok {
					differences = append(differences, ProviderModelDifference{Path: path + ".model_routing", Reason: "effective value is not a per-model routing map"})
				}
			case "model_shared":
				if shared, valid := effectiveMap(value); !valid || shared.Len() != 0 {
					differences = append(differences, ProviderModelDifference{Path: path + ".model_shared", Reason: "effective shared-model projection has no independent canonical provider field"})
				}
			case "wire_api":
				differences = append(differences, ProviderModelDifference{Path: path + ".wire_api", Reason: "provider-level wire_api has no protocol key and cannot be assigned to an endpoint without guessing"})
			default:
				differences = append(differences, ProviderModelDifference{Path: path + "." + key, Reason: "effective composed provider field has no canonical declaration mapping"})
			}
		}
		if routingByAlias != nil {
			for _, alias := range routingByAlias.Keys() {
				rawRouting, _ := routingByAlias.Get(alias)
				routing, valid := effectiveMap(rawRouting)
				if !valid {
					differences = append(differences, ProviderModelDifference{Path: path + ".model_routing." + alias, Reason: "effective OpenRouter routing value is not an object"})
					continue
				}
				wireID, bound := declaration.Models[alias]
				if !bound {
					differences = append(differences, ProviderModelDifference{Path: path + ".model_routing." + alias, Reason: "routing row has no effective model alias binding"})
					continue
				}
				if facts[name] == nil {
					facts[name] = map[string]packdecl.ProviderModel{}
				}
				model := facts[name][wireID]
				if model.OpenRouterRouting != nil && !reflect.DeepEqual(model.OpenRouterRouting, effectiveNativeMap(routing)) {
					differences = append(differences, ProviderModelDifference{Path: path + ".model_routing." + alias, Reason: fmt.Sprintf("aliases sharing wire id %q carry different OpenRouter routing facts", wireID)})
					continue
				}
				model.OpenRouterRouting = effectiveNativeMap(routing)
				facts[name][wireID] = model
			}
		}
		declarations = append(declarations, declaration)
		if facts[name] == nil {
			facts[name] = map[string]packdecl.ProviderModel{}
		}
	}
	if len(differences) != 0 {
		sortProviderModelDifferences(differences)
	}
	return declarations, facts, differences
}

func effectiveMap(value any) (*jsonx.OrderedMap, bool) {
	m, ok := value.(*jsonx.OrderedMap)
	return m, ok
}

func effectiveString(differences *[]ProviderModelDifference, path string, value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	*differences = append(*differences, ProviderModelDifference{Path: path, Reason: "effective value is not a string"})
	return ""
}

func effectiveStringList(differences *[]ProviderModelDifference, path string, value any) []string {
	values, ok := value.([]any)
	if !ok {
		*differences = append(*differences, ProviderModelDifference{Path: path, Reason: "effective value is not a list of strings"})
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			*differences = append(*differences, ProviderModelDifference{Path: fmt.Sprintf("%s[%d]", path, i), Reason: "effective list member is not a string"})
			continue
		}
		out[i] = text
	}
	return out
}

func effectiveNativeMap(value *jsonx.OrderedMap) map[string]any {
	out := make(map[string]any, value.Len())
	for _, key := range value.Keys() {
		child, _ := value.Get(key)
		out[key] = effectiveNativeValue(child)
	}
	return out
}

func effectiveNativeValue(value any) any {
	switch typed := value.(type) {
	case *jsonx.OrderedMap:
		return effectiveNativeMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = effectiveNativeValue(child)
		}
		return out
	default:
		return value
	}
}

func mergeOfflineProviderFacts(base, supplied map[string]map[string]packdecl.ProviderModel) map[string]map[string]packdecl.ProviderModel {
	out := make(map[string]map[string]packdecl.ProviderModel, len(base)+len(supplied))
	for provider, rows := range base {
		out[provider] = cloneOfflineModelFacts(rows)
	}
	for provider, rows := range supplied {
		if out[provider] == nil {
			out[provider] = cloneOfflineModelFacts(rows)
			continue
		}
		for id, facts := range rows {
			if existing, present := out[provider][id]; present {
				out[provider][id] = mergeProviderModel(facts, existing)
			} else {
				out[provider][id] = facts
			}
		}
	}
	return out
}

func cloneOfflineModelFacts(rows map[string]packdecl.ProviderModel) map[string]packdecl.ProviderModel {
	if rows == nil {
		return nil
	}
	out := make(map[string]packdecl.ProviderModel, len(rows))
	for id, facts := range rows {
		out[id] = cloneProviderModel(facts)
	}
	return out
}

func normalizeLegacyProviderDeclarations(declarations []packdecl.ProviderContribution,
	profiles map[string]packload.ResolvedProfile,
	offline map[string]map[string]packdecl.ProviderModel) LegacyProviderConsumerNormalization {
	result := LegacyProviderConsumerNormalization{}
	providers := make(map[string]packdecl.ProviderContribution, len(declarations))
	for _, declaration := range declarations {
		if _, duplicate := providers[declaration.Name]; duplicate {
			result.addDifference("providers."+declaration.Name, "multiple legacy declarations share this provider name; later-wins composition must be resolved before canonical conversion")
			continue
		}
		declaration = cloneProviderContribution(declaration)
		if modelOption, present := declaration.Options["model"]; present {
			if modelOption.Defaulted {
				if modelOption.Value == "" {
					result.addDifference("providers."+declaration.Name+".options.model", "an empty default cannot be distinguished from an omitted provider model")
				} else if declaration.Model != "" && declaration.Model != modelOption.Value {
					result.addDifference("providers."+declaration.Name+".options.model", fmt.Sprintf("default %q conflicts with provider model %q", modelOption.Value, declaration.Model))
				} else {
					declaration.Model = modelOption.Value
					delete(declaration.Options, "model")
				}
			} else if declaration.Model != "" {
				result.addDifference("providers."+declaration.Name+".options.model", "a defaultless model option conflicts with the provider's model default")
			}
		}
		normalized := NormalizeLegacyProviderModels(declaration, offline[declaration.Name])
		for _, difference := range normalized.Differences {
			result.Differences = append(result.Differences, ProviderModelDifference{
				Path:   "providers." + declaration.Name + "." + strings.TrimPrefix(difference.Path, "providers."+declaration.Name+"."),
				Reason: difference.Reason,
			})
		}
		if normalized.Canonical == nil {
			providers[declaration.Name] = declaration
			continue
		}
		providers[normalized.Canonical.Name] = *normalized.Canonical
	}

	selectors := make(map[string]string, len(profiles))
	profileNames := make([]string, 0, len(profiles))
	for name := range profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, name := range profileNames {
		profile := profiles[name]
		path := "profiles." + name
		providerName := profile.Provider
		declaration, exists := providers[providerName]
		if !exists {
			result.addDifference(path+".provider", fmt.Sprintf("provider %q is absent from the effective provider declarations", profile.Provider))
			continue
		}
		if profile.Carrier != "" {
			result.addDifference(path+".carrier", "legacy carrier routing is computed from the selected pack/protocol closure and has no provider/variant field")
		}
		if profile.CarrierBase != "" {
			result.addDifference(path+".carrier_base", "legacy carrier address is runtime-derived and has no authored provider/variant field")
		}
		if len(profile.Carried) != 0 {
			result.addDifference(path+".carried", "legacy per-agent carrier membership is computed from selected program and protocol declarations")
		}
		if profile.Via != "" && profile.ViaBase == "" {
			result.addDifference(path+".via", "legacy via has no resolved service address in the effective pack composition")
		}
		if profile.Via == "" && profile.ViaBase != "" {
			result.addDifference(path+".via_base", "legacy via address exists without a via service name")
		}
		if profile.Via == "" && declaration.Via != "" {
			result.addDifference(path+".via", "legacy profile leaves via unset while the provider declaration selects a via; a variant's omitted via inherits the provider setting")
		}

		variant := packdecl.ProviderVariant{}
		variantNeeded := false
		legacyModel, modelSet := profile.Options["model"]
		if !modelSet && declaration.Model != "" {
			result.addDifference(path+".options.model", fmt.Sprintf("legacy resolved profile has no model value while provider default is %q; provider/variant model inheritance would add it", declaration.Model))
		}
		if modelSet {
			if legacyModel == "" {
				result.addDifference(path+".options.model", "an explicit empty model cannot be distinguished from an omitted provider/variant model")
			} else if legacyModel != declaration.Model {
				variant.Model = legacyModel
				variantNeeded = true
			}
		}
		optionNames := make([]string, 0, len(profile.Options))
		for optionName := range profile.Options {
			optionNames = append(optionNames, optionName)
		}
		sort.Strings(optionNames)
		for _, optionName := range optionNames {
			if optionName == "model" {
				continue
			}
			value := profile.Options[optionName]
			defaultValue, declared := declaration.Options[optionName]
			if !declared {
				result.addDifference(path+".options."+optionName, "legacy profile option has no declared provider option, which the canonical variant requires")
				continue
			}
			if defaultValue.Defaulted && defaultValue.Value == value {
				continue
			}
			variant.Options = ensureOptionMap(variant.Options)
			variant.Options[optionName] = packdecl.OptionDefault{Defaulted: true, Value: value}
			variantNeeded = true
		}
		for optionName, defaultValue := range declaration.Options {
			if optionName == "model" || !defaultValue.Defaulted {
				continue
			}
			if _, exists := profile.Options[optionName]; !exists {
				variant.Options = ensureOptionMap(variant.Options)
				variant.Options[optionName] = packdecl.OptionDefault{}
				variantNeeded = true
			}
		}
		if profile.Via != declaration.Via {
			variant.Via = profile.Via
			variantNeeded = true
		}
		if profile.EnforceModels != nil {
			value := *profile.EnforceModels
			variant.EnforceModels = &value
			variantNeeded = true
		}
		if profile.Subagents.Default != "" || len(profile.Subagents.Roles) != 0 {
			variant.Subagents = legacySubagentsValue(profile.Subagents)
			variantNeeded = true
		}
		if !variantNeeded && name == providerName {
			selectors[name] = providerName
			continue
		}
		if problem := providerNameProblem(name); problem != "" || strings.Contains(name, "/") {
			if problem == "" {
				problem = "variant names cannot contain '/'"
			}
			result.addDifference(path, "profile cannot be represented as a provider variant: "+problem)
			continue
		}
		if _, collision := declaration.Variants[name]; collision {
			result.addDifference("providers."+providerName+".variants."+name, "variant name is already declared and cannot be replaced by the legacy profile")
			continue
		}
		declaration.Variants = cloneProviderVariants(declaration.Variants)
		declaration.Variants[name] = variant
		providers[providerName] = declaration
		selectors[name] = providerName + "/" + name
	}

	if len(result.Differences) == 0 {
		canonical := make([]packdecl.ProviderContribution, 0, len(providers))
		for _, declaration := range providers {
			canonical = append(canonical, declaration)
		}
		sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
		if _, err := ResolveActiveProviders(canonical, ActiveProviderSelection{}, nil); err != nil {
			result.addDifference("providers", "provider namespace or declaration is not representable without changing its contract: "+err.Error())
		} else {
			result.Canonical = canonical
			result.ProfileSelectors = selectors
		}
	}
	sortProviderModelDifferences(result.Differences)
	return result
}

func (result *LegacyProviderConsumerNormalization) addDifference(path, reason string) {
	result.Differences = append(result.Differences, ProviderModelDifference{Path: path, Reason: reason})
}

func ensureOptionMap(options map[string]packdecl.OptionDefault) map[string]packdecl.OptionDefault {
	if options == nil {
		return map[string]packdecl.OptionDefault{}
	}
	return options
}

func legacySubagentsValue(subagents luahook.Subagents) map[string]any {
	out := map[string]any{}
	if subagents.Default != "" {
		out["default"] = subagents.Default
	}
	if len(subagents.Roles) != 0 {
		roles := make(map[string]any, len(subagents.Roles))
		for role, model := range subagents.Roles {
			roles[role] = model
		}
		out["roles"] = roles
	}
	return out
}

func cloneProviderVariants(variants map[string]packdecl.ProviderVariant) map[string]packdecl.ProviderVariant {
	if variants == nil {
		return map[string]packdecl.ProviderVariant{}
	}
	out := make(map[string]packdecl.ProviderVariant, len(variants))
	for name, variant := range variants {
		variant.Options = cloneOptionDefaults(variant.Options)
		variant.Subagents = cloneAnyMap(variant.Subagents)
		variant.Capabilities = cloneStrings(variant.Capabilities)
		variant.Protocols = cloneStrings(variant.Protocols)
		if variant.EnforceModels != nil {
			value := *variant.EnforceModels
			variant.EnforceModels = &value
		}
		out[name] = variant
	}
	return out
}
