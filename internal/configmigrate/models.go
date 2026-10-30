package configmigrate

// models.go converts a provider's legacy model rows into the readable rows and folds the facts
// every row repeats into the provider's `every_model` (docs/design/config-migration.md §4 item 4,
// docs/design/readable-config.md OQ-RC18, RC-D27, RC-D29). The equality check's config layers
// compare both sides with the folds undone (unfoldEveryModel), so what proves a conversion or a
// fold is the extra layer this file adds: the provider table a launch resolves from each side
// (providerTable), composed by the one composition every notch calls.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// FoldedFact is one fact every model row of a provider wrote alike, written once in its
// `every_model` instead.
type FoldedFact struct {
	// Path is the new key, dotted; Rows is how many rows it was written on.
	Path string
	Rows int
}

// legacyRowFacts are the legacy object-row keys a readable row has a spelling for, each with
// that spelling (docs/design/model-facts-migration.md §3): `id` is the row's `model` (MF-D1),
// `extends` names shared facts in both (MF-D2), and `wire_id` is kept or becomes `twin_of`
// (MF-D3, twinRows).
var legacyRowFacts = map[string]string{
	"id": "model", "vendor": "vendor",
	"name": "name", "description": "description", "reasoning": "reasoning", "max_tokens": "max_tokens",
	"base": "base", "openrouter_routing": "openrouter_routing",
	"context_window": "context", "input": "accepts", "cost": "price",
	"extends": "extends", "wire_id": "wire_id",
}

// Twin is one legacy `wire_id` row written as `twin_of` the row whose id it sends (MF-D3).
type Twin struct {
	// Path is the twin's row, dotted; Of is the key of the row it is a twin of.
	Path, Of string
}

// legacySharedFacts are the legacy `model_facts` entry keys respelled in the readable entry
// (MF-D2); every other key keeps its name.
var legacySharedFacts = map[string]string{"context_window": "context", "input": "accepts", "cost": "price"}

// lowerModelFacts respells each `model_facts` entry's keys as a row's are (MF-D2). renamed maps
// each new path to the old one, for comment placement; an entry that is not a table passes as it
// is, for validation to name.
func lowerModelFacts(value any) (any, map[string][]string) {
	entries, ok := value.(*jsonx.OrderedMap)
	if !ok || entries == nil {
		return value, nil
	}
	renamed := map[string][]string{}
	out := jsonx.NewOrderedMap()
	for _, name := range entries.Keys() {
		raw, _ := entries.Get(name)
		entry, isMap := raw.(*jsonx.OrderedMap)
		if !isMap || entry == nil {
			out.Set(name, raw)
			continue
		}
		respelled := jsonx.NewOrderedMap()
		for _, k := range entry.Keys() {
			v, _ := entry.Get(k)
			key := k
			if spelled, ok := legacySharedFacts[k]; ok {
				if _, taken := entry.Get(spelled); !taken {
					key = spelled
					renamed[strings.Join([]string{"model_facts", name, key}, "\x00")] = []string{"model_facts", name, k}
				}
			}
			respelled.Set(key, v)
		}
		out.Set(name, respelled)
	}
	return out, renamed
}

// foldableFacts are the readable facts the rename table says `every_model` may carry
// (config.RenameDescriptors, the RenameFoldWhenEqual rows), in the order they are written.
var foldableFacts = []string{"context", "max_tokens", "reasoning", "accepts", "price"}

