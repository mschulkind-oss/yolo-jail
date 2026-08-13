package entrypoint

// tomltrivia.go is E4's `rmw` half: COMMENT PRESERVATION on a read-modify-write TOML
// surface.
//
// # Why this is the mode where it is small
//
// docs/plans/host-file-staging.md ranked five ways to keep a comment through a structured
// codec and put the "trivia sidecar" third, as real work wanting a decision. Three of its
// four listed costs — widening the capture overlay to carry trivia, a sidecar migration,
// and the staleness rule needing per-key provenance — are costs of CAPTURED STATE. An
// `rmw` surface has no captured state at all (pack-config-collaboration.md §0: "no
// sidecars"), and the source and the destination are the same file, read and rewritten in
// one operation. So for `rmw` the option collapses to what is here: scan the file's
// comments on the way in, put them back on the way out. No sidecar, no migration, no new
// layer.
//
// The other two modes are deliberately NOT served by this file, and the reasons differ:
//
//   - `computed` — yolo is the sole author and the file is a pure function of its layers
//     (pack-system.md §5). There is no user comment in it to preserve; a rendered comment
//     would be one yolo wrote itself, which is a different feature (the "yolo-authored
//     header" option, ranked second there). Wholesale overwrite is correct.
//   - `stateful` — the file is COMPOSED, so a comment could only come from the `host`
//     layer, and preserving it is a PROJECTION from one file into another. That is the
//     case the ranked options were actually about, and it still needs the decision they
//     name: an optional TriviaCodec on the engine's Codec interface, trivia surviving the
//     Lua transform boundary, and rule ① keyed on Result.Provenance. Out of scope here.
//
// # The rule for which comments survive
//
// host-file-staging.md's sub-question ① rules that trivia is DROPPED WHEN ITS VALUE IS
// OVERRIDDEN — "better a missing explanation than a lying one", the failure being a
// `# pinned to 2.13` sitting above `"2.15"`. Translated to `rmw`, where the file itself is
// the layer the comment came from, that is exactly: a comment survives iff the render did
// not change the value it sits above. rmwTriviaKeeper is that rule and nothing else.
//
// The doc conceded that rule "silently drops the user's comment". Here it does not: every
// drop is reported by key through HostRenderResult.Formatting, in observe as well as
// assert, so a user sees it before the write.
//
// # Association, and what is honestly out of reach
//
// The attachment convention is the one every formatter uses: the comment block immediately
// above a key or a table header belongs to it, as does a trailing comment on its own line.
// A block separated from what follows by a blank line is not attached to anything —
// preserved only in the two positions where "not attached" still has a meaning (a file
// header, a file footer) and reported as unplaceable anywhere else, since hoisting a
// mid-file block somewhere it did not come from destroys the association that made it worth
// keeping (the doc's "detached comments are close to noise").
//
// Two more populations are unplaceable by construction and counted rather than guessed at:
// a comment INSIDE a bracketed value (a multi-line array), which the canonical emitter
// re-renders inline, and a comment anywhere under an `[[array of tables]]`, whose elements
// have no distinguishing key path to attach to.
//
// # Why this does not touch the shared emitter
//
// The re-attachment is a post-pass over internal/agentcfg/codec's canonical output, not a
// change to it. That emitter is also the `stateful` path's, which is on the A12-fatal boot
// path and is pinned byte-for-byte by TestRenderFingerprintStable; keeping the change
// outside it makes "the jail's renders are unaffected" structural rather than something to
// re-verify. The post-pass is safe by construction too: it only ever INSERTS `#` lines and
// appends a trailing `#` to a line, neither of which can make valid TOML invalid.

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// tomlPathSep joins key-path segments into one map key. NUL cannot appear in a TOML bare
// key and must be escaped in a quoted one, so a joined path cannot collide with a real key
// containing the separator (the same trick internal/tomlx uses for MetaData keys).
const tomlPathSep = "\x00"

