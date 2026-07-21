package codec

import (
	"bytes"
	"encoding/json"
)

// JSON is the encoding/json-backed codec: the widest surface (Claude's
// settings.json, MCP, LSP, and most agent settings — §1.1).
//
// Decode uses the stdlib decoder, so objects become map[string]any, arrays
// []any, and scalars string/bool/float64/nil — exactly the generic model the
// engine (internal/agentcfg) merges over.
//
// Encode renders with STABLE key order (encoding/json sorts map keys
// alphabetically) and 2-space indentation so a regenerated file is diff-stable
// (§3.1 "regenerated files are diff-stable"). HTML escaping is disabled so
// characters like <, >, & survive verbatim rather than becoming \u00XX; the
// composition pipeline is not producing HTML and the round-trip must not mutate
// string content. No trailing newline is emitted (the stdlib encoder appends
// one; we strip it) so the encoded form is a single canonical value.
type JSON struct{}

// Name returns "json".
func (JSON) Name() string { return "json" }

// Decode parses JSON bytes into the generic value model via encoding/json.
func (JSON) Decode(data []byte) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Encode renders v as sorted-key, 2-space-indented JSON with HTML escaping
// disabled and no trailing newline.
func (JSON) Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder always appends a trailing '\n'; drop it for a canonical,
	// newline-free encoded value.
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
