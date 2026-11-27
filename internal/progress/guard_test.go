package progress

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// screen is a minimal terminal: "\r" returns to column 0, "\x1b[K" and "\x1b[2K" clear the row,
// "\n" commits it. It records every row the way a person reads the terminal: what stays on screen.
type screen struct {
	rows []string
	cur  []byte
	col  int
}

func (s *screen) feed(out string) {
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == '\n':
			s.rows = append(s.rows, string(s.cur))
			s.cur, s.col = nil, 0
		case out[i] == '\r':
			s.col = 0
		case strings.HasPrefix(out[i:], "\x1b[2K"), strings.HasPrefix(out[i:], "\x1b[K"):
			s.cur = s.cur[:s.col]
			if strings.HasPrefix(out[i:], "\x1b[2K") {
				s.cur = nil
				i += 3
			} else {
				i += 2
			}
		default:
			if s.col < len(s.cur) {
				s.cur[s.col] = out[i]
			} else {
				s.cur = append(s.cur, out[i])
			}
			s.col++
		}
	}
}

// all is every row committed so far plus the one being written.
func (s *screen) all() []string { return append(append([]string{}, s.rows...), string(s.cur)) }

// shownLine is a live line past its grace period on a recording terminal.
func shownLine(t *testing.T, label string) (*Line, *bytes.Buffer, *fakeClock) {
	t.Helper()
	cfg, clk := newCfg(true)
	var term bytes.Buffer
	l := cfg.Start(&term, label)
	t.Cleanup(l.Abandon)
	clk.advance(DefaultGrace)
	l.Tick()
	return l, &term, clk
}

// OTHER WRITERS TO THE TERMINAL MUST NOT GLUE TO THE LIVE LINE: a slow-span notice or a capture
// jail's output written straight to the terminal, while a step's line is on it, used to land after
// the spinner on the same row ("Checking the programs packs declare… (6m00s)yolo adds ..."). A
// writer taken from Guard clears the line first and the line is redrawn below what was written.
// Red with Guard writing straight through.
func TestAGuardedWriterNeverLandsOnTheLiveLine(t *testing.T) {
	l, term, clk := shownLine(t, "Checking the programs packs declare")
	var sc screen
	g := Guard(term)
	if _, err := g.Write([]byte("yolo: host.floor.pi.prepare took 5.741s\n")); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Second)
	l.Tick()
	if _, err := g.Write([]byte("yolo adds pack-declared programs\nsecond line\n")); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Second)
	l.Tick()
	sc.feed(term.String())
	for _, row := range sc.all() {
		if strings.Contains(row, "Checking the programs packs declare") && strings.Contains(row, "yolo") {
			t.Errorf("a row carries the spinner and another writer's text: %q\nall rows: %q", row, sc.all())
		}
	}
	rows := sc.all()
	if rows[0] != "yolo: host.floor.pi.prepare took 5.741s" || rows[1] != "yolo adds pack-declared programs" ||
		rows[2] != "second line" {
		t.Errorf("the guarded writes did not arrive whole and in order: %q", rows)
	}
	if last := rows[len(rows)-1]; !strings.HasPrefix(last, "Checking the programs packs declare…") {
		t.Errorf("the live line was not redrawn below the output: %q", rows)
	}
}

// The same shape written WITHOUT Guard glues: the check above is only meaningful if the screen
// model shows the collision this fix removes.
func TestAnUnguardedWriteGluesToTheLiveLine(t *testing.T) {
	_, term, _ := shownLine(t, "Checking the programs packs declare")
	term.WriteString("yolo: host.floor.pi.prepare took 5.741s\n")
	var sc screen
	sc.feed(term.String())
	if row := sc.all()[0]; !strings.Contains(row, "Checking") || !strings.Contains(row, "yolo:") {
		t.Fatalf("the screen model does not show the collision: %q", sc.all())
	}
}

// A partial line from a child (a prompt, a progress bar) is not overdrawn by the spinner until
// its newline arrives.
func TestAGuardedPartialLineIsNotOverdrawnUntilItEnds(t *testing.T) {
	l, term, clk := shownLine(t, "Checking the programs packs declare")
	g := Guard(term)
	_, _ = g.Write([]byte("downloading 40%"))
	clk.advance(time.Second)
	l.Tick()
	var sc screen
	sc.feed(term.String())
	if row := sc.all()[len(sc.all())-1]; row != "downloading 40%" {
		t.Errorf("the spinner overdrew a partial line: %q", sc.all())
	}
	_, _ = g.Write([]byte(" done\n"))
	clk.advance(time.Second)
	l.Tick()
	sc = screen{}
	sc.feed(term.String())
	rows := sc.all()
	if rows[0] != "downloading 40% done" || !strings.HasPrefix(rows[len(rows)-1], "Checking") {
		t.Errorf("after the newline: %q", rows)
	}
}

// With no live line (plain, no-color or a pipe) a guarded writer is the writer itself, byte for byte.
func TestAGuardedWriterWithNoLiveLineWritesThrough(t *testing.T) {
	cfg, clk := newCfg(false)
	var buf bytes.Buffer
	l := cfg.Start(&buf, "Checking")
	clk.advance(DefaultGrace)
	l.Tick()
	buf.Reset()
	g := Guard(&buf)
	_, _ = g.Write([]byte("a\nb"))
	if got := buf.String(); got != "a\nb" {
		t.Errorf("plain output changed: %q", got)
	}
	l.Abandon()
}

// A line that has finished no longer owns the terminal: nothing is cleared or redrawn for it.
func TestAGuardedWriterIgnoresAFinishedLine(t *testing.T) {
	l, term, _ := shownLine(t, "Checking")
	l.Done("")
	term.Reset()
	_, _ = Guard(term).Write([]byte("x\n"))
	if got := term.String(); got != "x\n" {
		t.Errorf("a finished line still touched the terminal: %q", got)
	}
}
