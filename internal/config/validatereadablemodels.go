package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// everyModelKey is a provider's facts for every one of its models (docs/design/pack-manifests.md
// §4, docs/reference/config/rulings.md#RC-D29).
const everyModelKey = "every_model"

// legacyOnlyModelKeys are the object-form keys only the legacy model vocabulary spells
// (docs/reference/config/rulings.md#OQ-RC13): a row without `id` that writes one of them is a
// legacy row missing its id, and keeps that refusal, rather than a readable row.
var legacyOnlyModelKeys = set("input", "cost", "context_window")

// splitReadableModels takes one provider entry's readable model vocabulary out of it, for the
// launch's shape check (validateProviderEntries): its `every_model` and each readable row (an
// object `models.<id>` with no `id` and no legacy-only key), checked by the decoder a pack's
// `pack.toml` and an edit's composition use, so one spelling cannot be accepted by one and refused
// by the other. What it returns is the entry the legacy checks read, without them; the original
// is untouched. Edit validation hands this an entry legacyProviderValidationProjection already
// stripped, so there it finds nothing.
func splitReadableModels(name string, cfg *jsonx.OrderedMap, errs *[]string) *jsonx.OrderedMap {
	every, hasEvery := cfg.Get(everyModelKey)
	models, _ := asMap(func() any { v, _ := cfg.Get("models"); return v }())
	var ids []string
	if models != nil {
		for _, id := range models.Keys() {
			v, _ := models.Get(id)
			row, ok := asMap(v)
			if !ok || row == nil {
				continue
			}
			if _, legacy := row.Get("id"); legacy {
				continue
			}
			legacyShaped := false
			for _, key := range row.Keys() {
				if _, only := legacyOnlyModelKeys[key]; only {
					legacyShaped = true
				}
			}
			if !legacyShaped {
				ids = append(ids, id)
			}
		}
	}
	if !hasEvery && len(ids) == 0 {
		return cfg
	}
	rest, _ := jsonx.DeepCopy(cfg).(*jsonx.OrderedMap)
	rest.Delete(everyModelKey)
	part := jsonx.NewOrderedMap()
	if hasEvery && every != nil {
		part.Set(everyModelKey, jsonx.DeepCopy(every))
	}
	if len(ids) > 0 {
		rows := jsonx.NewOrderedMap()
		left, _ := asMap(func() any { v, _ := rest.Get("models"); return v }())
		for _, id := range ids {
			row, _ := models.Get(id)
			rows.Set(id, jsonx.DeepCopy(row))
			left.Delete(id)
		}
		part.Set("models", rows)
	}
	table := jsonx.NewOrderedMap()
	table.Set(name, part)
	root := jsonx.NewOrderedMap()
	root.Set(providersKey, table)
	if _, err := packdecl.DecodeProviderTable(root); err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, providersKey+".") {
			msg = "config." + msg
		} else {
			msg = "config." + providersKey + "." + name + ": " + msg
		}
		add(errs, msg)
	}
	return rest
}
