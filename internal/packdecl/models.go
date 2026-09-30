package packdecl

// models.go is the `models` contribution kind (docs/design/model-lists-and-pickers.md §7,
// OQ-BR12, ruled 2026-09-29): a pack SHAPES a provider's model list without owning the
// provider. It names any provider — the `bedrock` provider or a single pack's own — and either
// ADDS entries to its list or keeps ONLY the ids it names:
//
//	{"kind": "models", "provider": "bedrock", "add": [
//	   {"id": "global.moonshot.kimi-k3", "vendor": "moonshot", "name": "Kimi K3"}]}
//	{"kind": "models", "provider": "bedrock", "only": ["global.moonshot.kimi-k3"]}
//
// WHY A KIND AND NOT A FIELD ON `provider`. `provider` is sole-owned by name (kinds.go), so
// before this kind nothing let one pack shape another pack's list: a company that wanted its
// people to see four Bedrock models had to have each of them copy the list into their own
// config (§3). This is the narrow operation for that, leaving `provider` exclusive.
//
// THE ENGINEER'S OWN CONFIG WRITES LAST. How a composition applies these — every `add` in pack
// order, then every `only`, then the user's `providers.<name>.models` over the result — is
// packload.ComposeProviders' business; this file owns only the declaration. It reads nothing
// from the host, so it needs no fetched-pack approval and is never review-worthy.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// ModelEntry is one model an `add` contributes: the wire id, the maker, and the facts the
// user's object-form `providers.<name>.models.<alias>` entry already carries
// (config.knownModelKeys), plus the two a pack needs that a user's alias-keyed map never did
// (§7.3): `alias`, since a list keyed by id could not otherwise say which entry is `default`,
// and `description`, the second line claude's picker shows.
type ModelEntry struct {
	// ID is the model's wire id, the one every consumer sends. REQUIRED.
	ID string `json:"id"`
	// Vendor is the model's maker, one lowercase open-vocabulary token ("anthropic", "openai",
	// "moonshot"), read by the derives that can call only some makers' models on a provider
	// (docs/design/bedrock-plumbing.md OQ-BR9) and interpreted by no core code. REQUIRED on a
	// pack's entry (§7.2): an entry with no maker would be offered to every agent, which is the
	// user's string-form shorthand, never a shareable declaration's.
	Vendor string `json:"vendor"`
	// Alias is the name the entry is ALSO reachable under in the provider's `models` map,
	// such as "default" or "fast". Optional; every entry is always reachable under its id.
	Alias string `json:"alias,omitempty"`
	// Name is the display name a picker shows; absent, the agent shows its own catalog's
	// name for the id, or the id (MM-D7).
	Name string `json:"name,omitempty"`
	// Description is the second line claude's picker shows beside the name.
	Description string `json:"description,omitempty"`
	// ContextWindow and MaxTokens are the model's token limits, positive when declared.
	ContextWindow float64 `json:"context_window,omitempty"`
	MaxTokens     float64 `json:"max_tokens,omitempty"`
	// Reasoning says the model thinks; nil when undeclared.
	Reasoning *bool `json:"reasoning,omitempty"`
	// Input is the model's input modalities, from the closed set "text" and "image".
	Input []string `json:"input,omitempty"`
	// Cost is the per-million-token rates, all four or none.
	Cost *ModelCost `json:"cost,omitempty"`
}

// ModelCost is ModelEntry.Cost: the four rates the user's `cost` object carries
// (config.knownModelCostKeys), every one required, for that object's reason — pi discards its
// whole models.json over a partial cost.
type ModelCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// ModelsContribution is one `models` contribution: the provider it shapes and its one verb.
// Exactly one of Add and Only is non-empty (validateModels).
type ModelsContribution struct {
	// Provider is the name of the provider whose list this shapes, an entry of the composed
	// `providers` table.
	Provider string
	// Add is the entries to append, in declaration order.
	Add []ModelEntry
	// Only is the ids to keep; every other id leaves the list.
	Only []string
}

// ModelsContributions returns every `models` contribution the pack declares, in declaration
// order, which is the order packload.ComposeProviders applies them in (after pack order). An
// `add` whose entries do not decode is skipped rather than half-read; the validator already
// refused it on every path that validates.
func (m *Manifest) ModelsContributions() []ModelsContribution {
	var out []ModelsContribution
	for _, c := range m.Contributions() {
		if c.Kind != KindModels {
			continue
		}
		mc := ModelsContribution{Provider: c.Provider, Only: append([]string(nil), c.Only...)}
		if len(c.Add) > 0 {
			entries, err := decodeModelEntries(c.Add)
			if err != nil {
				continue
			}
			mc.Add = entries
		}
		out = append(out, mc)
	}
	return out
}

// ModelEntries decodes a `models` contribution's `add` array, nil when it is absent or does not
// decode (the validator refuses the latter on every path that validates).
func (c Contribution) ModelEntries() []ModelEntry {
	if c.Kind != KindModels || len(c.Add) == 0 {
		return nil
	}
	entries, err := decodeModelEntries(c.Add)
	if err != nil {
		return nil
	}
	return entries
}

