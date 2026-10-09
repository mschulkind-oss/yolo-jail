package macosuser

import (
	"testing"
)

// A native reader's samples render into a table parsePSTable accepts, so parsePSTable stays
// the one judge on every reader: KiB rounded UP (a resident process never reads as 0), and an
// unsized process as "-", which reads as 0 and is never the session's.
func TestRenderProcTableIsWhatParsePSTableReads(t *testing.T) {
	out := renderProcTable([]procSample{
		{pid: 1, ppid: 0, sized: false},
		{pid: 100, ppid: 1, rssBytes: 4096, sized: true},
		{pid: 101, ppid: 100, rssBytes: 1, sized: true},
		{pid: 102, ppid: 100, rssBytes: 0, sized: true},
	})
	if want := "1 0 -\n100 1 4\n101 100 1\n102 100 0\n"; out != want {
		t.Fatalf("renderProcTable = %q, want %q", out, want)
	}
	rows, err := parsePSTable(out)
	if err != nil {
		t.Fatalf("parsePSTable refused a rendered table: %v\n%s", err, out)
	}
	want := []psRow{{1, 0, 0}, {100, 1, 4}, {101, 100, 1}, {102, 100, 0}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}
