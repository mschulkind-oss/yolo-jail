package perf

import (
	"bytes"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIndependentProcessSpanUsesJoinedParentAndSeparateSinks(t *testing.T) {
	base := time.Unix(100, 0)
	var tick time.Duration
	now := func() time.Time {
		at := base.Add(tick)
		tick += time.Second
		return at
	}
	var legacy, traced []Event
	log := New(now, func(e Event) { legacy = append(legacy, e) })
	carrier := Context{Trace: MintTrace().ID, Span: newSpanID()}
	joined := carrier.Join()
	if !log.InstallIndependentTrace(joined, func(e Event) { traced = append(traced, e) }) {
		t.Fatal("joined installation declined")
	}
	lifetime := log.IndependentProcessSpan("keeper")
	if lifetime == nil {
		t.Fatal("joined lifetime declined")
	}
	ctx, ok := lifetime.Context()
	if !ok || ctx.Trace != carrier.Trace || ctx.Span.IsZero() || ctx.Span != lifetime.id {
		t.Fatal("lifetime context lost its trace or generated ID")
	}
	child := lifetime.Child("child", Attr{Key: "kind", Value: "fixture"})
	child.End()
	log.Span("ordinary").End()
	log.Mark("mark")
	lifetime.End()
	lifetime.End()
	if len(traced) != 7 || len(legacy) != 5 {
		t.Fatalf("lifetime reached legacy sinks or emitted twice: traced=%#v legacy=%#v", traced, legacy)
	}
	start, end := traced[0], traced[6]
	if start.Kind != KindStart || end.Kind != KindEnd || start.Name != "keeper" || end.Name != "keeper" || start.Span != ctx.Span || end.Span != ctx.Span || start.Parent != carrier.Span {
		t.Fatal("lifetime start/end identity or supplied parent changed", start, end)
	}
	if start.At != base.Add(time.Second) || lifetime.at != start.At || end.At != base.Add(7*time.Second) || end.Dur != 6*time.Second || lifetime.parent != carrier.Span || lifetime.trace != carrier.Trace || log.TraceID() != carrier.Trace || log.traceParent != carrier.Span {
		t.Fatal("lifetime did not retain the real clock start and joined identity")
	}
	for _, event := range traced {
		if event.launcherRoot {
			t.Fatal("joined lifetime designated a launcher root")
		}
	}
	if len(start.Attrs) != 0 || len(end.Attrs) != 0 || start.Recorded || end.Recorded || !end.Parent.IsZero() {
		t.Fatal("lifetime changed the existing start/end protocol")
	}
	if traced[1].Parent != ctx.Span || traced[1].Attrs[0] != (Attr{Key: "kind", Value: "fixture"}) || traced[2].Span != traced[1].Span || !traced[3].Parent.IsZero() || traced[4].Span != traced[3].Span || traced[5].Kind != KindMark {
		t.Fatal("ordinary child/span/mark semantics changed")
	}
	for i, event := range legacy {
		if !reflect.DeepEqual(event, traced[i+1]) {
			t.Fatal("ordinary sinks differ")
		}
	}
	if _, ok := log.LastEvent("keeper"); ok {
		t.Fatal("trace-only lifetime entered the legacy collector")
	}
	var report bytes.Buffer
	log.Report(&report, now())
	if strings.Contains(report.String(), "keeper") || !strings.Contains(report.String(), "child") || !strings.Contains(report.String(), "ordinary") || !strings.Contains(report.String(), "mark") {
		t.Fatal("lifetime changed legacy report rows", report.String())
	}
	if log.IndependentProcessSpan("again") != nil {
		t.Fatal("ended lifetime allowed a second acquisition")
	}
}

func TestIndependentProcessSpanDeclinesWithoutJoinedIndependentTrace(t *testing.T) {
	joined := Context{Trace: MintTrace().ID, Span: newSpanID()}.Join()
	for _, tc := range []struct {
		name string
		log  *Log
	}{
		{"nil", nil},
		{"disabled", Disabled()},
		{"zero-log", &Log{}},
		{"uninstalled", New(nil)},
		{"joined-without-independent-sinks", NewTraced(nil, joined)},
		{"zero-trace", NewTraced(nil, Trace{Parent: joined.Parent})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before TraceID
			if tc.log != nil {
				before = tc.log.TraceID()
			}
			if tc.log.IndependentProcessSpan("declined") != nil {
				t.Fatal("invalid collector acquired a lifetime")
			}
			if tc.log != nil && (tc.log.TraceID() != before || len(tc.log.events) != 0 || len(tc.log.traceSinks) != 0) {
				t.Fatal("decline changed collector identity or emitted events")
			}
		})
	}
	var legacy, traced []Event
	log := New(nil, func(e Event) { legacy = append(legacy, e) })
	sink := func(e Event) { traced = append(traced, e) }
	if log.InstallIndependentTrace(Trace{Parent: joined.Parent}, sink) || log.InstallIndependentTrace(joined, nil) || log.IndependentProcessSpan("invalid-install") != nil || !log.TraceID().IsZero() || len(legacy) != 0 || len(traced) != 0 {
		t.Fatal("invalid installation or decline changed identity/sinks")
	}
	if !log.InstallIndependentTrace(joined, sink) {
		t.Fatal("declines stole valid installation")
	}
	if log.IndependentRoot("wrong-root") != nil || log.IndependentLauncherRoot() != nil || len(traced) != 0 || len(legacy) != 0 {
		t.Fatal("joined root restriction changed")
	}
	lifetime := log.IndependentProcessSpan("joined")
	if lifetime == nil || log.IndependentProcessSpan("second") != nil || log.InstallIndependentTrace(joined, sink) {
		t.Fatal("root decline stole acquisition or acquired lifetime allowed reinstall/second acquisition")
	}
	lifetime.End()
	if len(traced) != 2 || len(legacy) != 0 || log.TraceID() != joined.ID || log.traceParent != joined.Parent {
		t.Fatal("declines changed joined identity or legacy output")
	}
	for _, launcher := range []bool{false, true} {
		rootLog := New(nil)
		var events []Event
		rootLog.InstallIndependentTrace(MintTrace(), func(e Event) { events = append(events, e) })
		if rootLog.IndependentProcessSpan("unjoined") != nil || len(events) != 0 {
			t.Fatal("unjoined collector acquired a process span")
		}
		var root *Span
		if launcher {
			root = rootLog.IndependentLauncherRoot()
		} else {
			root = rootLog.IndependentRoot("root")
		}
		if root == nil || rootLog.IndependentProcessSpan("root-minted") != nil {
			t.Fatal("unjoined decline stole root acquisition or root allowed process acquisition")
		}
		root.End()
		if len(events) != 2 || events[0].launcherRoot != launcher || !events[0].Parent.IsZero() {
			t.Fatal("root compatibility changed")
		}
	}
}

