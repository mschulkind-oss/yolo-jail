package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkLog writes a file of n bytes at <dir>/<name>, mkdir-ing the dir, and
// backdates it to mtime.
func mkLog(t *testing.T, dir, name string, n int, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), n), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPurgeAgentLogs(t *testing.T) {
	ws := t.TempDir()
	cache := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-60 * 24 * time.Hour)
	fresh := now.Add(-1 * time.Hour)

	home := filepath.Join(ws, ".yolo", "home")
	// Purgeable, OLD → removed.
	oldCopilot := mkLog(t, filepath.Join(home, "copilot", "logs"), "process-1.log", 2048, old)
	oldGeminiTmp := mkLog(t, filepath.Join(home, "gemini", "tmp"), "scratch.log", 1024, old)
	oldGlobalGemini := mkLog(t, filepath.Join(cache, "gemini-cli", "logs"), "g.log", 512, old)
	// Purgeable but FRESH → kept.
	freshCopilot := mkLog(t, filepath.Join(home, "copilot", "logs"), "process-2.log", 4096, fresh)
	// PROTECTED: Claude transcripts must never be purged, even when old.
	transcript := mkLog(t, filepath.Join(home, "claude", "projects", "-workspace"), "session.jsonl", 8192, old)

	// Dry-run: accurate counts, no mutation. 2048+1024+512 = 3584 across 3 files.
	b, f := PurgeAgentLogs([]string{ws}, cache, 30, false, now)
	if b != 3584 || f != 3 {
		t.Errorf("dry-run = (%d, %d), want (3584, 3)", b, f)
	}
	for _, p := range []string{oldCopilot, oldGeminiTmp, oldGlobalGemini, freshCopilot, transcript} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dry-run deleted %s", p)
		}
	}

	// Apply: the three old logs go; fresh log and the transcript stay.
	b, f = PurgeAgentLogs([]string{ws}, cache, 30, true, now)
	if b != 3584 || f != 3 {
		t.Errorf("apply = (%d, %d), want (3584, 3)", b, f)
	}
	for _, gone := range []string{oldCopilot, oldGeminiTmp, oldGlobalGemini} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("apply did not remove %s", gone)
		}
	}
	if _, err := os.Stat(freshCopilot); err != nil {
		t.Errorf("apply removed the fresh log %s", freshCopilot)
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Errorf("apply removed a Claude transcript %s — durable user data must be preserved", transcript)
	}
}

// TestPurgeAgentLogsNoDirs: missing workspace/cache trees are a clean no-op.
func TestPurgeAgentLogsNoDirs(t *testing.T) {
	if b, f := PurgeAgentLogs([]string{filepath.Join(t.TempDir(), "absent")}, filepath.Join(t.TempDir(), "absent"), 30, true, time.Now()); b != 0 || f != 0 {
		t.Errorf("no dirs → want (0,0), got (%d,%d)", b, f)
	}
}

// TestPurgeAgentLogsSkipsSymlink: a symlinked log file is never followed or removed.
func TestPurgeAgentLogsSkipsSymlink(t *testing.T) {
	ws := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-60 * 24 * time.Hour)
	logs := filepath.Join(ws, ".yolo", "home", "copilot", "logs")
	target := mkLog(t, ws, "real-target.log", 1000, old)
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(logs, "linked.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if b, f := PurgeAgentLogs([]string{ws}, t.TempDir(), 30, true, now); b != 0 || f != 0 {
		t.Errorf("symlink must be skipped → want (0,0), got (%d,%d)", b, f)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("symlink was removed: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("symlink target was removed: %v", err)
	}
}
