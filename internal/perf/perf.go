// Package perf is the host-side timing-span collector behind `--timing` and
// `--verbose`.
//
// # Why a package of its own
//
// The launcher had exactly one timing number — a `Total (host-side)` printed
// under `--timing` — and zero coverage of the window the number was asked
// about: quitting an agent left the host shell waiting 30+ seconds with no
// way to tell WHO was holding it (measured 2026-09-06, the motivating
// incident). The candidates span four subsystems (podman's own `--rm`
// cleanup, the tty proxy's exit drain, serial loophole teardown, the signal
// arm's `podman stop`), so the recorder has to be callable from all of them
// without any of them importing the others. A leaf package with no deps is
// that shared vocabulary.
//
// The entrypoint keeps its own marks (boot.go's perfLog, always on); this
// package is the HOST half and deliberately renders in the same
// elapsed/delta/label register so the two halves of one launch read as
// siblings — `<ws>/.yolo/host-perf.log` beside the jail's `yolo-perf.log`.
//
// # Non-negotiables
//
//   - Best-effort, ALWAYS. A timing logger that can fail a launch is a worse
//     bug than the blindness it fixes (bootlog.go states the principle for the
//     boot log; it holds here). No method returns an error; sinks swallow
//     theirs and go quiet.
//   - Nil-safe and free when off. Every entry point works on a nil *Log and on
//     the shared Disabled() log; the disabled path is one pointer check, so
//     call sites never need `if timing { ... }` guards around the spans
//     themselves.
//   - Events reach the sinks AS THEY HAPPEN, not at report time. Two reasons,
//     both shutdown-shaped: the tty proxy's signal arm tears down via
//     os.Exit(128+n) where defers do not run, so anything buffered for an
//     end-of-run dump is lost on exactly the path most worth seeing; and a
//     HANG is only diagnosable if the file already shows the last span that
//     STARTED and never ended — that dangling start line is the answer to
//     "who is doing it", which is the question this package exists to answer.
package perf

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Kind says what an Event is. Starts are recorded so a hang leaves a dangling
// line (see the package doc); the Report renders only ends and marks.
type Kind uint8

const (
	KindStart Kind = iota
	KindEnd
	KindMark
)

// Event is one timing observation, handed to every Sink the moment it happens.
type Event struct {
	Kind Kind
	Name string
	At   time.Time
	// Dur is the span's duration; set for KindEnd only.
	Dur time.Duration
}

// Sink receives events as they occur. It cannot fail: a sink that hits an
// error warns (or stays quiet) on its own and keeps being called — see
// FileSink for the warn-once-then-quiet contract.
type Sink func(Event)

// SlowSpanThreshold is how long a span may run before it is worth naming out
// loud. The run package wires a sink that prints one dim stderr line per span
// that crosses it — the culprit announced the moment it finishes, which is the
// answer to "who is doing it" without waiting for (or ever reaching) a report.
// A second, not a finer unit: the delays this exists to catch are seconds.
const SlowSpanThreshold = 1 * time.Second

// Log is a launch's timing collector. Construct one per process (New) or use
// the shared Disabled(); nil is equally valid. Safe for concurrent use —
// spans end on goroutines (the tty proxy's onTerminate runs on its signal
// goroutine), and the signal arm can be tearing down while the main goroutine
// is still reporting.
type Log struct {
	on     bool
	now    func() time.Time
	start  time.Time
	mu     sync.Mutex
	events []Event
	sinks  []Sink
}

// disabled is the shared off log: every method is a no-op on it.
var disabled = &Log{}

// Disabled returns the shared no-op Log. The zero Log is equally inert; this
// exists so Options fields can be defaulted to something named.
func Disabled() *Log { return disabled }

// New returns an enabled Log whose clock is now (nil => time.Now) and whose
// events go to sinks as they happen. The Log's zero of time is the moment of
// construction — create it where "the launch started" means something, right
// after the early refusals.
func New(now func() time.Time, sinks ...Sink) *Log {
	if now == nil {
		now = time.Now
	}
	return &Log{on: true, now: now, start: now(), sinks: sinks}
}

