package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock; the Tick channel is never fed, so the
// test drives every redraw through Line.Tick and nothing races it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newCfg(live bool) (Config, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return Config{Live: live, Now: clk.now, Tick: make(chan time.Time)}, clk
}

// A step that ends inside the grace period prints NOTHING in either rendering —
// the property that keeps a warm launch's output exactly what it was.
func TestAQuickStepPrintsNothing(t *testing.T) {
	for _, live := range []bool{true, false} {
		cfg, clk := newCfg(live)
		var buf bytes.Buffer
		l := cfg.Start(&buf, "Building the jail image")
		l.Set("Building foo")
		clk.advance(DefaultGrace / 2)
		l.Tick()
		l.Done("")
		if buf.Len() != 0 {
			t.Errorf("live=%v: a step inside the grace period printed %q", live, buf.String())
		}
	}
}

// The line-oriented rendering: a start line once the grace period passes, a
// heartbeat per Heartbeat carrying the detail, and a result line — and never a
// carriage return, because this is what a log or a pipe receives.
func TestLineOrientedRenderingIsStartHeartbeatResult(t *testing.T) {
	cfg, clk := newCfg(false)
	var buf bytes.Buffer
	l := cfg.Start(&buf, "Copying the image into podman")
	clk.advance(DefaultGrace)
	l.Tick()
	l.Set("3 of 92 layers")
	clk.advance(DefaultHeartbeat / 2)
	l.Tick() // not yet a heartbeat
	clk.advance(DefaultHeartbeat / 2)
	l.Tick()
	l.Set("92 of 92 layers")
	clk.advance(time.Second)
	l.Done("")
	got := buf.String()
	want := "Copying the image into podman…\n" +
		"  Copying the image into podman… 3 of 92 layers (17s)\n" +
		"Copying the image into podman: done — 92 of 92 layers (18.0s)\n"
	if got != want {
		t.Errorf("line-oriented rendering:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("a line-oriented rendering wrote a carriage return: %q", got)
	}
}

// transientTee is launch.log's shape: the terminal gets everything, the log only
// what is not transient.
type transientTee struct{ term, log bytes.Buffer }

func (t *transientTee) Write(p []byte) (int, error) {
	t.term.Write(p)
	t.log.Write(p)
	return len(p), nil
}
func (t *transientTee) WriteTransient(p []byte) (int, error) { return t.term.Write(p) }

// The live rendering redraws in place on the terminal, keeps every redraw out of a
// tee's log half, and leaves one persistent result line in both.
func TestLiveRenderingRedrawsOnTheTerminalOnly(t *testing.T) {
	cfg, clk := newCfg(true)
	w := &transientTee{}
	l := cfg.Start(w, "Copying the image into podman")
	clk.advance(DefaultGrace)
	l.Tick()
	l.Set("40 of 92 layers, 1.2 of 3.2 GB")
	clk.advance(time.Second)
	l.Tick()
	l.Println("Building skopeo") // a persistent line mid-step
	clk.advance(time.Second)
	l.Done("")

	term := w.term.String()
	for _, want := range []string{
		"\r\x1b[KCopying the image into podman… (2s)",
		"\r\x1b[KCopying the image into podman… 40 of 92 layers, 1.2 of 3.2 GB (2s)",
		"(3s)",
		"\r\x1b[KBuilding skopeo\n",
		"Copying the image into podman: done — 40 of 92 layers, 1.2 of 3.2 GB (4.0s)\n",
	} {
		if !strings.Contains(term, want) {
			t.Errorf("terminal missing %q in %q", want, term)
		}
	}
	log := w.log.String()
	wantLog := "Building skopeo\nCopying the image into podman: done — 40 of 92 layers, 1.2 of 3.2 GB (4.0s)\n"
	if log != wantLog {
		t.Errorf("log half:\n got %q\nwant %q", log, wantLog)
	}
}

// Immediate shows the step at Start, for a wait known to be slow before it begins.
func TestImmediateShowsAtStart(t *testing.T) {
	cfg, _ := newCfg(false)
	cfg.Immediate = true
	var buf bytes.Buffer
	l := cfg.Start(&buf, "Waiting for another launch")
	if got := buf.String(); got != "Waiting for another launch…\n" {
		t.Errorf("Immediate start line = %q", got)
	}
	l.Done("")
}

// A live line never wraps: a wrapped line cannot be redrawn in place.
func TestLiveLineIsTruncatedToTheTerminal(t *testing.T) {
	cfg, clk := newCfg(true)
	cfg.Width = func() int { return 20 }
	var buf bytes.Buffer
	l := cfg.Start(&buf, "Copying the image into podman")
	clk.advance(DefaultGrace)
	l.Tick()
	l.Done("")
	frame := strings.SplitN(strings.TrimPrefix(buf.String(), "\r\x1b[K"), "\r", 2)[0]
	if n := len([]rune(frame)); n > 19 {
		t.Errorf("live frame is %d runes on a 20-column terminal: %q", n, frame)
	}
}

// The real ticker drives a step with no test seam: a step that outlives its grace
// period shows itself without anyone calling Tick.
func TestTheRealTickerShowsASlowStep(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) })
	l := Config{Grace: time.Millisecond}.Start(w, "Slow step")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := buf.String()
		mu.Unlock()
		if strings.Contains(s, "Slow step…") {
			l.Done("")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	l.Done("")
	t.Fatalf("the ticker never showed the step: %q", buf.String())
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestNilLineIsANoOp(t *testing.T) {
	var l *Line
	l.Set("x")
	l.Println("x")
	l.Tick()
	l.Done("")
	if (Config{}).Start(nil, "x") != nil {
		t.Error("Start on a nil writer should return a nil line")
	}
}

func TestByteFormatting(t *testing.T) {
	for _, c := range []struct {
		done, total int64
		want        string
	}{
		{1288490188, 3435973836, "1.2 of 3.2 GB"},
		{300 << 20, 3435973836, "300 MB of 3.2 GB"},
		{10 << 20, 40 << 20, "10 of 40 MB"},
	} {
		if got := Of(c.done, c.total); got != c.want {
			t.Errorf("Of(%d, %d) = %q, want %q", c.done, c.total, got, c.want)
		}
	}
	if got := formatElapsed(147*time.Second, true); got != "2m27s" {
		t.Errorf("formatElapsed(147s) = %q", got)
	}
	if got := Elapsed(12800 * time.Millisecond); got != "12.8s" {
		t.Errorf("Elapsed(12.8s) = %q, want a result line's spelling", got)
	}
}

// Write is how a subprocess summarizer prints through a live line: each complete
// line lands persistently above it, a partial one waits for its newline.
func TestWriteKeepsTheLiveLineWhole(t *testing.T) {
	cfg, clk := newCfg(true)
	w := &transientTee{}
	l := cfg.Start(w, "Building the jail image with nix")
	clk.advance(DefaultGrace)
	l.Tick()
	_, _ = l.Write([]byte("Building foo\nBuil"))
	_, _ = l.Write([]byte("ding bar\n"))
	_, _ = l.Write([]byte("tail"))
	l.Done("")
	if got, want := w.log.String(), "Building foo\nBuilding bar\ntail\n"+
		"Building the jail image with nix: done (2.0s)\n"; got != want {
		t.Errorf("log half:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(w.term.String(), "\r\x1b[KBuilding foo\n") {
		t.Errorf("a summary line did not erase the live line first: %q", w.term.String())
	}
}
