package progress

import (
	"io"
	"sync"
)

// guard.go lets a writer that is NOT the line share a terminal with a live line. A Line keeps
// its status row on the terminal with "\r" and erase-line, so anything else written to the same
// terminal lands after it on the same row ("Checking the programs packs declare… (6m00s)yolo
// adds ..."): the slow-span notices and a capture jail's own output, which write straight to
// stderr, did exactly that. Guard is the way through: the guarded write clears every live row
// first, writes, and the rows are redrawn below what was written.
//
// A line-oriented rendering (no terminal) registers nothing, so Guard is then the writer itself,
// byte for byte.

// live is the lines whose status row is on a terminal now, in the order they were shown.
var live struct {
	mu    sync.Mutex
	lines []*Line
}

func registerLive(l *Line) {
	live.mu.Lock()
	live.lines = append(live.lines, l)
	live.mu.Unlock()
}

func unregisterLive(l *Line) {
	live.mu.Lock()
	defer live.mu.Unlock()
	for i, x := range live.lines {
		if x == l {
			live.lines = append(live.lines[:i:i], live.lines[i+1:]...)
			return
		}
	}
}

// Guard returns a writer to w, a terminal a live Line may be drawing on. Each Write clears the
// live rows, writes p, and redraws them when p ended a line; after a partial line the rows stay
// clear until a later write ends it, so the spinner never overdraws text a program has half
// written. Output is never held back or reordered.
func Guard(w io.Writer) io.Writer { return guarded{w} }

type guarded struct{ w io.Writer }

func (g guarded) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	around(p[len(p)-1] == '\n', func() { n, err = g.w.Write(p) })
	return n, err
}

// Around runs write, which prints to a terminal a live Line may be drawing on, with the live
// rows cleared, and redraws them after. write must print whole lines and must not call into a Line.
func Around(write func()) { around(true, write) }

// around is Around for output that ended a line (endsLine) or left one open.
func around(endsLine bool, write func()) {
	live.mu.Lock()
	lines := append([]*Line(nil), live.lines...)
	live.mu.Unlock()
	if len(lines) == 0 {
		write()
		return
	}
	for _, l := range lines {
		l.mu.Lock()
		if !l.finished {
			l.erase()
		}
	}
	write()
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		l.midline = !endsLine
		if endsLine && !l.finished && l.shown && l.cfg.Live {
			l.draw()
		}
		l.mu.Unlock()
	}
}
