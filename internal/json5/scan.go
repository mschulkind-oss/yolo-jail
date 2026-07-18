package json5

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Infinity / NaN as float64 values — jsonx.formatFloat encodes these as the
// literals "Infinity" / "-Infinity" / "NaN" (matching Python json.dumps and
// pyjson5's parse targets).
func posInf() any { return math.Inf(1) }
func negInf() any { return math.Inf(-1) }
func nan() any    { return math.NaN() }

// parseString parses a double- or single-quoted JSON5 string and returns it as
// a Go string value.
func (p *parser) parseString() (any, error) {
	s, err := p.parseStringRaw()
	if err != nil {
		return nil, err
	}
	return s, nil
}

// parseStringRaw parses a quoted string (either quote char) with JSON5 escapes,
// returning the decoded string. Supports \uXXXX, \xXX, common short escapes,
// line continuations (\ followed by newline -> nothing), and \0.
func (p *parser) parseStringRaw() (string, error) {
	quote := p.s[p.pos]
	p.pos++ // consume opening quote
	var b strings.Builder
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == quote {
			p.pos++
			return b.String(), nil
		}
		if c == '\\' {
			p.pos++
			if p.pos >= len(p.s) {
				return "", p.errf("unterminated escape")
			}
			e := p.s[p.pos]
			switch e {
			case '"', '\'', '\\', '/':
				b.WriteByte(e)
				p.pos++
			case 'b':
				b.WriteByte('\b')
				p.pos++
			case 'f':
				b.WriteByte('\f')
				p.pos++
			case 'n':
				b.WriteByte('\n')
				p.pos++
			case 'r':
				b.WriteByte('\r')
				p.pos++
			case 't':
				b.WriteByte('\t')
				p.pos++
			case 'v':
				b.WriteByte('\v')
				p.pos++
			case '0':
				// \0 (null) — JSON5 allows it when not followed by a digit.
				b.WriteByte(0)
				p.pos++
			case 'x':
				p.pos++
				if p.pos+2 > len(p.s) {
					return "", p.errf("bad \\x escape")
				}
				n, err := strconv.ParseUint(p.s[p.pos:p.pos+2], 16, 32)
				if err != nil {
					return "", p.errf("bad \\x escape")
				}
				b.WriteRune(rune(n))
				p.pos += 2
			case 'u':
				p.pos++
				r, err := p.parseUnicodeEscape()
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			case '\n':
				// Line continuation: backslash-newline -> nothing.
				p.pos++
			case '\r':
				p.pos++
				if p.pos < len(p.s) && p.s[p.pos] == '\n' {
					p.pos++
				}
			default:
				// JSON5: an escaped non-escape char is the char itself.
				r, size := utf8.DecodeRuneInString(p.s[p.pos:])
				b.WriteRune(r)
				p.pos += size
			}
			continue
		}
		// Ordinary char (may be multibyte).
		r, size := utf8.DecodeRuneInString(p.s[p.pos:])
		b.WriteRune(r)
		p.pos += size
	}
	return "", p.errf("unterminated string")
}

// parseUnicodeEscape parses the 4 hex digits after \u, handling surrogate pairs
// (𐀀 -> the combined code point), matching JSON string semantics.
func (p *parser) parseUnicodeEscape() (rune, error) {
	if p.pos+4 > len(p.s) {
		return 0, p.errf("bad \\u escape")
	}
	hi, err := strconv.ParseUint(p.s[p.pos:p.pos+4], 16, 32)
	if err != nil {
		return 0, p.errf("bad \\u escape")
	}
	p.pos += 4
	r := rune(hi)
	// High surrogate: look for a following \uXXXX low surrogate.
	if r >= 0xD800 && r <= 0xDBFF && p.pos+6 <= len(p.s) &&
		p.s[p.pos] == '\\' && p.s[p.pos+1] == 'u' {
		lo, err := strconv.ParseUint(p.s[p.pos+2:p.pos+6], 16, 32)
		if err == nil && lo >= 0xDC00 && lo <= 0xDFFF {
			p.pos += 6
			return ((r - 0xD800) << 10) + (rune(lo) - 0xDC00) + 0x10000, nil
		}
	}
	return r, nil
}

