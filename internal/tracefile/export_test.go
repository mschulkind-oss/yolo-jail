package tracefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

func TestCanonicalRootNameReachesExistingEncoders(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := filepath.Join(base, "store")
	store, err := perf.OpenTraceStore(storeRoot, filepath.Join(base, "private"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := perf.MintTrace()
	claim, err := store.MintPending(identity.ID.String(), 123)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Close()
	sink, closer, err := perf.AdmittedLauncherTraceSink(claim, identity, func(s string) { t.Error(s) })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	log := perf.New(func() time.Time { return now })
	if !log.InstallIndependentTrace(identity, sink) {
		t.Fatal("install")
	}
	root := log.IndependentLauncherRoot()
	ctx, ok := root.Context()
	if !ok {
		t.Fatal("root context")
	}
	now = now.Add(10 * time.Nanosecond)
	child := root.Child("ordinary")
	childCtx, ok := child.Context()
	if !ok {
		t.Fatal("child context")
	}
	now = now.Add(10 * time.Nanosecond)
	child.End()
	now = now.Add(80 * time.Nanosecond)
	root.End()
	if _, err := claim.PublishPending(perf.TraceMetadata{Trace: identity.ID.String(), Kind: "launch", Start: 123, Jail: "test", Workspace: "ab12", Version: "test", Commit: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	// Copy only public evidence; the reader/exporter neither reads nor recreates controls.
	source := filepath.Join(storeRoot, identity.ID.String())
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	public := map[string]string{}
	for _, entry := range entries {
		if entry.Name() == MetaFile || strings.HasSuffix(entry.Name(), ".jsonl") {
			p, err := os.ReadFile(filepath.Join(source, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(entry.Name(), ".jsonl") && bytes.Count(p, []byte(`"name":"invocation"`)) != 2 {
				t.Fatal("publication rewrote the observed root names")
			}
			public[entry.Name()] = string(p)
		}
	}
	copyDir := writeTrace(t, filepath.Join(base, "copy"), identity.ID.String(), public)
	tr, err := ReadDir(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 2 || len(tr.Spans) != 2 {
		t.Fatal("public evidence shape")
	}
	observedRoot := span(t, tr, ctx.Span.String())
	observedChild := span(t, tr, childCtx.Span.String())
	if observedRoot.Name != "launch" || observedRoot.Start != 1700000000000000000 || observedRoot.End != 1700000000000000100 || observedRoot.ExplicitParent != "" || observedChild.ExplicitParent != observedRoot.ID || observedChild.Parent != observedRoot || observedChild.Start != 1700000000000000010 || observedChild.End != 1700000000000000020 {
		t.Fatal("recorded IDs, times or parents changed")
	}
	for name, encode := range map[string]func(io.Writer, *Trace) error{"chrome": WriteChrome, "otlp": WriteOTLP} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := encode(&out, tr); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(out.Bytes(), []byte(`"name":"launch"`)) {
				t.Fatal("resolved name missing", out.String())
			}
			if bytes.Contains(out.Bytes(), []byte(".pending")) || bytes.Contains(out.Bytes(), []byte("launcher_root")) {
				t.Fatal("private/control export")
			}
			if !bytes.Contains(out.Bytes(), []byte(ctx.Span.String())) || !bytes.Contains(out.Bytes(), []byte(childCtx.Span.String())) {
				t.Fatal("encoder lost recorded IDs")
			}
			if name == "chrome" {
				var payload struct {
					Events []chromeEvent `json:"traceEvents"`
				}
				if json.Unmarshal(out.Bytes(), &payload) != nil {
					t.Fatal("chrome decode")
				}
				observed := map[string]chromeEvent{}
				for _, event := range payload.Events {
					if id, ok := event.Args["yolo.span_id"].(string); ok {
						observed[id] = event
					}
				}
				r, c := observed[ctx.Span.String()], observed[childCtx.Span.String()]
				if len(observed) != 2 || r.Name != "launch" || r.Time.String() != "1700000000000000" || r.Duration.String() != "0.1" || c.Time.String() != "1700000000000000.01" || c.Duration.String() != "0.01" || c.Args["yolo.parent_span_id"] != ctx.Span.String() || r.Args["yolo.trace_id"] != identity.ID.String() {
					t.Fatal("chrome changed identity/time/parent")
				}
			} else {
				var payload struct {
					Resources []otlpResourceSpans `json:"resourceSpans"`
				}
				if json.Unmarshal(out.Bytes(), &payload) != nil {
					t.Fatal("otlp decode")
				}
				observed := map[string]otlpSpan{}
				for _, resource := range payload.Resources {
					for _, scope := range resource.ScopeSpans {
						for _, s := range scope.Spans {
							observed[s.SpanID] = s
						}
					}
				}
				r, c := observed[ctx.Span.String()], observed[childCtx.Span.String()]
				if len(observed) != 2 || r.Name != "launch" || r.Start != "1700000000000000000" || r.End != "1700000000000000100" || r.TraceID != identity.ID.String() || r.ParentID != "" || c.Start != "1700000000000000010" || c.End != "1700000000000000020" || c.ParentID != ctx.Span.String() {
					t.Fatal("otlp changed identity/time/parent")
				}
			}
		})
	}
}

func TestExportWireFormats(t *testing.T) {
	const id = "11111111111111111111111111111111"
	dir := filepath.Join(t.TempDir(), id)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	lines := `{"k":"lane","name":"launcher","pid":7,"trace":"11111111111111111111111111111111"}
{"t":1795111105000000000,"k":"start","name":"launch","span":"1111111111111111"}
{"t":1795111105001000000,"k":"start","name":"check","span":"2222222222222222","parent":"1111111111111111","attrs":{"key":"pack/tool"}}
{"t":1795111105003000000,"k":"end","name":"check","span":"2222222222222222","dur":2000000}
{"t":1795111105004000000,"k":"end","name":"launch","span":"1111111111111111","dur":4000000}
`
	if err := os.WriteFile(filepath.Join(dir, "launcher-7.jsonl"), []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var chrome, otlp bytes.Buffer
	if err := WriteChrome(&chrome, tr); err != nil {
		t.Fatal(err)
	}
	var c struct {
		TraceEvents []struct {
			Name string            `json:"name"`
			Ph   string            `json:"ph"`
			Ts   float64           `json:"ts"`
			Dur  float64           `json:"dur"`
			Args map[string]string `json:"args"`
		} `json:"traceEvents"`
	}
	if err := json.Unmarshal(chrome.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range c.TraceEvents {
		if e.Name == "check" && e.Ph == "X" {
			found = true
			if e.Ts != 1795111105001000 || e.Dur != 2000 || e.Args["key"] != "pack/tool" {
				t.Fatalf("Chrome check: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("Chrome lacks check interval: %s", &chrome)
	}
	if err := WriteOTLP(&otlp, tr); err != nil {
		t.Fatal(err)
	}
	var o struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []struct {
					TraceID      string `json:"traceId"`
					SpanID       string `json:"spanId"`
					ParentSpanID string `json:"parentSpanId"`
					Name         string `json:"name"`
					Start        string `json:"startTimeUnixNano"`
					End          string `json:"endTimeUnixNano"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(otlp.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, r := range o.ResourceSpans {
		for _, scope := range r.ScopeSpans {
			for _, s := range scope.Spans {
				if s.Name != "check" {
					continue
				}
				found = true
				if s.TraceID != id || s.SpanID != "2222222222222222" || s.ParentSpanID != "1111111111111111" || s.Start != "1795111105001000000" || s.End != "1795111105003000000" {
					t.Fatalf("OTLP check: %+v", s)
				}
			}
		}
	}
	if !found {
		t.Fatalf("OTLP lacks check span: %s", &otlp)
	}
	for name, got := range map[string][]byte{"chrome": chrome.Bytes(), "otlp": otlp.Bytes()} {
		want, err := os.ReadFile(filepath.Join("testdata", "export-"+name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s golden mismatch: %s", name, got)
		}
	}
}

func TestExportResolvedGraphAndEvents(t *testing.T) {
	tr := read(t, map[string]string{
		"a.jsonl": fmt.Sprintf(`{"k":"lane","name":"launcher","pid":7,"trace":%q}
{"t":1795111105000000123,"k":"start","name":"launch","span":%q}
{"t":1795111105000001124,"k":"end","span":%q}
{"t":1795111105000000200,"k":"start","name":"check\"\\","span":%q,"attrs":{"key":"pack/\"tool\\"}}
{"t":1795111105000000800,"k":"end","span":%q}
{"t":1795111105000000200,"k":"start","name":"concurrent","span":%q}
{"t":1795111105000000800,"k":"end","span":%q}
{"t":1795111105000000400,"k":"mark","name":"ready","attrs":{"phase":"boot"}}
{"t":1795111105000001100,"k":"note","name":"note"}
{"t":1795111105000000100,"k":"mark","name":"orphan","attrs":{"why":"outside"}}
`, tid, spanL, spanL, spanA, spanA, spanB, spanB),
		"b.jsonl": fmt.Sprintf(`{"k":"lane","name":"keeper","pid":7,"trace":%q,"parent":%q}
{"t":1795111105000000500,"k":"start","name":"open","span":%q}
{"t":1795111105000000700,"k":"mark","name":"waiting"}
`, tid, spanA, spanC),
		"c.jsonl": fmt.Sprintf(`{"k":"lane","name":"service","pid":7,"trace":%q}
{"t":1795111105000000300,"k":"start","name":"service.start","span":%q,"parent":%q}
{"t":1795111105000000900,"k":"end","span":%q}
`, tid, spanD, spanB, spanD),
	})
	tr.Skipped = []Skipped{{Path: filepath.Join(tr.Dir, "bad.jsonl"), Reason: "bad input " + filepath.Join(tr.Dir, "bad.jsonl")}}
	tr.Anomalies = append(tr.Anomalies, "accepted anomaly")
	root, check, concurrent, open, service := span(t, tr, spanL), span(t, tr, spanA), span(t, tr, spanB), span(t, tr, spanC), span(t, tr, spanD)
	var chrome, otlp bytes.Buffer
	if err := WriteChrome(&chrome, tr); err != nil {
		t.Fatal(err)
	}
	if err := WriteOTLP(&otlp, tr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(chrome.String(), tr.Dir) || strings.Contains(otlp.String(), tr.Dir) {
		t.Fatal("export leaked source directory")
	}
	var c struct {
		Events      []chromeEvent     `json:"traceEvents"`
		Diagnostics exportDiagnostics `json:"yolo.diagnostics"`
	}
	if err := json.Unmarshal(chrome.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	seen := map[string]chromeEvent{}
	for _, e := range c.Events {
		if e.Phase != "M" {
			seen[e.Name] = e
		}
	}
	if got := seen["launch"]; got.Time.String() != "1795111105000000.123" || got.Duration.String() != "1.001" {
		t.Fatalf("exact microseconds lost: %+v", got)
	}
	if got := seen[check.Name]; got.Args["key"] != "pack/\"tool\\" || got.Args["yolo.parent_span_id"] != root.ID {
		t.Fatalf("escaped attrs/resolved parent: %+v", got)
	}
	if got := seen["open"]; got.Args["yolo.unfinished"] != true || got.Args["yolo.parent_span_id"] != check.ID || got.Duration.String() != "0.624" || got.PID == seen["launch"].PID {
		t.Fatalf("lane/unfinished: %+v", got)
	}
	if got := seen["service.start"]; got.Args["yolo.parent_span_id"] != concurrent.ID || got.PID == seen["open"].PID {
		t.Fatalf("cross-lane explicit parent: %+v", got)
	}
	for _, name := range []string{"ready", "note", "orphan", "waiting"} {
		if got := seen[name]; got.Phase != "i" || got.Scope != "p" {
			t.Fatalf("instant %s: %+v", name, got)
		}
	}
	if c.Diagnostics.Skipped[0].File != "bad.jsonl" || c.Diagnostics.Skipped[0].Reason != "bad input bad.jsonl" || !reflect.DeepEqual(c.Diagnostics.Anomalies, tr.Anomalies) {
		t.Fatalf("diagnostics: %+v", c.Diagnostics)
	}
	var o struct {
		Resources []otlpResourceSpans `json:"resourceSpans"`
	}
	if err := json.Unmarshal(otlp.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	if len(o.Resources) != 3 {
		t.Fatalf("resources: %d", len(o.Resources))
	}
	spans := map[string]otlpSpan{}
	for _, r := range o.Resources {
		for _, s := range r.ScopeSpans[0].Spans {
			spans[s.SpanID] = s
		}
	}
	if got := spans[check.ID]; got.Start != "1795111105000000200" || got.End != "1795111105000000800" || got.ParentID != root.ID || got.Name != check.Name || len(got.Events) != 1 || got.Events[0].Name != "ready" || got.Events[0].Time != "1795111105000000400" || *attribute(t, got.Events[0].Attributes, "yolo.event_kind").String != KindMark {
		t.Fatalf("shortest/tie event, exact time, parent: %+v", got)
	}
	if got := spans[concurrent.ID]; len(got.Events) != 0 {
		t.Fatalf("equal interval tie not stable: %+v", got)
	}
	if got := spans[root.ID]; len(got.Events) != 1 || got.Events[0].Name != "note" || *attribute(t, got.Events[0].Attributes, "yolo.event_kind").String != KindNote {
		t.Fatalf("note: %+v", got)
	}
	if got := spans[open.ID]; got.ParentID != check.ID || got.End != "1795111105000001124" || !*attribute(t, got.Attributes, "yolo.unfinished").Bool || len(got.Events) != 1 {
		t.Fatalf("unfinished: %+v", got)
	}
	if got := spans[service.ID]; got.ParentID != concurrent.ID {
		t.Fatalf("explicit parent: %+v", got)
	}
	var orphans []otlpOrphanEvent
	if err := json.Unmarshal([]byte(*attribute(t, o.Resources[0].Resource.Attributes, "yolo.unparented_events").String), &orphans); err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].Time != "1795111105000000100" || orphans[0].Kind != KindMark || orphans[0].Name != "orphan" || orphans[0].Attrs["why"] != "outside" {
		t.Fatalf("orphan not preserved: %+v", orphans)
	}
	if open.Ended || open.End != tr.End || open.Parent != nil || open.Lane.ParentSpan != check || service.Parent != concurrent || check.Parent != root {
		t.Fatal("export mutated resolved input")
	}
}

func attribute(t *testing.T, attrs []otlpAttribute, key string) otlpValue {
	t.Helper()
	for _, a := range attrs {
		if a.Key == key {
			return a.Value
		}
	}
	t.Fatalf("missing attribute %s", key)
	return otlpValue{}
}

func TestExportEmptyAndMalformed(t *testing.T) {
	for name, encode := range map[string]func(io.Writer, *Trace) error{"chrome": WriteChrome, "otlp": WriteOTLP} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := encode(&b, &Trace{ID: tid}); err != nil {
				t.Fatal(err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(b.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			key := "traceEvents"
			if name == "otlp" {
				key = "resourceSpans"
			}
			if string(decoded[key]) != "[]" {
				t.Fatalf("empty: %s", &b)
			}
			for _, malformed := range []*Trace{nil, {}, {ID: "bad"}, {ID: tid, Lanes: []*Lane{nil}}, {ID: tid, Lanes: []*Lane{{Spans: []*Span{nil}}}}, {ID: tid, Spans: []*Span{nil}}} {
				b.Reset()
				if err := encode(&b, malformed); err == nil || b.Len() != 0 {
					t.Fatalf("malformed input accepted/written: %+v", malformed)
				}
			}
			for _, modify := range []func(*Span){
				func(s *Span) { s.ID = "bad" },
				func(s *Span) { s.End = s.Start - 1 },
				func(s *Span) { s.Start = -1 },
				func(s *Span) { s.Lane = nil },
				func(s *Span) { s.Parent = &Span{ID: spanB} },
			} {
				lane := &Lane{}
				s := &Span{ID: spanA, Lane: lane, Start: 1, End: 2, Ended: true}
				lane.Spans = []*Span{s}
				modify(s)
				if err := encode(io.Discard, &Trace{ID: tid, Lanes: []*Lane{lane}, Spans: []*Span{s}}); err == nil {
					t.Fatalf("malformed span accepted: %+v", s)
				}
			}
			if err := encode(nil, &Trace{ID: tid}); err == nil {
				t.Fatal("nil writer accepted")
			}
			var nilBuffer *bytes.Buffer
			if err := encode(nilBuffer, &Trace{ID: tid}); err == nil {
				t.Fatal("typed nil writer accepted")
			}
			var nilMap exportMapWriter
			if err := encode(nilMap, &Trace{ID: tid}); err == nil {
				t.Fatal("nil map writer accepted")
			}
		})
	}
}

type exportMapWriter map[string]string

func (w exportMapWriter) Write(b []byte) (int, error) {
	w["written"] = string(b)
	return len(b), nil
}

type exportErrorWriter struct{ err error }

func (w exportErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestExportWriterErrorsAndAcceptedAnomalies(t *testing.T) {
	tr := read(t, map[string]string{
		"a.jsonl": fmt.Sprintf("{\"k\":\"lane\",\"name\":\"a\",\"pid\":1,\"trace\":%q}\n{\"t\":1,\"k\":\"start\",\"name\":\"a\",\"span\":%q}\n", tid, spanA),
		"b.jsonl": fmt.Sprintf("{\"k\":\"lane\",\"name\":\"b\",\"pid\":2,\"trace\":%q}\n{\"t\":1,\"k\":\"start\",\"name\":\"b\",\"span\":%q}\n", tid, spanA),
	})
	if len(tr.Anomalies) == 0 {
		t.Fatal("fixture must exercise accepted duplicate")
	}
	var b bytes.Buffer
	if err := WriteOTLP(&b, tr); err == nil || b.Len() != 0 {
		t.Fatal("OTLP emitted ambiguous duplicate ids")
	}
	if err := WriteChrome(&b, tr); err != nil || !strings.Contains(b.String(), "references resolve to the first") {
		t.Fatalf("Chrome lost accepted duplicate/diagnostics: %v %s", err, &b)
	}
	failure := errors.New("export write failure")
	for _, encode := range []func(io.Writer, *Trace) error{WriteChrome, WriteOTLP} {
		if err := encode(exportErrorWriter{failure}, &Trace{ID: tid}); !errors.Is(err, failure) {
			t.Fatalf("writer error not returned: %v", err)
		}
	}
	// An event-only lane is valid: OTLP must preserve it without inventing a span.
	events := read(t, map[string]string{"a.jsonl": fmt.Sprintf("{\"k\":\"lane\",\"name\":\"a\",\"pid\":1,\"trace\":%q}\n{\"t\":1,\"k\":\"note\",\"name\":\"only\"}\n", tid)})
	b.Reset()
	if err := WriteOTLP(&b, events); err != nil || !strings.Contains(b.String(), `"spans":[]`) || !strings.Contains(b.String(), "yolo.unparented_events") {
		t.Fatalf("event-only lane: %v %s", err, &b)
	}
}
