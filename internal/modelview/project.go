// Package modelview applies composer-owned per-agent model menu views at derive boundaries.
package modelview

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const (
	// ViewsKey is composer-owned state that must never reach a derive or user config.
	ViewsKey = "models_views"
	// ModelsOnlyKey tells existing derives to render a projected list as exact.
	ModelsOnlyKey = "models_only"
)

// ProjectProviderTable returns an agent-specific copy of a composed providers table. It strips
// the composer-owned view key from every provider and narrows only entries with a view for agent.
// Inputs and every nested entry are left untouched.
func ProjectProviderTable(providers any, agent string) any {
	switch table := providers.(type) {
	case *jsonx.OrderedMap:
		if table == nil {
			return providers
		}
		changed := false
		for _, name := range table.Keys() {
			entry, _ := table.Get(name)
			if hasViews(entry) {
				changed = true
				break
			}
		}
		if !changed {
			return providers
		}
		out := jsonx.DeepCopy(table).(*jsonx.OrderedMap)
		for _, name := range out.Keys() {
			entry, _ := out.Get(name)
			if hasViews(entry) {
				out.Set(name, projectEntry(entry, agent))
			}
		}
		return out
	case map[string]any:
		var out map[string]any
		for name, entry := range table {
			if !hasViews(entry) {
				continue
			}
			if out == nil {
				out = make(map[string]any, len(table))
				for k, v := range table {
					out[k] = v
				}
			}
			out[name] = projectEntry(entry, agent)
		}
		if out == nil {
			return providers
		}
		return out
	default:
		return providers
	}
}

func hasViews(entry any) bool {
	_, ok := objectGet(entry, ViewsKey)
	return ok
}

func projectEntry(entry any, agent string) any {
	out := jsonx.DeepCopy(entry)
	views, _ := objectGet(out, ViewsKey)
	keep, scoped := agentIDs(views, agent)
	objectDelete(out, ViewsKey)
	if !scoped {
		return out
	}
	models, ok := objectGet(out, "models")
	if !ok {
		objectSet(out, ModelsOnlyKey, true)
		return out
	}
	models = jsonx.DeepCopy(models)
	options, _ := objectGet(out, "model_options")
	if options != nil {
		options = jsonx.DeepCopy(options)
	}
	for _, alias := range objectKeys(models) {
		id, ok := objectGet(models, alias)
		s, stringID := id.(string)
		if !ok || !stringID || !keep[s] {
			objectDelete(models, alias)
			objectDelete(options, alias)
		}
	}
	objectSet(out, "models", models)
	if options != nil {
		objectSet(out, "model_options", options)
	}
	objectSet(out, ModelsOnlyKey, true)
	return out
}

func agentIDs(views any, agent string) (map[string]bool, bool) {
	value, ok := objectGet(views, agent)
	if !ok {
		return nil, false
	}
	keep := map[string]bool{}
	if _, structured := objectGet(value, "only"); structured {
		addIDs(keep, mustObjectGet(value, "only"))
		addIDs(keep, mustObjectGet(value, "user"))
		return keep, true
	}
	addIDs(keep, value)
	return keep, true
}

func addIDs(keep map[string]bool, value any) {
	switch ids := value.(type) {
	case []string:
		for _, id := range ids {
			keep[id] = true
		}
	case []any:
		for _, raw := range ids {
			if id, ok := raw.(string); ok {
				keep[id] = true
			}
		}
	}
}

func mustObjectGet(value any, key string) any {
	v, _ := objectGet(value, key)
	return v
}

func objectGet(value any, key string) (any, bool) {
	switch m := value.(type) {
	case *jsonx.OrderedMap:
		return m.Get(key)
	case map[string]any:
		v, ok := m[key]
		return v, ok
	default:
		return nil, false
	}
}

func objectSet(value any, key string, v any) {
	switch m := value.(type) {
	case *jsonx.OrderedMap:
		m.Set(key, v)
	case map[string]any:
		m[key] = v
	}
}

func objectDelete(value any, key string) {
	switch m := value.(type) {
	case *jsonx.OrderedMap:
		m.Delete(key)
	case map[string]any:
		delete(m, key)
	}
}

func objectKeys(value any) []string {
	switch m := value.(type) {
	case *jsonx.OrderedMap:
		return append([]string(nil), m.Keys()...)
	case map[string]any:
		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		return keys
	default:
		return nil
	}
}
