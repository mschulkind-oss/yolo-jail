package perf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileSinkWritesHeaderAndIncrementalLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host-perf.log")
	var errb bytes.Buffer
	c := newFakeClock()

	sink := FileSink(path, "yolo-ws-abcd1234", &errb, c.Now())
	sink(Event{Kind: KindStart, Name: "teardown.ports", At: c.Now()})
	c.Advance(1500 * time.Millisecond)
	sink(Event{Kind: KindEnd, Name: "teardown.ports", At: c.Now(), Dur: 1500 * time.Millisecond})
	sink(Event{Kind: KindMark, Name: "child.exited", At: c.Now()})

	if errb.Len() != 0 {
		t.Fatalf("sink warned unexpectedly: %q", errb.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "=== YOLO Host Perf") || !strings.Contains(out, "jail=yolo-ws-abcd1234") {
		t.Errorf("run header wrong:\n%s", out)
	}
	// Every event is on disk the moment it happens — no end-of-run flush
	// exists (the signal arm os.Exits past defers), so this is the contract.
	for _, want := range []string{
		"start  teardown.ports\n",
		"end    teardown.ports  dur=1.500s\n",
		"mark   child.exited\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sink file missing %q; got:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(strings.SplitN(out, "\n", 2)[1], "2026-09-06T12:00:00.000") {
		t.Errorf("event lines must carry millisecond timestamps:\n%s", out)
	}
}

func TestFileSinkTrimsToMaxRunsOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host-perf.log")
	var b strings.Builder
	for i := 0; i < MaxRuns+10; i++ {
		fmt.Fprintf(&b, "=== YOLO Host Perf === run %d ===\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	var errb bytes.Buffer
	FileSink(path, "yolo-ws-abcd1234", &errb, time.Now())

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Count(string(got), runPrefix)
	// MaxRuns-1 kept runs + this run's header.
	if runs != MaxRuns {
		t.Errorf("after open, file holds %d runs, want %d", runs, MaxRuns)
	}
	if !strings.Contains(string(got), fmt.Sprintf("run %d", MaxRuns+9)) {
		t.Error("trim dropped the NEWEST run; it must keep the oldest of the trimmed window at the front")
	}
}

func TestFileSinkFailureWarnsOnceAndStaysQuiet(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should be: OpenFile fails on it.
	path := filepath.Join(dir, "occupied")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer

	sink := FileSink(path, "yolo-ws-abcd1234", &errb, time.Now())
	if errb.Len() == 0 {
		t.Fatal("a sink that cannot open its file must warn once")
	}
	warning := errb.String()

	// ...and then never again, whatever flows through it.
	for i := 0; i < 10; i++ {
		sink(Event{Kind: KindMark, Name: "x", At: time.Now()})
	}
	if errb.String() != warning {
		t.Errorf("sink warned more than once: %q", errb.String())
	}
}
