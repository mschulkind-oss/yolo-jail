// Package progress renders the live status of one long launch step: a line that
// says what the launch is waiting on and how long it has waited, while it waits.
//
// # Why a package
//
// A launch runs a handful of steps that are usually instant and occasionally take
// minutes — the image copy after `podman image prune`, a cold nix build, a cold
// copier compile, an Apple Container archive load — and each of them is a
// subprocess whose own output yolo discards or summarizes. Printing a line BEFORE
// such a step says what started; it does not say the launch is still alive a
// minute later. The image copy's "Streaming image... N%" line did that until the
// C9 copy replaced the stream (3b1ebdd7) and nothing replaced the line. One
// renderer, used by every long step, is what keeps the next such replacement from
// deleting the only progress a step had.
//
// # The two renderings
//
//   - LIVE (a terminal): one line redrawn in place ("\r" + erase-line) every
//     second, carrying the step's latest detail and its elapsed time. The redraw is
//     TRANSIENT: a writer that implements TransientWriter (the run pipeline's
//     launch.log tee) receives it on the terminal half only, so the log never holds
//     carriage-return spam. When the step ends the line is erased and replaced by
//     one persistent result line.
//   - LINE-ORIENTED (a pipe, a file, CI): a start line, a heartbeat line at most
//     every Heartbeat while the step runs, and the result line. Never a "\r".
//
// Both renderings stay SILENT for a step that ends within Grace, so a launch whose
// steps are warm prints exactly what it printed before this package existed.
//
// # What this is not
//
// It is not a quiet mode or a density control (docs/reference/report-tiers.md,
// OQ-RO3): nothing here hides a line some other code prints, and a step that is
// shown always closes with its result. It never reads the environment; the caller
// decides Live, from the stream it writes to.
package progress

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// DefaultGrace is how long a step runs before any progress is shown. Two seconds
// because the warm steps a launch runs every time sit just under it (the warm image
// nix build measured 1.6–1.7 s on the maintainer's host, 2026-09-28), and a line
// that appears and vanishes on every launch is noise rather than progress.
const DefaultGrace = 2 * time.Second

// DefaultHeartbeat is the line-oriented rendering's cadence: often enough that a
// reader tailing a log can tell a slow step from a hung one, rarely enough that a
// four-minute copy is a dozen lines rather than hundreds.
const DefaultHeartbeat = 15 * time.Second

// redrawEvery is the live rendering's tick, which is also the resolution of its
// elapsed-time field.
const redrawEvery = time.Second

// TransientWriter is implemented by a writer that can take output meant for a
// terminal only. The live rendering's redraws go through it when the writer offers
// it, so a tee to a log file sees the persistent lines and none of the redraws.
type TransientWriter interface {
	WriteTransient(p []byte) (int, error)
}

// Config is how a caller asks for a rendering. The zero value is the
// line-oriented rendering with the default timings.
type Config struct {
	// Live selects the in-place rendering. Set it only when the writer is a
	// terminal.
	Live bool
	// Width reports the terminal's width, so a live line never wraps (a wrapped
	// line cannot be redrawn in place). nil or <= 0 => 100 columns.
	Width func() int
	// Grace overrides DefaultGrace (0 => DefaultGrace).
	Grace time.Duration
	// Immediate shows the step at Start instead of after the grace period, for a
	// step that is known to be slow before it begins (a wait on another launch).
	Immediate bool
	// Heartbeat overrides DefaultHeartbeat (0 => DefaultHeartbeat).
	Heartbeat time.Duration
	// Now and Tick are test seams. nil => time.Now and a real one-second ticker.
	Now  func() time.Time
	Tick <-chan time.Time
}

// Line is one step's progress. A nil *Line is valid and every method on it is a
// no-op, so a caller with nothing to render can hold one unconditionally.
type Line struct {
	mu        sync.Mutex
	w         io.Writer
	cfg       Config
	label     string
	detail    string
	start     time.Time
	lastBeat  time.Time
	shown     bool
	drawn     bool // a transient line is on the terminal right now
	finished  bool
	stop      chan struct{}
	stopped   chan struct{}
	lastFrame string
	pending   []byte // a partial line written through Write
}

// Start begins a step labelled label ("Copying the image into podman"), written to
// w. The label is a present-participle phrase with no trailing punctuation.
func (c Config) Start(w io.Writer, label string) *Line {
	if w == nil {
		return nil
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Grace <= 0 {
		c.Grace = DefaultGrace
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = DefaultHeartbeat
	}
	l := &Line{w: w, cfg: c, label: label, start: c.Now(),
		stop: make(chan struct{}), stopped: make(chan struct{})}
	tick := c.Tick
	var ticker *time.Ticker
	if tick == nil {
		ticker = time.NewTicker(redrawEvery)
		tick = ticker.C
	}
	if c.Immediate {
		l.mu.Lock()
		l.show()
		l.mu.Unlock()
	}
	go func() {
		defer close(l.stopped)
		if ticker != nil {
			defer ticker.Stop()
		}
		for {
			select {
			case <-l.stop:
				return
			case <-tick:
				l.Tick()
			}
		}
	}()
	return l
}

// Set replaces the step's detail ("37 of 92 layers, 1.2 of 3.2 GB"). A live line
// redraws at once; the line-oriented rendering carries it on the next heartbeat and
// on the result line.
func (l *Line) Set(detail string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return
	}
	l.detail = detail
	if l.shown && l.cfg.Live {
		l.draw()
	}
}

