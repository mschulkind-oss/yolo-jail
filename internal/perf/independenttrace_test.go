package perf

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

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
