package packload

// modellists.go applies the `models` contribution kind (packdecl/models.go;
// docs/design/model-lists-and-pickers.md §7, OQ-BR12) to the composed providers table: a pack
// adds entries to, or keeps only some ids of, the model list of a provider another pack may own.
//
// WHERE IT RUNS: inside ComposeProviders, after every selected pack's own providers are laid
// down and BEFORE the user's `providers` entries merge over them. So the order is the ruling's:
// the provider's own list, then the packs' `models` contributions, then the engineer's own
// `providers.<name>.models`, which writes last "because they could make that happen
// regardless" (OQ-BR12's answer). Composing it anywhere later would be a second composition of
// one table, the drift providers.go's file comment names.
//
// THE OUTPUT IS THE SHAPE EVERY CONSUMER ALREADY READS: `models` maps an alias to a wire id, and
// an added entry lands under its own id (ML-D1's shape: "models maps each alias to an identical
// id"), plus its `alias` when it declares one; its facts land in `model_options.<id>` in the
// flat string vocabulary liftModelFacts lowers a user's object-form entry into, with `vendor`,
// `description` and `order` beside them. No derive learns a second shape.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ModelsOnlyKey is the composed-table key a provider's entry carries, true, once a `models`
// contribution's `only` narrowed its list. It is how a derive tells "this list was narrowed on
// purpose" (render it as the agent's exact menu, docs/design/model-lists-and-pickers.md §14.1)
// from a list that merely exists. A composed fact, never a user key: config.knownProviderKeys
// does not hold it, so a user's config cannot spell it, and the user's own list over a narrowed
// one keeps the mark (the user writes last, and the menu is still the one list).
const ModelsOnlyKey = "models_only"

// WithModelNotes asks ComposeProviders to report, through note, what its `models` pass could
// not do as written: a duplicate id or alias (the first writer's kept), an `only` id nothing
// added (dropped, since `only` never adds), a provider no selected pack ships and the user
// does not declare (the contribution is inert), and an `only` that leaves a list empty. `yolo
// check` is the reader (§7.2: "yolo check names the duplicate").
func WithModelNotes(note func(string)) ComposeOption {
	return func(o *composeOpts) { o.modelNote = note }
}

// WithModelListPresence asks ComposeProviders to report whether each surviving provider had a
// model list supplied by a winning pack declaration, a models contribution, or the final user
// layer. Presence is out of band: it does not add a key to the composed provider table.
func WithModelListPresence(report func(provider string, supplied bool)) ComposeOption {
	return func(o *composeOpts) { o.modelListPresence = report }
}

// shapedModels is one pack's `models` contribution with the pack that declared it.
type shapedModels struct {
	pack string
	c    packdecl.ModelsContribution
}

// applyModelContributions applies every selected pack's `models` contributions to out, the
// table of the packs' own providers, before the user layer (see the file comment). user is the
// user's `providers` map, read only to tell a provider the user alone declares from one nobody
// does.
//
// ORDER: every `add`, in pack order then declaration order, and only then every `only`, which
// intersect. A narrowing a company ships therefore cannot be escaped by a pack that happens to
// be listed after it in `packs`, and adds from several packs union in the order the packs are
// listed. The design text's literal "packs apply in `packs` order" would let a later add
// re-open an earlier only; the implementation decision to apply the verbs in two passes is
// recorded as MM-D11.
func applyModelContributions(out, user *jsonx.OrderedMap, packs []*Pack, note func(string)) map[string]bool {
	applied := map[string]bool{}
	if note == nil {
		note = func(string) {}
	}
	var all []shapedModels
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.ModelsContributions() {
			all = append(all, shapedModels{pack: p.Name, c: c})
		}
	}
	if len(all) == 0 {
		return applied
	}
	byProvider := map[string][]shapedModels{}
	var providers []string
	for _, s := range all {
		if _, seen := byProvider[s.c.Provider]; !seen {
			providers = append(providers, s.c.Provider)
		}
		byProvider[s.c.Provider] = append(byProvider[s.c.Provider], s)
	}
	for _, name := range providers {
		shapes := byProvider[name]
		entry := providerEntry(out, name)
		if entry == nil {
			var uv any
			declared := false
			if user != nil {
				uv, declared = user.Get(name)
			}
			if !declared || uv == nil {
				// A null user entry disables the provider outright, so there is no list to shape
				// and nothing to say; an undeclared one is a contribution that shapes nothing.
				if !declared {
					for _, s := range shapes {
						note(fmt.Sprintf("pack %s's `models` contribution names provider %q, which no "+
							"selected pack ships and your config's `providers` does not declare, so it "+
							"shapes nothing", s.pack, name))
					}
				}
				continue
			}
			if _, isMap := uv.(*jsonx.OrderedMap); !isMap {
				continue
			}
			// A provider the USER alone declares: the packs' list is empty, and the user's own
			// entry merges over what they add (the user loop finds this entry and merges into it).
			entry = jsonx.NewOrderedMap()
			out.Set(name, entry)
		}
		applied[name] = true
		shapeProviderModels(name, entry, shapes, userModelIDs(user, name), note)
	}
	return applied
}

