package json5

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Span is where one value sits in a document: bytes [Start, End) are the text Decode read it
// from, a string's quotes included.
type Span struct {
	Start, End int
}

// Step is one step of a path into a document: an object member by its key, or an array
// element by its index. Key and Elem build them.
type Step struct {
	key   string
	index int
	elem  bool
}

// Key is the step to the member of an object whose key is k.
func Key(k string) Step { return Step{key: k} }

// Elem is the step to the element of an array at index i, counted from 0.
func Elem(i int) Step { return Step{index: i, elem: true} }

// String is the step as an error names it: the key, or "[i]".
func (s Step) String() string {
	if s.elem {
		return "[" + strconv.Itoa(s.index) + "]"
	}
	return s.key
}

// Locate returns the span of the value at path, a list of object keys from the top-level
// object down, read by the same grammar Decode uses. It is how a tool EDITS a value in place,
// replacing those bytes and nothing else, so every comment and every other byte of a
// hand-written file survives (the pack binaries' pin tool, tools/pack-binaries, writes a
// manifest's url and sha256 this way).
//
// ok is false when nothing sits at path. A key written twice along the path is an error rather
// than a guess: Decode keeps the last one, and an edit made to the other would change nothing
// Decode reads. Steps through arrays are never matched, so an object inside an array is not
// mistaken for a member of the one around it; LocateSteps is the form that can step into one.
func Locate(data []byte, path ...string) (span Span, ok bool, err error) {
	steps := make([]Step, len(path))
	for i, k := range path {
		steps[i] = Key(k)
	}
	return LocateSteps(data, steps...)
}

// LocateSteps is Locate over a path that may step into arrays as well as objects: Key(k) for
// an object member, Elem(i) for an array element. It is how a config refusal names the line
// a value was written on (internal/config's Sources), where the value can be the third entry
// of a list.
func LocateSteps(data []byte, path ...Step) (span Span, ok bool, err error) {
	ix, err := NewIndex(data)
	if err != nil {
		return Span{}, false, err
	}
	return ix.Locate(path...)
}

// Index is where every value of one document sits, read by ONE parse, for a caller that
// locates many paths in the same bytes: internal/config's Sources builds one per config file,
// lazily, the first time a message about that file is located, so a refusal naming several
// keys (or a key several files write) parses each file once rather than once per location.
// Its Locate answers exactly as LocateSteps does, which is built on it.
type Index struct {
	spans map[string]Span // the last value read at each path
	hits  map[string]int  // how many values were read at each path: >1 is a key written twice
}

// NewIndex parses data and records the span of every object member's value and every array
// element, or returns the parse error.
func NewIndex(data []byte) (*Index, error) {
	ix := &Index{spans: map[string]Span{}, hits: map[string]int{}}
	p := &parser{s: string(data)}
	var key []byte
	p.at = func(steps []pathStep, start, end int) {
		key = key[:0]
		for _, st := range steps {
			key = appendPathKey(key, st.index, st.pos, st.key)
		}
		k := string(key)
		ix.hits[k]++
		ix.spans[k] = Span{Start: start, End: end}
	}
	if _, err := p.document(); err != nil {
		return nil, err
	}
	return ix, nil
}

// Locate is LocateSteps over the indexed document: the span at path, ok false when nothing
// sits there, and an error when a key along the path is written more than once.
func (ix *Index) Locate(path ...Step) (span Span, ok bool, err error) {
	var key []byte
	for i, st := range path {
		key = appendPathKey(key, st.elem, st.index, st.key)
		if n := ix.hits[string(key)]; n > 1 {
			names := make([]string, i+1)
			for j, s := range path[:i+1] {
				names[j] = s.String()
			}
			return Span{}, false, fmt.Errorf("json5: %s is written %d times, and only the last "+
				"one is read", strings.Join(names, "."), n)
		}
	}
	if len(path) == 0 {
		return Span{}, false, nil
	}
	span, ok = ix.spans[string(key)]
	return span, ok, nil
}

// appendPathKey appends one step to an Index key: "#<i>;" for an element, "<len>:<key>" for a
// member, so no key's text can read as another path.
func appendPathKey(b []byte, elem bool, i int, key string) []byte {
	if elem {
		b = append(b, '#')
		b = strconv.AppendInt(b, int64(i), 10)
		return append(b, ';')
	}
	b = strconv.AppendInt(b, int64(len(key)), 10)
	b = append(b, ':')
	return append(b, key...)
}

// Position is where byte offset sits in data as a person counts it: line and column, both
// from 1, the column in characters (UTF-8 runes), so a line holding a non-ASCII string still
// lands on the character the offset names. An offset past the end is clamped to it.
func Position(data []byte, offset int) (line, col int) {
	if offset > len(data) {
		offset = len(data)
	}
	if offset < 0 {
		offset = 0
	}
	line = 1 + strings.Count(string(data[:offset]), "\n")
	lineStart := strings.LastIndexByte(string(data[:offset]), '\n') + 1
	return line, 1 + utf8.RuneCount(data[lineStart:offset])
}