func TestIndependentProcessSpanAndOrdinaryEventsAreConcurrentSafe(t *testing.T) {
	var mu sync.Mutex
	var legacy, traced []Event
	log := New(nil, func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		legacy = append(legacy, e)
	})
	joined := Context{Trace: MintTrace().ID, Span: newSpanID()}.Join()
	if !log.InstallIndependentTrace(joined, func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		traced = append(traced, e)
	}) {
		t.Fatal("install")
	}
	const n = 64
	start := make(chan struct{})
	acquired := make(chan *Span, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			acquired <- log.IndependentProcessSpan("keeper")
			if log.InstallIndependentTrace(joined, func(Event) {}) {
				t.Error("concurrent reinstallation accepted")
			}
			log.Span("ordinary").End()
			log.Mark("mark")
		}()
	}
	close(start)
	wg.Wait()
	close(acquired)
	var lifetime *Span
	for span := range acquired {
		if span != nil {
			if lifetime != nil {
				t.Fatal("multiple concurrent acquisitions")
			}
			lifetime = span
		}
	}
	if lifetime == nil {
		t.Fatal("no concurrent acquisition")
	}
	end := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-end
			lifetime.End()
		}()
	}
	close(end)
	wg.Wait()
	if len(legacy) != 3*n || len(traced) != 3*n+2 {
		t.Fatalf("concurrent sink counts: legacy=%d traced=%d", len(legacy), len(traced))
	}
	starts, ends := make(map[SpanID]Event), make(map[SpanID]Event)
	marks := 0
	for _, event := range traced {
		if event.launcherRoot {
			t.Fatal("joined event designated root")
		}
		switch event.Kind {
		case KindStart:
			if event.Span.IsZero() || !starts[event.Span].At.IsZero() {
				t.Fatal("zero or duplicate start ID")
			}
			starts[event.Span] = event
		case KindEnd:
			if event.Span.IsZero() || !ends[event.Span].At.IsZero() {
				t.Fatal("zero or duplicate end ID")
			}
			ends[event.Span] = event
		case KindMark:
			marks++
		}
	}
	if len(starts) != n+1 || len(ends) != n+1 || marks != n || starts[lifetime.id].Parent != joined.Parent || lifetime.trace != joined.ID || log.TraceID() != joined.ID || log.traceParent != joined.Parent {
		t.Fatal("concurrent identity/count/parent mismatch")
	}
	for id, event := range starts {
		ended, ok := ends[id]
		if !ok || ended.Name != event.Name || ended.Dur != ended.At.Sub(event.At) {
			t.Fatal("actual start/end pair lost")
		}
		if id != lifetime.id && !event.Parent.IsZero() {
			t.Fatal("ordinary phase implicitly adopted joined parent")
		}
	}
	for _, event := range legacy {
		if event.Name == "keeper" {
			t.Fatal("lifetime entered legacy sink")
		}
	}
}

