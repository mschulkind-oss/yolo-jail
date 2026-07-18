package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
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
// a ".0") and everything else as float64, matching Python's int/float split
// and its normalizations:
//   - "-0" (integer) parses to int 0 and re-encodes as "0" (Python:
//     json.loads("-0") == 0). Note "-0.0" (float) is preserved as -0.0.
//   - a float literal that overflows float64 (e.g. "1e400") parses to ±Inf,
//     which re-encodes to "Infinity"/"-Infinity" (Python: json.loads("1e400")
//     == inf). strconv.ParseFloat returns ±Inf WITH an ErrRange error, so we
//     must keep the ±Inf value rather than falling back to the literal.
func numberToValue(n json.Number) any {
	s := n.String()
	if isIntegerLiteral(s) {
		// Python normalizes the integer -0 to 0.
		if isNegativeZeroInt(s) {
			return jsonInt("0")
		}
		return jsonInt(s)
	}
	f, err := n.Float64()
	if err != nil {
		// Overflow to ±Inf still yields the right float value (ErrRange);
		// keep it so it re-encodes as Infinity like Python. Only a genuine
		// syntax error (not ErrRange) should fall back to the raw literal.
		if math.IsInf(f, 0) {
			return f
		}
		return jsonInt(s)
	}
	return f
}

// NumberValue converts a JSON number LITERAL string to the same value model
// numberToValue produces (jsonInt for integers with the -0→0 normalization;
// float64 otherwise, overflow→±Inf). Exported so internal/json5's hand-written
// parser reuses the exact int/float parity logic rather than duplicating it.
// The literal must be a valid JSON/JSON5 number (the caller's lexer guarantees
// this); a bad literal returns (nil, false).
func NumberValue(literal string) (any, bool) {
	if literal == "" {
		return nil, false
	}
	if isIntegerLiteral(literal) {
		if isNegativeZeroInt(literal) {
			return jsonInt("0"), true
		}
		// Validate it's actually an integer literal.
		if !looksNumeric(literal) {
			return nil, false
		}
		return jsonInt(literal), true
	}
	f, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		if math.IsInf(f, 0) {
			return f, true // overflow -> ±Inf, re-encodes as Infinity
		}
		return nil, false
	}
	return f, true
}

// looksNumeric is a cheap guard for integer literals: optional leading '-'
// then all digits.
func looksNumeric(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isIntegerLiteral(s string) bool {
	return !strings.ContainsAny(s, ".eE")
}

// isNegativeZeroInt reports whether s is an integer literal equal to negative
// zero ("-0", "-00", ...). Python's int parse collapses these to 0.
func isNegativeZeroInt(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	for _, c := range s[1:] {
		if c != '0' {
			return false
		}
	}
	return true
}
