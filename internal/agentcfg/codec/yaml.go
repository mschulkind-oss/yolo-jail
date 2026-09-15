package codec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// YAML is the structured YAML codec. YAML is an object codec because generated
// YAML surfaces are top-level mappings; yaml.v3 keeps parsing and deterministic
// serialization at the codec boundary instead of leaving a pack to serialize a
// format it does not own.
type YAML struct{}

func (YAML) Name() string { return "yaml" }

func (YAML) Decode(data []byte) (any, error) {
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return yamlPlain(value)
}

func (YAML) Encode(value any) ([]byte, error) {
	if _, err := yamlPlain(value); err != nil {
		return nil, err
	}
	return yaml.Marshal(value)
}

// yamlPlain lowers yaml.v3's permissive value model to the config engine's
// JSON-shaped one. YAML permits non-string mapping keys, but a configuration
// surface cannot represent them; refuse instead of losing their identity later.
func yamlPlain(value any) (any, error) {
	switch v := value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return v, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			plain, err := yamlPlain(child)
			if err != nil {
				return nil, fmt.Errorf("yaml key %q: %w", key, err)
			}
			out[key] = plain
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("YAML mapping key %T is not a string", key)
			}
			plain, err := yamlPlain(child)
			if err != nil {
				return nil, fmt.Errorf("yaml key %q: %w", name, err)
			}
			out[name] = plain
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			plain, err := yamlPlain(child)
			if err != nil {
				return nil, fmt.Errorf("yaml item %d: %w", i, err)
			}
			out[i] = plain
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported YAML value %T", value)
	}
}
