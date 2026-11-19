package tracefile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// JailFile is the file the host's reader writes from the jail half (design §5.3). It may open
// with a lane line; when it does not, its lane is named JailLane.
const (
	JailFile = "jail.jsonl"
	JailLane = "jail"
	MetaFile = "meta.json"
)

// Meta is a trace's meta.json. The design names its contents (§5.3) but not their keys; these
// are this reader's, and a meta.json it cannot read or whose trace id disagrees with the
// directory's is ignored, never fatal.
type Meta struct {
	Trace     string `json:"trace"`
	Kind      string `json:"kind"`
	Start     int64  `json:"start"`
	Jail      string `json:"jail"`
	Workspace string `json:"workspace"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
}

// Skipped names a file or directory the reader passed over, and why.
type Skipped struct {
	Path   string
	Reason string
}

// Event is a mark or a note.
type Event struct {
	T     int64
	Kind  string
	Name  string
	Attrs map[string]string
	Lane  *Lane
}

// Lane is one writing process: one file of the trace.
type Lane struct {
	Name string
	PID  int
	File string
	// Jail is true for the lane read from JailFile.
	Jail bool
	// ParentID is the span the lane's header names as its parent, possibly in another lane;
	// ParentSpan is that span once resolved (nil when absent, unknown, or refused as a cycle).
	ParentID   string
	ParentSpan *Span
	Spans      []*Span
	Events     []Event
	// Dropped counts lines that did not parse or broke the contract; Truncated is set when the
	// file ran past a reader bound and was read only up to it.
	Dropped   int
	Truncated bool
	// Start and End are the lane's extent: its earliest and latest instant.
	Start, End int64
}

// Span is one span after reading and parent placement. Times are nanoseconds since the Unix
// epoch.
type Span struct {
	ID    string
	Name  string
	Lane  *Lane
	Start int64
	End   int64
	// Ended is false for a start with no end; its End is then the trace's last instant.
	Ended bool
	// Recorded is true for a span read from a record, or from an end whose start is absent,
	// whose start was derived from its duration.
	Recorded bool
	Attrs    map[string]string
	// ExplicitParent is the parent id the line named, if any. Parent is the placed parent: nil
	// means the lane's root. Explicit is true when Parent came from ExplicitParent.
	ExplicitParent string
	Parent         *Span
	Explicit       bool
	Children       []*Span
	ChildLanes     []*Lane

	order int // position in the lane's (start asc, end desc, file order) sort
	seq   int // file order of the line that opened the span
}

// Duration is the span's length.
func (s *Span) Duration() int64 { return s.End - s.Start }

// Trace is one trace directory, read and placed.
type Trace struct {
	ID   string
	Dir  string
	Meta *Meta
	// Lanes are in file-name order. Spans holds every lane's spans.
	Lanes   []*Lane
	Spans   []*Span
	Skipped []Skipped
	// Anomalies names what the reader refused that was not one line: a duplicate span id, an
	// explicit parent that would make a cycle.
	Anomalies []string
	// Start and End bound every instant in the trace.
	Start, End int64
}

// Kind is the trace's kind from meta.json, or "" when it has none.
func (t *Trace) Kind() string {
	if t.Meta == nil {
		return ""
	}
	return t.Meta.Kind
}

// ReadDir reads one trace directory, whose base name must be a trace id. Only a directory that
// cannot be listed, or is not a trace's, is an error; a bad file is skipped and named.
func ReadDir(dir string) (*Trace, error) {
	id := filepath.Base(dir)
	if !IsTraceID(id) {
		return nil, fmt.Errorf("%s is not a trace directory: its name is not a trace id", dir)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	t := &Trace{ID: id, Dir: dir}
	for _, e := range entries { // os.ReadDir sorts by name
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case name == MetaFile:
			if m, reason := readMeta(path, id); reason != "" {
				t.Skipped = append(t.Skipped, Skipped{path, reason})
			} else {
				t.Meta = m
			}
		case strings.HasSuffix(name, ".jsonl"):
			if !e.Type().IsRegular() {
				t.Skipped = append(t.Skipped, Skipped{path, "not a regular file"})
				continue
			}
			lane, reason := readLane(path, id, name == JailFile)
			if reason != "" {
				t.Skipped = append(t.Skipped, Skipped{path, reason})
				continue
			}
			t.Lanes = append(t.Lanes, lane)
		}
	}
	t.assemble()
	return t, nil
}

// ReadRoot reads every trace directory directly under root, the caller's trace root. Entries
// that are not trace directories are ignored; a trace directory that cannot be read is skipped
// and named. Traces are returned oldest first.
func ReadRoot(root string) ([]*Trace, []Skipped, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}
	var traces []*Trace
	var skipped []Skipped
	for _, e := range entries {
		if !IsTraceID(e.Name()) {
			continue
		}
		path := filepath.Join(root, e.Name())
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			skipped = append(skipped, Skipped{path, "not a directory"})
			continue
		}
		t, err := ReadDir(path)
		if err != nil {
			skipped = append(skipped, Skipped{path, err.Error()})
			continue
		}
		traces = append(traces, t)
	}
	sort.SliceStable(traces, func(i, j int) bool { return traces[i].Start < traces[j].Start })
	return traces, skipped, nil
}

// openRegular opens path for reading without following a link at its last component, without
// blocking on a FIFO, and only if it is a regular file.
func openRegular(path string) (*os.File, string) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, "a link, not a regular file"
		}
		return nil, "unreadable: " + err.Error()
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, "not a regular file"
	}
	return f, ""
}

func readMeta(path, id string) (*Meta, string) {
	f, reason := openRegular(path)
	if reason != "" {
		return nil, reason
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxMetaBytes+1))
	if err != nil {
		return nil, "unreadable: " + err.Error()
	}
	if len(b) > MaxMetaBytes {
		return nil, "larger than the reader's bound"
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "does not parse"
	}
	if m.Trace != id {
		return nil, "names another trace"
	}
	m.Kind = cleanText(m.Kind, MaxNameBytes)
	m.Jail = cleanText(m.Jail, MaxNameBytes)
	m.Workspace = cleanText(m.Workspace, MaxNameBytes)
	m.Version = cleanText(m.Version, MaxNameBytes)
	m.Commit = cleanText(m.Commit, MaxNameBytes)
	return &m, ""
}

// readLane reads one line file. A file whose first line is not a lane line for this trace is
// skipped, except the jail file, which may open with an event and then takes the jail lane's
// name.
func readLane(path, id string, jail bool) (*Lane, string) {
	f, reason := openRegular(path)
	if reason != "" {
		return nil, reason
	}
	defer f.Close()
	lr := &laneReader{
		lane: &Lane{File: filepath.Base(path), Jail: jail},
		open: map[string]*Span{},
		seen: map[string]bool{},
	}
	r := bufio.NewReaderSize(io.LimitReader(f, MaxFileBytes+1), 64<<10)
	var read int64
	lines := 0
	for {
		line, tooLong, err := readBoundedLine(r)
		read += int64(len(line))
		if read > MaxFileBytes {
			lr.lane.Truncated = true
			break
		}
		if line = bytes.TrimSpace(line); len(line) > 0 || tooLong {
			if lines == MaxLinesPerFile {
				lr.lane.Truncated = true
				break
			}
			lines++
			if reason := lr.line(line, tooLong, lines == 1, id); reason != "" {
				return nil, reason
			}
		}
		if err != nil {
			if err != io.EOF {
				lr.lane.Truncated = true
			}
			break
		}
	}
	if lines == 0 {
		return nil, "empty"
	}
	for _, s := range lr.open {
		lr.lane.Spans = append(lr.lane.Spans, s)
	}
	return lr.lane, ""
}

// readBoundedLine reads up to the next newline. A line over MaxLineBytes is consumed whole and
// reported as tooLong with no content.
func readBoundedLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > MaxLineBytes+1 {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, tooLong, err
	}
}

// laneReader is one file's reading state: the started spans not yet ended, and every span id the
// lane has used, so a reused id is dropped rather than matched to the wrong span.
type laneReader struct {
	lane *Lane
	open map[string]*Span
	seen map[string]bool
	seq  int
}

// line applies one non-empty line. A non-empty reason skips the whole file: only the first line
// can do that.
func (lr *laneReader) line(b []byte, tooLong, first bool, id string) (reason string) {
	l, ok := rawLine{}, false
	if !tooLong {
		l, ok = parseLine(b)
	}
	if first {
		switch {
		case ok && l.K == KindLane:
			if l.Trace != id {
				return "its first line names another trace"
			}
			lr.lane.Name, lr.lane.PID, lr.lane.ParentID = l.Name, l.PID, l.Parent
			return ""
		case lr.lane.Jail && ok:
			lr.lane.Name = JailLane
		default:
			return "its first line is not a lane line"
		}
	}
	if !ok || l.K == KindLane {
		lr.lane.Dropped++
		return ""
	}
	lr.add(l)
	return ""
}

// add applies one event line.
func (lr *laneReader) add(l rawLine) {
	lane := lr.lane
	lr.seq++
	switch l.K {
	case KindMark, KindNote:
		lane.Events = append(lane.Events, Event{T: l.T, Kind: l.K, Name: l.Name, Attrs: l.Attrs, Lane: lane})
	case KindStart:
		if lr.seen[l.Span] {
			lane.Dropped++
			return
		}
		lr.seen[l.Span] = true
		lr.open[l.Span] = &Span{
			ID: l.Span, Name: l.Name, Lane: lane, Start: l.T, Attrs: l.Attrs,
			ExplicitParent: l.Parent, seq: lr.seq,
		}
	case KindEnd, KindRecord:
		if s, ok := lr.open[l.Span]; ok && l.K == KindEnd {
			if l.T < s.Start {
				lane.Dropped++
				return
			}
			delete(lr.open, l.Span)
			s.End, s.Ended = l.T, true
			s.Attrs = mergeAttrs(s.Attrs, l.Attrs)
			if s.ExplicitParent == "" {
				s.ExplicitParent = l.Parent
			}
			lane.Spans = append(lane.Spans, s)
			return
		}
		// A record, or an end whose start is absent: placed by its duration.
		if l.Dur == nil || l.Name == "" || lr.seen[l.Span] {
			lane.Dropped++
			return
		}
		lr.seen[l.Span] = true
		lane.Spans = append(lane.Spans, &Span{
			ID: l.Span, Name: l.Name, Lane: lane, Start: l.T - *l.Dur, End: l.T, Ended: true,
			Recorded: true, Attrs: l.Attrs, ExplicitParent: l.Parent, seq: lr.seq,
		})
	}
}

func mergeAttrs(a, b map[string]string) map[string]string {
	if len(b) == 0 {
		return a
	}
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return cleanAttrs(out)
}
