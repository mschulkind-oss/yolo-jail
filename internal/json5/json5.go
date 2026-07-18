// Package json5 is a hand-written JSONC/JSON5 decoder driven to observed
// equivalence with Python's pyjson5 (the parser src/cli/config.py and
// src/host_processes.py use via pyjson5.loads). It decodes into the SAME value
// model as internal/jsonx (*jsonx.OrderedMap / []any / string / bool / nil /
// jsonx integer|float), so a json5-parsed config round-trips through
// jsonx.DumpsSnapshot/DumpsCompact byte-identically.
//
// Hard requirement (go-port plan §14, user decision): comments and trailing
// commas MUST be supported. This parser also supports the rest of the JSON5
// grammar pyjson5 accepts — single quotes, unquoted (identifier) object keys,
// hex integers, leading +, leading/trailing-dot floats, Infinity/-Infinity/NaN,
// and string line continuations — so any config that uses them stays parity-
// correct rather than ledger-accepted. Divergences (if any surface via the
// oracle) are recorded in docs/design/go-port-divergences.md.
//
// Dependency-free (hand-written lexer/parser) — keeps the module's zero-dep
// property (no vendor/ churn).
//
// Source of truth: pyjson5 observed behavior. Where a summary disagrees with
// pyjson5, pyjson5 wins.
package json5

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Decode parses JSONC/JSON5 bytes into the jsonx value model, or returns an
// error. Trailing non-whitespace/comment data after the top-level value is an
// error (matching pyjson5.loads, which consumes the whole document).
func Decode(data []byte) (any, error) {
	p := &parser{s: string(data)}
	p.skipWS()
	if p.wsErr != nil {
		return nil, p.wsErr
	}
	v, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.wsErr != nil {
		return nil, p.wsErr
	}
	if p.pos < len(p.s) {
		return nil, p.errf("trailing data after top-level value")
	}
	return v, nil
}

type parser struct {
	s     string
	pos   int
	wsErr error // set by skipWS on an unterminated block comment
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("json5: "+format+" at offset %d", append(args, p.pos)...)
}

// skipWS skips whitespace AND comments (// line, /* block */). JSON5 whitespace
// includes the JSON set plus a few unicode spaces; pyjson5 accepts the common
// ones — we handle the ASCII + common unicode whitespace.
func (p *parser) skipWS() {
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			p.pos++
		case c == '/' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '/':
			// line comment to EOL
			p.pos += 2
			for p.pos < len(p.s) && p.s[p.pos] != '\n' {
				p.pos++
			}
		case c == '/' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '*':
			// block comment to */
			p.pos += 2
			closed := false
			for p.pos+1 < len(p.s) {
				if p.s[p.pos] == '*' && p.s[p.pos+1] == '/' {
					p.pos += 2
					closed = true
					break
				}
				p.pos++
			}
			if !closed {
				// pyjson5 REJECTS an unterminated block comment ('/* ...' with
				// no closing '*/' -> Json5EOF); a truncated config must error,
				// not load silently. Record it; Decode surfaces it.
				p.pos = len(p.s)
				p.wsErr = p.errf("unterminated block comment")
				return
			}
		case c >= 0x80:
			// Possible unicode whitespace (NBSP, etc.); decode a rune.
			r, size := utf8.DecodeRuneInString(p.s[p.pos:])
			if isUnicodeSpace(r) {
				p.pos += size
			} else {
				return
			}
		default:
			return
		}
	}
}

func isUnicodeSpace(r rune) bool {
	switch r {
	case 0x00A0, 0xFEFF, 0x2028, 0x2029, 0x1680,
		0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007,
		0x2008, 0x2009, 0x200A, 0x202F, 0x205F, 0x3000:
		return true
	}
	return false
}

func (p *parser) parseValue() (any, error) {
	if p.pos >= len(p.s) {
		return nil, p.errf("unexpected end of input")
	}
	c := p.s[p.pos]
	switch {
	case c == '{':
		return p.parseObject()
	case c == '[':
		return p.parseArray()
	case c == '"' || c == '\'':
		return p.parseString()
	case c == 't' || c == 'f':
		return p.parseBool()
	case c == 'n':
		return p.parseNull()
	case c == 'I' || c == 'N':
		return p.parseNamedNumber(false)
	default:
		// number (incl. leading +/-, ., Infinity/NaN via sign)
		return p.parseNumber()
	}
}

func (p *parser) parseObject() (any, error) {
	p.pos++ // consume {
	m := jsonx.NewOrderedMap()
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return m, nil
	}
	for {
		p.skipWS()
		if p.pos >= len(p.s) {
			return nil, p.errf("unterminated object")
		}
		// key: string, single-quoted string, or unquoted identifier.
		var key string
		var err error
		c := p.s[p.pos]
		if c == '"' || c == '\'' {
			key, err = p.parseStringRaw()
		} else {
			key, err = p.parseIdentKey()
		}
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if p.pos >= len(p.s) || p.s[p.pos] != ':' {
			return nil, p.errf("expected ':' after object key")
		}
		p.pos++ // consume :
		p.skipWS()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		m.Set(key, val)
		p.skipWS()
		if p.pos >= len(p.s) {
			return nil, p.errf("unterminated object")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
			p.skipWS()
			if p.pos < len(p.s) && p.s[p.pos] == '}' {
				p.pos++ // trailing comma
				return m, nil
			}
		case '}':
			p.pos++
			return m, nil
		default:
			return nil, p.errf("expected ',' or '}' in object")
		}
	}
}

func (p *parser) parseArray() (any, error) {
	p.pos++ // consume [
	arr := []any{}
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == ']' {
		p.pos++
		return arr, nil
	}
	for {
		p.skipWS()
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
		p.skipWS()
		if p.pos >= len(p.s) {
			return nil, p.errf("unterminated array")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
			p.skipWS()
			if p.pos < len(p.s) && p.s[p.pos] == ']' {
				p.pos++ // trailing comma
				return arr, nil
			}
		case ']':
			p.pos++
			return arr, nil
		default:
			return nil, p.errf("expected ',' or ']' in array")
		}
	}
}

func (p *parser) parseBool() (any, error) {
	if strings.HasPrefix(p.s[p.pos:], "true") {
		p.pos += 4
		return true, nil
	}
	if strings.HasPrefix(p.s[p.pos:], "false") {
		p.pos += 5
		return false, nil
	}
	return nil, p.errf("invalid literal")
}

func (p *parser) parseNull() (any, error) {
	if strings.HasPrefix(p.s[p.pos:], "null") {
		p.pos += 4
		return nil, nil
	}
	return nil, p.errf("invalid literal")
}
