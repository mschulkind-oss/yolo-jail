package perf

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is the injected seam: tests step it explicitly so durations are
// exact, not slept-for. Mutex-guarded because the seam's contract is
// "concurrency-safe like time.Now" — Span calls it from goroutines.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
}

func TestSpanRecordsStartEndAndDuration(t *testing.T) {
	c := newFakeClock()
	var got []Event
	l := New(c.Now, func(e Event) { got = append(got, e) })

	s := l.Span("probes")
	c.Advance(500 * time.Millisecond)
	s.End()

	if len(got) != 2 {
		t.Fatalf("expected start+end events, got %d: %+v", len(got), got)
	}
	if got[0].Kind != KindStart || got[0].Name != "probes" {
		t.Errorf("first event = %+v, want KindStart probes", got[0])
	}
	if got[1].Kind != KindEnd || got[1].Dur != 500*time.Millisecond {
		t.Errorf("end event = %+v, want KindEnd dur=500ms", got[1])
	}
}

func TestMarkRecordsPointEvent(t *testing.T) {
	c := newFakeClock()
	var got []Event
	l := New(c.Now, func(e Event) { got = append(got, e) })

	c.Advance(2 * time.Second)
	l.Mark("child.exited")

	if len(got) != 1 || got[0].Kind != KindMark || got[0].Name != "child.exited" {
		t.Fatalf("got %+v, want one mark child.exited", got)
	}
	if el := got[0].At.Sub(l.start); el != 2*time.Second {
		t.Errorf("mark elapsed = %v, want 2s", el)
	}
}

func TestNilAndDisabledLogsAreNoOps(t *testing.T) {
	var nilLog *Log
	if s := nilLog.Span("x"); s != nil {
		t.Error("nil Log returned a non-nil Span")
	}
	nilLog.Span("x").End() // must not panic
	nilLog.Mark("x")

	d := Disabled()
	if s := d.Span("x"); s != nil {
		t.Error("Disabled Log returned a non-nil Span")
	}
	d.Mark("x")
	var buf bytes.Buffer
	d.Report(&buf, time.Now())
	if buf.Len() != 0 {
		t.Errorf("Disabled Report wrote %q", buf.String())
	}

	// The zero Log is inert too — no one can construct a panicking one.
	var zero Log
	zero.Mark("x")
}

func TestSpanEndIsIdempotent(t *testing.T) {
	c := newFakeClock()
	var ends int
	l := New(c.Now, func(e Event) {
		if e.Kind == KindEnd {
			ends++
		}
	})

	s := l.Span("double")
	s.End()
	c.Advance(time.Second)
	s.End()
	if ends != 1 {
		t.Errorf("ends = %d, want 1 (End must be idempotent)", ends)
	}
}

func TestReportRendersRegister(t *testing.T) {
	c := newFakeClock()
	l := New(c.Now)

	s := l.Span("probes")
	c.Advance(400 * time.Millisecond)
	s.End()
	s = l.Span("image.load")
	c.Advance(2100 * time.Millisecond)
	s.End()
	c.Advance(50 * time.Millisecond)
	l.Mark("spawned")

	var buf bytes.Buffer
	l.Report(&buf, c.Now())

	out := buf.String()
	// Same register as the entrypoint's dump: elapsed, +delta, label — with a
	// duration column the entrypoint has no need for. Pinned byte-exact.
	want := "    0.400s               0.400s  probes\n" +
		"    2.500s    +2.100s    2.100s  image.load\n" +
		"    2.550s    +0.050s         -  spawned\n" +
		"  Total: 2.550s\n"
	if out != want {
		t.Errorf("report = %q,\nwant %q", out, want)
	}
	if strings.Contains(out, "start") {
		t.Errorf("report renders start events; it must not:\n%s", out)
	}
}

func TestReportEmptyLogWritesNothing(t *testing.T) {
	l := New(newFakeClock().Now)
	var buf bytes.Buffer
	l.Report(&buf, time.Now())
	if buf.Len() != 0 {
		t.Errorf("empty Log reported %q", buf.String())
	}
}

func TestConcurrentSpansAreAllRecorded(t *testing.T) {
	c := newFakeClock()
	l := New(c.Now)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := l.Span("parallel")
			c.Advance(time.Duration(i+1) * time.Millisecond)
			s.End()
		}(i)
	}
	wg.Wait()

	l.mu.Lock()
	defer l.mu.Unlock()
	starts, ends := 0, 0
	for _, e := range l.events {
		switch e.Kind {
		case KindStart:
			starts++
		case KindEnd:
			ends++
		}
	}
	if starts != n || ends != n {
		t.Errorf("recorded %d starts / %d ends, want %d/%d", starts, ends, n, n)
	}
}