// tomlTrivia is the comment structure of a TOML source file: everything the canonical
// emitter would drop, indexed by what it was attached to.
type tomlTrivia struct {
	// header is the detached comment block at the very top of the file — the "what is
	// this file" preamble, the one comment whose meaning does not depend on position.
	header []string
	// footer is a trailing comment block with nothing after it.
	footer []string
	// leading maps a joined key path (a leaf key or a table header) to the comment block
	// immediately above it, no blank line between.
	leading map[string][]string
	// inline maps a joined key path to the trailing comment on its own line, `#` included.
	inline map[string]string
	// unplaced counts comment LINES that are attached to nothing this pass can address —
	// a detached mid-file block, a comment inside a bracketed value, anything under an
	// array of tables. Counted rather than dropped silently.
	unplaced int
}

// empty reports whether the file carried no comments at all, in which case the whole
// re-attachment pass is a no-op and the caller can use the canonical bytes directly.
func (t *tomlTrivia) empty() bool {
	return t == nil || (len(t.header) == 0 && len(t.footer) == 0 &&
		len(t.leading) == 0 && len(t.inline) == 0 && t.unplaced == 0)
}

// scanTOMLTrivia parses TOML source for its comments and what each is attached to.
//
// It runs only on bytes that have ALREADY decoded successfully (the RMW path refuses an
// unparseable file before it gets here), so it is parsing known-valid TOML. It still fails
// CLOSED: anything it does not confidently understand returns ok=false, and the caller
// falls back to the canonical emit plus the blanket "comments are not preserved" warning.
// A misattributed comment — one moved above a key it does not describe — would be worse
// than the loss this exists to prevent.
func scanTOMLTrivia(data []byte) (*tomlTrivia, bool) {
	tv := &tomlTrivia{leading: map[string][]string{}, inline: map[string]string{}}
	var table []string
	var pending []string
	inArrayTable := false
	sawItem := false

	// flush disposes of a comment block that turned out to be attached to nothing: the
	// file's opening block becomes the header, anything else is unplaceable.
	flush := func() {
		if len(pending) == 0 {
			return
		}
		if !sawItem && tv.header == nil {
			tv.header = pending
		} else {
			tv.unplaced += len(pending)
		}
		pending = nil
	}

	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\r') {
			j++
		}
		if j >= len(data) {
			break
		}
		switch {
		case data[j] == '\n':
			flush()
			i = j + 1
		case data[j] == '#':
			end := tomlLineEnd(data, j)
			pending = append(pending, strings.TrimRight(string(data[j:end]), " \t\r"))
			i = tomlPastNewline(data, end)
		case data[j] == '[':
			path, isArray, next, ok := parseTOMLTableHeader(data, j)
			if !ok {
				return nil, false
			}
			sawItem = true
			inArrayTable = isArray
			table = path
			if isArray {
				// Elements of an array of tables share one path, so a comment inside one
				// has nothing unambiguous to attach to.
				tv.unplaced += len(pending)
				pending = nil
			} else {
				key := strings.Join(path, tomlPathSep)
				if len(pending) > 0 {
					tv.leading[key] = pending
					pending = nil
				}
				if c, has := tomlInlineComment(data, next); has {
					tv.inline[key] = c
				}
			}
			i = tomlPastNewline(data, tomlLineEnd(data, next))
		default:
			path, next, ok := parseTOMLKeyPath(data, j)
			if !ok {
				return nil, false
			}
			next = tomlSkipSpace(data, next)
			if next >= len(data) || data[next] != '=' {
				return nil, false
			}
			end, inValue, ok := skipTOMLValue(data, next+1)
			if !ok {
				return nil, false
			}
			sawItem = true
			tv.unplaced += inValue
			if inArrayTable {
				tv.unplaced += len(pending)
				pending = nil
				i = tomlPastNewline(data, tomlLineEnd(data, end))
				continue
			}
			key := strings.Join(append(append([]string(nil), table...), path...), tomlPathSep)
			if len(pending) > 0 {
				tv.leading[key] = pending
				pending = nil
			}
			if c, has := tomlInlineComment(data, end); has {
				tv.inline[key] = c
			}
			i = tomlPastNewline(data, tomlLineEnd(data, end))
		}
	}
	// A block with nothing after it is the file's footer — the other position where
	// "attached to nothing" still reads correctly.
	if len(pending) > 0 {
		if !sawItem && tv.header == nil {
			tv.header = pending
		} else {
			tv.footer = pending
		}
	}
	return tv, true
}

