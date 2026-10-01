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
	hits := make([]int, len(path))
	p := &parser{s: string(data)}
	p.at = func(steps []pathStep, start, end int) {
		if len(steps) > len(path) {
			return
		}
		for i, st := range steps {
			want := path[i]
			if st.index != want.elem {
				return
			}
			if (want.elem && st.pos != want.index) || (!want.elem && st.key != want.key) {
				return
			}
		}
		hits[len(steps)-1]++
		if len(steps) == len(path) {
			span, ok = Span{Start: start, End: end}, true
		}
	}
	if _, err := p.document(); err != nil {
		return Span{}, false, err
	}
	for i, n := range hits {
		if n > 1 {
			names := make([]string, i+1)
			for j, st := range path[:i+1] {
				names[j] = st.String()
			}
			return Span{}, false, fmt.Errorf("json5: %s is written %d times, and only the last "+
				"one is read", strings.Join(names, "."), n)
		}
	}
	return span, ok, nil
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
