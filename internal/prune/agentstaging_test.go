package prune

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// mkStaging creates AGENTS_DIR/<name> with `bytes` of content and ages it.
func mkStaging(t *testing.T, agentsDir, name string, bytes int, age time.Duration, now time.Time) {
	t.Helper()
	dir := filepath.Join(agentsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if bytes > 0 {
		if err := os.WriteFile(filepath.Join(dir, "briefing.md"), make([]byte, bytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	when := now.Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestPruneOrphanAgentStaging(t *testing.T) {
	agentsDir := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	old := 48 * time.Hour

	mkStaging(t, agentsDir, "yolo-live", 100, old, now)    // live → keep
	mkStaging(t, agentsDir, "yolo-tracked", 100, old, now) // tracked → keep
	mkStaging(t, agentsDir, "yolo-orphan", 4096, old, now) // orphan + old → reap
	mkStaging(t, agentsDir, "yolo-recent", 100, time.Minute, now)

	known := map[string]struct{}{"yolo-live": {}, "yolo-tracked": {}}
	gotBytes, gotDirs, gotNames := PruneOrphanAgentStaging(agentsDir, known, true, time.Hour, false, now)
	if gotDirs != 1 || gotBytes != 4096 {
		t.Errorf("dry-run = (%d bytes, %d dirs), want (4096, 1)", gotBytes, gotDirs)
	}
	if !reflect.DeepEqual(gotNames, []string{"yolo-orphan"}) {
		t.Errorf("reaped names = %v, want [yolo-orphan]", gotNames)
	}
	// Dry-run mutated nothing.
	if _, err := os.Stat(filepath.Join(agentsDir, "yolo-orphan")); err != nil {
		t.Error("dry-run deleted the orphan dir")
	}

	// Apply removes only the orphan.
	PruneOrphanAgentStaging(agentsDir, known, true, time.Hour, true, now)
	if _, err := os.Stat(filepath.Join(agentsDir, "yolo-orphan")); !os.IsNotExist(err) {
		t.Error("apply must remove the orphan staging dir")
	}
	for _, keep := range []string{"yolo-live", "yolo-tracked", "yolo-recent"} {
		if _, err := os.Stat(filepath.Join(agentsDir, keep)); err != nil {
			t.Errorf("%s must be kept", keep)
		}
	}
}

func TestPruneOrphanAgentStagingFailSafe(t *testing.T) {
	agentsDir := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	mkStaging(t, agentsDir, "yolo-orphan", 4096, 48*time.Hour, now)

	// Liveness unknown → reap NOTHING, even a clearly-old orphan.
	b, d, names := PruneOrphanAgentStaging(agentsDir, nil, false, time.Hour, true, now)
	if b != 0 || d != 0 || len(names) != 0 {
		t.Errorf("unknown liveness must decline: got (%d,%d,%v)", b, d, names)
	}
	if _, err := os.Stat(filepath.Join(agentsDir, "yolo-orphan")); err != nil {
		t.Error("fail-safe decline must not delete anything")
	}
}

func TestPruneOrphanAgentStagingSkipsSymlink(t *testing.T) {
	agentsDir := t.TempDir()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	// A symlink entry under agents/ is not a staging dir — never followed/removed.
	target := t.TempDir()
	link := filepath.Join(agentsDir, "yolo-symlink")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	when := now.Add(-48 * time.Hour)
	_ = os.Chtimes(target, when, when)
	_, d, _ := PruneOrphanAgentStaging(agentsDir, map[string]struct{}{}, true, time.Hour, true, now)
	if d != 0 {
		t.Errorf("a symlink entry must not be swept, got %d dirs", d)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("the symlink must be left in place")
	}
}

func TestTrackedContainerNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo-a"), []byte("/ws/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo-b"), []byte("/ws/b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A subdir is not a tracking file → ignored.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := TrackedContainerNames(dir)
	if _, ok := got["yolo-a"]; !ok {
		t.Error("yolo-a should be tracked")
	}
	if _, ok := got["yolo-b"]; !ok {
		t.Error("yolo-b should be tracked")
	}
	if _, ok := got["subdir"]; ok {
		t.Error("a subdir must not count as a tracking file")
	}
	if got := TrackedContainerNames(filepath.Join(dir, "nope")); len(got) != 0 {
		t.Errorf("missing dir → empty, got %v", got)
	}
}
