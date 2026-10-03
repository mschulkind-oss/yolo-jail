package nixstderr

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// TestReadLineCappedReadsEveryLineWhole: each line comes back without its newline, cut to the cap
// and flagged when longer, and the reader is left at the next line either way.
func TestReadLineCappedReadsEveryLineWhole(t *testing.T) {
	long := strings.Repeat("y", 100)
	r := bufio.NewReaderSize(strings.NewReader("short\n"+"exactly10!\n"+long+"\n"+"last, no newline"), 16)
	want := []struct {
		line string
		long bool
	}{
		{"short", false},
		{"exactly10!", false},
		{long[:10], true},
		{"last, no n", true},
	}
	for i, w := range want {
		line, isLong, err := readLineCapped(r, 10)
		if string(line) != w.line || isLong != w.long {
			t.Errorf("line %d: got (%q, long %v), want (%q, long %v)", i, line, isLong, w.line, w.long)
		}
		if i < len(want)-1 && err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if i == len(want)-1 && err != io.EOF {
			t.Errorf("the unterminated last line ended with %v, want io.EOF", err)
		}
	}
}
