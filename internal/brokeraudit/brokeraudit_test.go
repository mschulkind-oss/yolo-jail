package brokeraudit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func exitCode(n int) *int { return &n }

func TestAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker", "audit.jsonl")
	l := Open(path, nil)
	l.now = func() time.Time { return time.Date(2026, 9, 29, 14, 7, 0, 0, time.UTC) }
	l.Append(Event{Event: "call", Service: "github", Jail: "abc", Argv: []string{"pr", "view", "1"},
		Set: "read-only", Outcome: "ran", Exit: exitCode(0)})
	l.Append(Event{Event: "call", Service: "github", Jail: "abc", Argv: []string{"auth", "token"},
		Set: "refused", Outcome: "refused", Reason: "never brokered"})

	events, skipped, err := Read(path)
	if err != nil || skipped != 0 {
		t.Fatalf("Read: %v, skipped %d", err, skipped)
	}
	if len(events) != 2 || events[0].Time != "2026-09-29T14:07:00Z" || events[1].Set != "refused" {
		t.Fatalf("events %+v", events)
	}
	if *events[0].Exit != 0 || events[1].Exit != nil {
		t.Fatalf("exit codes %+v", events)
	}
}

// The log carries every workspace's argv, so it is owner-only, in an owner-only
// directory (BB-D15).
func TestLogIsOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broker")
	path := filepath.Join(dir, "audit.jsonl")
	Open(path, nil).Append(Event{Event: "call"})
	for p, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode %o, want %o", p, got, want)
		}
	}
}

func TestRotationKeepsFourArchives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l := Open(path, nil)
	l.rotateAt = 200
	for i := 0; i < 40; i++ {
		l.Append(Event{Event: "call", Service: "github", Argv: []string{strings.Repeat("x", 60)}})
	}
	files := Files(path)
	if len(files) != Archives+1 {
		t.Fatalf("files %v, want the log and %d archives", files, Archives)
	}
	if _, err := os.Stat(path + ".5"); !os.IsNotExist(err) {
		t.Fatalf("a fifth archive survived: %v", err)
	}
	for _, f := range files {
		fi, _ := os.Stat(f)
		if fi.Size() > 400 {
			t.Errorf("%s is %d bytes; rotation should bound it", f, fi.Size())
		}
	}
}

// Concurrent writers — several brokers on one machine — never tear a line.
func TestConcurrentAppendsStayWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := Open(path, nil)
			for i := 0; i < 25; i++ {
				l.Append(Event{Event: "call", Argv: []string{strings.Repeat("y", 3000)}})
			}
		}()
	}
	wg.Wait()
	events, skipped, err := Read(path)
	if err != nil || skipped != 0 || len(events) != 200 {
		t.Fatalf("read %d events, skipped %d, err %v", len(events), skipped, err)
	}
}

// A failing write warns once and never panics or blocks the call.
func TestWriteFailureWarnsOnce(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	l := Open(filepath.Join(blocker, "audit.jsonl"), func(s string) { warnings = append(warnings, s) })
	l.Append(Event{Event: "call"})
	l.Append(Event{Event: "call"})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "calls are not blocked") {
		t.Fatalf("warnings %q", warnings)
	}
}
