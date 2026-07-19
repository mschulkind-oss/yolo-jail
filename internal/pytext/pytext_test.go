package pytext

import (
	"testing"
)

// goldenRepr pins Python repr()'s observed output.
var goldenRepr = map[string]string{
	"abc":         "'abc'",
	"a'b":         `"a'b"`,         // has ' but no " -> double-quoted
	"a\"b":        `'a"b'`,         // has " but no ' -> single-quoted
	"a'b\"c":      `'a\'b"c'`,      // both -> single, ' escaped
	"tab\there":   `'tab\there'`,   // \t short escape
	"new\nline":   `'new\nline'`,   // \n short escape
	"café":        "'café'",        // printable non-ASCII stays literal
	"back\\slash": `'back\\slash'`, // backslash doubled
	"":            "''",            // empty
	"quote\"only": `'quote"only'`,  // " without ' -> single-quoted
}

func TestReprGolden(t *testing.T) {
	for in, want := range goldenRepr {
		if got := Repr(in); got != want {
			t.Errorf("Repr(%q) = %q, want %q", in, got, want)
		}
	}
}
