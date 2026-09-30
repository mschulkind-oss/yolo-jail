package json5

import (
	"fmt"
	"strings"
)

// Span is where one value sits in a document: bytes [Start, End) are the text Decode read it
// from, a string's quotes included.
type Span struct {
	Start, End int
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
// mistaken for a member of the one around it.
func Locate(data []byte, path ...string) (span Span, ok bool, err error) {
	hits := make([]int, len(path))
	p := &parser{s: string(data)}
	p.at = func(steps []pathStep, start, end int) {
		if len(steps) > len(path) {
			return
		}
		for i, st := range steps {
			if st.index || st.key != path[i] {
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
			return Span{}, false, fmt.Errorf("json5: %s is written %d times, and only the last "+
				"one is read", strings.Join(path[:i+1], "."), n)
		}
	}
	return span, ok, nil
}
