package packdecl

import (
	"strings"
	"testing"
)

// reserved_notes describes a FENCED child, in one line: a note for a name the contribution does
// not reserve, or one that is empty or spans lines, is refused rather than silently printed.
func TestReservedNotesMustDescribeAReservedChildInOneLine(t *testing.T) {
	decode := func(notes string) []string {
		t.Helper()
		_, problems := Decode([]byte(`{"name":"p","description":"d","contributes":[` +
			`{"kind":"skills","into":".claude/skills","reserved":["synced"],` +
			`"reserved_notes":` + notes + `}]}`))
		return problems
	}
	if probs := decode(`{"synced":"skills a tool syncs"}`); len(probs) != 0 {
		t.Errorf("a note for a reserved child was refused: %v", probs)
	}
	for _, bad := range []string{`{"other":"x"}`, `{"synced":""}`, `{"synced":"two\nlines"}`} {
		probs := decode(bad)
		if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "reserved_notes") {
			t.Errorf("reserved_notes %s was not refused: %v", bad, probs)
		}
	}
}
