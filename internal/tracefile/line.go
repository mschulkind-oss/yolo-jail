// Package tracefile reads a launch's trace back: the directory of per-process line files the
// collector writes, the parent rule that turns flat spans into a tree, and the summary that ranks
// where the startup time went.
//
// Design: docs/design/startup-tracing.md (§5.3 the files, §5.4 the parent rule, §6 the summary).
// The line format is the wire contract in that design's plan; this package parses it and nothing
// else, so it builds against the contract and never against the writer's code.
//
// # The input is untrusted
//
// A trace directory holds a file the host rewrites from what the jail wrote (jail.jsonl), and
// where the directory lives is not yet ruled, so every file is read as hostile: regular files only,
// never through a link, bounded in bytes, lines and line length; a line that does not parse is
// dropped and counted, never fatal; names and attribute values lose control characters and are
// capped; ids must be the W3C lowercase-hex forms; and an explicit parent that would make a cycle
// is refused. Reading never fails a caller over one bad file: the file is skipped and named.
package tracefile

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The line kinds of the contract. KindLane is a file's first line, naming its writer.
const (
	KindLane   = "lane"
	KindStart  = "start"
	KindEnd    = "end"
	KindMark   = "mark"
	KindNote   = "note"
	KindRecord = "record"
)

// Reader bounds. A writer stops at 10,000 events plus its lane line and one truncated mark
// (design §5.7, TR-D11); a file longer than that, or a line or file larger than these, was not
// written by a well-behaved writer and is read only up to the bound.
const (
	MaxLinesPerFile = 10_002
	MaxLineBytes    = 64 << 10
	MaxFileBytes    = 8 << 20
	MaxMetaBytes    = 64 << 10
	MaxNameBytes    = 128
	MaxAttrs        = 8
	MaxAttrValue    = 240
)

// rawLine is one decoded line. Fields the contract does not name are ignored by encoding/json.
type rawLine struct {
	T      int64             `json:"t"`
	K      string            `json:"k"`
	Name   string            `json:"name"`
	Span   string            `json:"span"`
	Parent string            `json:"parent"`
	Dur    *int64            `json:"dur"`
	Attrs  map[string]string `json:"attrs"`
	PID    int               `json:"pid"`
	Trace  string            `json:"trace"`
}

// parseLine decodes and validates one line. ok is false for anything the contract does not
// allow; the caller counts it as dropped.
func parseLine(b []byte) (rawLine, bool) {
	var l rawLine
	if err := json.Unmarshal(b, &l); err != nil {
		return rawLine{}, false
	}
	l.Name = cleanText(l.Name, MaxNameBytes)
	if l.Parent != "" && !IsSpanID(l.Parent) {
		return rawLine{}, false
	}
	switch l.K {
	case KindLane:
		if l.Name == "" || !IsTraceID(l.Trace) || l.PID < 0 {
			return rawLine{}, false
		}
	case KindStart:
		if l.T <= 0 || l.Name == "" || !IsSpanID(l.Span) {
			return rawLine{}, false
		}
	case KindEnd, KindRecord:
		if l.T <= 0 || !IsSpanID(l.Span) {
			return rawLine{}, false
		}
		if l.Dur != nil && (*l.Dur < 0 || *l.Dur >= l.T) {
			return rawLine{}, false
		}
	case KindMark, KindNote:
		if l.T <= 0 || l.Name == "" {
			return rawLine{}, false
		}
	default:
		return rawLine{}, false
	}
	l.Attrs = cleanAttrs(l.Attrs)
	return l, true
}

// IsTraceID reports whether s is a W3C trace id: 32 lowercase hex digits, not all zero.
func IsTraceID(s string) bool { return isHexID(s, 32) }

// IsSpanID reports whether s is a W3C span id: 16 lowercase hex digits, not all zero.
func IsSpanID(s string) bool { return isHexID(s, 16) }

func isHexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	nonzero := false
	for i := 0; i < n; i++ {
		c := s[i]
		switch {
		case c == '0':
		case (c >= '1' && c <= '9') || (c >= 'a' && c <= 'f'):
			nonzero = true
		default:
			return false
		}
	}
	return nonzero
}

// cleanText drops invalid UTF-8 and every control or non-printing rune, so nothing read from a
// trace can carry a terminal escape into a summary, and caps the result at max bytes on a rune
// boundary.
func cleanText(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > max {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cleanAttrs keeps at most MaxAttrs attributes, the first by key order, with keys and values
// cleaned and capped. An attribute whose key cleans to nothing is dropped.
func cleanAttrs(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, min(len(keys), MaxAttrs))
	for _, k := range keys {
		if len(out) == MaxAttrs {
			break
		}
		ck := cleanText(k, MaxNameBytes)
		if ck == "" {
			continue
		}
		if _, dup := out[ck]; dup {
			continue
		}
		out[ck] = cleanText(in[k], MaxAttrValue)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
