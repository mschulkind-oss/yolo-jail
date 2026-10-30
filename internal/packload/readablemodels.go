package packload

// readablemodels.go carries a model said once into the launch's provider table
// (docs/design/pack-manifests.md §4, docs/design/readable-config.md RC-D29): the readable rows a
// pack's `pack.toml` or a user's config writes (`models.<id>` tables with no `id` field), their
// provider's `every_model`, `twin_of`, `compact_at`, and the catalog facts a row named without
// them takes (docs/design/config-user.md §8 C7). The derives read one vocabulary, the alias→wire
// map and its flat `model_options` facts, so the rows are lowered into it here, inside the one
// composition every notch calls (ComposeProviders), and never reach a derive in their own shape.
//
// A table with no readable row and no `every_model` passes through untouched, so every config
// and pack that composed before composes byte for byte the same
// (internal/cli/run/providertable_equivalence_test.go).

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// everyModelKey is a provider's facts for every one of its models (pack-manifests.md §4).
const everyModelKey = "every_model"

// WithModelCatalog hands ComposeProviders the offline catalog's facts by model id: what a readable
// row named without facts takes (config-user.md §8 C7), beneath its provider's `every_model` and
// its own. load runs at most once per composition, and only when the table holds a readable row,
// so a launch with none never reads a catalog. A legacy alias never takes catalog facts: it
// composed without them before, and still does.
func WithModelCatalog(load func() (map[string]packdecl.ProviderModel, error)) ComposeOption {
	return func(o *composeOpts) { o.modelCatalog = load }
}

// everyModelFill is one provider's `every_model`, lowered, waiting for fillEveryModel to lay it
// under the legacy aliases once liftModelFacts has composed their own facts.
type everyModelFill struct {
	provider string
	facts    *jsonx.OrderedMap
	routing  *jsonx.OrderedMap
	readable map[string]bool
}

// lowerReadableModels rewrites, in place, every provider entry that holds readable rows or an
// `every_model`: each readable row becomes `models.<id>` naming its id, with its composed facts
// (catalog, then `every_model`, then the row, a twin taking its base whole) laid OVER any facts
// a lower layer already shipped for that id under `model_options.<id>`, and `every_model` leaves
// the entry. It runs before liftModelFacts, so a row's `extends` resolves as a legacy alias's
// does. What it returns is the `every_model` facts the legacy aliases still need (fillEveryModel).
func lowerReadableModels(providers *jsonx.OrderedMap, cfg composeOpts) ([]everyModelFill, error) {
	if providers == nil {
		return nil, nil
	}
	var (
		catalog       map[string]packdecl.ProviderModel
		catalogLoaded bool
		fills         []everyModelFill
	)
	for _, name := range providers.Keys() {
		entry := childMap(providers, name)
		if entry == nil {
			continue
		}
		every, hasEvery := entry.Get(everyModelKey)
		models := childMap(entry, "models")
		ids := readableRowIDs(models)
		if !hasEvery && len(ids) == 0 {
			continue
		}
		entry.Delete(everyModelKey)
		part := jsonx.NewOrderedMap()
		if hasEvery && every != nil {
			part.Set(everyModelKey, jsonx.DeepCopy(every))
		}
		if len(ids) > 0 {
			rows := jsonx.NewOrderedMap()
			for _, id := range ids {
				row, _ := models.Get(id)
				rows.Set(id, jsonx.DeepCopy(row))
			}
			part.Set("models", rows)
		}
		table := jsonx.NewOrderedMap()
		table.Set(name, part)
		root := jsonx.NewOrderedMap()
		root.Set("providers", table)
		decoded, err := packdecl.DecodeProviderTable(root)
		if err != nil {
			return nil, err
		}
		if len(decoded.Declarations) != 1 {
			return nil, fmt.Errorf("providers.%s: its model rows decoded to no provider", name)
		}
		decl := decoded.Declarations[0]
		decl.Name = name
		readable := map[string]bool{}
		if len(ids) > 0 {
			if !catalogLoaded && cfg.modelCatalog != nil {
				if catalog, err = cfg.modelCatalog(); err != nil {
					return nil, err
				}
				catalogLoaded = true
			}
			rows, err := composeLaunchModels(decl, catalog)
			if err != nil {
				return nil, err
			}
			projection, err := ProjectProviderModelsForLegacyDerives(rows)
			if err != nil {
				return nil, fmt.Errorf("providers.%s: %w", name, err)
			}
			layProjection(entry, models, projection.ProviderEntry)
			for _, id := range ids {
				readable[id] = true
				if fields := projection.Unsupported[id]; len(fields) > 0 && cfg.modelNote != nil {
					cfg.modelNote(unreadFactsNote(name, "models."+id, fields))
				}
			}
		}
		if decl.EveryModel != nil {
			facts, routing, unsupported, err := lowerEveryModel(*decl.EveryModel)
			if err != nil {
				return nil, fmt.Errorf("providers.%s.every_model: %w", name, err)
			}
			if len(unsupported) > 0 && cfg.modelNote != nil {
				cfg.modelNote(unreadFactsNote(name, everyModelKey, unsupported))
			}
			fills = append(fills, everyModelFill{provider: name, facts: facts, routing: routing, readable: readable})
		}
	}
	return fills, nil
}

