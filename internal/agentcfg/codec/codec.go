// Package codec is the decode/encode boundary of yolo's generated-config
// composition pipeline (docs/plans/agent-settings-composition.md §3.1 pipeline,
// §3.3 format-agnostic). The engine in internal/agentcfg composes purely over
// already-decoded generic values (map[string]any / []any / scalars / nil); this
// package turns on-disk bytes into that model and back.
//
// A Codec is the per-surface format adapter the manifest names (§3.3):
//
//   - json  — encoding/json (stdlib). Structured; the widest surface (Claude,
//     MCP, LSP, most agent settings).
//   - toml  — Codex's config.toml and the global mise config. Decode is backed
//     by internal/tomlx (BurntSushi, vendored); Encode is a small deterministic
//     emitter local to this package (see toml.go's gap note).
//   - lines — newline-delimited list <-> []any of strings, for allowlist-style
//     files.
//   - raw   — passthrough []byte <-> string, the escape hatch for formats yolo
//     will not structurally round-trip (§3.3).
//
// Design constraints (per the composition plan and this piece's brief):
// stdlib + internal/jsonx + internal/tomlx ONLY — no new dependency, so the
// package must not import github.com/BurntSushi/toml directly.
//
// The decoded model matches the engine's: JSON/TOML objects decode to
// map[string]any, arrays to []any, scalars to string/bool/numeric/nil.
package codec

import "sort"

// Codec converts between a surface's on-disk bytes and the generic decoded
// value model the composition engine operates over.
//
// The round-trip contract, per §3.3 ("yolo owns the decode/encode round-trip"):
// for a value produced by Decode, Encode must yield bytes that decode back to an
// equal value, and re-encoding a decoded value is stable (the same bytes every
// time) so regenerated config files are diff-stable. Encode does NOT promise to
// reproduce the exact original bytes (formatting/whitespace may normalize); it
// promises a canonical, deterministic rendering.
type Codec interface {
	// Decode parses bytes into the generic value model (map[string]any /
	// []any / string / bool / numeric / nil). It returns an error on malformed
	// input.
	Decode([]byte) (any, error)
	// Encode renders a generic value back to the surface's format. It must be
	// deterministic (stable key order) so output is diff-stable.
	Encode(any) ([]byte, error)
	// Name is the codec's manifest identifier (e.g. "json", "toml").
	Name() string
}

// registry maps a manifest codec name to its implementation. It is populated at
// package init and never mutated afterward, so concurrent LookupCodec is safe.
var registry = map[string]Codec{
	"json":  JSON{},
	"toml":  TOML{},
	"lines": Lines{},
	"raw":   Raw{},
}

// LookupCodec returns the codec registered under name and whether it exists.
// The names mirror the manifest's `codec` field (§3.3): "json", "toml",
// "lines", "raw".
func LookupCodec(name string) (Codec, bool) {
	c, ok := registry[name]
	return c, ok
}

// Names returns the sorted names of every IMPLEMENTED codec — the single source
// of truth for "which codec names exist".
//
// internal/agentcfg/manifest derives its accepted-name set from this function
// rather than keeping its own list. That is not tidiness: the two lists had
// already drifted. The manifest accepted "yaml" while the registry never had a
// YAML implementation, so a surface declaring codec:yaml passed manifest
// validation and `yolo check`, then died at render with "unknown codec" — a
// validated-then-fatal name. Deriving the set makes that class of drift
// unrepresentable: a name is accepted iff something here can decode it.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Kind is the SHAPE of the top-level value a codec decodes to. It is not the
// codec's identity (that is Name) but the answer to "what Go type does a decoded
// whole file have", which the engine needs in three places:
//
//   - the composition engine, to pick deep-merge (objects) vs. whole-value
//     replacement (everything else) — see agentcfg.Compose;
//   - the Lua transform boundary, to check that a hook returned the same shape
//     it was handed (a raw transform must return a string, not a table) —
//     see luahook;
//   - the zero value for an absent layer, so "no host file" is an empty object
//     for JSON but an empty string for raw.
//
// Object is deliberately the only structured kind. A codec whose top level is an
// array (lines) or a scalar (raw) has no keys, so per-key provenance, tombstones,
// and `managed` are all whole-file operations for it.
type Kind int

const (
	// KindObject decodes to map[string]any — json, toml. Deep-mergeable.
	KindObject Kind = iota
	// KindArray decodes to []any — lines. Whole-value replacement.
	KindArray
	// KindScalar decodes to string — raw. Whole-value replacement.
	KindScalar
)

// String names the kind for error messages, in the vocabulary a config author
// would recognize from the Lua side.
func (k Kind) String() string {
	switch k {
	case KindObject:
		return "object/table"
	case KindArray:
		return "array/list"
	case KindScalar:
		return "string"
	default:
		return "unknown"
	}
}

// ZeroValue returns the empty value of the kind: the right "absent layer" for a
// surface, so a missing host file composes as nothing-to-merge rather than as a
// type error.
func (k Kind) ZeroValue() any {
	switch k {
	case KindObject:
		return map[string]any{}
	case KindArray:
		return []any{}
	case KindScalar:
		return ""
	default:
		return nil
	}
}

// Matches reports whether v has this kind's Go shape. nil matches nothing: an
// absent value is the caller's business (see ZeroValue), not a shape match.
func (k Kind) Matches(v any) bool {
	switch k {
	case KindObject:
		_, ok := v.(map[string]any)
		return ok
	case KindArray:
		_, ok := v.([]any)
		return ok
	case KindScalar:
		_, ok := v.(string)
		return ok
	default:
		return false
	}
}

// kinds maps each codec name to the shape of its decoded top-level value. It is
// keyed by name (not by a Codec method) so a caller holding only the manifest's
// codec STRING can ask about the shape without resolving the implementation.
var kinds = map[string]Kind{
	"json":  KindObject,
	"toml":  KindObject,
	"lines": KindArray,
	"raw":   KindScalar,
}

// KindOf returns the decoded shape of the named codec, and whether the name is
// known. An unknown name reports (KindObject, false) — callers must check ok
// rather than lean on the zero value.
func KindOf(name string) (Kind, bool) {
	k, ok := kinds[name]
	return k, ok
}
