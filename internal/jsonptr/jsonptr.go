// Package jsonptr is the syntax of an RFC 6901 JSON Pointer
// (https://www.rfc-editor.org/rfc/rfc6901): parsing a pointer string into its reference
// tokens, and formatting tokens back into one. Nothing else — it resolves no pointer
// against a document, because what a missing or mistyped step means is the caller's rule,
// not the syntax's.
//
// It exists as its own leaf because two layers need ONE spelling of the grammar and may not
// import each other: internal/packdecl validates a `config-list` contribution's `path` at
// authoring time and must stay free of the config engine, while internal/agentcfg walks the
// same path through a composed document. Two hand-written parsers would be two answers to
// "is `/a~2b` a pointer?", and the manifest would validate a path the engine then reads
// differently.
//
// A pointer, not a dotted path, because real configuration keys contain dots — model ids,
// MCP server names, host names — so `a.b` cannot say whether it names one key or two.
package jsonptr

import (
	"fmt"
	"strings"
)

// Parse splits pointer p into its UNESCAPED reference tokens, in order.
//
// The empty string is the whole-document pointer and parses to zero tokens with no error;
// whether a caller accepts the root is the caller's rule. Any other pointer must begin with
// "/", and every "~" must be followed by "0" (a literal "~") or "1" (a literal "/"). A
// token may be empty: "/" is the pointer to the key "" of the root, exactly as RFC 6901 §5
// reads it.
//
// Unescaping follows the RFC's order — "~1" to "/" first, then "~0" to "~" — so "~01"
// decodes to the two characters "~1" and never to "/".
func Parse(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("JSON Pointer %q must be empty or start with \"/\" (RFC 6901)", p)
	}
	raw := strings.Split(p[1:], "/")
	tokens := make([]string, len(raw))
	for i, r := range raw {
		t, err := UnescapeToken(r)
		if err != nil {
			return nil, fmt.Errorf("JSON Pointer %q: %w", p, err)
		}
		tokens[i] = t
	}
	return tokens, nil
}

// Format joins tokens into a pointer, escaping each one. Zero tokens format as "", the
// whole-document pointer, so Format(Parse(p)) == p for every valid p.
func Format(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(EscapeToken(t))
	}
	return b.String()
}

// EscapeToken escapes one reference token: "~" becomes "~0" and "/" becomes "~1". The "~"
// is escaped FIRST, or the "~" a "/" escape introduces would be escaped again.
func EscapeToken(t string) string {
	if !strings.ContainsAny(t, "~/") {
		return t
	}
	return strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1")
}

// UnescapeToken reverses EscapeToken for one reference token, refusing a "~" that is not
// followed by "0" or "1" — the only two escapes RFC 6901 defines, so any other is a
// malformed pointer rather than a literal "~".
func UnescapeToken(t string) (string, error) {
	if !strings.Contains(t, "~") {
		return t, nil
	}
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c != '~' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(t) {
			return "", fmt.Errorf("token %q ends in a bare \"~\" (escape a literal \"~\" as \"~0\")", t)
		}
		switch t[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("token %q has escape \"~%c\"; RFC 6901 defines only \"~0\" (a \"~\") "+
				"and \"~1\" (a \"/\")", t, t[i+1])
		}
		i++
	}
	return b.String(), nil
}
