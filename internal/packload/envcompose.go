package packload

// envcompose.go is THE ONE ORDERED ENV COMPOSITION (docs/plans/notch-convergence.md item 16,
// row C3): what one process receives when several of yolo's own sources set the same variable,
// composed once here and serialized by every vehicle — the host exec (internal/cli host.go), the
// container's shared file and per-agent files, and the macos-user session env
// (internal/cli/run). Each vehicle used to layer the sources in its own order, so one key had a
// different winner per vehicle; the MEASURED table is notch-convergence's row C3.
//
// THE ORDER, lowest to highest, is OQ-NC12's option A, decided on its leaning under the
// maintainer's 2026-10-04 delegation and open to revision:
//
//  1. the pack env fold (EnvFold, unchanged inside: per pack, its static keys, then its gated
//     ones), as the notch serves it (FoldFor);
//  2. env_sources: the entries this process receives (EnvSourcesFor: the unclaimed ones, and
//     the ones its own provider claims or its grant names), then the REMOVALS
//     (ScopeInput.EnvSourceRemovals, an inline null no later entry cancelled). A removal ranks
//     HERE: it takes out the fold's value and, at the host, the invoking shell's, and never a
//     shape var, so a null cannot split a derive's address from the credential it pairs with;
//  3. this agent's shape vars (AgentDelivery.Shape: its pack's env derive's output, then the
//     region fill), tombstones included: a shape tombstone beats the two layers below.
//
// The most specific source wins: the derive composes for the profile this agent selected,
// env_sources is the user's standing file for every process, and the fold is each pack's default.
// A user who wants a value to beat a profile has the per-command spelling OQ-CN8 keeps.
//
// ONE ENTRY PER KEY, in the order each key's winning entry was written, with the source it came
// from and the pack it is attributed to, so a vehicle never layers two values for one name and
// lets its own grammar pick. What a vehicle does with the user's own value (the jail's per-agent
// file defers to it, OQ-CN8; the host composes over the shell pending OQ-NC13) and where the
// wire tables go stays the vehicle's.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EnvEntry is one variable's winning entry in a composition.
type EnvEntry struct {
	Key   string
	Value string
	// Unset is a removal: an env_sources null, or a shape var's tombstone. Value is "" then.
	Unset bool
	// Origin is the source the entry came from, as the launch names it: FromPackEnv for the fold,
	// FromEnvSources for env_sources (a null included), FromProfileEnv for a shape var.
	Origin string
	// Pack is the pack the entry is attributed to: the declaring pack of a fold entry, the agent's
	// installing pack for a shape var, "" for env_sources.
	Pack string
}

// EnvComposition is one process's composed environment: one entry per key, in the order each
// key's winning entry was written. The zero value is an empty composition.
type EnvComposition struct {
	entries []EnvEntry
	index   map[string]int
}

// Entries is every entry, in the order each key's winning entry was written.
func (c EnvComposition) Entries() []EnvEntry {
	return append([]EnvEntry(nil), c.entries...)
}

// Lookup is key's winning entry, a removal included; false when no layer wrote key.
func (c EnvComposition) Lookup(key string) (EnvEntry, bool) {
	i, ok := c.index[key]
	if !ok {
		return EnvEntry{}, false
	}
	return c.entries[i], true
}

// Value is what the process receives under key, non-empty: false for a removal, an empty value,
// or a key no layer wrote. An empty value is unset at every reader, as the launch treats it.
func (c EnvComposition) Value(key string) (string, bool) {
	e, ok := c.Lookup(key)
	if !ok || e.Unset || e.Value == "" {
		return "", false
	}
	return e.Value, true
}

// put writes e as key's entry, a later write winning: the earlier entry is dropped and e
// appended, so the order is the order of each key's winning write.
func (c *EnvComposition) put(e EnvEntry) {
	if e.Key == "" {
		return
	}
	if c.index == nil {
		c.index = map[string]int{}
	}
	if i, ok := c.index[e.Key]; ok {
		c.entries = append(c.entries[:i], c.entries[i+1:]...)
		for k, j := range c.index {
			if j > i {
				c.index[k] = j - 1
			}
		}
	}
	c.index[e.Key] = len(c.entries)
	c.entries = append(c.entries, e)
}

// composeEnv is the one ordering (this file's header): the fold, then env_sources and its
// removals, then the shape vars, each later layer beating the ones below it.
func composeEnv(fold []EnvFoldEntry, sources *jsonx.OrderedMap, removals []string,
	shape []agentenv.Var, shapePack string) EnvComposition {
	var c EnvComposition
	for _, e := range fold {
		c.put(EnvEntry{Key: e.Key, Value: e.Value, Origin: FromPackEnv, Pack: e.Pack})
	}
	if sources != nil {
		for _, k := range sources.Keys() {
			v, _ := sources.Get(k)
			if s, ok := v.(string); ok {
				c.put(EnvEntry{Key: k, Value: s, Origin: FromEnvSources})
			}
		}
	}
	for _, k := range removals {
		c.put(EnvEntry{Key: k, Unset: true, Origin: FromEnvSources})
	}
	for _, v := range shape {
		c.put(EnvEntry{Key: v.Key, Value: v.Value, Unset: v.Unset, Origin: FromProfileEnv, Pack: shapePack})
	}
	return c
}

// SharedEnv is the composition EVERY process of the launch receives: the shared fold (no gate
// fires for it), then the unclaimed env_sources and every removal. A removal carries no value, so
// no claim scopes it. It is what the container's shared file carries and what a bare shell, or a
// program no profile selects, starts with. Empty on a nil receiver.
func (s *CredentialScope) SharedEnv() EnvComposition {
	if s == nil {
		return EnvComposition{}
	}
	return composeEnv(s.sharedFold, s.sharedEnvSources, s.removals, nil, "")
}

// EnvFor is the whole composition agent's process receives: its fold as this notch serves it
// (FoldFor), the env_sources it receives (EnvSourcesFor) and every removal, then its shape vars.
// An agent with no delivery of its own (no profile, no grant) receives SharedEnv's answer. Empty
// on a nil receiver.
func (s *CredentialScope) EnvFor(agent string) EnvComposition {
	if s == nil {
		return EnvComposition{}
	}
	var shape []agentenv.Var
	if d := s.agents[agent]; d != nil {
		shape = d.Shape
	}
	owner := ""
	if p := binOwner(s.packs, agent); p != nil {
		owner = p.Name
	}
	return composeEnv(s.FoldFor(agent), s.EnvSourcesFor(agent), s.removals, shape, owner)
}

// Delivered is the entry name reaches SOME process of the launch with, non-empty: the shared
// composition's winner, else the first agent's (sorted) whose own composition delivers it. It is
// the launch-wide reading of the composition the env-override pre-flight asks, at the launch
// (run's deliverySource) and in `yolo check`'s prediction alike, so both name the source that
// wins rather than the first source that holds the name. False on a nil receiver.
func (s *CredentialScope) Delivered(name string) (EnvEntry, bool) {
	if s == nil {
		return EnvEntry{}, false
	}
	if e, ok := s.SharedEnv().Lookup(name); ok && !e.Unset && e.Value != "" {
		return e, true
	}
	for _, agent := range s.Agents() {
		if e, ok := s.EnvFor(agent).Lookup(name); ok && !e.Unset && e.Value != "" {
			return e, true
		}
	}
	return EnvEntry{}, false
}
