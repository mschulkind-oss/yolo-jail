package mcpauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// marshalRawObject emits members in deterministic key order while preserving each raw value
// verbatim. Encoding/json's RawMessage marshaler compacts values, which would rewrite untouched
// agent-owned entries during a broker update.
func marshalRawObject(object map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(object))
	for key, value := range object {
		if !json.Valid(value) {
			return nil, fmt.Errorf("invalid raw JSON value at key %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		encodedKey, _ := json.Marshal(key)
		out.Write(encodedKey)
		out.WriteByte(':')
		out.Write(object[key])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