// identStart / identPart approximate JSON5's identifier grammar (which pyjson5
// accepts for unquoted keys): letters, digits, '_', '$', and unicode letters.
func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

// parseIdentKey parses an unquoted object key (a JSON5 identifier name).
func (p *parser) parseIdentKey() (string, error) {
	start := p.pos
	first := true
	for p.pos < len(p.s) {
		r, size := utf8.DecodeRuneInString(p.s[p.pos:])
		if first {
			if !isIdentStart(r) {
				break
			}
			first = false
		} else if !isIdentPart(r) {
			break
		}
		p.pos += size
	}
	if p.pos == start {
		return "", p.errf("expected object key")
	}
	return p.s[start:p.pos], nil
}

// parseNamedNumber parses Infinity / NaN (optionally sign-prefixed by the
// caller). `neg` applies to Infinity.
func (p *parser) parseNamedNumber(neg bool) (any, error) {
	if strings.HasPrefix(p.s[p.pos:], "Infinity") {
		p.pos += len("Infinity")
		if neg {
			return negInf(), nil
		}
		return posInf(), nil
	}
	if strings.HasPrefix(p.s[p.pos:], "NaN") {
		p.pos += len("NaN")
		return nan(), nil
	}
	return nil, p.errf("invalid literal")
}

// parseNumber parses a JSON5 number: optional +/- sign, then Infinity/NaN, hex
// (0x...), or a decimal/float (with leading- or trailing-dot allowed). Produces
// a jsonx integer or float64 via jsonx.NumberValue for byte-identical
// re-encoding.
func (p *parser) parseNumber() (any, error) {
	start := p.pos
	neg := false
	if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
		neg = p.s[p.pos] == '-'
		p.pos++
	}
	// Signed Infinity/NaN.
	if p.pos < len(p.s) && (p.s[p.pos] == 'I' || p.s[p.pos] == 'N') {
		return p.parseNamedNumber(neg)
	}
	// Hex integer.
	if p.pos+1 < len(p.s) && p.s[p.pos] == '0' && (p.s[p.pos+1] == 'x' || p.s[p.pos+1] == 'X') {
		p.pos += 2
		hexStart := p.pos
		for p.pos < len(p.s) && isHex(p.s[p.pos]) {
			p.pos++
		}
		if p.pos == hexStart {
			return nil, p.errf("bad hex literal")
		}
		u, err := strconv.ParseUint(p.s[hexStart:p.pos], 16, 64)
		if err != nil {
			return nil, p.errf("hex literal out of range")
		}
		iv := int64(u)
		if neg {
			iv = -iv
		}
		return jsonx.IntValue(iv), nil
	}
	// Decimal / float: consume digits, optional '.', optional exponent.
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' ||
			c == '+' || c == '-' {
			p.pos++
		} else {
			break
		}
	}
	lit := p.s[start:p.pos]
	if lit == "" || lit == "+" || lit == "-" {
		return nil, p.errf("invalid number")
	}
	return jsonNumberValue(lit)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// jsonNumberValue normalizes a JSON5 decimal/float literal to the jsonx value
// model, handling JSON5-only forms (leading '+', leading-dot .5, trailing-dot
// 5.) that jsonx.NumberValue (JSON-strict) would reject — by rewriting them to
// a canonical form before delegating.
func jsonNumberValue(lit string) (any, error) {
	canon := lit
	canon = strings.TrimPrefix(canon, "+")
	// Leading-dot: ".5" -> "0.5"; "-.5" -> "-0.5".
	if strings.HasPrefix(canon, ".") {
		canon = "0" + canon
	} else if strings.HasPrefix(canon, "-.") {
		canon = "-0" + canon[1:]
	}
	// Trailing-dot: "5." -> "5.0"; "5.e3" -> "5.0e3".
	if i := strings.IndexByte(canon, '.'); i >= 0 {
		if i+1 >= len(canon) || canon[i+1] == 'e' || canon[i+1] == 'E' {
			canon = canon[:i+1] + "0" + canon[i+1:]
		}
	}
	v, ok := jsonx.NumberValue(canon)
	if !ok {
		return nil, &numErr{lit}
	}
	return v, nil
}

type numErr struct{ lit string }

func (e *numErr) Error() string { return "json5: invalid number literal " + strconv.Quote(e.lit) }
