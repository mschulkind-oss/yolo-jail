package progress

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// vt is the smallest terminal that understands what the renderer writes: text, "\r", "\n",
// CSI nA (cursor up), CSI K and CSI 2K (erase line), and SGR (ignored). It lets a test assert
// what a person would SEE, which is the only thing the cursor arithmetic is for.
type vt struct {
	rows     [][]rune
	row, col int
}

func (v *vt) feed(s string) {
	if len(v.rows) == 0 {
		v.rows = [][]rune{nil}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case '\r':
			v.col = 0
		case '\n':
			v.row++
			for v.row >= len(v.rows) {
				v.rows = append(v.rows, nil)
			}
		case 0x1b:
			j := i + 2 // past "ESC ["
			for j < len(rs) && (rs[j] >= '0' && rs[j] <= '9' || rs[j] == ';') {
				j++
			}
			arg := string(rs[i+2 : j])
			switch rs[j] {
			case 'A':
				n := 1
				if arg != "" {
					fmt.Sscan(arg, &n)
				}
				v.row -= n
				if v.row < 0 {
					panic("cursor moved above the screen")
				}
			case 'K':
				if arg == "2" {
					v.rows[v.row] = nil
				} else if v.col < len(v.rows[v.row]) {
					v.rows[v.row] = v.rows[v.row][:v.col]
				}
			}
			i = j
		default:
			for len(v.rows[v.row]) < v.col {
				v.rows[v.row] = append(v.rows[v.row], ' ')
			}
			if v.col < len(v.rows[v.row]) {
				v.rows[v.row][v.col] = r
			} else {
				v.rows[v.row] = append(v.rows[v.row], r)
			}
			v.col++
		}
	}
}

