package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// LegacyProviderModelSnapshot owns the exact legacy alias and flat-fact maps. It deliberately
// retains Go nil/empty distinctions, nil fact rows, tombstones, opaque keys and string lexemes.
type LegacyProviderModelSnapshot struct {
	Models       map[string]string
	ModelOptions packdecl.ModelFacts
}

// ProviderModelDifference names a legacy fact or alias behavior not representable by canonical
// ID rows without losing information. Paths use the old providers.<name> vocabulary.
type ProviderModelDifference struct {
	Path   string
	Reason string
}

// LegacyProviderModelNormalization carries both the preserved legacy declaration and, only when
// the entire model declaration is representable, its canonical provider declaration. Callers may
// always restore Snapshot; they must not treat Differences as a partial-conversion permission.
type LegacyProviderModelNormalization struct {
	Snapshot    LegacyProviderModelSnapshot
	Canonical   *packdecl.ProviderContribution
	Differences []ProviderModelDifference
}

// NormalizeLegacyProviderModels interprets the existing Models alias→wire map and flat
// ModelOptions facts using only explicitly supplied offline facts. Equal aliases for one wire ID
// can share a canonical row only when their complete parsed facts agree. No source is discovered
// here, and no catalog/provider defaults are guessed.
func NormalizeLegacyProviderModels(provider packdecl.ProviderContribution, offline map[string]packdecl.ProviderModel) LegacyProviderModelNormalization {
	result := LegacyProviderModelNormalization{Snapshot: LegacyProviderModelSnapshot{
		Models: cloneLegacyModels(provider.Models), ModelOptions: cloneLegacyModelFacts(provider.ModelOptions),
	}}
	canonical := cloneProviderContribution(provider)
	canonical.Models = nil
	canonical.ModelOptions = nil
	if provider.Models == nil {
		if len(provider.ModelOptions) != 0 {
			for alias := range provider.ModelOptions {
				result.addDifference("model_options."+alias, "has no legacy model binding to supply a canonical ID/wire identity")
			}
		}
		if len(result.Differences) == 0 {
			result.Canonical = &canonical
		}
		sortProviderModelDifferences(result.Differences)
		return result
	}

	if provider.ModelRows != nil && provider.Models != nil {
		result.addDifference("model_rows", "canonical ID rows and legacy model bindings coexist, including explicit empty legacy maps; their precedence/order must be resolved by the owning layer")
	}
	if provider.EveryModel != nil {
		result.addDifference("every_model", "shared model facts are not emitted by the legacy provider composer and cannot be safely activated alongside legacy model bindings")
	}
	canonical.ModelRows = make(map[string]packdecl.ProviderModel)
	type legacyModelOrder struct {
		hasValue bool
		value    int
	}
	orderByID := map[string]legacyModelOrder{}
	wireFacts := map[string]packdecl.ProviderModel{}
	wireRow := map[string]string{}
	aliases := make([]string, 0, len(provider.Models))
	for alias := range provider.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		wire := provider.Models[alias]
		if alias == "" || wire == "" {
			result.addDifference("models."+alias, "empty aliases or wire IDs have no canonical model identity")
			continue
		}
		if alias != wire {
			result.addDifference("models."+alias, fmt.Sprintf("legacy selectable alias %q differs from wire ID %q; canonical rows cannot preserve that selector key", alias, wire))
		}
		facts, order, hasOrder, diffs := legacyFactsToProviderModel(alias, wire, provider.ModelOptions[alias], offline[wire])
		result.Differences = append(result.Differences, diffs...)
		if priorID, ok := wireRow[wire]; ok {
			if !reflect.DeepEqual(wireFacts[wire], facts) {
				result.addDifference("models."+alias, fmt.Sprintf("shares wire ID %q with %q but complete model facts differ", wire, priorID))
			}
			continue
		}
		wireRow[wire] = alias
		wireFacts[wire] = facts
		canonical.ModelRows[alias] = facts
		orderByID[alias] = legacyModelOrder{hasValue: hasOrder, value: order}
	}
	// Options with no model binding are visible to old consumers but cannot become canonical rows.
	for alias := range provider.ModelOptions {
		if _, ok := provider.Models[alias]; !ok {
			result.addDifference("model_options."+alias, "has no matching models alias and cannot be assigned a canonical ID")
		}
	}
	if len(provider.Models) == 0 {
		canonical.ModelRows = map[string]packdecl.ProviderModel{}
		canonical.ModelOrder = []string{}
	} else {
		canonical.ModelOrder = make([]string, 0, len(canonical.ModelRows))
		for id := range canonical.ModelRows {
			canonical.ModelOrder = append(canonical.ModelOrder, id)
		}
		sort.Slice(canonical.ModelOrder, func(i, j int) bool {
			a, b := canonical.ModelOrder[i], canonical.ModelOrder[j]
			oa, ob := orderByID[a], orderByID[b]
			if oa.hasValue != ob.hasValue {
				return oa.hasValue
			}
			if oa.value != ob.value {
				return oa.value < ob.value
			}
			return a < b
		})
	}
	if len(result.Differences) == 0 {
		result.Canonical = &canonical
	}
	sortProviderModelDifferences(result.Differences)
	return result
}

