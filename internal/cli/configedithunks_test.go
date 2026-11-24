package cli

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
)

// A multi-line hunk marks EVERY line, as a unified diff does: the removed key of a last-key
// unset must not print like an unchanged context line.
func TestWriteConfigEditHunksMarksEveryLineOfAMultiLineHunk(t *testing.T) {
	var out bytes.Buffer
	writeConfigEditHunks(&out, []configedit.Hunk{
		{Before: []byte("[env.set]\nA = \"a\"\n"), After: nil},
		{Before: nil, After: []byte("[env.set]\nB = \"b\"")},
	})
	want := "@@\n-[env.set]\n-A = \"a\"\n@@\n+[env.set]\n+B = \"b\"\n"
	if out.String() != want {
		t.Fatalf("hunks printed\n%q\nwant\n%q", out.String(), want)
	}
}
