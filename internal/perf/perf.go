// Package perf is the host-side timing-span collector behind `--timing` and
// `--verbose`.
//
// Architecture, invariants and the gates: docs/reference/perf-logging.md
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
// siblings — `<ws>/.yolo/logs/host-perf.log` beside the jail's `yolo-perf.log`.
//
// # Trace identity
//
// A Log built with NewTraced also gives every span an id and lets a span be handed to another
// process (context.go), and TraceSink is a second sink writing this process's lane of the trace
// as JSON lines (trace.go). host-perf.log keeps its format beside it. The design is
// docs/design/startup-tracing.md.
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
	"unicode/utf8"
)

// Kind says what an Event is. Starts are recorded so a hang leaves a dangling
// line (see the package doc); the Report renders only ends and marks.
type Kind uint8

const (
	KindStart Kind = iota
	KindEnd
	KindMark
	// KindNote is a point event that carries a DETAIL line: an observation too
	// long or too numerous for the report table (a /proc sample, one podman
	// event), written to the file and nowhere else. Report skips notes and the
	// slow-span notice never sees one (it reads ends only).
	KindNote
)

// Event is one timing observation, handed to every Sink the moment it happens.
type Event struct {
	Kind Kind
	Name string
	At   time.Time
	// Dur is the span's duration; set for KindEnd only.
	Dur time.Duration
	// Detail is a note's text; set for KindNote only.
	Detail string

	// The trace fields below are set only on a Log that records under a trace (NewTraced); on any
	// other Log they stay zero, and the sinks that predate tracing ignore them (TR-D8).

	// Span is the span's id, on a KindStart and its KindEnd alike, and on a Record's KindEnd.
	Span SpanID
	// Parent is the span the caller started this one under (Span.Child); zero when it gave none,
	// which is every existing call site: the reader places those by enclosure in time (TR-D7).
	Parent SpanID
	// Attrs are a span's attributes, on its KindStart only, already bounded by MaxAttrs and
	// MaxAttrValueBytes.
	Attrs []Attr
	// Recorded marks the KindEnd that Record entered: a completed interval with no start line.
	Recorded     bool
	launcherRoot bool
}

// Attr is one span attribute: a name, a count, an exit status or a key yolo already prints (a pack,
// a fork, a program, a service). Never an environment value, a token, a path under the home, or
// command output (startup-tracing.md §5.7).
type Attr struct {
	Key, Value string
}

// The attribute limits of TR-D11: the excess is dropped, never an error.
const (
	// MaxAttrs is how many attributes one span keeps; later ones are dropped.
	MaxAttrs = 8
	// MaxAttrValueBytes is the longest attribute value kept; a longer one is cut at a character
	// boundary at or below it.
	MaxAttrValueBytes = 240
)

// boundAttrs applies MaxAttrs and MaxAttrValueBytes, dropping an attribute with no key. It returns
// a copy, so the caller's slice is never retained.
func boundAttrs(attrs []Attr) []Attr {
	var out []Attr
	for _, a := range attrs {
		if len(out) == MaxAttrs {
			break
		}
		if a.Key == "" {
			continue
		}
		out = append(out, Attr{Key: a.Key, Value: truncateUTF8(a.Value, MaxAttrValueBytes)})
	}
	return out
}

// truncateUTF8 cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
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
//
// A var, not a const, and that is the whole point: the run package's slow-span
// tests can shrink it and observe a real Span cross it in milliseconds instead of
// sleeping the second out. Production never assigns it (the default IS the ruling).
var SlowSpanThreshold = 1 * time.Second

