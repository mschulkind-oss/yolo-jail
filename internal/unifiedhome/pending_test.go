package unifiedhome

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPendingCountsOnlyWhatAMergeWouldCarryOut: regular files count; links, lock files and
// Stay paths do not, and nothing is touched.
func TestPendingCountsOnlyWhatAMergeWouldCarryOut(t *testing.T) {
	home := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/projects/a/s.jsonl")
	write(".claude/projects/a/t.jsonl")
	write(".claude/settings.json") // Stay
	write(".claude/SingletonLock") // a process file
	if err := os.Symlink("/elsewhere", filepath.Join(home, ".claude", "plugins")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	got := Pending(Source{Root: root, Dirs: []string{".claude", "claude"}, Stay: []string{"settings.json"}})
	if got != 2 {
		t.Errorf("Pending = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Errorf("Pending touched the home: %v", err)
	}
}