// tomlLineEnd returns the index of the newline ending the line containing i, or len(data).
func tomlLineEnd(data []byte, i int) int {
	for ; i < len(data); i++ {
		if data[i] == '\n' {
			return i
		}
	}
	return len(data)
}

// tomlPastNewline returns the index just past the newline at i (or len(data)).
func tomlPastNewline(data []byte, i int) int {
	if i < len(data) && data[i] == '\n' {
		return i + 1
	}
	return i
}

// tomlSkipSpace advances past spaces and tabs.
func tomlSkipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	return i
}

// tomlInlineComment returns the trailing comment on the line starting at i, if the rest of
// the line is exactly a comment. The `#` is included, so the caller re-emits it verbatim.
func tomlInlineComment(data []byte, i int) (string, bool) {
	i = tomlSkipSpace(data, i)
	if i >= len(data) || data[i] != '#' {
		return "", false
	}
	return strings.TrimRight(string(data[i:tomlLineEnd(data, i)]), " \t\r"), true
}

// parseTOMLTableHeader parses `[a.b]` or `[[a.b]]` at i, returning the path, whether it is
// an array of tables, and the index just past the closing bracket.
func parseTOMLTableHeader(data []byte, i int) (path []string, isArray bool, next int, ok bool) {
	i++ // past '['
	if i < len(data) && data[i] == '[' {
		isArray = true
		i++
	}
	i = tomlSkipSpace(data, i)
	path, i, ok = parseTOMLKeyPath(data, i)
	if !ok {
		return nil, false, 0, false
	}
	i = tomlSkipSpace(data, i)
	if i >= len(data) || data[i] != ']' {
		return nil, false, 0, false
	}
	i++
	if isArray {
		if i >= len(data) || data[i] != ']' {
			return nil, false, 0, false
		}
		i++
	}
	return path, isArray, i, true
}

// parseTOMLKeyPath parses a dotted key — bare, basic-string, or literal-string segments —
// at i, returning the segments and the index just past the last one.
func parseTOMLKeyPath(data []byte, i int) ([]string, int, bool) {
	var parts []string
	for {
		i = tomlSkipSpace(data, i)
		seg, next, ok := parseTOMLKeySegment(data, i)
		if !ok {
			return nil, 0, false
		}
		parts = append(parts, seg)
		i = tomlSkipSpace(data, next)
		if i < len(data) && data[i] == '.' {
			i++
			continue
		}
		return parts, i, true
	}
}

// parseTOMLKeySegment parses one key segment at i.
func parseTOMLKeySegment(data []byte, i int) (string, int, bool) {
	if i >= len(data) {
		return "", 0, false
	}
	switch data[i] {
	case '"':
		end := skipBasicString(data, i+1)
		if end > len(data) {
			return "", 0, false
		}
		lit := string(data[i:end])
		unquoted, ok := unquoteTOMLBasic(lit)
		if !ok {
			return "", 0, false
		}
		return unquoted, end, true
	case '\'':
		end := skipToAny(data, i+1, '\'', '\n')
		if end > len(data) || end == 0 || data[end-1] != '\'' {
			return "", 0, false
		}
		return string(data[i+1 : end-1]), end, true
	}
	start := i
	for i < len(data) && isTOMLBareKeyByte(data[i]) {
		i++
	}
	if i == start {
		return "", 0, false
	}
	return string(data[start:i]), i, true
}

// isTOMLBareKeyByte reports whether c may appear in a TOML bare key.
func isTOMLBareKeyByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-'
}