// decodeModelEntries decodes an `add` array strictly: an unknown field is an error, the
// closed-schema rule config.knownModelKeys states for the user's own entries.
func decodeModelEntries(raw json.RawMessage) ([]ModelEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var entries []ModelEntry
	if err := dec.Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// modelsProblems validates the `only` field on every kind (it is this kind's alone) and, on
// a `models` contribution, the whole body.
func modelsProblems(label string, c Contribution) []string {
	if c.Kind != KindModels {
		if len(c.Only) > 0 {
			return []string{fmt.Sprintf("%s: kind %q does not take \"only\" — it is the ids a "+
				"\"models\" contribution keeps on a provider's list; no consumer reads it on this "+
				"kind", label, c.Kind)}
		}
		return nil
	}
	var problems []string
	if c.Provider == "" {
		problems = append(problems, label+": kind \"models\" needs \"provider\" — the name of "+
			"the provider whose model list it shapes")
	}
	switch {
	case len(c.Add) > 0 && len(c.Only) > 0:
		problems = append(problems, label+": a \"models\" contribution takes one verb, \"add\" or "+
			"\"only\", not both — write two contributions: every add applies before every only, "+
			"whatever their order")
	case len(c.Add) == 0 && len(c.Only) == 0:
		problems = append(problems, label+": kind \"models\" needs \"add\" (entries to append to "+
			"the provider's list) or \"only\" (the ids to keep)")
	case len(c.Add) > 0:
		problems = append(problems, modelAddProblems(label+".add", c.Add)...)
	default:
		seen := map[string]bool{}
		for i, id := range c.Only {
			at := fmt.Sprintf("%s.only[%d]", label, i)
			switch {
			case id == "":
				problems = append(problems, at+": an empty id names no model")
			case seen[id]:
				problems = append(problems, fmt.Sprintf("%s: %q is listed twice", at, id))
			}
			seen[id] = true
		}
	}
	return problems
}

// modelAddProblems checks an `add` array: it decodes into entries with no unknown field,
// and each entry is shaped as §7 says.
func modelAddProblems(label string, raw json.RawMessage) []string {
	entries, err := decodeModelEntries(raw)
	if err != nil {
		return []string{fmt.Sprintf("%s: expected an array of model entries "+
			"({\"id\", \"vendor\", and optionally \"alias\", \"name\", \"description\", "+
			"\"context_window\", \"max_tokens\", \"reasoning\", \"input\", \"cost\"}): %v", label, err)}
	}
	if len(entries) == 0 {
		return []string{label + ": an empty \"add\" adds nothing — omit the contribution"}
	}
	var problems []string
	ids, aliases := map[string]bool{}, map[string]bool{}
	for i, e := range entries {
		at := fmt.Sprintf("%s[%d]", label, i)
		if e.ID != "" {
			at = fmt.Sprintf("%s (%s)", at, e.ID)
		}
		switch {
		case e.ID == "":
			problems = append(problems, at+": needs \"id\", the model's wire id")
		case ids[e.ID]:
			problems = append(problems, at+": this id is added twice")
		}
		ids[e.ID] = true
		switch {
		case e.Vendor == "":
			problems = append(problems, at+": needs \"vendor\", the model's maker (such as "+
				"\"anthropic\" or \"moonshot\") — an agent that calls only some makers' models on "+
				"a provider reads it, and a pack entry with none would be offered to every agent")
		case strings.IndexFunc(e.Vendor, unicode.IsSpace) >= 0 || e.Vendor != strings.ToLower(e.Vendor):
			problems = append(problems, fmt.Sprintf("%s: vendor %q must be one lowercase token", at, e.Vendor))
		}
		if e.Alias != "" {
			switch {
			case strings.IndexFunc(e.Alias, unicode.IsSpace) >= 0:
				problems = append(problems, fmt.Sprintf("%s: alias %q carries whitespace", at, e.Alias))
			case aliases[e.Alias]:
				problems = append(problems, fmt.Sprintf("%s: alias %q names two entries", at, e.Alias))
			case e.Alias == e.ID:
				problems = append(problems, fmt.Sprintf("%s: alias %q is the entry's own id, which "+
					"it is always reachable under — omit it", at, e.Alias))
			}
			aliases[e.Alias] = true
		}
		if e.ContextWindow < 0 {
			problems = append(problems, at+": context_window must be a positive number")
		}
		if e.MaxTokens < 0 {
			problems = append(problems, at+": max_tokens must be a positive number")
		}
		for _, in := range e.Input {
			if in != "text" && in != "image" {
				problems = append(problems, fmt.Sprintf("%s: input %q is not \"text\" or \"image\"", at, in))
			}
		}
		if e.Input != nil && len(e.Input) == 0 {
			problems = append(problems, at+": input needs at least one of \"text\", \"image\"")
		}
		if c := e.Cost; c != nil {
			for _, r := range []struct {
				name string
				v    *float64
			}{{"input", c.Input}, {"output", c.Output}, {"cache_read", c.CacheRead}, {"cache_write", c.CacheWrite}} {
				switch {
				case r.v == nil:
					problems = append(problems, fmt.Sprintf("%s: cost.%s is required — the "+
						"agents' cost schemas need all four rates", at, r.name))
				case *r.v < 0:
					problems = append(problems, fmt.Sprintf("%s: cost.%s must not be negative", at, r.name))
				}
			}
		}
	}
	return problems
}