// convertModelRows returns providers with each legacy row rewritten as a readable row: the keys
// respelled, `id` written as `model` when it is not the row's key (MF-D1) and left out when it is,
// and `vendor` left out when the id names the same maker. With twinOf, a row whose `wire_id` is
// the id of another converted row of its provider says `twin_of` that row's key instead (MF-D3,
// twinRows). A row this cannot convert (a null fact, a key with no readable spelling) is left as
// it is, for the rename table to report. renamed maps each new path to the old one, for comment
// placement; twins are the rows written as `twin_of`.
//
// only, when not nil, limits the twins to the rows whose dotted paths it holds: the merged read
// twins only what the per-file conversion did, since a sibling in the other file is out of reach
// of the file actually written.
func convertModelRows(value any, path []string, twinOf bool, only map[string]bool) (out any, renamed map[string][]string, twins []Twin) {
	providers, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return value, nil, nil
	}
	renamed = map[string][]string{}
	result := jsonx.NewOrderedMap()
	for _, name := range providers.Keys() {
		raw, _ := providers.Get(name)
		entry, isMap := raw.(*jsonx.OrderedMap)
		models, hasModels := mapAt(entry, "models")
		if !isMap || entry == nil || !hasModels {
			result.Set(name, raw)
			continue
		}
		rows := jsonx.NewOrderedMap()
		keys := append([]string(nil), models.Keys()...)
		converted := map[string]*jsonx.OrderedMap{}
		for _, key := range keys {
			v, _ := models.Get(key)
			if row, ok := readableFromLegacy(key, v); ok {
				converted[key] = row
			}
		}
		// A readable row's place in its provider is an order every derive sorts by, where a
		// legacy alias sorts by its name: the converted rows go in that order so the sort is
		// unchanged. Rows left legacy keep their place after them.
		if len(converted) > 0 {
			sort.SliceStable(keys, func(i, j int) bool {
				_, ci := converted[keys[i]]
				_, cj := converted[keys[j]]
				if ci != cj {
					return ci
				}
				return ci && keys[i] < keys[j]
			})
		}
		rowPath := func(key string, leaf ...string) []string {
			return append(append(append([]string(nil), path...), name, "models", key), leaf...)
		}
		var twinned map[string]string
		if twinOf {
			twinned = twinRows(converted, func(key string) bool { return only == nil || only[dotted(rowPath(key))] })
		}
		for _, key := range keys {
			row, ok := converted[key]
			if !ok {
				v, _ := models.Get(key)
				rows.Set(key, v)
				continue
			}
			rows.Set(key, row)
			old, _ := models.Get(key)
			for _, k := range old.(*jsonx.OrderedMap).Keys() {
				if spelled := legacyRowFacts[k]; spelled != k {
					renamed[strings.Join(rowPath(key, spelled), "\x00")] = rowPath(key, k)
				}
			}
			if of, ok := twinned[key]; ok {
				renamed[strings.Join(rowPath(key, "twin_of"), "\x00")] = rowPath(key, "wire_id")
				twins = append(twins, Twin{Path: dotted(rowPath(key)), Of: of})
			}
		}
		copied := jsonx.NewOrderedMap()
		for _, key := range entry.Keys() {
			v, _ := entry.Get(key)
			if key == "models" {
				v = rows
			}
			copied.Set(key, v)
		}
		result.Set(name, copied)
	}
	return result, renamed, twins
}

// twinRows rewrites, in place, each converted row whose `wire_id` is the id another converted row
// lists (its `model`, or its key) as `twin_of` that row's key, and returns twin → sibling. A twin
// takes every fact of its sibling, its `extends` included, so an `extends` naming the sibling's
// is left out; whether the twin's facts are then still its own is the plan's proof, which plans
// again with `wire_id` when they are not (Plan). A sibling that sends another row's id is not
// named: the twin's facts would then come from a third row.
func twinRows(converted map[string]*jsonx.OrderedMap, may func(key string) bool) map[string]string {
	listed := map[string]string{}
	for key, row := range converted {
		if _, sends := row.Get("wire_id"); sends {
			continue
		}
		id := key
		if model, ok := row.Get("model"); ok {
			id, _ = model.(string)
		}
		listed[id] = key
	}
	twins := map[string]string{}
	for key, row := range converted {
		wire, sends := row.Get("wire_id")
		wireID, _ := wire.(string)
		sibling, found := listed[wireID]
		if !sends || !found || sibling == key || !may(key) {
			continue
		}
		out := jsonx.NewOrderedMap()
		for _, k := range row.Keys() {
			v, _ := row.Get(k)
			switch k {
			case "wire_id":
				out.Set("twin_of", sibling)
				continue
			case "extends":
				// Strings only: a malformed list `extends` is not comparable with ==.
				theirs, ok := converted[sibling].Get("extends")
				ts, tok := theirs.(string)
				vs, vok := v.(string)
				if ok && tok && vok && ts == vs {
					continue
				}
			}
			out.Set(k, v)
		}
		for _, k := range append([]string(nil), row.Keys()...) {
			row.Delete(k)
		}
		for _, k := range out.Keys() {
			v, _ := out.Get(k)
			row.Set(k, v)
		}
		twins[key] = sibling
	}
	return twins
}