// RestoreLegacyProviderModels returns an independent declaration with the exact captured legacy
// maps, including nil maps/rows and original numeric string spelling.
func (n LegacyProviderModelNormalization) RestoreLegacyProviderModels(provider packdecl.ProviderContribution) packdecl.ProviderContribution {
	out := cloneProviderContribution(provider)
	out.Models = cloneLegacyModels(n.Snapshot.Models)
	out.ModelOptions = cloneLegacyModelFacts(n.Snapshot.ModelOptions)
	return out
}

func (n *LegacyProviderModelNormalization) addDifference(path, reason string) {
	n.Differences = append(n.Differences, ProviderModelDifference{Path: path, Reason: reason})
}

func legacyFactsToProviderModel(alias, wire string, flat map[string]*string, offline packdecl.ProviderModel) (packdecl.ProviderModel, int, bool, []ProviderModelDifference) {
	model := cloneProviderModel(offline)
	var diffs []ProviderModelDifference
	add := func(key, why string) {
		path := "model_options." + alias
		if key != "" {
			path += "." + key
		}
		diffs = append(diffs, ProviderModelDifference{Path: path, Reason: why})
	}
	if flat == nil {
		flat = map[string]*string{}
	}
	order := 0
	hasOrder := false
	setString := func(key string, dst *string) {
		v, ok := flat[key]
		if !ok {
			return
		}
		if v == nil {
			add(key, "tombstones have no canonical fact-presence representation")
			return
		}
		*dst = *v
	}
	setIntegerAliases := func(keys []string, dst *int64) {
		var selected *int64
		for index, key := range keys {
			v, ok := flat[key]
			if !ok {
				continue
			}
			if v == nil {
				add(key, "tombstones have no canonical fact-presence representation")
				continue
			}
			n, err := strconv.ParseInt(strings.TrimSpace(*v), 10, 64)
			if err != nil || n < 0 {
				add(key, "is not a non-negative integer canonical fact")
				continue
			}
			if n == 0 {
				add(key, "authored zero is omitted by the canonical projection and can change the legacy consumer fallback")
				continue
			}
			if index > 0 {
				primary, present := flat[keys[0]]
				primaryValue := int64(0)
				primaryValid := false
				if present && primary != nil {
					parsed, err := strconv.ParseInt(strings.TrimSpace(*primary), 10, 64)
					if err == nil && parsed > 0 {
						primaryValue, primaryValid = parsed, true
					}
				}
				if !primaryValid || primaryValue != n {
					add(key, fmt.Sprintf("alternate spelling is not consumed by Pi's model-list path without an equivalent %s fact", keys[0]))
					continue
				}
			}
			if selected == nil {
				value := n
				selected = &value
			} else if *selected != n {
				add(key, "conflicts with another legacy spelling for the same canonical fact")
			}
		}
		if selected != nil {
			*dst = *selected
		}
	}
	setBool := func(key string, dst **bool) {
		v, ok := flat[key]
		if !ok {
			return
		}
		if v == nil {
			add(key, "tombstones have no canonical fact-presence representation")
			return
		}
		b := false
		switch *v {
		case "true":
			b = true
		case "false":
		default:
			add(key, "is not an exact legacy boolean spelling (use true or false)")
			return
		}
		*dst = &b
	}
	setString("name", &model.Name)
	setString("description", &model.Description)
	setString("vendor", &model.Vendor)
	setString("api", &model.API)
	setString("base", &model.Base)
	setString("extends", &model.Extends)
	setIntegerAliases([]string{"context_window", "max_context_tokens"}, &model.Context)
	setIntegerAliases([]string{"max_tokens", "max_output_tokens"}, &model.MaxTokens)
	setBool("reasoning", &model.Reasoning)
	setBool("strict_tools", &model.StrictTools)
	if v, ok := flat["input"]; ok {
		if v == nil {
			add("input", "tombstones have no canonical fact-presence representation")
		} else {
			model.Accepts = strings.Split(*v, ",")
			for i := range model.Accepts {
				model.Accepts[i] = strings.TrimSpace(model.Accepts[i])
			}
		}
	}
	if v, ok := flat["order"]; ok {
		if v == nil {
			add("order", "tombstones have no canonical ordering representation")
		} else if n, err := strconv.Atoi(strings.TrimSpace(*v)); err != nil || n < 0 {
			add("order", "is not a non-negative integer")
		} else {
			order = n
			hasOrder = true
		}
	}
	if v, ok := flat["wire_id"]; ok {
		if v == nil {
			add("wire_id", "wire identity tombstones cannot be represented")
		} else if *v != wire {
			add("wire_id", fmt.Sprintf("conflicts with models.%s wire identity %q", alias, wire))
		} else {
			model.WireID = *v
		}
	}
	prices := map[string]**float64{}
	ensurePrice := func() {
		if model.Price == nil {
			model.Price = &packdecl.ProviderPrice{}
		}
		prices["cost_input"] = &model.Price.Input
		prices["cost_output"] = &model.Price.Output
		prices["cost_cache_read"] = &model.Price.CacheRead
		prices["cost_cache_write"] = &model.Price.CacheWrite
	}
	for _, key := range []string{"cost_input", "cost_output", "cost_cache_read", "cost_cache_write"} {
		v, ok := flat[key]
		if !ok {
			continue
		}
		if v == nil {
			add(key, "tombstones have no canonical fact-presence representation")
			continue
		}
		ensurePrice()
		n, err := strconv.ParseFloat(strings.TrimSpace(*v), 64)
		if err != nil {
			add(key, "is not a numeric canonical fact")
			continue
		}
		p := n
		*prices[key] = &p
	}
	known := map[string]bool{"name": true, "description": true, "vendor": true, "api": true, "base": true, "extends": true, "context_window": true, "max_context_tokens": true, "max_tokens": true, "max_output_tokens": true, "reasoning": true, "strict_tools": true, "input": true, "order": true, "wire_id": true, "cost_input": true, "cost_output": true, "cost_cache_read": true, "cost_cache_write": true}
	for key := range flat {
		if !known[key] {
			add(key, "opaque legacy model fact has no declared canonical mapping")
		}
	}
	if model.Vendor == "" {
		add("vendor", "canonical composition would infer a maker or refuse; legacy omission must remain unguessed")
	}
	if err := validateProviderModelFacts(model); err != nil {
		add("", "parsed canonical fact set is invalid: "+err.Error())
	}
	if err := completeProviderPrice(model.Price); err != nil {
		add("price", err.Error())
	}
	if model.Extends != "" {
		add("extends", "depends on legacy shared model data not carried by ProviderContribution")
	}
	if model.WireID != "" && model.WireID != wire {
		add("wire_id", "offline/model fact wire identity conflicts with the alias binding")
	}
	return model, order, hasOrder, diffs
}

