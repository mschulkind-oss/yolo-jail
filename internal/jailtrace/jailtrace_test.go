package jailtrace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodParent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

func TestTraceIDAcceptsOnlyAWellFormedTraceparent(t *testing.T) {
	if id, ok := TraceID(goodParent); !ok || id != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("TraceID(good) = %q, %v", id, ok)
	}
	for _, bad := range []string{
		"",
		"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331",       // no flags
		"01-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",    // version
		"00-0AF7651916CD43DD8448EB211C80319C-b7ad6b7169203331-01",    // uppercase
		"00-0af7651916cd43dd8448eb211c80319g-b7ad6b7169203331-01",    // non-hex
		"00-00000000000000000000000000000000-b7ad6b7169203331-01",    // zero trace
		"00-0af7651916cd43dd8448eb211c80319c-0000000000000000-01",    // zero span
		"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01-xx", // trailing
		"00-0af7651916cd43dd8448eb211c80319c_b7ad6b7169203331-01",    // separator
		"00-../../../etc/passwd/aaaaaaaaaaaaaaaaaa-b7ad6b7169203331-01",
	} {
		if id, ok := TraceID(bad); ok {
			t.Errorf("TraceID(%q) = %q, want no context", bad, id)
		}
	}
}

type line struct {
	T    int64  `json:"t"`
	K    string `json:"k"`
	Name string `json:"name"`
	PID  int    `json:"pid"`
	Res  string `json:"res"`
}

func parseLines(t *testing.T, b []byte) []line {
	t.Helper()
	var out []line
	for _, s := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if s == "" {
			continue
		}
		var l line
		if err := json.Unmarshal([]byte(s), &l); err != nil {
			t.Fatalf("line %q is not JSON: %v", s, err)
		}
		out = append(out, l)
	}
	return out
}

func TestWriterWritesTheContractLine(t *testing.T) {
	var buf bytes.Buffer
	at := time.Unix(1795111105, 285000000)
	w := NewWriter(&buf, 42, func() time.Time { return at })
	w.Start("boot.configure_timezone")
	w.End("boot.configure_timezone")
	w.Mark(`session.exec"; rm -rf / <b>` + strings.Repeat("x", 100))
	got := parseLines(t, buf.Bytes())
	if len(got) != 3 {
		t.Fatalf("got %d lines: %s", len(got), buf.String())
	}
	if first := strings.SplitN(buf.String(), "\n", 2)[0]; first != `{"t":1795111105285000000,"k":"start","name":"boot.configure_timezone","pid":42}` {
		t.Errorf("first line = %s", first)
	}
	if got[1].K != "end" || got[2].K != "mark" {
		t.Errorf("kinds = %+v", got)
	}
	if n := got[2].Name; len(n) != maxNameBytes || strings.ContainsAny(n, `"; <>/`) {
		t.Errorf("name not sanitized and bounded: %q", n)
	}
}

func TestNilWriterWritesNothing(t *testing.T) {
	var w *Writer
	w.Start("a")
	w.End("a")
	w.Mark("a")
	w.Close()
}

func TestTheEventAfterTheCapIsOneTruncatedMark(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, 1, time.Now)
	for i := 0; i < MaxEvents+5; i++ {
		w.Mark("m")
	}
	got := parseLines(t, buf.Bytes())
	if len(got) != MaxEvents+1 {
		t.Fatalf("wrote %d lines, want %d", len(got), MaxEvents+1)
	}
	if last := got[len(got)-1]; last.K != "mark" || last.Name != "truncated" {
		t.Errorf("last line = %+v, want the truncated mark", last)
	}
}

func TestOpenCreatesTheFileNamedForTheTrace(t *testing.T) {
	home := t.TempDir()
	w := Open(home, goodParent)
	if w == nil {
		t.Fatal("Open returned no writer for a valid context")
	}
	w.Mark("x")
	w.Close()
	w.Mark("after close")
	b, err := os.ReadFile(filepath.Join(home, ".yolo-trace-0af7651916cd43dd8448eb211c80319c.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseLines(t, b)
	if len(got) != 1 || got[0].PID != os.Getpid() || got[0].Name != "x" {
		t.Errorf("file holds %+v", got)
	}
}

func TestOpenWithoutAContextOrThroughALinkWritesNoFile(t *testing.T) {
	home := t.TempDir()
	if Open(home, "") != nil || Open(home, "garbage") != nil || Open("", goodParent) != nil {
		t.Error("Open returned a writer with no usable context or home")
	}
	if Open(filepath.Join(home, "missing"), goodParent) != nil {
		t.Error("Open returned a writer for a home that does not exist")
	}
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, FileName("0af7651916cd43dd8448eb211c80319c"))); err != nil {
		t.Fatal(err)
	}
	if w := Open(home, goodParent); w != nil {
		w.Mark("through the link")
		t.Error("Open followed a link at the trace path")
	}
	if b, _ := os.ReadFile(target); len(b) != 0 {
		t.Errorf("the link's target was written: %q", b)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 2 {
		t.Errorf("home holds %d entries, want the target and the link only", len(entries))
	}
}

type failingWriter struct{ calls int }

func (f *failingWriter) Write([]byte) (int, error) { f.calls++; return 0, os.ErrClosed }

func TestAFailedWriteIsNotRetried(t *testing.T) {
	f := &failingWriter{}
	w := NewWriter(f, 1, time.Now)
	for i := 0; i < 10; i++ {
		w.Mark("m")
	}
	if f.calls != 1 {
		t.Errorf("a failing sink was written %d times, want once", f.calls)
	}
}

// BenchmarkWriterEvent is the cost-per-event half of the boot's 5 ms budget (§8, TR-D13): the
// ceiling is 400 jail lines, so a per-event cost under ~12 µs keeps the boot inside it. A
// benchmark, never a test's pass condition.
func BenchmarkWriterEvent(b *testing.B) {
	w := Open(b.TempDir(), goodParent)
	if w == nil {
		b.Fatal("no writer")
	}
	defer w.Close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i%MaxEvents == 0 {
			w.n = 0
		}
		w.Mark("boot.configure_pack_surfaces")
	}
}
