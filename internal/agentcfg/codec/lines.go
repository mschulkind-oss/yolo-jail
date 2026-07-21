package codec

import (
	"fmt"
	"strings"
)

// Lines is the newline-delimited codec for allowlist-style files (§3.3
// "lines"): the on-disk form is one string per line, decoded to a []any of
// strings so the engine can deep-merge / append over it like any array.
//
// # Trailing-newline and round-trip behavior
//
// Decode splits on '\n'. A single trailing newline is treated as a line
// TERMINATOR, not a data line — i.e. "a\nb\n" and "a\nb" both decode to
// ["a", "b"]. This is the POSIX text-file convention and is what keeps the
// round-trip stable: Encode ALWAYS terminates each line with '\n' (so
// ["a","b"] -> "a\nb\n"), and decoding that back yields the same slice.
// Consequently the canonical encoded form is trailing-newline-terminated, and
// Decode->Encode is idempotent on already-canonical input.
//
// Empty input ("" or a lone "\n") decodes to an empty (non-nil) slice, which
// encodes back to "" — an empty allowlist stays empty. Interior blank lines are
// preserved as empty strings (only ONE trailing newline is stripped).
type Lines struct{}

// Name returns "lines".
func (Lines) Name() string { return "lines" }

// Decode splits data into a []any of per-line strings, stripping a single
// trailing newline (and a trailing "\r\n"'s "\r"). Interior blank lines are
// kept as empty strings.
func (Lines) Decode(data []byte) (any, error) {
	s := string(data)
	if s == "" {
		return []any{}, nil
	}
	// Strip exactly one trailing newline so a terminator is not read as data.
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []any{}, nil
	}
	parts := strings.Split(s, "\n")
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSuffix(p, "\r")
	}
	return out, nil
}

// Encode renders a []any of strings as newline-terminated lines. Each element
// must be a string; an empty slice yields empty output. A trailing '\n'
// terminates the final line so the result decodes back to the same slice.
func (Lines) Encode(v any) ([]byte, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("codec: lines encode requires []any of strings, got %T", v)
	}
	if len(arr) == 0 {
		return []byte{}, nil
	}
	var sb strings.Builder
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("codec: lines encode requires string elements, got %T", e)
		}
		sb.WriteString(s)
		sb.WriteByte('\n')
	}
	return []byte(sb.String()), nil
}