// Log is a launch's timing collector. Construct one per process (New) or use
// the shared Disabled(); nil is equally valid. Safe for concurrent use —
// spans end on goroutines (the tty proxy's onTerminate runs on its signal
// goroutine), and the signal arm can be tearing down while the main goroutine
// is still reporting.
type Log struct {
	on         bool
	now        func() time.Time
	start      time.Time
	mu         sync.Mutex
	events     []Event
	sinks      []Sink
	traceSinks []Sink
	// trace is the trace this Log records under (NewTraced or InstallIndependentTrace); zero for
	// New until a trace is explicitly installed.
	trace       TraceID
	traceParent SpanID
	traceRoot   bool
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

// NewTraced is New for a process that records under a trace: every span it starts gets a fresh
// span id, a child started through Span.Child names its parent, and Span.Context hands a span's
// identity to another process. tr is MintTrace() in the process that starts a trace, and the
// Context it was handed, Joined, in any other. Install TraceSink among sinks to write the trace's
// file; the other sinks see the same events and ignore the ids.
//
// A zero tr.ID is New: a Log with no trace, not an error.
func NewTraced(now func() time.Time, tr Trace, sinks ...Sink) *Log {
	l := New(now, sinks...)
	l.trace = tr.ID
	l.traceParent = tr.Parent
	return l
}

// InstallIndependentTrace installs a second trace-only sink set and its identity on a collector
// that has not emitted events. Existing sinks and reports remain unchanged; late installation is
// declined rather than assigning an identity retroactively.
func (l *Log) InstallIndependentTrace(tr Trace, sinks ...Sink) bool {
	if !l.enabled() || tr.ID.IsZero() {
		return false
	}
	var independent []Sink
	for _, sink := range sinks {
		if sink != nil {
			independent = append(independent, sink)
		}
	}
	if len(independent) == 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) != 0 || l.traceRoot || len(l.traceSinks) != 0 || (!l.trace.IsZero() && (l.trace != tr.ID || l.traceParent != tr.Parent)) {
		return false
	}
	l.trace = tr.ID
	l.traceParent = tr.Parent
	l.traceSinks = append(l.traceSinks, independent...)
	return true
}

// IndependentRoot starts a trace-only root for a trace minted by this process. Its start and end
// go only to independently installed trace sinks, never to legacy sinks, report rows, or notices.
// Joined collectors have a parent span and must not create another root.
func (l *Log) IndependentRoot(name string) *Span {
	return l.independentRoot(name, false)
}

// IndependentLauncherRoot emits one immutable neutral invocation start with designation.
func (l *Log) IndependentLauncherRoot() *Span {
	return l.independentRoot("invocation", true)
}

func (l *Log) independentRoot(name string, launcher bool) *Span {
	if !l.enabled() {
		return nil
	}
	l.mu.Lock()
	if l.trace.IsZero() || !l.traceParent.IsZero() || l.traceRoot || len(l.traceSinks) == 0 {
		l.mu.Unlock()
		return nil
	}
	l.traceRoot = true
	s := &Span{l: l, name: name, at: l.now(), id: newSpanID(), trace: l.trace, traceOnly: true}
	e := Event{Kind: KindStart, Name: name, At: s.at, Span: s.id, launcherRoot: launcher}
	sinks := append([]Sink(nil), l.traceSinks...)
	l.mu.Unlock()
	for _, sink := range sinks {
		sink(e)
	}
	return s
}

