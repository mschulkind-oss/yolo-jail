package config

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EnvPlace is where one hydrated value was set: which set declared it, the config key that
// spelled the entry, and, for a dotenv file, the entry as the loader left it (a `~` entry stays
// `~`, a relative one arrives anchored beside its declaring file). It is what a refusal or a line
// prints to tell the user which file or table to look at (secret-env-scoping-plan.md#SEP-D7); it
// never carries a value.
type EnvPlace struct {
	// Private is the set bit of this place (secret-env-scoping.md#SE-D1): true when
	// `[env.secrets]` declared it.
	Private bool
	// Key is the config key that spelled the entry: "env_sources", "env.sources", "env.files",
	// "env.set", "env.secrets.files" or "env.secrets.set".
	Key string
	// Entry is the dotenv entry as the loader left it; "" for an inline table.
	Entry string
}

// HydratedEnv is what a launch read out of the config's dotenv files and inline tables, with
// the one fact the merge used to drop: which set declared each value
// (secret-env-scoping.md#SE-D1). It replaces the nil-encoded map a launch carried before
// (secret-env-scoping-plan.md#SEP-D1): one ordered value map holding both sets in hydration
// order, the set bit per name, the removals, and per name the places that set its winning value.
//
// The set bit lives in this value, not beside it, because a launch re-composes its channel from
// the hydration it already holds (an attach rekey, the pack-skew path, the grant recomposition):
// a bit carried anywhere else is silently lost there, and every secret becomes public.
//
// A name set in both sets holds the PRIVATE copy in Values, and Places lists the public place
// then the private one; BothSets names it so the caller can refuse (SE-D2).
//
// Every method answers on a nil receiver as an empty hydration.
type HydratedEnv struct {
	values   *jsonx.OrderedMap
	public   map[string]bool
	private  map[string]bool
	pubPlace map[string]EnvPlace
	prvPlace map[string]EnvPlace
	removals []string
}

// NewHydratedEnv returns an empty hydration, for callers (and tests) that build one by hand.
func NewHydratedEnv() *HydratedEnv {
	return &HydratedEnv{
		values:   jsonx.NewOrderedMap(),
		public:   map[string]bool{},
		private:  map[string]bool{},
		pubPlace: map[string]EnvPlace{},
		prvPlace: map[string]EnvPlace{},
	}
}

func (h *HydratedEnv) dropRemoval(name string) {
	for i, r := range h.removals {
		if r == name {
			h.removals = append(h.removals[:i:i], h.removals[i+1:]...)
			return
		}
	}
}

// SetPublic records name as set by the public set at place. A name the private set also holds
// keeps the private copy as its value (SEP-D1); the public place is still recorded.
func (h *HydratedEnv) SetPublic(name, value string, place EnvPlace) {
	place.Private = false
	h.public[name] = true
	h.pubPlace[name] = place
	h.dropRemoval(name)
	if !h.private[name] {
		h.values.Set(name, value)
	}
}

// SetPrivate records name as set by the private set at place; its value wins over a public copy.
func (h *HydratedEnv) SetPrivate(name, value string, place EnvPlace) {
	place.Private = true
	h.private[name] = true
	h.prvPlace[name] = place
	h.dropRemoval(name)
	h.values.Set(name, value)
}

// Remove takes name out of both sets and records it as a removal (SE-D4, SEP-D2).
func (h *HydratedEnv) Remove(name string) {
	delete(h.public, name)
	delete(h.private, name)
	delete(h.pubPlace, name)
	delete(h.prvPlace, name)
	h.values.Delete(name)
	for _, r := range h.removals {
		if r == name {
			return
		}
	}
	h.removals = append(h.removals, name)
}

// Values is every assignment, both sets, in hydration order; never a nil value. The caller must
// not mutate it.
func (h *HydratedEnv) Values() *jsonx.OrderedMap {
	if h == nil || h.values == nil {
		return jsonx.NewOrderedMap()
	}
	return h.values
}

// PublicValues is the public set's assignments alone, in hydration order: what every process may
// receive from config (secret-env-scoping.md §3.4 row 1, before a provider's claim narrows it).
func (h *HydratedEnv) PublicValues() *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if h == nil {
		return out
	}
	for _, k := range h.values.Keys() {
		if h.private[k] {
			continue
		}
		v, _ := h.values.Get(k)
		out.Set(k, v)
	}
	return out
}

// IsPrivate reports whether the private set holds name.
func (h *HydratedEnv) IsPrivate(name string) bool {
	return h != nil && h.private[name]
}

// Private is the set bit for every private name, as a copy; nil on a nil receiver.
func (h *HydratedEnv) Private() map[string]bool {
	if h == nil {
		return nil
	}
	out := make(map[string]bool, len(h.private))
	for k, v := range h.private {
		if v {
			out[k] = true
		}
	}
	return out
}