// screen is the visible rows, trailing blank ones dropped.
func (v *vt) screen() []string {
	var out []string
	for _, r := range v.rows {
		out = append(out, strings.TrimRight(string(r), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// termTee is launch.log's shape with a terminal emulator on the terminal half.
type termTee struct {
	mu   sync.Mutex
	term vt
	raw  bytes.Buffer
	log  bytes.Buffer
}

func (t *termTee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.term.feed(string(p))
	t.raw.Write(p)
	t.log.Write(p)
	return len(p), nil
}
func (t *termTee) WriteTransient(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.term.feed(string(p))
	t.raw.Write(p)
	return len(p), nil
}
func (t *termTee) screen() []string { t.mu.Lock(); defer t.mu.Unlock(); return t.term.screen() }

func tailCfg() (Config, *fakeClock) {
	cfg, clk := newCfg(true)
	cfg.Plain = true
	cfg.Width = func() int { return 40 }
	return cfg, clk
}

func startShown(cfg Config, clk *fakeClock, w *termTee) *Line {
	l := cfg.Start(w, "Building")
	clk.advance(DefaultGrace)
	l.Tick()
	return l
}

func TestTailShowsOnlyTheLatestLines(t *testing.T) {
	cfg, clk := tailCfg()
	w := &termTee{}
	l := startShown(cfg, clk, w)
	tail := l.Tail("")
	fmt.Fprint(tail, "one\ntwo\nthree\nfour\nfive\n")
	l.Tick()
	want := []string{"Building… (2s)", "  three", "  four", "  five"}
	if got := w.screen(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("screen = %q, want %q", got, want)
	}
	l.Done("")
}

func TestTailLinesIsConfigurable(t *testing.T) {
	cfg, clk := tailCfg()
	cfg.TailLines = 1
	w := &termTee{}
	l := startShown(cfg, clk, w)
	fmt.Fprint(l.Tail(""), "one\ntwo\n")
	l.Tick()
	if got := w.screen(); len(got) != 2 || got[1] != "  two" {
		t.Errorf("screen = %q", got)
	}
	l.Done("")
}

func TestTailRowsAreTruncatedToTheTerminalAndNeverWrap(t *testing.T) {
	cfg, clk := tailCfg()
	cfg.Width = func() int { return 20 }
	w := &termTee{}
	l := startShown(cfg, clk, w)
	fmt.Fprint(l.Tail(""), strings.Repeat("x", 100)+"\n")
	l.Tick()
	for _, row := range w.screen() {
		if n := len([]rune(row)); n > 19 {
			t.Errorf("row of %d runes on a 20-column terminal: %q", n, row)
		}
	}
	if !strings.HasSuffix(w.screen()[1], "…") {
		t.Errorf("a cut row is not marked: %q", w.screen()[1])
	}
	l.Done("")
}

func TestTailStripsANSIAndHandlesCarriageReturns(t *testing.T) {
	cfg, clk := tailCfg()
	w := &termTee{}
	l := startShown(cfg, clk, w)
	tail := l.Tail("")
	// A progress bar redrawn with CR keeps its last frame; CRLF just ends the line; colors go;
	// a partial line without a newline is not shown yet.
	fmt.Fprint(tail, "\x1b[32mCompiling\x1b[0m foo\r\n")
	fmt.Fprint(tail, "10%\r50%\r100%\n")
	fmt.Fprint(tail, "\x1b]0;title\x07tab\there\n")
	fmt.Fprint(tail, "partial")
	l.Tick()
	want := []string{"Building… (2s)", "  Compiling foo", "  100%", "  tab here"}
	if got := w.screen(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("screen = %q, want %q", got, want)
	}
	if got := w.screen(); strings.Contains(strings.Join(got, "|"), "partial") {
		t.Errorf("an unfinished line was shown: %q", got)
	}
	l.Done("")
}

// Done erases exactly the rows the tail drew: the screen holds the result line and nothing
// else, and the log half never saw a tail row.
func TestDoneLeavesOnlyTheResultLine(t *testing.T) {
	cfg, clk := tailCfg()
	w := &termTee{}
	l := startShown(cfg, clk, w)
	fmt.Fprint(l.Tail("a"), "secret-one\nsecret-two\nsecret-three\n")
	l.Tick()
	clk.advance(time.Second)
	l.Done("")
	if got, want := w.screen(), []string{"Building: done (3.0s)"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("screen after Done = %q, want %q", got, want)
	}
	if got, want := w.log.String(), "Building: done (3.0s)\n"; got != want {
		t.Errorf("log half = %q, want only the result line %q", got, want)
	}
}

func TestAbandonLeavesNothing(t *testing.T) {
	cfg, clk := tailCfg()
	w := &termTee{}
	l := startShown(cfg, clk, w)
	fmt.Fprint(l.Tail(""), "a\nb\nc\n")
	l.Tick()
	l.Abandon()
	if got := w.screen(); len(got) != 0 {
		t.Errorf("screen after Abandon = %q", got)
	}
}

// A shrinking region erases the rows the taller frame left below it.
func TestAClosedStreamLeavesNoStaleRows(t *testing.T) {
	cfg, clk := tailCfg()
	w := &termTee{}
	l := startShown(cfg, clk, w)
	a, b := l.Tail("a"), l.Tail("b")
	fmt.Fprint(a, "a1\na2\n")
	fmt.Fprint(b, "b1\nb2\n")
	l.Tick()
	if got := w.screen(); len(got) != 3 {
		t.Fatalf("two streams, one line each: screen = %q", got)
	}
	a.Close()
	b.Close()
	if got := w.screen(); len(got) != 1 {
		t.Errorf("closed streams left rows behind: %q", got)
	}
	l.Done("")
}

// Println and Write print persistent lines above a region that has a tail, without tearing it.
func TestPrintlnWorksWithATailPresent(t *testing.T) {
	cfg, clk := tailCfg()
	w := &termTee{}
	l := startShown(cfg, clk, w)
	fmt.Fprint(l.Tail(""), "t1\nt2\n")
	l.Tick()
	l.Println("a lock-wait notice")
	_, _ = l.Write([]byte("summary line\n"))
	want := []string{"a lock-wait notice", "summary line", "Building… (2s)", "  t1", "  t2"}
	if got := w.screen(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("screen = %q, want %q", got, want)
	}
	l.Done("")
	if got, want := w.log.String(), "a lock-wait notice\nsummary line\nBuilding: done (2.0s)\n"; got != want {
		t.Errorf("log half = %q, want %q", got, want)
	}
}

// The line-oriented rendering prints no tail and is byte-for-byte what it was without one.
func TestLineOrientedRenderingPrintsNoTail(t *testing.T) {
	run := func(withTail bool) string {
		cfg, clk := newCfg(false)
		var buf bytes.Buffer
		l := cfg.Start(&buf, "Copying the image into podman")
		clk.advance(DefaultGrace)
		l.Tick()
		l.Set("3 of 92 layers")
		if withTail {
			tail := l.Tail("x")
			fmt.Fprint(tail, "noisy build output\nmore\n")
			tail.Close()
		}
		clk.advance(DefaultHeartbeat)
		l.Tick()
		clk.advance(time.Second)
		l.Done("")
		return buf.String()
	}
	if with, without := run(true), run(false); with != without {
		t.Errorf("a tail changed the line-oriented output:\n with %q\nwithout %q", with, without)
	}
	want := "Copying the image into podman…\n" +
		"  Copying the image into podman… 3 of 92 layers (17s)\n" +
		"Copying the image into podman: done — 3 of 92 layers (18.0s)\n"
	if got := run(true); got != want {
		t.Errorf("line-oriented rendering:\n got %q\nwant %q", got, want)
	}
}

// A live line with no tail writes exactly what it wrote before the tail existed.
func TestLiveRenderingWithoutATailIsUnchanged(t *testing.T) {
	cfg, clk := newCfg(true)
	w := &termTee{}
	l := cfg.Start(w, "Step")
	clk.advance(DefaultGrace)
	l.Tick()
	clk.advance(time.Second)
	l.Tick()
	l.Done("")
	want := "\r\x1b[KStep… (2s)" + "\r\x1b[KStep… (3s)" + "\r\x1b[K" + "Step: done (3.0s)\n"
	if got := w.raw.String(); got != want {
		t.Errorf("raw output:\n got %q\nwant %q", got, want)
	}
}

// Concurrent builds share ONE region: whatever the interleaving of their writes, every frame
// is the status row plus whole rows of the streams, each stream's rows are its own latest lines,
// and the screen at the end is the result line alone.
func TestConcurrentStreamsShareOneRegionWithoutTearing(t *testing.T) {
	cfg, clk := tailCfg()
	cfg.TailLines = 6
	w := &termTee{}
	l := startShown(cfg, clk, w)
	names := []string{"a", "b", "c"}
	var wg sync.WaitGroup
	for _, n := range names {
		tail := l.Tail(n)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= 50; i++ {
				fmt.Fprintf(tail, "%s line %d\n", n, i)
			}
		}()
	}
	wg.Wait()
	l.Tick()
	got := w.screen()
	// Three streams with six lines each: two rows apiece, the latest two, named.
	want := []string{"Building… (2s)",
		"  [a] a line 49", "  [a] a line 50",
		"  [b] b line 49", "  [b] b line 50",
		"  [c] c line 49", "  [c] c line 50"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("screen = %q, want %q", got, want)
	}
	l.Done("")
	if got := w.screen(); len(got) != 1 {
		t.Errorf("screen after Done = %q", got)
	}
}

func TestNilTailIsANoOp(t *testing.T) {
	var l *Line
	tail := l.Tail("x")
	if _, err := tail.Write([]byte("x\n")); err != nil {
		t.Error(err)
	}
	tail.Close()
}