// TraceID returns the trace this Log records under; zero when it records under none (a disabled
// or nil Log, or one from New).
func (l *Log) TraceID() TraceID {
	if !l.enabled() {
		return TraceID{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.trace
}

func (l *Log) enabled() bool { return l != nil && l.on }

// Enabled reports whether the Log records anything, for a caller whose event detail costs
// something to compose (a formatted argv): it composes only when a recording reads it.
func (l *Log) Enabled() bool { return l.enabled() }

// Span starts a named span. The returned Span's End records the duration;
// ending is idempotent, and a nil Span (timing off, or a nil Log) makes End a
// no-op — `defer o.Perf.Span("x").End()` is safe unconditionally.
func (l *Log) Span(name string) *Span {
	return l.startSpan(name, SpanID{}, nil)
}

// SpanWith is Span with attributes, which go on the span's start line (bounded by MaxAttrs and
// MaxAttrValueBytes). Same-named spans run concurrently (the fork and extension checks) are told
// apart by their ids and carry their key here.
func (l *Log) SpanWith(name string, attrs ...Attr) *Span {
	return l.startSpan(name, SpanID{}, attrs)
}

func (l *Log) startSpan(name string, parent SpanID, attrs []Attr) *Span {
	if !l.enabled() {
		return nil
	}
	l.mu.Lock()
	s := &Span{l: l, name: name, parent: parent, trace: l.trace}
	if !l.trace.IsZero() {
		s.id = newSpanID()
	}
	s.at = l.now()
	e := Event{Kind: KindStart, Name: name, At: s.at, Span: s.id, Parent: s.parent, Attrs: boundAttrs(attrs)}
	l.events = append(l.events, e)
	legacy, independent := l.sinks, l.traceSinks
	l.mu.Unlock()
	dispatchEvent(e, legacy, independent)
	return s
}

// Mark records a point event — a thing that happened, not a thing that took
// time ("child exited", "image loaded from cache").
func (l *Log) Mark(name string) {
	if !l.enabled() {
		return
	}
	l.emit(Event{Kind: KindMark, Name: name, At: l.now()})
}

// Note records a point event with a detail line — see KindNote. For the
// observations that explain a number rather than being one: the Window A
// sampler's /proc readings and each podman event it priced (run/lingerprobe.go,
// run/perfevents.go). The file gets `note   <name>  <detail>`; the report
// table gets nothing, so a thirty-line sample never buries the spans.
func (l *Log) Note(name, detail string) {
	if !l.enabled() {
		return
	}
	l.emit(Event{Kind: KindNote, Name: name, At: l.now(), Detail: detail})
}

// Record enters a COMPLETED interval whose duration was measured somewhere
// else. It is the one shape Span cannot express: a Span times work this
// process is doing, and this is the opposite — an interval no yolo code was
// present for, priced afterwards from another system's record.
//
// Window A (run/perfevents.go) is one caller: the stretch inside the podman
// child, whose length comes from podman's own event log once the child is
// already gone. The other is a boot's own step timings, read back from a file
// the boot wrote (StepsFromMarks: macos_user.bootstrap.*, container.boot.*).
//
// It emits a KindEnd with NO KindStart, deliberately. A synthetic start would
// have to carry a timestamp from the past, putting the file's lines out of
// time order, and it would claim yolo was present when the interval began. The
// package's one structural reading of a lone start — "this is where it hung" —
// has to keep meaning exactly that, so a lone END is the honest asymmetry.
func (l *Log) Record(name string, dur time.Duration) {
	if !l.enabled() {
		return
	}
	l.mu.Lock()
	e := Event{Kind: KindEnd, Name: name, At: l.now(), Dur: dur, Recorded: true}
	if !l.trace.IsZero() {
		e.Span = newSpanID()
	}
	l.events = append(l.events, e)
	legacy, independent := l.sinks, l.traceSinks
	l.mu.Unlock()
	dispatchEvent(e, legacy, independent)
}

// emit appends to the in-memory record and fans out to the sinks, in order,
// outside the lock (sinks take their own; holding ours across a file write
// would serialize the report against every span end for no gain).
func (l *Log) emit(e Event) {
	l.mu.Lock()
	l.events = append(l.events, e)
	legacy, independent := l.sinks, l.traceSinks
	l.mu.Unlock()
	dispatchEvent(e, legacy, independent)
}

func (l *Log) emitIndependent(e Event) {
	l.mu.Lock()
	independent := append([]Sink(nil), l.traceSinks...)
	l.mu.Unlock()
	for _, sink := range independent {
		if sink != nil {
			sink(e)
		}
	}
}

func dispatchEvent(e Event, legacy, independent []Sink) {
	for _, sink := range legacy {
		if sink != nil {
			sink(e)
		}
	}
	for _, sink := range independent {
		if sink != nil {
			sink(e)
		}
	}
}

// Span is one in-flight timing span. Nil means off; End is idempotent so a
// manual End plus a deferred End cannot double-record.
type Span struct {
	l         *Log
	name      string
	at        time.Time
	once      sync.Once
	id        SpanID // zero unless the Log records under a trace
	parent    SpanID
	trace     TraceID
	traceOnly bool
}

// Child starts a span whose parent is s, explicitly. A caller that runs work concurrently, where
// enclosure in time would mislead the reader, starts it this way (TR-D7); everything else keeps
// calling Log.Span. On a nil Span it is a nil Span, so a chain stays safe when timing is off.
func (s *Span) Child(name string, attrs ...Attr) *Span {
	if s == nil {
		return nil
	}
	return s.l.startSpan(name, s.id, attrs)
}

// Context is s as the reference another process is handed to become its child (Context.String,
// Context.Environ); false on a nil Span or one whose Log records under no trace.
func (s *Span) Context() (Context, bool) {
	if s == nil || s.id.IsZero() || s.trace.IsZero() {
		return Context{}, false
	}
	return Context{Trace: s.trace, Span: s.id}, true
}

// End records the span's duration. No-op on a nil Span.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		at := s.l.now()
		e := Event{Kind: KindEnd, Name: s.name, At: at, Dur: at.Sub(s.at), Span: s.id}
		if s.traceOnly {
			s.l.emitIndependent(e)
		} else {
			s.l.emit(e)
		}
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
		if e.Kind != KindStart && e.Kind != KindNote {
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
