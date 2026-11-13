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

func TestMCPAuthAuditFieldsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker", "audit.jsonl")
	Open(path, nil).Append(Event{Service: "mcp-auth", Event: "refresh", Server: "linear", Generation: 4, Outcome: "ok"})

	events, skipped, err := Read(path)
	if err != nil || skipped != 0 || len(events) != 1 {
		t.Fatalf("Read: %v, skipped %d, events %+v", err, skipped, events)
	}
	got := events[0]
	if got.Service != "mcp-auth" || got.Event != "refresh" || got.Server != "linear" || got.Generation != 4 || got.Outcome != "ok" {
		t.Fatalf("event = %+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, `"server":"linear"`) || !strings.Contains(line, `"generation":4`) {
		t.Fatalf("persisted event omitted its server/generation: %s", line)
	}
	for _, secret := range []string{"access_token", "refresh_token", "Authorization", "Bearer ", "header-secret"} {
		if strings.Contains(line, secret) {
			t.Errorf("persisted event contains sensitive material %q: %s", secret, line)
		}
	}
}

func TestLegacyEventOmitsNewMCPFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker", "audit.jsonl")
	Open(path, nil).Append(Event{Service: "github", Event: "call", Argv: []string{"pr", "list"}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if strings.Contains(line, `"server"`) || strings.Contains(line, `"generation"`) {
		t.Fatalf("legacy event gained empty MCP fields: %s", line)
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

// A git push's line carries the remote and one entry per ref (docs/reference/git-broker.md ID-13); a gh
// line, which has neither, writes neither key.
func TestGitRemoteAndRefsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker", "audit.jsonl")
	l := Open(path, nil)
	l.Append(Event{Event: "call", Service: "git", Argv: []string{"git-receive-pack"}, Repo: "github.com/o/r",
		Remote: "git@github.com:o/r.git", Set: "refused", Outcome: "refused",
		Refs: []RefUpdate{{Ref: "refs/heads/main", Old: "a", New: "b"}, {Ref: "refs/heads/agent/x", Old: "0", New: "c", CoveredBy: "w-1"}}})
	l.Append(Event{Event: "call", Service: "github", Argv: []string{"pr", "view"}, Set: "read-only", Outcome: "ran"})
	events, _, err := Read(path)
	if err != nil || len(events) != 2 {
		t.Fatalf("Read: %v %+v", err, events)
	}
	if events[0].Remote != "git@github.com:o/r.git" || len(events[0].Refs) != 2 || events[0].Refs[1].CoveredBy != "w-1" {
		t.Fatalf("git event %+v", events[0])
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !strings.Contains(lines[0], `"refs":[{"ref":"refs/heads/main","old":"a","new":"b"},{"ref":"refs/heads/agent/x","old":"0","new":"c","covered_by":"w-1"}]`) {
		t.Fatalf("git line %s", lines[0])
	}
	if strings.Contains(lines[1], `"remote"`) || strings.Contains(lines[1], `"refs"`) {
		t.Fatalf("gh line carries git keys: %s", lines[1])
	}
}
