package configmigrate

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// comment is one comment of an old file: where it starts, and its lines as TOML comments.
type comment struct {
	start int
	lines []string
}

// oldComments is every comment of a JSONC document in document order, each converted to `#`
// lines. Changing the delimiter is format conversion; no comment is read as belonging to a key
// (config-migration.md §4 item 3).
func oldComments(doc *configdoc.Document) []comment {
	var out []comment
	for _, span := range doc.Index.Comments() {
		text := string(doc.Bytes[span.Start:span.End])
		var lines []string
		switch {
		case strings.HasPrefix(text, "//"):
			lines = []string{tomlCommentLine(strings.TrimPrefix(text, "//"))}
		case strings.HasPrefix(text, "/*"):
			body := strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
			raw := strings.Split(body, "\n")
			// A block whose every continuation line opens with `*` uses the star as part of
			// its delimiter, which `#` replaces.
			starred := len(raw) > 1
			for _, line := range raw[1:] {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && !strings.HasPrefix(trimmed, "*") {
					starred = false
				}
			}
			for i, line := range raw {
				line = strings.TrimLeft(strings.TrimRight(line, "\r"), " \t")
				if starred && i > 0 {
					line = strings.TrimPrefix(line, "*")
				}
				if strings.TrimSpace(line) == "" && (len(lines) == 0) {
					continue
				}
				lines = append(lines, tomlCommentLine(line))
			}
			for len(lines) > 0 && lines[len(lines)-1] == "#" {
				lines = lines[:len(lines)-1]
			}
			if len(lines) == 0 {
				lines = []string{"#"}
			}
		default:
			lines = []string{tomlCommentLine(text)}
		}
		out = append(out, comment{start: span.Start, lines: lines})
	}
	return out
}

func tomlCommentLine(text string) string {
	text = strings.TrimRight(text, " \t\r")
	if text == "" {
		return "#"
	}
	if strings.HasPrefix(text, " ") {
		return "#" + text
	}
	return "# " + text
}

// emitter writes a TOML document from new values, placing each old comment, in document order,
// before the first written line whose old key came after it, and the rest at the end
// (docs/design/config-migration.md#4-what-yolo-config-migrate-does, "Keeps comments, in order").
type emitter struct {
	out      strings.Builder
	comments []comment
	next     int
	anchor   func(newPath []string) int
}

func (e *emitter) flushBefore(offset int) {
	for e.next < len(e.comments) && offset >= 0 && e.comments[e.next].start < offset {
		for _, line := range e.comments[e.next].lines {
			e.out.WriteString(line)
			e.out.WriteByte('\n')
		}
		e.next++
	}
}

func (e *emitter) flushAll() {
	for e.next < len(e.comments) {
		for _, line := range e.comments[e.next].lines {
			e.out.WriteString(line)
			e.out.WriteByte('\n')
		}
		e.next++
	}
}

// encodeTOML writes values as TOML with the comments of the old document. anchor gives the old
// byte offset a new path came from, -1 when none.
func encodeTOML(values *jsonx.OrderedMap, comments []comment, anchor func([]string) int) ([]byte, error) {
	e := &emitter{comments: comments, anchor: anchor}
	if err := e.table(values, nil); err != nil {
		return nil, err
	}
	e.flushAll()
	return []byte(e.out.String()), nil
}

func (e *emitter) table(t *jsonx.OrderedMap, path []string) error {
	if len(path) > 0 && hasScalar(t) {
		at := e.tableAnchor(t, path)
		if e.out.Len() > 0 {
			e.out.WriteByte('\n')
		}
		e.flushBefore(at)
		heading, err := keyPath(path)
		if err != nil {
			return err
		}
		e.out.WriteString("[" + heading + "]\n")
	}
	for _, k := range t.Keys() {
		v, _ := t.Get(k)
		if _, isTable := v.(*jsonx.OrderedMap); isTable {
			continue
		}
		child := append(append([]string(nil), path...), k)
		e.flushBefore(e.anchor(child))
		key, err := tomlKey(k)
		if err != nil {
			return err
		}
		encoded, err := configdoc.EncodeValue(tomlReady(v), configdoc.TOML)
		if err != nil {
			return fmt.Errorf("%s: %w", dotted(child), err)
		}
		e.out.WriteString(key + " = " + string(encoded) + "\n")
	}
	for _, k := range t.Keys() {
		v, _ := t.Get(k)
		sub, isTable := v.(*jsonx.OrderedMap)
		if !isTable {
			continue
		}
		if err := e.table(sub, append(append([]string(nil), path...), k)); err != nil {
			return err
		}
	}
	return nil
}

// hasScalar says whether a table holds a key of its own; one holding only tables needs no header,
// and an empty one does (its header is all that says it is there).
func hasScalar(t *jsonx.OrderedMap) bool {
	if len(t.Keys()) == 0 {
		return true
	}
	for _, k := range t.Keys() {
		v, _ := t.Get(k)
		if _, isTable := v.(*jsonx.OrderedMap); !isTable {
			return true
		}
	}
	return false
}

// tableAnchor is a table's own old offset, or its earliest child's when the table is new.
func (e *emitter) tableAnchor(t *jsonx.OrderedMap, path []string) int {
	if at := e.anchor(path); at >= 0 {
		return at
	}
	best := -1
	for _, k := range t.Keys() {
		v, _ := t.Get(k)
		child := append(append([]string(nil), path...), k)
		at := e.anchor(child)
		if sub, ok := v.(*jsonx.OrderedMap); ok && at < 0 {
			at = e.tableAnchor(sub, child)
		}
		if at >= 0 && (best < 0 || at < best) {
			best = at
		}
	}
	return best
}

func keyPath(path []string) (string, error) {
	parts := make([]string, len(path))
	for i, k := range path {
		s, err := tomlKey(k)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, "."), nil
}

func tomlKey(k string) (string, error) {
	bare := k != ""
	for _, r := range k {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			bare = false
			break
		}
	}
	if bare {
		return k, nil
	}
	quoted, err := configdoc.EncodeValue(k, configdoc.TOML)
	return string(quoted), err
}

// tomlReady turns every JSON integer into an int64 so the TOML encoder writes it as one.
func tomlReady(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		out := jsonx.NewOrderedMap()
		for _, k := range t.Keys() {
			item, _ := t.Get(k)
			out.Set(k, tomlReady(item))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = tomlReady(item)
		}
		return out
	case bool, string, nil:
		return t
	}
	if n, ok := jsonx.AsInt(v); ok {
		return n
	}
	return v
}

// countLines is the line count the transcript shows.
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