// readableFromLegacy is one legacy row as a readable row, false when it has none: a row with no
// string `id`, or with a fact that is null or has no readable spelling. The row keeps its key;
// its `id` is written first, as `model`, unless it is the key (MF-D1).
func readableFromLegacy(key string, value any) (*jsonx.OrderedMap, bool) {
	row, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	raw, hasID := row.Get("id")
	id, isString := raw.(string)
	if !hasID || !isString || id == "" {
		return nil, false
	}
	out := jsonx.NewOrderedMap()
	if id != key {
		out.Set("model", id)
	}
	for _, k := range row.Keys() {
		v, _ := row.Get(k)
		spelled, known := legacyRowFacts[k]
		if !known || v == nil {
			return nil, false
		}
		switch k {
		case "id":
			continue
		case "vendor":
			if maker, _ := v.(string); maker == packload.InferModelVendor(id) {
				continue
			}
		}
		out.Set(spelled, jsonx.DeepCopy(v))
	}
	return out, true
}

// isReadableRow says whether a `models` value is a readable row: a table with no `id`.
func isReadableRow(v any) (*jsonx.OrderedMap, bool) {
	row, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	if _, legacy := row.Get("id"); legacy {
		return nil, false
	}
	return row, true
}

// foldEveryModel moves each fact that every readable row of a provider writes alike into the
// provider's `every_model`, for a provider with at least two rows, only readable rows and no
// `every_model` of its own. Whether a fold keeps the launch's provider table is the plan's proof
// (Plan), which drops every fold when it does not.
func foldEveryModel(values *jsonx.OrderedMap) []FoldedFact {
	providers, ok := mapAt(values, "providers")
	if !ok {
		return nil
	}
	var out []FoldedFact
	for _, name := range providers.Keys() {
		entry, ok := mapAt(providers, name)
		if !ok {
			continue
		}
		if _, has := entry.Get(everyModelKey); has {
			continue
		}
		models, ok := mapAt(entry, "models")
		if !ok || models.Len() < 2 {
			continue
		}
		var rows []*jsonx.OrderedMap
		for _, id := range models.Keys() {
			v, _ := models.Get(id)
			row, readable := isReadableRow(v)
			if !readable {
				rows = nil
				break
			}
			rows = append(rows, row)
		}
		if len(rows) < 2 {
			continue
		}
		every := jsonx.NewOrderedMap()
		for _, fact := range foldableFacts {
			first, has := rows[0].Get(fact)
			if !has {
				continue
			}
			want, err := jsonx.DumpsSnapshot(first)
			if err != nil {
				continue
			}
			same := true
			for _, row := range rows[1:] {
				v, has := row.Get(fact)
				got, _ := jsonx.DumpsSnapshot(v)
				if !has || got != want {
					same = false
					break
				}
			}
			if !same {
				continue
			}
			every.Set(fact, jsonx.DeepCopy(first))
			for _, row := range rows {
				row.Delete(fact)
			}
			out = append(out, FoldedFact{Path: dotted([]string{"providers", name, everyModelKey, fact}), Rows: len(rows)})
		}
		if every.Len() > 0 {
			setBefore(entry, everyModelKey, every, "models")
		}
	}
	return out
}

// unfoldEveryModel undoes every fold in values for the config layers' comparison: each
// `every_model` fact is written on each readable row that does not write its own, and
// `every_model` leaves the entry. What a fold means for a launch is the provider table's to prove.
func unfoldEveryModel(values *jsonx.OrderedMap) {
	providers, ok := mapAt(values, "providers")
	if !ok {
		return
	}
	for _, name := range providers.Keys() {
		entry, ok := mapAt(providers, name)
		if !ok {
			continue
		}
		every, ok := mapAt(entry, everyModelKey)
		if !ok {
			continue
		}
		models, _ := mapAt(entry, "models")
		if models == nil {
			continue
		}
		for _, id := range models.Keys() {
			v, _ := models.Get(id)
			row, readable := isReadableRow(v)
			if !readable {
				continue
			}
			for _, fact := range every.Keys() {
				if _, own := row.Get(fact); !own {
					value, _ := every.Get(fact)
					row.Set(fact, jsonx.DeepCopy(value))
				}
			}
		}
		entry.Delete(everyModelKey)
	}
}

const everyModelKey = "every_model"

// modelOrderKey is the proof's own key in each provider of the table: the order a derive lists
// the provider's models in (by each one's `order` fact, then its name), which the `order` facts
// themselves stand for.
const modelOrderKey = "(model order)"