// Println writes one persistent line while the step runs — a subprocess's own
// summary, say — without tearing the live line: it is erased, the line is written,
// and it is redrawn below.
func (l *Line) Println(s string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.erase()
	fmt.Fprintln(l.w, s)
	if l.shown && l.cfg.Live && !l.finished {
		l.draw()
	}
}

// Write makes a Line an io.Writer for a subprocess summarizer that prints its own
// persistent lines while the step runs (nix's "Building …" digest): each complete
// line is written as Println writes it, so the live line is never torn. A partial
// line is held until its newline, or until Done.
func (l *Line) Write(p []byte) (int, error) {
	if l == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = append(l.pending, p...)
	wrote := false
	for {
		i := bytes.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		if !wrote {
			l.erase()
			wrote = true
		}
		_, _ = l.w.Write(l.pending[:i+1])
		l.pending = l.pending[i+1:]
	}
	if wrote && l.shown && l.cfg.Live && !l.finished {
		l.draw()
	}
	return len(p), nil
}

// Tick advances the rendering: it shows the step once the grace period has passed,
// redraws a live line, and writes a heartbeat. The goroutine Start launches calls
// it once a second; it is exported so a test can drive the clock.
func (l *Line) Tick() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return
	}
	now := l.cfg.Now()
	if !l.shown {
		if now.Sub(l.start) < l.cfg.Grace {
			return
		}
		l.show()
		return
	}
	if l.cfg.Live {
		l.draw()
		return
	}
	if now.Sub(l.lastBeat) >= l.cfg.Heartbeat {
		l.lastBeat = now
		fmt.Fprintln(l.w, "  "+l.frame(now))
	}
}

// Done ends the step. A step that was never shown prints nothing; one that was
// shown ends with a persistent line naming its result and how long it took:
// "Copying the image into podman: done — 92 of 92 layers, 3.2 GB (12.8s)".
// result "" means "done".
func (l *Line) Done(result string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.finished {
		l.mu.Unlock()
		return
	}
	l.finished = true
	close(l.stop)
	if len(l.pending) > 0 {
		l.erase()
		_, _ = l.w.Write(append(l.pending, '\n'))
		l.pending = nil
	}
	if l.shown {
		l.erase()
		if result == "" {
			result = "done"
		}
		msg := l.label + ": " + result
		if l.detail != "" {
			msg += " — " + l.detail
		}
		msg += " (" + formatElapsed(l.cfg.Now().Sub(l.start), true) + ")"
		fmt.Fprintln(l.w, msg)
	}
	l.mu.Unlock()
	<-l.stopped
}

// show makes the step visible: the live line's first frame, or the line-oriented
// start line. Callers hold mu.
func (l *Line) show() {
	l.shown = true
	now := l.cfg.Now()
	l.lastBeat = now
	if l.cfg.Live {
		l.draw()
		return
	}
	start := l.label + "…"
	if l.detail != "" {
		start += " " + l.detail
	}
	fmt.Fprintln(l.w, start)
}

// frame is the text of the step's current state.
func (l *Line) frame(now time.Time) string {
	s := l.label + "…"
	if l.detail != "" {
		s += " " + l.detail
	}
	return s + " (" + formatElapsed(now.Sub(l.start), false) + ")"
}

// draw redraws the live line in place. Callers hold mu.
func (l *Line) draw() {
	f := truncate(l.frame(l.cfg.Now()), l.width()-1)
	if l.drawn && f == l.lastFrame {
		return
	}
	l.lastFrame = f
	l.drawn = true
	l.transient("\r\x1b[K" + f)
}

// erase clears a live line, if one is on the terminal. Callers hold mu.
func (l *Line) erase() {
	if !l.drawn {
		return
	}
	l.drawn = false
	l.lastFrame = ""
	l.transient("\r\x1b[K")
}

func (l *Line) transient(s string) {
	if tw, ok := l.w.(TransientWriter); ok {
		_, _ = tw.WriteTransient([]byte(s))
		return
	}
	_, _ = io.WriteString(l.w, s)
}

func (l *Line) width() int {
	if l.cfg.Width != nil {
		if w := l.cfg.Width(); w > 0 {
			return w
		}
	}
	return 100
}

// truncate cuts s to n runes, marking the cut.
func truncate(s string, n int) string {
	if n <= 1 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Elapsed renders a duration as a result line's: "12.8s", or "2m27s" past a minute. For
// a caller that writes a step's result line itself, as a pool of steps under one
// line does for each of them, so its lines read like this package's own.
func Elapsed(d time.Duration) string { return formatElapsed(d, true) }

// formatElapsed renders a duration for a person: "12s" while running, "12.8s" in a
// result, and minutes past one.
func formatElapsed(d time.Duration, precise bool) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		if precise {
			return fmt.Sprintf("%.1fs", d.Seconds())
		}
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	d = d.Round(time.Second)
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", m, s)
}

// Bytes formats a byte count the way the image report does: MB below a GB, else
// GB with one decimal.
func Bytes(n int64) string {
	mb := float64(n) / (1024 * 1024)
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// Of renders "done of total" byte progress: "1.2 of 3.2 GB", or "300 MB of 3.2 GB"
// when the units differ.
func Of(done, total int64) string {
	d, t := Bytes(done), Bytes(total)
	du, tu := unit(d), unit(t)
	if du == tu {
		return strings.TrimSuffix(d, " "+du) + " of " + t
	}
	return d + " of " + t
}

func unit(s string) string {
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		return s[i+1:]
	}
	return ""
}