func (l *Log) enabled() bool { return l != nil && l.on }

// Span starts a named span. The returned Span's End records the duration;
// ending is idempotent, and a nil Span (timing off, or a nil Log) makes End a
// no-op — `defer o.Perf.Span("x").End()` is safe unconditionally.
func (l *Log) Span(name string) *Span {
	if !l.enabled() {
		return nil
	}
	at := l.now()
	l.emit(Event{Kind: KindStart, Name: name, At: at})
	return &Span{l: l, name: name, at: at}
}

// Mark records a point event — a thing that happened, not a thing that took
// time ("child exited", "image loaded from cache").
func (l *Log) Mark(name string) {
	if !l.enabled() {
		return
	}
	l.emit(Event{Kind: KindMark, Name: name, At: l.now()})
}

// emit appends to the in-memory record and fans out to the sinks, in order,
// outside the lock (sinks take their own; holding ours across a file write
// would serialize the report against every span end for no gain).
func (l *Log) emit(e Event) {
	l.mu.Lock()
	l.events = append(l.events, e)
	sinks := l.sinks
	l.mu.Unlock()
	for _, s := range sinks {
		if s == nil {
			continue // a nil sink is a wiring mistake, not a launch-ending event
		}
		s(e)
	}
}

// Span is one in-flight timing span. Nil means off; End is idempotent so a
// manual End plus a deferred End cannot double-record.
type Span struct {
	l    *Log
	name string
	at   time.Time
	once sync.Once
}

// End records the span's duration. No-op on a nil Span.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		at := s.l.now()
		s.l.emit(Event{Kind: KindEnd, Name: s.name, At: at, Dur: at.Sub(s.at)})
	})
}

// LastEvent returns the most recent recorded event with the given name.
// Window A attribution uses it to read the child.exited mark's timestamp
// without the run package keeping its own copy of the clock.
func (l *Log) LastEvent(name string) (Event, bool) {
	if !l.enabled() {
		return Event{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.events) - 1; i >= 0; i-- {
		if l.events[i].Name == name {
			return l.events[i], true
		}
	}
	return Event{}, false
}

// StartTime returns the collector's zero of time — the moment of construction,
// which initPerf places right after the launch's early refusals.
func (l *Log) StartTime() time.Time {
	if !l.enabled() {
		return time.Time{}
	}
	return l.start
}

// Report renders the completed events to w in the same register as the
// entrypoint's perf log (boot.go's dump): elapsed-since-start, delta from the
// previous line, and the label — plus a duration column the entrypoint has no
// need for. Marks print with a "-" duration. Starts are deliberately not
// rendered: they exist for the incremental file, where a dangling one names
// a hang. No-op on a disabled or empty Log.
func (l *Log) Report(w io.Writer, now time.Time) {
	if !l.enabled() {
		return
	}
	l.mu.Lock()
	events := append([]Event(nil), l.events...)
	l.mu.Unlock()

	var shown []Event
	for _, e := range events {
		if e.Kind != KindStart {
			shown = append(shown, e)
		}
	}
	if len(shown) == 0 {
		return
	}
	prev := -1.0
	for _, e := range shown {
		elapsed := e.At.Sub(l.start).Seconds()
		delta := "        "
		if prev >= 0 {
			delta = fmt.Sprintf("+%.3fs", elapsed-prev)
		}
		dur := "-"
		if e.Kind == KindEnd {
			dur = fmt.Sprintf("%.3fs", e.Dur.Seconds())
		}
		fmt.Fprintf(w, "  %7.3fs  %9s  %8s  %s\n", elapsed, delta, dur, e.Name)
		prev = elapsed
	}
	fmt.Fprintf(w, "  Total: %.3fs\n", now.Sub(l.start).Seconds())
}
