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
