package perf

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// line is a trace file line as a reader sees it: every field the contract names.
type line struct {
	T      int64             `json:"t"`
	K      string            `json:"k"`
	Name   string            `json:"name"`
	PID    int               `json:"pid"`
	Trace  string            `json:"trace"`
	Span   string            `json:"span"`
	Parent string            `json:"parent"`
	Dur    *int64            `json:"dur"`
	Attrs  map[string]string `json:"attrs"`
}

func readLines(t *testing.T, path string) ([]line, []string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the trace file: %v", err)
	}
	var out []line
	var raw []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var ln line
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil {
			t.Fatalf("trace line %q is not one JSON object: %v", sc.Text(), err)
		}
		out = append(out, ln)
		raw = append(raw, sc.Text())
	}
	return out, raw
}

func laneFile(root string, tr Trace, lane string) string {
	return filepath.Join(root, tr.ID.String(), TraceFileName(lane, os.Getpid()))
}

func TestTraceFileFollowsTheLineContract(t *testing.T) {
	root := t.TempDir()
	c := newFakeClock()
	tr := MintTrace()
	var errb bytes.Buffer
	l := NewTraced(c.Now, tr, TraceSink(root, "launcher", tr, &errb))

	sp := l.Span("launch.start_loopholes")
	c.Advance(1285 * time.Millisecond)
	sp.End()
	l.Mark("child.spawned")
	l.Note("window_a.sample", "read(0, ...) = 12 from podman")
	l.Record("container.boot.mounts", 250*time.Millisecond)

	if errb.Len() != 0 {
		t.Fatalf("sink warned unexpectedly: %q", errb.String())
	}
	got, raw := readLines(t, laneFile(root, tr, "launcher"))
	if len(got) != 6 {
		t.Fatalf("want the lane line and five events, got %d lines:\n%s", len(got), strings.Join(raw, "\n"))
	}

	if want := `{"k":"lane","name":"launcher","pid":`; !strings.HasPrefix(raw[0], want) {
		t.Errorf("first line %q must open %s", raw[0], want)
	}
	if got[0].Trace != tr.ID.String() || got[0].PID != os.Getpid() {
		t.Errorf("lane line names trace %q pid %d; want %s pid %d", got[0].Trace, got[0].PID, tr.ID, os.Getpid())
	}
	if strings.Contains(raw[0], `"parent"`) {
		t.Errorf("the process that minted the trace has no parent, so its lane line carries none: %s", raw[0])
	}

	start, end, mark, note, rec := got[1], got[2], got[3], got[4], got[5]
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC).UnixNano()
	if start.K != "start" || start.T != at || start.Name != "launch.start_loopholes" || len(start.Span) != 16 {
		t.Errorf("start line wrong: %s", raw[1])
	}
	if strings.Contains(raw[1], `"parent"`) || strings.Contains(raw[1], `"dur"`) || strings.Contains(raw[1], `"attrs"`) {
		t.Errorf("a start given no parent and no attributes carries neither, and no duration: %s", raw[1])
	}
	if end.K != "end" || end.Span != start.Span || end.Dur == nil || *end.Dur != int64(1285*time.Millisecond) ||
		end.T != at+int64(1285*time.Millisecond) {
		t.Errorf("end line wrong (want the start's span id and dur in ns): %s", raw[2])
	}
	if mark.K != "mark" || mark.Name != "child.spawned" || mark.Span != "" {
		t.Errorf("mark line wrong: %s", raw[3])
	}
	if note.K != "note" || note.Name != "window_a.sample" || strings.Contains(raw[4], "podman") {
		t.Errorf("a note is written without its detail, which is command output (§5.7): %s", raw[4])
	}
	if rec.K != "record" || len(rec.Span) != 16 || rec.Span == start.Span || rec.Dur == nil ||
		*rec.Dur != int64(250*time.Millisecond) {
		t.Errorf("a Record is a record line with its own span id and its duration: %s", raw[5])
	}
	// The field order is the contract's, so a line reads the same to a person as in the plan.
	if !strings.HasPrefix(raw[1], `{"t":`) || !strings.Contains(raw[2], `","dur":`) {
		t.Errorf("field order is t, k, name, span, parent, dur, attrs:\n%s\n%s", raw[1], raw[2])
	}
}