// providerTable is the provider table a launch resolves from one side's merged read, composed
// with every shipped pack and the offline catalog, canonical: each provider's `order` facts are
// replaced by the order they make (modelOrderKey), and a `vendor` the model's id names is filled
// in where the composition left none (deriveVendors, MF-D5). A composition that refuses is its
// refusal. derived is what the fill wrote.
func providerTable(read *jsonx.OrderedMap, packs []*packload.Pack) (out *jsonx.OrderedMap, derived []DerivedVendor) {
	out = jsonx.NewOrderedMap()
	providers, _ := mapAt(read, "providers")
	facts, _ := mapAt(read, "model_facts")
	table, err := packload.ComposeProviders(providers, packs, packload.WithModelFacts(facts),
		config.WithOfflineModelCatalog(read, packs))
	if err != nil {
		out.Set("(refused)", err.Error())
		return out, nil
	}
	if table == nil {
		return out, nil
	}
	for _, name := range table.Keys() {
		entry, ok := mapAt(table, name)
		if !ok {
			v, _ := table.Get(name)
			out.Set(name, v)
			continue
		}
		entry = jsonx.DeepCopyMap(entry)
		derived = append(derived, deriveVendors(name, entry)...)
		options, _ := mapAt(entry, "model_options")
		if models, ok := mapAt(entry, "models"); ok {
			aliases := append([]string(nil), models.Keys()...)
			order := func(alias string) (int, bool) {
				row, ok := mapAt(options, alias)
				if !ok {
					return 0, false
				}
				v, _ := row.Get("order")
				s, _ := v.(string)
				n, err := strconv.Atoi(s)
				return n, err == nil
			}
			if options == nil {
				order = func(string) (int, bool) { return 0, false }
			}
			sort.SliceStable(aliases, func(i, j int) bool {
				oi, hi := order(aliases[i])
				oj, hj := order(aliases[j])
				switch {
				case hi && hj && oi != oj:
					return oi < oj
				case hi != hj:
					return hi
				}
				return aliases[i] < aliases[j]
			})
			list := make([]any, len(aliases))
			for i, alias := range aliases {
				list[i] = alias
			}
			entry.Set(modelOrderKey, list)
		}
		if options != nil {
			for _, alias := range options.Keys() {
				if row, ok := mapAt(options, alias); ok {
					row.Delete("order")
				}
			}
		}
		out.Set(name, entry)
	}
	return out, derived
}

// deriveVendors writes, into one composed provider entry, the maker each model's id names
// (packload.InferModelVendor) as the `vendor` of every model whose facts carry none, and returns
// what it wrote. A readable row infers that maker in the composition and a legacy row does not,
// so the proof compares it as derived on both sides (model-facts-migration.md MF-D5, RC-D30 (2)):
// the one place the table is not compared byte for byte.
func deriveVendors(provider string, entry *jsonx.OrderedMap) []DerivedVendor {
	models, ok := mapAt(entry, "models")
	if !ok {
		return nil
	}
	var out []DerivedVendor
	for _, alias := range models.Keys() {
		v, _ := models.Get(alias)
		id, _ := v.(string)
		maker := packload.InferModelVendor(id)
		if maker == "" {
			continue
		}
		options, ok := mapAt(entry, "model_options")
		if !ok {
			options = jsonx.NewOrderedMap()
			entry.Set("model_options", options)
		}
		row, ok := mapAt(options, alias)
		if !ok {
			row = jsonx.NewOrderedMap()
			options.Set(alias, row)
		}
		if _, stated := row.Get("vendor"); stated {
			continue
		}
		row.Set("vendor", maker)
		out = append(out, DerivedVendor{Provider: provider, Model: alias, Vendor: maker})
	}
	return out
}

// derivedOnlyBefore is the vendors the old side's table was filled with that the new side's
// carried itself: the models the launch after migration names a maker for.
func derivedOnlyBefore(before, after []DerivedVendor) []DerivedVendor {
	seen := map[DerivedVendor]bool{}
	for _, d := range after {
		seen[d] = true
	}
	var out []DerivedVendor
	for _, d := range before {
		if !seen[d] {
			out = append(out, d)
		}
	}
	return out
}

func mapAt(m *jsonx.OrderedMap, key string) (*jsonx.OrderedMap, bool) {
	if m == nil {
		return nil, false
	}
	v, _ := m.Get(key)
	child, ok := v.(*jsonx.OrderedMap)
	return child, ok && child != nil
}

// setBefore sets key in m just before the existing key before, so a provider's `every_model` is
// written above the rows it is shared by.
func setBefore(m *jsonx.OrderedMap, key string, value any, before string) {
	keys := append([]string(nil), m.Keys()...)
	values := make([]any, len(keys))
	for i, k := range keys {
		values[i], _ = m.Get(k)
	}
	for _, k := range keys {
		m.Delete(k)
	}
	placed := false
	for i, k := range keys {
		if k == before && !placed {
			m.Set(key, value)
			placed = true
		}
		m.Set(k, values[i])
	}
	if !placed {
		m.Set(key, value)
	}
}
