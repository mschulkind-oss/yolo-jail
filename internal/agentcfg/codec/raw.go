package codec

import "fmt"

// Raw is the passthrough codec (§3.3 "raw"): the escape hatch for formats yolo
// will not structurally round-trip. Decode hands the config to the pipeline as
// a plain string and Encode turns a string back into bytes — no structure, no
// merge semantics beyond whole-value replacement. A Lua transform sees the
// config as a string and returns a string (§3.3).
//
// The round-trip is byte-exact: Decode(b) -> string(b), and Encode of that
// string reproduces the original bytes.
type Raw struct{}

// Name returns "raw".
func (Raw) Name() string { return "raw" }

// Decode returns the bytes as a string, unchanged.
func (Raw) Decode(data []byte) (any, error) {
	return string(data), nil
}

// Encode requires a string and returns its bytes unchanged.
func (Raw) Encode(v any) ([]byte, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("codec: raw encode requires a string, got %T", v)
	}
	return []byte(s), nil
}