func TestIndependentLauncherRootPreservesOrdinaryRootSemantics(t *testing.T) {
	var legacy, traced []Event
	log := New(nil, func(e Event) { legacy = append(legacy, e) })
	if !log.InstallIndependentTrace(MintTrace(), func(e Event) { traced = append(traced, e) }) {
		t.Fatal("install")
	}
	r := log.IndependentLauncherRoot()
	if r == nil {
		t.Fatal("root")
	}
	attrs := make([]Attr, MaxAttrs+2)
	for i := range attrs {
		attrs[i] = Attr{Key: string(rune('a' + i)), Value: strings.Repeat("v", MaxAttrValueBytes+1)}
	}
	log.SpanWith("ordinary", attrs...).End()
	r.End()
	if len(traced[1].Attrs) != MaxAttrs || len(traced[2].Attrs) != 0 {
		t.Fatal("producer exceeded per-span attributes")
	}
	for _, attr := range traced[1].Attrs {
		if len(attr.Value) != MaxAttrValueBytes {
			t.Fatal("producer value bound")
		}
	}
	if len(traced) != 4 || len(legacy) != 2 || !traced[0].launcherRoot || traced[0].Name != "invocation" || traced[0].Span.IsZero() || !traced[0].Parent.IsZero() || traced[3].launcherRoot {
		t.Fatal("root semantics", traced, legacy)
	}
	if log.IndependentLauncherRoot() != nil {
		t.Fatal("second root")
	}
	ordinary := New(nil)
	var got []Event
	ordinary.InstallIndependentTrace(MintTrace(), func(e Event) { got = append(got, e) })
	ordinary.IndependentRoot("old").End()
	if got[0].launcherRoot {
		t.Fatal("ordinary designated")
	}
	joined := MintTrace()
	joined.Parent = newSpanID()
	j := New(nil)
	j.InstallIndependentTrace(joined, func(Event) {})
	if j.IndependentLauncherRoot() != nil {
		t.Fatal("joined root")
	}
}

