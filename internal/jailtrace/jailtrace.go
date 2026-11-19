// Package jailtrace is the jail half's trace writer: the lines a process on the jail side of the
// boundary appends to the launch's trace file in the jail's home, as each event happens
// (docs/design/startup-tracing.md §5.3; TR-D4, TR-D5).
//
// THE JAIL MINTS NO IDS (TR-D5). A line carries a time, a kind, a name and the writer's pid, and
// nothing else; the host's reader mints span ids and parents each line under the host span that
// was waiting on the jail. So this package parses the context it is handed only far enough to
// name the file, and never writes the context back out.
//
// THE CONTEXT ARRIVES AS YOLO_TRACEPARENT (TR-D3), which the entrypoint, the in-jail daemon
// supervisor and the generated shell read, and a launcher never does: a `yolo` run inside the
// jail inherits this variable, and adopting it would attach a nested launch to the outer one's
// trace. Nothing in this package is a launcher's to call.
//
// A SINK THAT CANNOT FAIL THE BOOT. Every error is dropped: a context that does not parse, a home
// that is empty, a file that cannot be opened or written all yield a writer that writes nothing.
// Each line is one write to a descriptor opened O_APPEND, with no sync, so lines from several
// processes never split. The writer keeps its descriptor open (close-on-exec, as every Go open
// is) rather than paying an open per event, which is what keeps the boot inside the design's
// 5 ms budget (§8).
//
// The line, from startup-tracing-plan.md's contract:
//
//	{"t":<ns since the Unix epoch>,"k":"start|end|mark","name":"…","pid":<n>}
//
// with `"res":"s"` when the writer had whole seconds only (the shell's fallback; this writer
// always has nanoseconds).
package jailtrace

import (
	"io"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Env is the variable the jail receives its trace context in: a W3C traceparent,
// `00-<32 hex trace id>-<16 hex span id>-<2 hex flags>`.
const Env = "YOLO_TRACEPARENT"

// MaxEvents is how many events one process writes before it writes a single `truncated` mark
// and stops (TR-D11).
const MaxEvents = 10000

// maxNameBytes bounds a name, as the host's reader does (§5.3).
const maxNameBytes = 64

// filePrefix and fileSuffix bracket the trace id in the jail-home file name. The generated
// shell spells the same name (entrypoint's _yolo_trace); TestShellAndGoNameTheSameFile in the
// entrypoint package pins the two together.
const (
	filePrefix = ".yolo-trace-"
	fileSuffix = ".jsonl"
)

// TraceID returns the trace id of a well-formed traceparent, and false for anything else: a
// wrong length, a version other than 00, uppercase or non-hex digits, an all-zero id. A
// malformed context is "no context", never a panic.
func TraceID(traceparent string) (string, bool) {
	// 00-<32>-<16>-<2>: 2+1+32+1+16+1+2.
	if len(traceparent) != 55 || traceparent[:3] != "00-" || traceparent[35] != '-' || traceparent[52] != '-' {
		return "", false
	}
	trace, span, flags := traceparent[3:35], traceparent[36:52], traceparent[53:55]
	if !lowerHex(trace) || !lowerHex(span) || !lowerHex(flags) || allZero(trace) || allZero(span) {
		return "", false
	}
	return trace, true
}

// FileName is the jail-home file a trace's jail half is appended to.
func FileName(traceID string) string { return filePrefix + traceID + fileSuffix }

func lowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func allZero(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}

// Writer appends jail lines. A nil *Writer is a valid writer that writes nothing, so a caller
// with no context holds nil and calls it unconditionally. Safe for concurrent use: the
// supervisor writes from one goroutine per daemon.
type Writer struct {
	mu     sync.Mutex
	w      io.Writer
	closer io.Closer
	pid    int
	now    func() time.Time
	n      int
	done   bool
	buf    []byte
}

// Open returns the writer for the trace traceparent names, appending to its file under home,
// or nil when there is no context (an empty or malformed traceparent), no home, or the file
// cannot be opened. The file is created when absent: the first jail-side Go writer of a trace
// creates it, and the shell writers append only to a file that exists. A link at the path is
// refused (O_NOFOLLOW), so the writer never appends through one.
func Open(home, traceparent string) *Writer {
	id, ok := TraceID(traceparent)
	if !ok || home == "" {
		return nil
	}
	f, err := os.OpenFile(home+string(os.PathSeparator)+FileName(id),
		os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil
	}
	w := NewWriter(f, os.Getpid(), time.Now)
	w.closer = f
	return w
}

// NewWriter returns a writer over w, stamping lines with pid and the times now returns. Tests
// inject the destination here; Open is the production constructor.
func NewWriter(w io.Writer, pid int, now func() time.Time) *Writer {
	return &Writer{w: w, pid: pid, now: now}
}

// Start, End and Mark write one line of their kind. A span is a Start and an End of one name
// from one pid; the host's reader pairs them.
func (w *Writer) Start(name string) { w.write("start", name) }

// End closes the span Start opened under the same name.
func (w *Writer) End(name string) { w.write("end", name) }

// Mark records an instant.
func (w *Writer) Mark(name string) { w.write("mark", name) }

// Close releases the descriptor Open took. Later writes are dropped.
func (w *Writer) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	if w.closer != nil {
		_ = w.closer.Close()
		w.closer = nil
	}
}

func (w *Writer) write(kind, name string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	if w.n >= MaxEvents {
		kind, name, w.done = "mark", "truncated", true
	}
	w.n++
	b := w.buf[:0]
	b = append(b, `{"t":`...)
	b = strconv.AppendInt(b, w.now().UnixNano(), 10)
	b = append(b, `,"k":"`...)
	b = append(b, kind...)
	b = append(b, `","name":"`...)
	b = appendName(b, name)
	b = append(b, `","pid":`...)
	b = strconv.AppendInt(b, int64(w.pid), 10)
	b = append(b, "}\n"...)
	w.buf = b
	if _, err := w.w.Write(b); err != nil {
		// A sink that failed once is not retried for every later event: the boot pays for a
		// failed write once.
		w.done = true
	}
}

// appendName appends name reduced to [A-Za-z0-9_.-] and maxNameBytes, so a name needs no JSON
// escaping and cannot carry anything the reader would have to defuse. Names are yolo's own step
// names plus daemon and program names a pack declares; the host's reader sanitizes again.
func appendName(b []byte, name string) []byte {
	if len(name) > maxNameBytes {
		name = name[:maxNameBytes]
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' {
			b = append(b, c)
		} else {
			b = append(b, '_')
		}
	}
	return b
}
