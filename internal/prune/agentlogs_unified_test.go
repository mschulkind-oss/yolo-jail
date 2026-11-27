package prune

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPurgeAgentLogsSweepsTheUnifiedHome: agent state lives in <ws>/.yolo/unified-home, so the
// log sweep reads it there (the dotted spelling of each log directory). An old log is reported by
// a dry run and removed by an apply; a link in a slice to the unified home is never followed; the
// Claude transcripts stay.
func TestPurgeAgentLogsSweepsTheUnifiedHome(t *testing.T) {
	ws := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-60 * 24 * time.Hour)
	fresh := now.Add(-time.Hour)

	unified := filepath.Join(ws, ".yolo", "unified-home")
	oldLog := mkLog(t, filepath.Join(unified, ".copilot", "logs"), "x.log", 2048, old)
	oldPi := mkLog(t, filepath.Join(unified, ".pi", "agent", "logs"), "perf.jsonl", 1024, old)
	freshLog := mkLog(t, filepath.Join(unified, ".copilot", "logs"), "y.log", 4096, fresh)
	transcript := mkLog(t, filepath.Join(unified, ".claude", "projects", "-workspace"), "s.jsonl", 8192, old)

	// A slice whose .copilot is a link into the unified home: reached through the link it would
	// be counted twice, so the sweep must not follow it.
	slice := filepath.Join(ws, ".yolo", "setups", "podman-self", "home")
	if err := os.MkdirAll(slice, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(unified, ".copilot"), filepath.Join(slice, ".copilot")); err != nil {
		t.Fatal(err)
	}

	b, f := PurgeAgentLogs([]string{ws}, "", 30, false, now)
	if b != 3072 || f != 2 {
		t.Errorf("dry run = (%d, %d), want (3072, 2)", b, f)
	}
	for _, p := range []string{oldLog, oldPi, freshLog, transcript} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dry run removed %s", p)
		}
	}

	b, f = PurgeAgentLogs([]string{ws}, "", 30, true, now)
	if b != 3072 || f != 2 {
		t.Errorf("apply = (%d, %d), want (3072, 2)", b, f)
	}
	for _, p := range []string{oldLog, oldPi} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("apply left %s (err %v)", p, err)
		}
	}
	for _, p := range []string{freshLog, transcript} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("apply removed %s", p)
		}
	}
}
