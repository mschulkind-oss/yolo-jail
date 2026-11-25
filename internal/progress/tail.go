package progress

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// # The tail
//
// A step that runs a subprocess can show the LATEST few lines that subprocess
// prints, under its status line, redrawn in place (the way uv shows a build):
//
//	Waiting for this launch's builds… building a, b; 0 of 2 done (1m25s)
//	  [a] Compiling foo v0.1.0
//	  [b] npm http fetch GET 200 https://registry.npmjs.org/bar
//
// A step opens one Tail per subprocess with Line.Tail and writes the
// subprocess's output to it. Every Tail of a Line shares the Line's ONE region,
// which is why several builds that run at once cannot tear each other: there is
// a single renderer, the Line, and a frame is the status row plus every open
// Tail's rows, written in one piece.
//
// The tail is a LIVE-rendering feature only. The line-oriented rendering (a
// pipe, a log, CI) prints no tail, because the log already holds the whole
// output; a Tail there discards what it is given. Rows are never wrapped
// (a wrapped row breaks the cursor-up arithmetic), and a Done or Abandon erases
// every row, so nothing of the tail outlives the step and none of it ever
// reaches the non-transient half of a tee.

// DefaultTailLines is how many of a subprocess's latest lines a Line shows when
// Config.TailLines is 0. A negative TailLines turns the tail off.
const DefaultTailLines = 3

// maxTailRows bounds the streams whose rows one Line shows together, so the
// region stays shorter than any terminal worth running a launch in.
const maxTailRows = 8

// tailRedrawEvery is the least time between the redraws that new tail lines
// cause, so a build that prints thousands of lines a second costs a few
// redraws; the once-a-second tick catches up on the rest.
const tailRedrawEvery = 100 * time.Millisecond

// tailANSI matches the escape sequences a subprocess's output may carry: CSI
// sequences (colors, cursor movement, erase) and OSC ones (titles, hyperlinks).
var tailANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// Tail is one subprocess's output, as an io.Writer. A nil *Tail is valid and
// discards everything.
type Tail struct {
	l      *Line
	name   string
	cur    []byte   // the partial line
	cr     bool     // a carriage return came last: more text starts the line again
	lines  []string // the latest complete lines, at most the Line's tail size
	closed bool
}

// Tail opens a stream named name ("the build's label") in the line's region. The
// name prefixes the stream's rows when more than one stream has output; "" names
// none. Close it when the subprocess ends. On a line that is not live, or whose
// tail is off, the stream discards what it is written.
func (l *Line) Tail(name string) *Tail {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t := &Tail{l: l, name: name}
	if !l.cfg.Live || l.cfg.TailLines < 0 || l.finished {
		t.closed = true
		return t
	}
	l.tails = append(l.tails, t)
	return t
}

// Write takes subprocess output: it is split into lines, a carriage return that
// is followed by more text starts the line again (a redrawn progress bar keeps
// only its last frame) and one that precedes the newline just ends it, escape
// sequences are dropped, and blank lines are skipped.
func (t *Tail) Write(p []byte) (int, error) {
	if t == nil {
		return len(p), nil
	}
	l := t.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.closed {
		return len(p), nil
	}
	changed := false
	for _, c := range p {
		switch c {
		case '\n':
			changed = t.endLine() || changed
		case '\r':
			t.cr = true
		default:
			if t.cr {
				t.cur, t.cr = t.cur[:0], false
			}
			t.cur = append(t.cur, c)
		}
	}
	if changed {
		l.redrawTail()
	}
	return len(p), nil
}

// Close ends the stream: its rows leave the region at once (a partial line, which
// never ended, is dropped).
func (t *Tail) Close() error {
	if t == nil {
		return nil
	}
	l := t.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	for i, o := range l.tails {
		if o == t {
			l.tails = append(l.tails[:i], l.tails[i+1:]...)
			break
		}
	}
	if !l.finished && l.shown && l.cfg.Live {
		l.draw()
	}
	return nil
}

// endLine files the partial line, and reports whether the frame changed. Callers
// hold the line's mu.
func (t *Tail) endLine() bool {
	text := cleanTailText(string(t.cur))
	t.cur, t.cr = t.cur[:0], false
	if text == "" {
		return false
	}
	keep := t.l.tailSize()
	t.lines = append(t.lines, text)
	if len(t.lines) > keep {
		t.lines = t.lines[len(t.lines)-keep:]
	}
	return true
}

// cleanTailText drops escape sequences and control characters (a tab becomes a
// space) and surrounding blanks.
func cleanTailText(s string) string {
	s = tailANSI.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func (l *Line) tailSize() int {
	if l.cfg.TailLines > 0 {
		return l.cfg.TailLines
	}
	return DefaultTailLines
}

// redrawTail redraws the region for new tail lines, at most every
// tailRedrawEvery. Callers hold mu.
func (l *Line) redrawTail() {
	if !l.shown || !l.cfg.Live || l.finished {
		return
	}
	now := l.cfg.Now()
	if !l.lastTailDraw.IsZero() && now.Sub(l.lastTailDraw) < tailRedrawEvery {
		return
	}
	l.lastTailDraw = now
	l.draw()
}

// tailRows is the tail's rows under the status row: with one stream speaking,
// its latest TailLines lines; with several, each gets its share (at least one
// line) and its name in front, in the order the streams were opened. Callers
// hold mu.
func (l *Line) tailRows() []string {
	var active []*Tail
	for _, t := range l.tails {
		if len(t.lines) > 0 {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if len(active) > maxTailRows {
		active = active[:maxTailRows]
	}
	per := l.tailSize()
	named := len(active) > 1
	if named {
		per = max(1, per/len(active))
	}
	w := l.width() - 1
	var rows []string
	for _, t := range active {
		ls := t.lines
		if len(ls) > per {
			ls = ls[len(ls)-per:]
		}
		for _, s := range ls {
			row := "  "
			if named && t.name != "" {
				row += "[" + t.name + "] "
			}
			row = truncate(row+s, w)
			if !l.cfg.Plain {
				row = "\x1b[2m" + row + "\x1b[0m"
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// moveUp is the escape that moves the cursor up n rows.
func moveUp(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dA", n)
}