func TestChildSpanLineCarriesItsParentsID(t *testing.T) {
	root := t.TempDir()
	tr := MintTrace()
	l := NewTraced(nil, tr, TraceSink(root, "keeper", tr, nil))

	parent := l.Span("launch.fork_builds")
	child := parent.Child("preparation.check", Attr{Key: "fork", Value: "pi"})
	grandchild := child.Child("preparation.build")
	grandchild.End()
	child.End()
	parent.End()

	got, raw := readLines(t, laneFile(root, tr, "keeper"))
	spans := map[string]line{}
	for _, ln := range got[1:] {
		if ln.K == "start" {
			spans[ln.Name] = ln
		}
	}
	p, c, g := spans["launch.fork_builds"], spans["preparation.check"], spans["preparation.build"]
	if p.Parent != "" {
		t.Errorf("a span started with Log.Span has no parent on its line; got %q", p.Parent)
	}
	if c.Parent != p.Span || g.Parent != c.Span {
		t.Errorf("each child's start line names its parent's span id:\n%s", strings.Join(raw, "\n"))
	}
	if c.Attrs["fork"] != "pi" {
		t.Errorf("a child's attributes ride on its start line; got %v", c.Attrs)
	}
}

func TestConcurrentSameNamedSpansGetDistinctIDsAndMatchedEnds(t *testing.T) {
	var mu sync.Mutex
	var events []Event
	l := NewTraced(nil, MintTrace(), func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	const n = 32
	ids := make([]SpanID, n)
	started := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			sp := l.Span("preparation.check")
			ctx, ok := sp.Context()
			if !ok {
				t.Error("a span on a traced Log has a context")
				return
			}
			ids[i] = ctx.Span
			sp.End()
		}()
	}
	close(started)
	wg.Wait()

	startIDs := map[SpanID]bool{}
	for _, e := range events {
		if e.Kind == KindStart {
			if e.Span.IsZero() || startIDs[e.Span] {
				t.Fatalf("start of %s has id %s: every span's id is non-zero and its own", e.Name, e.Span)
			}
			startIDs[e.Span] = true
		}
	}
	ended := map[SpanID]bool{}
	for _, e := range events {
		if e.Kind == KindEnd {
			if !startIDs[e.Span] || ended[e.Span] {
				t.Fatalf("end with id %s matches no start, or a start that already ended", e.Span)
			}
			ended[e.Span] = true
		}
	}
	for i, id := range ids {
		if !ended[id] {
			t.Errorf("span %d's own id %s has no end", i, id)
		}
	}
	if len(startIDs) != n || len(ended) != n {
		t.Errorf("want %d starts and %d ends with distinct ids, got %d and %d", n, n, len(startIDs), len(ended))
	}
}