func TestIndependentTraceRootAndOrdinarySpansUseSeparateSinks(t *testing.T) {
	legacy := make([]Event, 0)
	traced := make([]Event, 0)
	log := New(nil, func(e Event) { legacy = append(legacy, e) })
	tr := MintTrace()
	if !log.InstallIndependentTrace(tr, func(e Event) { traced = append(traced, e) }) {
		t.Fatal("fresh independent trace installation was declined")
	}
	root := log.IndependentRoot("launch.root")
	if root == nil {
		t.Fatal("minted trace did not create its independent root")
	}
	ctx, ok := root.Context()
	if !ok || ctx.Trace != tr.ID || ctx.Span.IsZero() {
		t.Fatal("independent root did not expose its minted trace context")
	}
	child := root.Child("launch.child", Attr{Key: "kind", Value: "fixture"})
	child.End()
	ordinary := log.Span("launch.ordinary")
	ordinary.End()
	root.End()
	root.End()

	if len(legacy) != 4 || legacy[0].Name != "launch.child" || legacy[1].Name != "launch.child" || legacy[2].Name != "launch.ordinary" || legacy[3].Name != "launch.ordinary" {
		t.Fatalf("trace-only root entered or reordered the legacy sink: %#v", legacy)
	}
	if len(traced) != 6 || traced[0].Name != "launch.root" || traced[5].Name != "launch.root" {
		t.Fatalf("independent sink missed root or ordinary spans: %#v", traced)
	}
	if traced[1].Parent != ctx.Span || traced[2].Span != traced[1].Span {
		t.Fatal("root child events lost their explicit parent binding")
	}
	for _, event := range append(legacy, traced...) {
		if event.Span.IsZero() {
			t.Fatal("ordinary traced event lacks its span ID")
		}
	}
	var report bytes.Buffer
	log.Report(&report, time.Now())
	if bytes.Contains(report.Bytes(), []byte("launch.root")) || !bytes.Contains(report.Bytes(), []byte("launch.ordinary")) {
		t.Fatal("trace-only root affected report output or ordinary report behavior changed")
	}
}

func TestIndependentTraceInstallationDeclinesInvalidOrLateIdentity(t *testing.T) {
	var got []Event
	log := New(nil)
	if log.InstallIndependentTrace(Trace{}, func(e Event) { got = append(got, e) }) {
		t.Fatal("zero trace identity was installed")
	}
	joined := MintTrace()
	joined.Parent = newSpanID()
	if !log.InstallIndependentTrace(joined, func(e Event) { got = append(got, e) }) {
		t.Fatal("valid joined identity was declined")
	}
	if root := log.IndependentRoot("keeper.root"); root != nil {
		t.Fatal("joined process created a second trace root")
	}
	span := log.Span("keeper.work")
	ctx, ok := span.Context()
	if !ok || ctx.Trace != joined.ID {
		t.Fatal("joined ordinary span did not use installed identity")
	}
	span.End()
	if len(got) != 2 || !got[0].Parent.IsZero() || got[0].Span != got[1].Span || log.traceParent != joined.Parent {
		t.Fatal("joined identity or its lane parent was not retained")
	}

	late := New(nil)
	late.Mark("already-recorded")
	if late.InstallIndependentTrace(MintTrace(), func(e Event) { t.Error("late sink received retroactive event") }) {
		t.Fatal("late trace identity was retroactively installed")
	}
	if late.TraceID() != (TraceID{}) {
		t.Fatal("declined late identity changed the collector")
	}
}

func TestIndependentTraceInstallationAndEventsAreConcurrentSafe(t *testing.T) {
	var (
		mu     sync.Mutex
		seen   = make(map[SpanID]bool)
		starts int
		ends   int
	)
	log := New(nil)
	if !log.InstallIndependentTrace(MintTrace(), func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		if e.Kind == KindStart {
			if seen[e.Span] {
				t.Error("concurrent trace emitted a duplicate span ID")
			}
			seen[e.Span] = true
			starts++
		}
		if e.Kind == KindEnd {
			ends++
		}
	}) {
		t.Fatal("trace installation failed")
	}
	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			span := log.Span("parallel")
			span.End()
		}()
	}
	close(start)
	wg.Wait()
	if starts != n || ends != n {
		t.Fatalf("concurrent trace events starts=%d ends=%d, want %d each", starts, ends, n)
	}
}