// Removals is the names the config removes, in order: each `[env] unset` name and each legacy
// inline null no later entry cancelled.
func (h *HydratedEnv) Removals() []string {
	if h == nil {
		return nil
	}
	return append([]string(nil), h.removals...)
}

// Places is where name's winning value was set: one place, or two for a name in both sets (the
// public place first).
func (h *HydratedEnv) Places(name string) []EnvPlace {
	if h == nil {
		return nil
	}
	var out []EnvPlace
	if h.public[name] {
		out = append(out, h.pubPlace[name])
	}
	if h.private[name] {
		out = append(out, h.prvPlace[name])
	}
	return out
}

// BothSets names every name holding a value in both sets after removals, in hydration order
// (SEP-D3): the both-sets refusal's input (SE-D2).
func (h *HydratedEnv) BothSets() []string {
	if h == nil {
		return nil
	}
	var out []string
	for _, k := range h.values.Keys() {
		if h.public[k] && h.private[k] {
			out = append(out, k)
		}
	}
	return out
}

// ResolveEnv hydrates cfg's environment: both sets, from files and inline tables, in envEntries'
// one order, later wins per name within each set (secret-env-scoping.md#33-the-two-sets). It is
// the one env reader: the public walk keeps its later-wins rule
// across assignments and removals, the private walk assigns only (the private set has no removal
// spelling), and every name the public walk ends up removing is taken out of the private set too
// — one `[env] unset` removes a name from both (SE-D4, SEP-D2).
//
// Missing or unreadable files warn (via warn) and are skipped.
//
// Why a removal exists at all: a jail starts from an empty environment, so "removed" is already
// the default there, but `yolo host -- claude` starts from the invoking shell's os.Environ(),
// which may carry an AWS_PROFILE that has to go (docs/reference/host-agent-environment.md §2.2).
// A removal is not an empty assignment: `AWS_PROFILE=` and no AWS_PROFILE behave differently in
// every AWS SDK. Assignments and removals come from ONE walk so they cannot disagree about
// order: a dotenv file listed after an inline null cancels that null.
func ResolveEnv(workspace string, cfg *jsonx.OrderedMap, warn Warn) *HydratedEnv {
	if warn == nil {
		warn = func(string) {}
	}
	type assignment struct {
		name, value string
		place       EnvPlace
	}
	// The public walk: later wins across assignments
	// and removals, and a removal is remembered only while no later entry re-assigns the name.
	pub := jsonx.NewOrderedMap()
	pubPlace := map[string]EnvPlace{}
	removed := map[string]bool{}
	var removalOrder []string
	var private []assignment

	readFile := func(s string) (*jsonx.OrderedMap, bool) {
		p := ResolveEnvSourcePath(s, workspace)
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				warn("dotenv file not found, skipping: " + s + " (resolved to " + p + ")")
			} else {
				warn("dotenv file unreadable, skipping: " + s + ": " + err.Error())
			}
			return nil, false
		}
		return ParseDotenv(string(data)), true
	}

	for _, e := range envEntries(cfg) {
		if em, ok := asMap(e.value); ok {
			place := EnvPlace{Private: e.private, Key: e.key}
			for _, k := range em.Keys() {
				v, _ := em.Get(k)
				if v == nil {
					if e.private {
						continue // the private set spells no removal
					}
					if !removed[k] {
						removalOrder = append(removalOrder, k)
					}
					removed[k] = true
					pub.Delete(k)
					delete(pubPlace, k)
					continue
				}
				vs, vok := asStr(v)
				if !vok {
					continue
				}
				if e.private {
					private = append(private, assignment{k, vs, place})
					continue
				}
				removed[k] = false
				pub.Set(k, vs)
				pubPlace[k] = place
			}
			continue
		}
		s, ok := asStr(e.value)
		if !ok {
			continue
		}
		parsed, ok := readFile(s)
		if !ok {
			continue
		}
		place := EnvPlace{Private: e.private, Key: e.key, Entry: s}
		for _, k := range parsed.Keys() {
			v, _ := parsed.Get(k)
			vs, _ := v.(string)
			if e.private {
				private = append(private, assignment{k, vs, place})
				continue
			}
			removed[k] = false
			pub.Set(k, vs)
			pubPlace[k] = place
		}
	}

	h := NewHydratedEnv()
	for _, k := range pub.Keys() {
		v, _ := pub.Get(k)
		vs, _ := v.(string)
		h.SetPublic(k, vs, pubPlace[k])
	}
	for _, a := range private {
		if removed[a.name] {
			continue // SEP-D2: the public walk's removal takes the private value out too
		}
		h.SetPrivate(a.name, a.value, a.place)
	}
	for _, k := range removalOrder {
		if removed[k] {
			h.removals = append(h.removals, k)
		}
	}
	return h
}
