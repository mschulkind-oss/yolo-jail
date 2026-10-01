package json5

import (
	"strings"
	"testing"
)

// locateDoc is shaped like a loophole manifest a person wrote: comments everywhere, a
// single-quoted string, an unquoted key, a trailing comma, and an array holding an object whose
// keys repeat the ones being looked for.
const locateDoc = `// header comment
{
  "name": "tool",
  /* the builds */
  "binaries": {
    toold: {
      "linux/amd64": {"url": 'https://example.test/a', // trailing comment
                      "sha256": "aaaa"},
    },
  },
  "list": [{"binaries": {"toold": {"linux/amd64": {"url": "decoy"}}}}],
}
`

func TestLocateFindsTheValueDecodeReads(t *testing.T) {
	cases := []struct {
		path []string
		want string
	}{
		{[]string{"name"}, `"tool"`},
		{[]string{"binaries", "toold", "linux/amd64", "url"}, `'https://example.test/a'`},
		{[]string{"binaries", "toold", "linux/amd64", "sha256"}, `"aaaa"`},
	}
	for _, tc := range cases {
		span, ok, err := Locate([]byte(locateDoc), tc.path...)
		if err != nil || !ok {
			t.Fatalf("Locate(%v) = %v, %v, %v", tc.path, span, ok, err)
		}
		if got := locateDoc[span.Start:span.End]; got != tc.want {
			t.Errorf("Locate(%v) spans %q, want %q", tc.path, got, tc.want)
		}
	}
}

// The edit the span exists for: replacing exactly those bytes leaves every comment, and the
// result decodes to the new value.
func TestLocateSpanIsAnInPlaceEdit(t *testing.T) {
	path := []string{"binaries", "toold", "linux/amd64", "sha256"}
	span, ok, err := Locate([]byte(locateDoc), path...)
	if err != nil || !ok {
		t.Fatalf("Locate = %v, %v, %v", span, ok, err)
	}
	edited := locateDoc[:span.Start] + `"bbbb"` + locateDoc[span.End:]
	for _, comment := range []string{"// header comment", "/* the builds */", "// trailing comment"} {
		if !strings.Contains(edited, comment) {
			t.Errorf("the edit lost %q", comment)
		}
	}
	span2, ok, err := Locate([]byte(edited), path...)
	if err != nil || !ok || edited[span2.Start:span2.End] != `"bbbb"` {
		t.Fatalf("after the edit Locate = %v, %v, %v", span2, ok, err)
	}
	if _, err := Decode([]byte(edited)); err != nil {
		t.Fatalf("the edited document no longer decodes: %v", err)
	}
}

func TestLocateMissesWhatIsNotThere(t *testing.T) {
	for _, path := range [][]string{
		{"binaries", "toold", "darwin/arm64", "url"},
		{"nope"},
		// The decoy inside "list" is an array element's member, never a top-level path.
		{"list", "binaries"},
	} {
		if span, ok, err := Locate([]byte(locateDoc), path...); err != nil || ok {
			t.Errorf("Locate(%v) = %v, %v, %v; want nothing found", path, span, ok, err)
		}
	}
}

// A key written twice along the path is refused: Decode keeps the last, so an edit to either
// one could change nothing Decode reads.
func TestLocateRefusesADuplicatedKeyOnThePath(t *testing.T) {
	doc := `{"binaries": {"x": {"url": "a"}}, "binaries": {"y": {}}}`
	if _, _, err := Locate([]byte(doc), "binaries", "x", "url"); err == nil ||
		!strings.Contains(err.Error(), "binaries is written 2 times") {
		t.Errorf("Locate over a duplicated parent = %v, want the duplicate named", err)
	}
	// A duplicate OFF the path is Decode's business, not this lookup's.
	doc = `{"a": 1, "a": 2, "binaries": {"x": {"url": "u"}}}`
	if _, ok, err := Locate([]byte(doc), "binaries", "x", "url"); err != nil || !ok {
		t.Errorf("Locate beside an unrelated duplicate = %v, %v", ok, err)
	}
}

func TestLocateReportsAMalformedDocument(t *testing.T) {
	if _, _, err := Locate([]byte(`{"a": `), "a"); err == nil {
		t.Error("Locate over a truncated document returned no error")
	}
}

// LocateSteps steps into an array as well as an object: a config refusal names the second
// entry of a list, and the line it was written on is the element's.
func TestLocateStepsFindsAnArrayElement(t *testing.T) {
	doc := `{
  "packs": [
    "claude",
    // a comment between entries
    {"source": "git+https://example.test/p", "name": "p"},
  ],
}`
	cases := []struct {
		path []Step
		want string
	}{
		{[]Step{Key("packs"), Elem(0)}, `"claude"`},
		{[]Step{Key("packs"), Elem(1), Key("name")}, `"p"`},
	}
	for _, tc := range cases {
		span, ok, err := LocateSteps([]byte(doc), tc.path...)
		if err != nil || !ok {
			t.Fatalf("LocateSteps(%v) = %v, %v, %v", tc.path, span, ok, err)
		}
		if got := doc[span.Start:span.End]; got != tc.want {
			t.Errorf("LocateSteps(%v) spans %q, want %q", tc.path, got, tc.want)
		}
	}
	if _, ok, err := LocateSteps([]byte(doc), Key("packs"), Elem(2)); err != nil || ok {
		t.Errorf("LocateSteps past the end of the list = %v, %v; want nothing found", ok, err)
	}
	// A key step never matches an element, nor an element step a member.
	if _, ok, _ := LocateSteps([]byte(doc), Key("packs"), Key("0")); ok {
		t.Error(`Key("0") matched an array element`)
	}
	if _, ok, _ := LocateSteps([]byte(`{"a": {"0": 1}}`), Key("a"), Elem(0)); ok {
		t.Error("Elem(0) matched an object member")
	}
}

// Position counts lines and characters from 1, as an editor's file:line:col jump does.
func TestPositionCountsLinesAndCharactersFromOne(t *testing.T) {
	doc := "{\n  \"é\": 1,\n  \"k\": 2\n}"
	for _, tc := range []struct {
		needle    string
		line, col int
	}{
		{"{", 1, 1},
		{"1", 2, 8}, // the é is one character and two bytes
		{"\"k\"", 3, 3},
	} {
		line, col := Position([]byte(doc), strings.Index(doc, tc.needle))
		if line != tc.line || col != tc.col {
			t.Errorf("Position(%q) = %d:%d, want %d:%d", tc.needle, line, col, tc.line, tc.col)
		}
	}
	if line, col := Position([]byte(doc), len(doc)+5); line != 4 || col != 2 {
		t.Errorf("Position past the end = %d:%d, want the end, 4:2", line, col)
	}
}