// shapeProviderModels applies one provider's `models` contributions to its entry.
func shapeProviderModels(name string, entry *jsonx.OrderedMap, shapes []shapedModels,
	userIDs map[string]bool, note func(string)) {
	models := subOrdered(entry, "models")
	opts := subOrdered(entry, "model_options")

	// THE LIST'S ORDER BECOMES EXPLICIT. An `add` APPENDS, and the consumers order a list by
	// its `order` fact, declared before undeclared, then by id (codexModelList); an added entry
	// with an order would otherwise sort ahead of every base entry that has none. So the base
	// list's current order is written down first, as `order` facts, and each added entry
	// continues it. A provider no contribution names is never touched here.
	next := materializeOrder(models, opts)

	ids := map[string]bool{}
	for _, alias := range models.Keys() {
		if id, ok := stringAt(models, alias); ok {
			ids[id] = true
		}
	}
	for _, s := range shapes {
		for _, e := range s.c.Add {
			if ids[e.ID] {
				note(fmt.Sprintf("provider %q: pack %s adds %q, which the list already holds; "+
					"the first writer's entry is kept whole", name, s.pack, e.ID))
				continue
			}
			ids[e.ID] = true
			next++
			models.Set(e.ID, e.ID)
			opts.Set(e.ID, modelEntryFacts(e, next))
			if e.Alias != "" {
				if prior, taken := stringAt(models, e.Alias); taken {
					note(fmt.Sprintf("provider %q: pack %s's alias %q for %q is already an alias of "+
						"%q; the first writer's is kept", name, s.pack, e.Alias, e.ID, prior))
				} else {
					models.Set(e.Alias, e.ID)
				}
			}
		}
	}

	var keep map[string]bool
	for _, s := range shapes {
		if len(s.c.Only) == 0 {
			continue
		}
		set := map[string]bool{}
		for _, id := range s.c.Only {
			if !ids[id] && !userIDs[id] {
				note(fmt.Sprintf("provider %q: pack %s's `only` names %q, which no provider or "+
					"`add` lists, so it is dropped — `only` narrows and never adds", name, s.pack, id))
			}
			if keep == nil || keep[id] {
				set[id] = true
			}
		}
		keep = set
	}
	if keep != nil {
		// Over a COPY of the keys: Keys() is the map's own slice and Delete shifts it in place,
		// so ranging over it steps past the entry after each one dropped, which then survives.
		for _, alias := range slices.Clone(models.Keys()) {
			if id, _ := stringAt(models, alias); !keep[id] {
				models.Delete(alias)
				opts.Delete(alias)
			}
		}
		entry.Set(ModelsOnlyKey, true)
		if models.Len() == 0 && len(userIDs) == 0 {
			note(fmt.Sprintf("provider %q: its `only` contributions keep none of its models, so "+
				"no agent gets a model list or a start model for it", name))
		}
	}
	setOrDrop(entry, "models", models)
	setOrDrop(entry, "model_options", opts)
}

// materializeOrder writes the list's current order into `order` facts and returns the last
// order written: the consumers' rule (codexModelList) over rows keyed by id, whose facts come
// from the alias spelled as the id first, then any other alias naming it in sorted order; rows
// with an `order` first, by it, then the rest by id. An id's fact is written under the alias
// spelled as the id when there is one, else under its first alias in sorted order, which is
// where that rule reads it.
func materializeOrder(models, opts *jsonx.OrderedMap) int {
	type row struct {
		id, carrier string
		order       float64
		ordered     bool
	}
	aliases := append([]string(nil), models.Keys()...)
	sort.Strings(aliases)
	byID := map[string]*row{}
	var rows []*row
	absorb := func(id, alias string) {
		r := byID[id]
		if r == nil {
			r = &row{id: id, carrier: alias}
			byID[id] = r
			rows = append(rows, r)
		}
		if !r.ordered {
			if f := subOrderedOrNil(opts, alias); f != nil {
				if s, ok := stringAt(f, "order"); ok {
					if n, err := strconv.ParseFloat(s, 64); err == nil {
						r.order, r.ordered = n, true
					}
				}
			}
		}
	}
	for _, alias := range aliases {
		if id, ok := stringAt(models, alias); ok && id == alias {
			absorb(id, alias)
		}
	}
	for _, alias := range aliases {
		if id, ok := stringAt(models, alias); ok && id != "" && id != alias {
			absorb(id, alias)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ordered != b.ordered {
			return a.ordered
		}
		if a.ordered && a.order != b.order {
			return a.order < b.order
		}
		return a.id < b.id
	})
	for i, r := range rows {
		f := subOrderedOrNil(opts, r.carrier)
		if f == nil {
			f = jsonx.NewOrderedMap()
			opts.Set(r.carrier, f)
		}
		f.Set("order", strconv.Itoa(i+1))
	}
	return len(rows)
}