// readableRowIDs is the ids, in order, of models' readable rows: the object-form entries that
// carry no `id`. A legacy object names its wire id under `id` (config.validateModelEntry requires
// it), and a readable row is keyed by its id and never writes one, so the key alone tells them
// apart.
func readableRowIDs(models *jsonx.OrderedMap) []string {
	if models == nil {
		return nil
	}
	var ids []string
	for _, id := range models.Keys() {
		row := childMap(models, id)
		if row == nil {
			continue
		}
		if _, legacy := row.Get("id"); legacy {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// layProjection writes one provider's lowered rows into its entry: each id's alias in place, its
// flat facts over whatever facts the entry already carries for that id (a pack's, for an id the
// user wrote a row over, the rule liftModelFacts applies to a legacy object), and its routing.
func layProjection(entry, models, lowered *jsonx.OrderedMap) {
	if ids := childMap(lowered, "models"); ids != nil {
		for _, id := range ids.Keys() {
			wire, _ := ids.Get(id)
			models.Set(id, wire)
		}
	}
	if facts := childMap(lowered, "model_options"); facts != nil {
		options := childMap(entry, "model_options")
		if options == nil {
			options = jsonx.NewOrderedMap()
			entry.Set("model_options", options)
		}
		for _, id := range facts.Keys() {
			own := childMap(facts, id)
			if prior := childMap(options, id); prior != nil {
				overlayFacts(prior, own)
				continue
			}
			options.Set(id, own)
		}
	}
	if pins := childMap(lowered, "model_routing"); pins != nil {
		routing := childMap(entry, "model_routing")
		if routing == nil {
			routing = jsonx.NewOrderedMap()
			entry.Set("model_routing", routing)
		}
		for _, id := range pins.Keys() {
			pin, _ := pins.Get(id)
			routing.Set(id, pin)
		}
	}
}

// lowerEveryModel renders a provider's `every_model` in the flat vocabulary, through the same
// projection a row takes, less what only a row can say: its place in the order, its wire id and
// its shared-facts pointer.
func lowerEveryModel(every packdecl.ProviderModel) (facts, routing *jsonx.OrderedMap, unsupported []string, err error) {
	const id = everyModelKey
	projection, err := ProjectProviderModelsForLegacyDerives(ReadableModels{Declared: true,
		ByID:  map[string]ReadableModel{id: {ID: id, WireID: id, CompactAt: every.CompactAt, Facts: CloneProviderModel(every)}},
		Order: []string{id}})
	if err != nil {
		return nil, nil, nil, err
	}
	facts = childMap(childMap(projection.ProviderEntry, "model_options"), id)
	if facts == nil {
		facts = jsonx.NewOrderedMap()
	}
	for _, rowOnly := range []string{"order", "wire_id", "extends"} {
		facts.Delete(rowOnly)
	}
	routing = childMap(childMap(projection.ProviderEntry, "model_routing"), id)
	return facts, routing, projection.Unsupported[id], nil
}

// fillEveryModel lays each provider's `every_model` under its legacy aliases, after
// liftModelFacts: a fact the alias states itself, its pack ships for it, or its `extends` shares
// (a tombstone included) wins, and every_model fills only what all of those leave unsaid
// (RC-D29). A readable row took every_model in its own composition and is skipped.
func fillEveryModel(providers *jsonx.OrderedMap, fills []everyModelFill) {
	for _, fill := range fills {
		entry := childMap(providers, fill.provider)
		models := childMap(entry, "models")
		if models == nil {
			continue
		}
		for _, alias := range models.Keys() {
			if fill.readable[alias] {
				continue
			}
			if fill.facts.Len() > 0 {
				options := childMap(entry, "model_options")
				if options == nil {
					options = jsonx.NewOrderedMap()
					entry.Set("model_options", options)
				}
				own := childMap(options, alias)
				if own == nil {
					own = jsonx.NewOrderedMap()
					options.Set(alias, own)
				}
				for _, key := range fill.facts.Keys() {
					if _, stated := own.Get(key); !stated {
						value, _ := fill.facts.Get(key)
						own.Set(key, value)
					}
				}
			}
			if fill.routing != nil {
				routing := childMap(entry, "model_routing")
				if routing == nil {
					routing = jsonx.NewOrderedMap()
					entry.Set("model_routing", routing)
				}
				if _, stated := routing.Get(alias); !stated {
					routing.Set(alias, jsonx.DeepCopy(fill.routing))
				}
			}
		}
	}
}

// unreadFactsNote is the note for readable facts no agent's model list reads yet: the launch sends
// the model, and `yolo check` names what it carried no agent to.
func unreadFactsNote(provider, at string, fields []string) string {
	fields = slices.Clone(fields)
	slices.Sort(fields)
	return fmt.Sprintf("providers.%s.%s: %s reach no agent's model list yet, so each agent runs this model without them; "+
		"keep them for when one does, or remove them", provider, at, strings.Join(fields, ", "))
}

// readableRowOverAlias refuses a user's readable row that says no `model` (so is the model its key
// names) written over an alias a lower layer ships for a different wire id. The row would replace
// the alias whole and send the alias name as the model id, the silent wire-id rewrite RC-D29 (7)
// exists to prevent: a user adding one fact to a pack's `default` meant the pack's model, not a
// model named `default`. A row that says `model` names its model itself (MF-D1), so it is the one
// remedy the refusal names.
func readableRowOverAlias(provider string, base, override *jsonx.OrderedMap) error {
	shipped := childMap(base, "models")
	written := childMap(override, "models")
	if shipped == nil || written == nil {
		return nil
	}
	for _, id := range readableRowIDs(written) {
		if _, named := childMap(written, id).Get("model"); named {
			continue
		}
		wire, _ := shipped.Get(id)
		if s, isAlias := wire.(string); isAlias && s != id {
			return fmt.Errorf("providers.%s.models.%s: the pack ships %q as a name for the model %q, and a row "+
				"that says no `model` is the model its key names, so it would send %q instead; write `model = %q` in "+
				"it to add facts to the pack's model", provider, id, id, s, id, s)
		}
	}
	return nil
}
