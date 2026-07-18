package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Decode parses JSON into Go values that round-trip byte-identically through
// DumpsSnapshot / DumpsCompact:
//
//   - objects  -> *OrderedMap (insertion order preserved, like a Python dict)
//   - arrays   -> []any
//   - strings  -> string
//   - numbers  -> jsonInt for integer literals, float64 otherwise (Python's
//     int/float distinction; a plain Go decode would collapse both to float64)
//   - true/false/null -> bool / nil
//
// It uses the token stream so key order survives; encoding/json's map decode
// would lose it.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	// Reject trailing garbage the way json.loads does.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("jsonx: trailing data after JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeFromToken(tok, dec)
}

func decodeFromToken(tok json.Token, dec *json.Decoder) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec)
		case '[':
			return decodeArray(dec)
		default:
			return nil, fmt.Errorf("jsonx: unexpected delim %q", t)
		}
	case json.Number:
		return numberToValue(t), nil
	case string:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("jsonx: unexpected token %T", tok)
	}
}

func decodeObject(dec *json.Decoder) (any, error) {
	m := NewOrderedMap()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("jsonx: non-string object key %T", keyTok)
		}
		val, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		m.Set(key, val)
	}
	// consume closing '}'
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return m, nil
}

func decodeArray(dec *json.Decoder) (any, error) {
	arr := []any{}
	for dec.More() {
		val, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
	}
	// consume closing ']'
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return arr, nil
}

// numberToValue keeps integer literals as jsonInt (so they re-encode without
// a ".0") and everything else as float64.
func numberToValue(n json.Number) any {
	s := n.String()
	if isIntegerLiteral(s) {
		return jsonInt(s)
	}
	f, err := n.Float64()
	if err != nil {
		// Fall back to the raw literal — better than dropping precision.
		return jsonInt(s)
	}
	return f
}

func isIntegerLiteral(s string) bool {
	return !strings.ContainsAny(s, ".eE")
}