func TestAttributesAreBoundedNotRefused(t *testing.T) {
	var got []Event
	l := NewTraced(nil, MintTrace(), func(e Event) { got = append(got, e) })
	var attrs []Attr
	for i := range MaxAttrs + 3 {
		attrs = append(attrs, Attr{Key: string(rune('a' + i)), Value: "v"})
	}
	attrs[0].Value = strings.Repeat("x", MaxAttrValueBytes+50)
	// A two-byte character straddling the limit is dropped whole, never split.
	attrs[1].Value = strings.Repeat("y", MaxAttrValueBytes-1) + "é"
	attrs = append([]Attr{{Key: "", Value: "keyless"}}, attrs...)

	l.SpanWith("s", attrs...).End()
	kept := got[0].Attrs
	if len(kept) != MaxAttrs {
		t.Fatalf("kept %d attributes; want the first %d with a key", len(kept), MaxAttrs)
	}
	if kept[0].Key != "a" || len(kept[0].Value) != MaxAttrValueBytes {
		t.Errorf("an over-long value is cut to %d bytes; got key %q len %d", MaxAttrValueBytes, kept[0].Key, len(kept[0].Value))
	}
	if kept[1].Value != strings.Repeat("y", MaxAttrValueBytes-1) {
		t.Errorf("a cut never splits a character; got %q", kept[1].Value[len(kept[1].Value)-3:])
	}
	if got[1].Attrs != nil {
		t.Errorf("attributes ride the start line only; the end carried %v", got[1].Attrs)
	}
}