// unquoteTOMLBasic unescapes a quoted basic-string key. Only the escapes a key can
// realistically carry are handled; anything else fails closed, which costs at most the
// trivia for one exotic key.
func unquoteTOMLBasic(lit string) (string, bool) {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return "", false
	}
	body := lit[1 : len(lit)-1]
	var sb strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			sb.WriteByte(body[i])
			continue
		}
		i++
		if i >= len(body) {
			return "", false
		}
		switch body[i] {
		case '"':
			sb.WriteByte('"')
		case '\\':
			sb.WriteByte('\\')
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		default:
			return "", false
		}
	}
	return sb.String(), true
}

// skipTOMLValue advances past the value beginning at i, returning the index just after it
// and the number of comment LINES buried inside a bracketed value (which the canonical
// emitter re-renders inline, so they cannot be kept).
//
// String- and bracket-aware, so a multi-line array or a multi-line string is consumed
// whole. A `#` at bracket depth zero ends the value — it is the inline comment, which the
// caller reads separately.
func skipTOMLValue(data []byte, i int) (int, int, bool) {
	depth := 0
	inner := 0
	for i < len(data) {
		switch {
		case bytes.HasPrefix(data[i:], []byte(`"""`)):
			i = skipDelimited(data, i+3, `"""`)
		case bytes.HasPrefix(data[i:], []byte("'''")):
			i = skipDelimited(data, i+3, "'''")
		case data[i] == '"':
			i = skipBasicString(data, i+1)
		case data[i] == '\'':
			i = skipToAny(data, i+1, '\'', '\n')
		case data[i] == '[' || data[i] == '{':
			depth++
			i++
		case data[i] == ']' || data[i] == '}':
			depth--
			if depth < 0 {
				return 0, 0, false
			}
			i++
		case data[i] == '#':
			if depth == 0 {
				return i, inner, true
			}
			inner++
			i = tomlLineEnd(data, i)
		case data[i] == '\n':
			if depth == 0 {
				return i, inner, true
			}
			i++
		default:
			i++
		}
	}
	if depth != 0 {
		return 0, 0, false
	}
	return len(data), inner, true
}

// rmwTriviaKeeper builds sub-question ①'s rule as a predicate over joined key paths: a
// comment survives iff the render did not change the value it sits above.
//
// Two shapes, and the difference is what a comment MEANS in each position:
//
//   - a LEAF key — kept iff its value is byte-equal before and after. A changed value makes
//     the explanation above it a statement about a value that is no longer there.
//   - a TABLE header — kept iff the table still exists. A section comment describes the
//     section, not its contents, so yolo adding a key under `[tui]` does not falsify
//     "# my terminal settings"; the table disappearing does.
func rmwTriviaKeeper(before, after *jsonx.OrderedMap) func(string) bool {
	return func(path string) bool {
		segs := strings.Split(path, tomlPathSep)
		prev, hadPrev := lookupOrderedPath(before, segs)
		next, hasNext := lookupOrderedPath(after, segs)
		if !hasNext {
			return false
		}
		if _, isTable := next.(*jsonx.OrderedMap); isTable {
			return true
		}
		return hadPrev && sameJSON(prev, next)
	}
}