// modelEntryFacts lowers an added entry into the flat fact vocabulary a provider's
// `model_options` carries (flattenModelFacts is the user-side twin), with its list position.
func modelEntryFacts(e packdecl.ModelEntry, order int) *jsonx.OrderedMap {
	f := jsonx.NewOrderedMap()
	f.Set("order", strconv.Itoa(order))
	f.Set("vendor", e.Vendor)
	if e.Name != "" {
		f.Set("name", e.Name)
	}
	if e.Description != "" {
		f.Set("description", e.Description)
	}
	if e.ContextWindow > 0 {
		f.Set("context_window", strconv.FormatFloat(e.ContextWindow, 'f', -1, 64))
	}
	if e.MaxTokens > 0 {
		f.Set("max_tokens", strconv.FormatFloat(e.MaxTokens, 'f', -1, 64))
	}
	if e.Reasoning != nil {
		f.Set("reasoning", strconv.FormatBool(*e.Reasoning))
	}
	if len(e.Input) > 0 {
		f.Set("input", strings.Join(e.Input, ","))
	}
	if c := e.Cost; c != nil && c.Input != nil && c.Output != nil && c.CacheRead != nil && c.CacheWrite != nil {
		for _, r := range []struct {
			key string
			v   float64
		}{{"cost_input", *c.Input}, {"cost_output", *c.Output},
			{"cost_cache_read", *c.CacheRead}, {"cost_cache_write", *c.CacheWrite}} {
			f.Set(r.key, jsonx.FormatFloatRepr(r.v))
		}
	}
	return f
}

// userModelIDs is the set of wire ids the user's own `providers.<name>.models` lists, string
// or object form, so an `only` note does not call an id nobody added when the user's own
// config, which writes after it, does.
func userModelIDs(user *jsonx.OrderedMap, name string) map[string]bool {
	out := map[string]bool{}
	entry := providerEntry(user, name)
	if entry == nil {
		return out
	}
	models := subOrderedOrNil(entry, "models")
	if models == nil {
		return out
	}
	for _, alias := range models.Keys() {
		v, _ := models.Get(alias)
		switch t := v.(type) {
		case string:
			out[t] = true
		case *jsonx.OrderedMap:
			if id, ok := stringAt(t, "id"); ok {
				out[id] = true
			}
		}
	}
	return out
}

// modelsClaimDetail describes one `models` contribution for its footprint line.
func modelsClaimDetail(c packdecl.Contribution) string {
	if len(c.Only) > 0 {
		return "keeps only " + strings.Join(c.Only, ", ") + " (your own providers." +
			c.Provider + ".models still writes last)"
	}
	var ids []string
	for _, e := range c.ModelEntries() {
		ids = append(ids, e.ID)
	}
	return "adds " + strings.Join(ids, ", ") + " (after the provider's own models)"
}

// subOrdered returns entry[key] as an ordered map, a fresh one when it is absent or not a map.
func subOrdered(entry *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m := subOrderedOrNil(entry, key); m != nil {
		return m
	}
	return jsonx.NewOrderedMap()
}

func subOrderedOrNil(entry *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if entry == nil {
		return nil
	}
	v, _ := entry.Get(key)
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// setOrDrop sets entry[key] to m, or deletes the key when m is empty, so a list narrowed to
// nothing carries no empty object a consumer would read as a declared list.
func setOrDrop(entry *jsonx.OrderedMap, key string, m *jsonx.OrderedMap) {
	if m.Len() == 0 {
		entry.Delete(key)
		return
	}
	entry.Set(key, m)
}

func stringAt(m *jsonx.OrderedMap, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	s, isString := v.(string)
	return s, isString
}