func sortProviderModelDifferences(differences []ProviderModelDifference) {
	sort.Slice(differences, func(i, j int) bool {
		if differences[i].Path != differences[j].Path {
			return differences[i].Path < differences[j].Path
		}
		return differences[i].Reason < differences[j].Reason
	})
}

func cloneLegacyModels(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func cloneLegacyModelFacts(src packdecl.ModelFacts) packdecl.ModelFacts {
	if src == nil {
		return nil
	}
	dst := make(packdecl.ModelFacts, len(src))
	for alias, row := range src {
		if row == nil {
			dst[alias] = nil
			continue
		}
		copied := make(map[string]*string, len(row))
		for key, value := range row {
			if value == nil {
				copied[key] = nil
			} else {
				v := *value
				copied[key] = &v
			}
		}
		dst[alias] = copied
	}
	return dst
}
func cloneProviderContribution(src packdecl.ProviderContribution) packdecl.ProviderContribution {
	out := src
	out.Models = cloneLegacyModels(src.Models)
	out.ModelOptions = cloneLegacyModelFacts(src.ModelOptions)
	if src.ModelRows != nil {
		out.ModelRows = make(map[string]packdecl.ProviderModel, len(src.ModelRows))
		for k, v := range src.ModelRows {
			out.ModelRows[k] = cloneLegacyProviderModel(v)
		}
	}
	if src.ModelOrder != nil {
		out.ModelOrder = cloneStrings(src.ModelOrder)
	}
	out.VariantOrder = cloneStrings(src.VariantOrder)
	if src.EveryModel != nil {
		m := cloneLegacyProviderModel(*src.EveryModel)
		out.EveryModel = &m
	}
	if src.Variants != nil {
		out.Variants = make(map[string]packdecl.ProviderVariant, len(src.Variants))
		for k, variant := range src.Variants {
			variant.Options = cloneOptionDefaults(variant.Options)
			variant.Subagents = cloneLegacyAnyMap(variant.Subagents)
			if variant.EnforceModels != nil {
				value := *variant.EnforceModels
				variant.EnforceModels = &value
			}
			variant.Capabilities = cloneStrings(variant.Capabilities)
			variant.Protocols = cloneStrings(variant.Protocols)
			out.Variants[k] = variant
		}
	}
	if src.Endpoints != nil {
		out.Endpoints = make(map[string]packdecl.ProviderEndpoint, len(src.Endpoints))
		for k, v := range src.Endpoints {
			out.Endpoints[k] = v
		}
	}
	if src.Options != nil {
		out.Options = make(map[string]packdecl.OptionDefault, len(src.Options))
		for k, v := range src.Options {
			out.Options[k] = v
		}
	}
	if src.RegionFile != nil {
		region := *src.RegionFile
		out.RegionFile = &region
	}
	out.APIKeyEnvName = packdecl.EnvNames(cloneStrings(src.APIKeyEnvName))
	out.RegionEnvName = cloneStrings(src.RegionEnvName)
	out.Capabilities = cloneStrings(src.Capabilities)
	return out
}

func cloneLegacyProviderModel(model packdecl.ProviderModel) packdecl.ProviderModel {
	out := cloneProviderModel(model)
	out.OpenRouterRouting = cloneLegacyAnyMap(model.OpenRouterRouting)
	return out
}

func cloneLegacyAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if value == nil {
			out[key] = nil
			continue
		}
		out[key] = cloneLegacyValue(reflect.ValueOf(value)).Interface()
	}
	return out
}

func cloneLegacyValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneLegacyValue(value.Elem())
		out := reflect.New(value.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneLegacyValue(value.Elem()))
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneLegacyValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(cloneLegacyValue(value.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(cloneLegacyValue(value.Index(i)))
		}
		return out
	default:
		return value
	}
}