// lookupOrderedPath walks a joined key path through an OrderedMap tree.
func lookupOrderedPath(m *jsonx.OrderedMap, segs []string) (any, bool) {
	var cur any = m
	for _, s := range segs {
		om, isMap := cur.(*jsonx.OrderedMap)
		if !isMap {
			return nil, false
		}
		v, ok := om.Get(s)
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// attachTOMLTrivia re-inserts a file's comments into the canonical emitter's output,
// keeping only what keep() allows, and reports every comment it could not put back.
//
// It walks the emitted text rather than the value model because the emitter's output is
// exactly regular — a table header line, or a complete `key = value` line, never a value
// spanning lines — so the key path of every line is unambiguous. Any line it cannot read
// is left alone, so an emitter change can cost trivia but cannot corrupt output.
func attachTOMLTrivia(encoded string, tv *tomlTrivia, keep func(string) bool) (string, []string) {
	if tv.empty() {
		return encoded, nil
	}
	used := map[string]bool{}
	var out []string
	if len(tv.header) > 0 {
		out = append(out, tv.header...)
		if encoded != "" {
			out = append(out, "")
		}
	}
	var table []string
	for _, line := range strings.Split(strings.TrimRight(encoded, "\n"), "\n") {
		key, isHeader, ok := emittedLineKey(line, table)
		if ok && isHeader {
			table = strings.Split(key, tomlPathSep)
		}
		if !ok || key == "" {
			out = append(out, line)
			continue
		}
		if !keep(key) {
			out = append(out, line)
			continue
		}
		if block, has := tv.leading[key]; has {
			used[key] = true
			out = append(out, block...)
		}
		if c, has := tv.inline[key]; has {
			used[key] = true
			line += "  " + c
		}
		out = append(out, line)
	}
	if len(tv.footer) > 0 {
		out = append(out, "")
		out = append(out, tv.footer...)
	}
	text := strings.Join(out, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, triviaDropReport(tv, used, keep)
}

// emittedLineKey reads one line of canonical emitter output, returning the joined key path
// it defines and whether that path is a TABLE HEADER (which also changes the current table
// for the lines that follow).
//
// A blank line, an `[[array of tables]]` header, or anything unrecognized returns ok=false;
// an array-of-tables header additionally clears the table context, since keys inside it
// have no addressable path.
func emittedLineKey(line string, table []string) (string, bool, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false, false
	}
	if strings.HasPrefix(trimmed, "[[") {
		return "", false, false
	}
	if strings.HasPrefix(trimmed, "[") {
		path, _, next, ok := parseTOMLTableHeader([]byte(trimmed), 0)
		if !ok || next != len(trimmed) {
			return "", false, false
		}
		return strings.Join(path, tomlPathSep), true, true
	}
	path, next, ok := parseTOMLKeyPath([]byte(trimmed), 0)
	if !ok {
		return "", false, false
	}
	if next >= len(trimmed) || trimmed[tomlSkipSpace([]byte(trimmed), next)] != '=' {
		return "", false, false
	}
	return strings.Join(append(append([]string(nil), table...), path...), tomlPathSep),
		false, true
}

// triviaDropReport names every comment this render did not put back, one line per cause.
// It is what turns host-file-staging.md's conceded "silently drops the user's comment" into
// a reported one — and it stays empty when nothing is lost, or the line stops being read.
func triviaDropReport(tv *tomlTrivia, used map[string]bool, keep func(string) bool) []string {
	dropped := map[string]bool{}
	for key := range tv.leading {
		if !used[key] {
			dropped[key] = true
		}
	}
	for key := range tv.inline {
		if !used[key] {
			dropped[key] = true
		}
	}
	changed := make([]string, 0, len(dropped))
	gone := make([]string, 0, len(dropped))
	for key := range dropped {
		dotted := strings.ReplaceAll(key, tomlPathSep, ".")
		if keep(key) {
			// keep() says the value is intact, so the comment was lost for a structural
			// reason instead — the key is no longer emitted where this pass can find it.
			gone = append(gone, dotted)
			continue
		}
		changed = append(changed, dotted)
	}
	sort.Strings(changed)
	sort.Strings(gone)
	var out []string
	if len(changed) > 0 {
		out = append(out, "comments are preserved, EXCEPT above "+
			strings.Join(quoteAll(changed), ", ")+" — this render changes those keys' "+
			"values, and a comment explaining a value that is no longer there is worse "+
			"than no comment")
	}
	if len(gone) > 0 {
		out = append(out, "the comments above "+strings.Join(quoteAll(gone), ", ")+
			" are dropped — those keys are not in the rendered file")
	}
	if tv.unplaced > 0 {
		out = append(out, fmt.Sprintf("%d comment line(s) attached to no key are dropped "+
			"— a block separated from what follows by a blank line, a comment inside a "+
			"multi-line value, or one under an [[array of tables]]", tv.unplaced))
	}
	return out
}

// quoteAll backticks each name for the report.
func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + n + "`"
	}
	return out
}
