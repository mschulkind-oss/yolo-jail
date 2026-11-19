package perf

// trace.go is the collector's second sink: one process's file in a trace directory, one JSON line
// per event, appended as it happens (TR-D4). host-perf.log is untouched beside it (TR-D8). The
// design is docs/design/startup-tracing.md §5.3 and §5.7.
//
// The file is <root>/<trace id>/<lane>-<pid>.jsonl. Its first line names the writer:
//
//	{"k":"lane","name":"keeper","pid":4242,"trace":"<32 hex>","parent":"<16 hex>"}
//
// ("parent" only when another process handed this one the trace), and every later line is an
// event:
//
//	{"t":<ns since the epoch>,"k":"start","name":"launch.start_loopholes","span":"<16 hex>","parent":"<16 hex>"}
//	{"t":<ns>,"k":"end","name":"launch.start_loopholes","span":"<16 hex>","dur":<ns>}
//
// k is start, end, mark, note or record (an end with no start, from Log.Record). "parent" is
// present only when the caller gave one (Span.Child), "attrs" only when there are attributes. A
// note's detail is NOT written here: it is a /proc sample or a podman event, and §5.7 keeps
// command output out of a trace. It stays in host-perf.log, where the note's time joins the two.
//
// Where the root is, is OQ-TR3, so the caller passes it; this file decides nothing about it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// MaxTraceEvents is how many events one process writes into its trace file (TR-D11). The event
// after the last one writes a single "truncated" mark, and nothing is written after that.
const MaxTraceEvents = 10000

// TruncatedMark is the name of the mark a trace file ends on when its process recorded more than
// MaxTraceEvents events.
const TruncatedMark = "truncated"

// traceLine is one event line. The field order is the contract's: t, k, name, span, parent, dur,
// attrs.
type traceLine struct {
	T      int64             `json:"t"`
	K      string            `json:"k"`
	Name   string            `json:"name"`
	Span   string            `json:"span,omitempty"`
	Parent string            `json:"parent,omitempty"`
	Dur    *int64            `json:"dur,omitempty"` // a pointer, so a zero-length span still has one
	Attrs  map[string]string `json:"attrs,omitempty"`
}

// laneLine is a file's first line.
type laneLine struct {
	K      string `json:"k"`
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Trace  string `json:"trace"`
	Parent string `json:"parent,omitempty"`
}

// TraceFileName is the name of a lane's file within its trace directory: <lane>-<pid>.jsonl, the
// lane reduced to a span-name segment ("process" if nothing is left of it).
func TraceFileName(lane string, pid int) string {
	return fmt.Sprintf("%s-%d.jsonl", laneName(lane), pid)
}

func laneName(lane string) string {
	if n := SegmentName(lane); n != "" {
		return n
	}
	return "process"
}

// TraceSink returns the Sink that writes this process's lane of trace tr, under root: the file
// <root>/<tr.ID>/<lane>-<pid>.jsonl, created now with its lane line, then one line per event as it
// happens. Pair it with NewTraced(now, tr, ...) for the same tr.
//
// Best-effort per the package contract. When the directory or the file cannot be made, the failure
// is reported ONCE to errw and the sink stays installed and silent; the launch proceeds and
// host-perf.log is still written (§5.7). A zero tr.ID is refused the same way, because it names no
// directory.
//
// ONE WRITER PER FILE is the point of the layout, so the file is created exclusively: a second
// sink for the same lane in the same process (two collectors constructed by one process) gets the
// warning and writes nothing, rather than interleaving a second lane into the first one's file.
// The directory beneath root is entered through an os.Root, so a link left at the trace's name
// cannot carry a write outside root, and an existing link at the file's name is never followed.
func TraceSink(root, lane string, tr Trace, errw io.Writer) Sink {
	f, path, err := openTraceFile(root, lane, tr.ID)
	if err != nil {
		warnTraceOnce(errw, path, err)
		return func(Event) {}
	}
	return traceSinkTo(f, laneName(lane), os.Getpid(), tr)
}

func openTraceFile(root, lane string, id TraceID) (*os.File, string, error) {
	name := TraceFileName(lane, os.Getpid())
	path := filepath.Join(root, id.String(), name)
	if id.IsZero() {
		return nil, path, errors.New("no trace id")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, path, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, path, err
	}
	defer r.Close()
	if err := r.Mkdir(id.String(), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, path, err
	}
	f, err := r.OpenFile(filepath.Join(id.String(), name), os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, path, err
	}
	return f, path, nil
}

// traceSinkTo writes the lane line to w now, then returns the sink writing each event to w as one
// write of one line. It owns w from here.
func traceSinkTo(w io.Writer, lane string, pid int, tr Trace) Sink {
	head := laneLine{K: "lane", Name: lane, PID: pid, Trace: tr.ID.String()}
	if !tr.Parent.IsZero() {
		head.Parent = tr.Parent.String()
	}
	writeJSONLine(w, head)

	var (
		mu      sync.Mutex // one write per event, under the sink's own lock
		written int
	)
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case written < MaxTraceEvents:
			writeJSONLine(w, eventLine(e))
		case written == MaxTraceEvents:
			writeJSONLine(w, traceLine{T: e.At.UnixNano(), K: "mark", Name: TruncatedMark})
		default:
			return
		}
		written++
	}
}

// eventLine is e in the line contract.
func eventLine(e Event) traceLine {
	ln := traceLine{T: e.At.UnixNano(), Name: e.Name}
	if !e.Span.IsZero() {
		ln.Span = e.Span.String()
	}
	if !e.Parent.IsZero() {
		ln.Parent = e.Parent.String()
	}
	switch e.Kind {
	case KindStart:
		ln.K = "start"
	case KindMark:
		ln.K = "mark"
	case KindNote:
		ln.K = "note"
	default:
		ln.K = "end"
		if e.Recorded {
			ln.K = "record"
		}
		d := int64(e.Dur)
		ln.Dur = &d
	}
	if len(e.Attrs) > 0 {
		ln.Attrs = make(map[string]string, len(e.Attrs))
		for _, a := range e.Attrs {
			ln.Attrs[a.Key] = a.Value
		}
	}
	return ln
}

// writeJSONLine writes v as one line in a single write, so an O_APPEND file never holds half of
// one. Errors are swallowed: the sink cannot fail (package doc).
func writeJSONLine(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}

// warnTraceOnce is the trace sink's whole error story, as warnOnce is the file sink's.
func warnTraceOnce(errw io.Writer, path string, err error) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "yolo: trace file unavailable at %s: %v (continuing without it; host-perf.log is still written)\n", path, err)
}
