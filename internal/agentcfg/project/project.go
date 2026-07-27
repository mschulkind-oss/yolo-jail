// Package project implements the declarative PROJECTION of a canonical exported
// value into one agent's dialect (C6).
//
// The problem it solves: several agents consume the same conceptual thing — an MCP
// server, an LSP server — in incompatible shapes. Today each agent has a
// hand-written Go builder, so N agents × M export types is N×M functions that must
// each be updated when either side changes. A projection expresses the reshape as
// DATA, which is what lets an agent pack ship its own without shipping Go.
//
// The operation set is DERIVED FROM THE THREE PROJECTIONS THAT EXIST, not invented:
//
//	codex MCP     {command, args, env} → near-passthrough, args defaults to [],
//	              env included only when non-empty
//	opencode MCP  → RENAME env→environment, FOLD command+args into one array,
//	              INJECT type="local" and enabled=true
//	copilot LSP   → defaults for args/fileExtensions, and command OMITTED when
//	              absent rather than emitted as null
//
// That last one is the operation most easily got wrong: `omit` and "emit null" are
// NOT the same. A null leaf is an RFC-7386 tombstone the compose engine treats as a
// DELETION, so a projection that rendered "absent" as null would delete the key from
// whatever it merges over. Both behaviors must be expressible, and they are separate
// ops here for exactly that reason.
//
// Deliberately NOT a general template language. The ops are a closed set covering
// real cases; a projection that needs more than this is the signal to reach for the
// subprocess projector (docs/design/third-party-pack-logic.md), not to grow this
// into a language.
package project

import (
	"fmt"
	"sort"
)

// Op is one reshape step, applied in declaration order to build ONE output entry
// from ONE input entry.
//
// Exactly one of the operation fields is set per Op; Validate enforces that, so a
// malformed projection fails loudly at load rather than silently doing nothing.
type Op struct {
	// Copy takes input[From] to output[To] (To defaults to From). Absent input is
	// skipped — use Default to supply a fallback, or Require to make it an error.
	Copy *CopyOp `json:"copy,omitempty"`

	// Fold concatenates several input fields into ONE output array: a scalar
	// contributes itself, an array contributes its elements. This is opencode's
	// command+args → command case, which no key-mapping can express.
	Fold *FoldOp `json:"fold,omitempty"`

	// Inject sets a constant. opencode's type="local" and enabled=true.
	Inject *InjectOp `json:"inject,omitempty"`

	// Default sets output[To] only when it is not already present, so it composes
	// with Copy (copy-then-default = "use the input, else this").
	Default *InjectOp `json:"default,omitempty"`
}

// CopyOp moves a field, optionally renaming it, optionally dropping empties.
type CopyOp struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	// OmitEmpty drops the field entirely when the input value is empty (nil, "",
	// or an empty array/object) INSTEAD of emitting it.
	//
	// This is the codex `env` case and the copilot `command` case, and it is
	// deliberately "omit" rather than "emit null": a null leaf is an RFC-7386
	// tombstone that DELETES the key downstream. Conflating them would turn "I have
	// nothing to say about this key" into "remove this key", which is a different
	// and destructive statement.
	OmitEmpty bool `json:"omitEmpty,omitempty"`
}

// FoldOp concatenates Froms into one output array at To.
type FoldOp struct {
	Froms []string `json:"froms"`
	To    string   `json:"to"`
}

// InjectOp sets a literal value.
type InjectOp struct {
	To    string `json:"to"`
	Value any    `json:"value"`
}

// Projection is the full reshape for one (export type → agent surface) pair.
type Projection struct {
	// Ops are applied in order to each entry.
	Ops []Op `json:"ops"`
	// KeySuffix is appended to each entry's KEY (not its value), for a projection
	// that must namespace what it emits.
	KeySuffix string `json:"keySuffix,omitempty"`
}

// Validate reports every structural problem in a projection.
//
// Returns ALL problems rather than the first: a pack author fixing a projection
// wants the whole list, and stopping early turns one edit-check cycle into several.
func (p Projection) Validate() []string {
	var problems []string
	for i, op := range p.Ops {
		set := 0
		if op.Copy != nil {
			set++
			if op.Copy.From == "" {
				problems = append(problems, fmt.Sprintf("ops[%d].copy: missing \"from\"", i))
			}
		}
		if op.Fold != nil {
			set++
			if len(op.Fold.Froms) == 0 {
				problems = append(problems, fmt.Sprintf("ops[%d].fold: missing \"froms\"", i))
			}
			if op.Fold.To == "" {
				problems = append(problems, fmt.Sprintf("ops[%d].fold: missing \"to\"", i))
			}
		}
		if op.Inject != nil {
			set++
			if op.Inject.To == "" {
				problems = append(problems, fmt.Sprintf("ops[%d].inject: missing \"to\"", i))
			}
		}
		if op.Default != nil {
			set++
			if op.Default.To == "" {
				problems = append(problems, fmt.Sprintf("ops[%d].default: missing \"to\"", i))
			}
		}
		switch set {
		case 0:
			problems = append(problems, fmt.Sprintf(
				"ops[%d]: no operation set (expected one of copy/fold/inject/default)", i))
		case 1:
		default:
			problems = append(problems, fmt.Sprintf(
				"ops[%d]: %d operations set, expected exactly one", i, set))
		}
	}
	return problems
}

// Apply projects a whole table: map of entry name → entry object.
//
// Entry ORDER is not preserved because the input is a map; callers that need
// deterministic output encode through a codec that sorts keys (every one here does).
func (p Projection) Apply(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in))
	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := in[name].(map[string]any)
		if !ok {
			// A non-object entry cannot be reshaped field-wise. Skipping silently
			// would lose it; erroring names the entry so the author can see which.
			return nil, fmt.Errorf("entry %q is not an object (%T)", name, in[name])
		}
		projected, err := p.applyEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", name, err)
		}
		out[name+p.KeySuffix] = projected
	}
	return out, nil
}

// applyEntry projects ONE entry.
func (p Projection) applyEntry(in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for i, op := range p.Ops {
		switch {
		case op.Copy != nil:
			to := op.Copy.To
			if to == "" {
				to = op.Copy.From
			}
			v, present := in[op.Copy.From]
			if !present {
				continue
			}
			if op.Copy.OmitEmpty && isEmpty(v) {
				continue
			}
			out[to] = v
		case op.Fold != nil:
			folded := []any{}
			for _, from := range op.Fold.Froms {
				v, present := in[from]
				if !present {
					continue
				}
				if arr, isArr := v.([]any); isArr {
					folded = append(folded, arr...)
					continue
				}
				folded = append(folded, v)
			}
			out[op.Fold.To] = folded
		case op.Inject != nil:
			out[op.Inject.To] = op.Inject.Value
		case op.Default != nil:
			if _, present := out[op.Default.To]; !present {
				out[op.Default.To] = op.Default.Value
			}
		default:
			return nil, fmt.Errorf("ops[%d]: no operation set", i)
		}
	}
	return out, nil
}

// isEmpty reports whether v carries no content, for OmitEmpty. A false bool and a
// zero number are NOT empty: they are real values a projection must be able to
// carry, and treating them as absent is a classic silent-data-loss bug.
func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}