func TestTraceSinkWritesOneTruncatedMarkThenNothing(t *testing.T) {
	var buf bytes.Buffer
	tr := MintTrace()
	sink := traceSinkTo(&buf, "launcher", 1, tr)
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	for i := range MaxTraceEvents + 5 {
		sink(Event{Kind: KindMark, Name: "m", At: at.Add(time.Duration(i))})
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if want := 1 + MaxTraceEvents + 1; len(lines) != want {
		t.Fatalf("want the lane line, %d events and one truncated mark (%d lines); got %d", MaxTraceEvents, want, len(lines))
	}
	var last line
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.K != "mark" || last.Name != TruncatedMark || last.T != at.Add(time.Duration(MaxTraceEvents)).UnixNano() {
		t.Errorf("the event after the limit writes the truncated mark at its own time; got %s", lines[len(lines)-1])
	}
}

func TestTraceSinkThatCannotMakeItsDirectoryWarnsOnceAndStaysSilent(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	tr := MintTrace()
	l := NewTraced(nil, tr, TraceSink(filepath.Join(blocker, "traces"), "launcher", tr, &errb))
	first := errb.String()
	if strings.Count(first, "yolo: trace file unavailable") != 1 || !strings.Contains(first, "continuing without it") {
		t.Fatalf("want exactly one warning that names the path and continues; got %q", first)
	}
	for range 5 {
		l.Span("x").End()
		l.Mark("m")
	}
	if errb.String() != first {
		t.Errorf("the sink went on warning after the first time:\n%s", errb.String())
	}
	if b, _ := os.ReadFile(blocker); string(b) != "x" {
		t.Errorf("the file in the way was written to: %q", b)
	}
}

func TestASecondSinkForTheSameLaneInOneProcessWritesNothing(t *testing.T) {
	root := t.TempDir()
	tr := MintTrace()
	first := NewTraced(nil, tr, TraceSink(root, "keeper", tr, nil))
	var errb bytes.Buffer
	second := NewTraced(nil, tr, TraceSink(root, "keeper", tr, &errb))
	if !strings.Contains(errb.String(), "trace file unavailable") {
		t.Fatalf("the second sink for one file warns; got %q", errb.String())
	}
	first.Mark("first")
	second.Mark("second")
	got, raw := readLines(t, laneFile(root, tr, "keeper"))
	if len(got) != 2 || got[1].Name != "first" {
		t.Errorf("one writer per file: only the first sink's lane and events are in it:\n%s", strings.Join(raw, "\n"))
	}
}

func TestTraceSinkNeverWritesThroughALinkAtTheTraceName(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	tr := MintTrace()
	if err := os.Symlink(outside, filepath.Join(root, tr.ID.String())); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	NewTraced(nil, tr, TraceSink(root, "launcher", tr, &errb)).Mark("m")
	if errb.Len() == 0 {
		t.Error("a link at the trace's name is refused with the one warning")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("the sink wrote through the link into %s: %v", outside, ents)
	}
}

func TestUntracedNilAndDisabledLogsCarryNoIdentityAndWriteNoTraceFile(t *testing.T) {
	root := t.TempDir()
	var nilLog *Log
	for name, l := range map[string]*Log{
		"nil":                nilLog,
		"disabled":           Disabled(),
		"untraced":           New(nil),
		"zero trace, traced": NewTraced(nil, Trace{}),
	} {
		sp := l.SpanWith("x", Attr{Key: "k", Value: "v"})
		child := sp.Child("y")
		if ctx, ok := child.Context(); ok {
			t.Errorf("%s log: a span hands out context %v; only a traced Log's do", name, ctx)
		}
		if _, ok := sp.Context(); ok {
			t.Errorf("%s log: a span hands out a context", name)
		}
		child.End()
		sp.End()
		l.Record("r", time.Second)
		if !l.TraceID().IsZero() {
			t.Errorf("%s log reports trace %s", name, l.TraceID())
		}
	}
	var events []Event
	New(nil, func(e Event) { events = append(events, e) }).Span("x").Child("y").End()
	for _, e := range events {
		if !e.Span.IsZero() || !e.Parent.IsZero() {
			t.Errorf("an untraced Log mints no ids; event %+v carries some", e)
		}
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Errorf("no trace file appears without a trace sink: %v", ents)
	}
}

func TestTracedEventsLeaveTheHostPerfLogLineUnchanged(t *testing.T) {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	plain := Event{Kind: KindEnd, Name: "teardown.ports", At: at, Dur: time.Second}
	traced := plain
	traced.Span, traced.Parent, traced.Attrs = newSpanID(), newSpanID(), []Attr{{Key: "k", Value: "v"}}
	if formatLine(plain) != formatLine(traced) {
		t.Errorf("host-perf.log keeps its format (TR-D8):\n%q\n%q", formatLine(plain), formatLine(traced))
	}
	rec := plain
	rec.Recorded = true
	if formatLine(plain) != formatLine(rec) {
		t.Errorf("a Record is still an end line in host-perf.log:\n%q", formatLine(rec))
	}
}

// TestTraceContextReachesAChildProcessThroughItsEnvironment is the propagation test: the parent
// hands a span's context to a re-exec of this binary through the child's environment, and the
// child's own file names that span as its parent. Deleting the hand-over (Context.String into the
// child's environment) or the adoption (ParseContext and Join, in runTraceHelper) fails it.
func TestTraceContextReachesAChildProcessThroughItsEnvironment(t *testing.T) {
	testtmp.RequireIsolated(t)
	root := t.TempDir()
	tr := MintTrace()
	l := NewTraced(nil, tr, TraceSink(root, "launcher", tr, nil))
	sp := l.Span("launch.spawn_service")
	ctx, ok := sp.Context()
	if !ok {
		t.Fatal("a span on a traced Log has a context")
	}

	cmd := exec.Command(os.Args[0])
	cmd.Env = testtmp.ChildEnv([]string{traceHelperRootEnv + "=" + root, traceHelperCtxEnv + "=" + ctx.String()})
	out, err := cmd.CombinedOutput()
	sp.End()
	if err != nil {
		t.Fatalf("helper process: %v\n%s", err, out)
	}

	got, raw := readLines(t, filepath.Join(root, tr.ID.String(), TraceFileName("helper", cmd.Process.Pid)))
	head := got[0]
	if head.K != "lane" || head.Name != "helper" || head.PID != cmd.Process.Pid {
		t.Errorf("the child's first line names its own lane and pid: %s", raw[0])
	}
	if head.Trace != tr.ID.String() || head.Parent != ctx.Span.String() {
		t.Errorf("the child's lane line names trace %q parent %q; want %s and the parent's span %s",
			head.Trace, head.Parent, tr.ID, ctx.Span)
	}
	if len(got) != 3 || got[1].Name != "helper.work" || got[2].K != "end" {
		t.Errorf("the child recorded its span into its own file:\n%s", strings.Join(raw, "\n"))
	}
}
