package stores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock advances by step on every call, so a budget can be exhausted
// deterministically without sleeping and without a tree of thousands of files.
func fakeClock(start time.Time, step time.Duration) func() time.Time {
	cur := start
	return func() time.Time {
		t := cur
		cur = cur.Add(step)
		return t
	}
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWalkSumsApparentSize is the baseline the two failure modes below are
// deviations from.
func TestWalkSumsApparentSize(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 100)
	writeFile(t, filepath.Join(root, "sub", "b"), 200)
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	res, err := walkTree(root, time.Time{}, time.Minute, time.Now)
	if err != nil {
		t.Fatalf("walkTree: %v", err)
	}
	if res.Bytes != 300 || res.Files != 2 {
		t.Errorf("walk = %d bytes / %d files, want 300 / 2 (the symlink must not be counted or followed)",
			res.Bytes, res.Files)
	}
	if res.Partial {
		t.Error("a walk that finished reported itself partial")
	}
}

// TestWalkOutOfBudgetReportsPartial is the plan's "≥ N (partial)" requirement at
// the walker: a store bigger than its budget must yield a LOWER BOUND that says
// it is one, never a wrong total and never an error.
//
// MUTATION: delete the deadline check in walkTree and this fails — the walk
// completes, reports the full 600 bytes, and Partial is false.
func TestWalkOutOfBudgetReportsPartial(t *testing.T) {
	root := t.TempDir()
	for i, name := range []string{"a", "b", "c", "d", "e", "f"} {
		writeFile(t, filepath.Join(root, name), 100*(i+1))
	}
	// One second per clock read against a three-second budget: the walk gets the
	// root and the first file in, then must stop — a lower bound that is neither
	// zero nor the whole tree, which is exactly the case the report has to render.
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Second)
	res, err := walkTree(root, time.Time{}, 3*time.Second, clock)
	if err != nil {
		t.Fatalf("walkTree: %v", err)
	}
	if !res.Partial {
		t.Fatal("the walk exceeded its budget and did not say so — a figure that is a lower " +
			"bound must be labelled one")
	}
	if res.Bytes == 0 {
		t.Error("a partial walk reported 0 bytes; it must report what it summed so far")
	}
	if res.Bytes >= 2100 {
		t.Errorf("a partial walk reported the whole tree (%d bytes); the budget did nothing", res.Bytes)
	}

	// And the row built from it renders as a lower bound with the reason attached.
	s := Store{}
	o := Options{Budget: 3 * time.Second, Walk: func(string, time.Time, time.Duration, func() time.Time) (WalkResult, error) {
		return res, nil
	}}
	fillDefaults(&o)
	sizeStore(&s, root, o)
	if s.Sizing != SizingPartial {
		t.Errorf("sizing = %q, want %q", s.Sizing, SizingPartial)
	}
	if got := sizeCell(s); !strings.HasPrefix(got, "≥ ") {
		t.Errorf("a partial size renders as %q; it must be shown as a lower bound", got)
	}
	if s.Reason == "" {
		t.Error("a partial row named no reason; the report never prints a figure without saying how it was obtained")
	}
}

// TestAStoreThatCannotBeReadIsUnknownNotZero is the degenerate input §5.5 calls
// out by name. Zero and "I could not look" are different facts, and printing the
// first for the second is how a store nobody can read comes to look like a store
// nobody needs.
//
// The unreadable case is a root that is not a regular file or directory (a
// dangling symlink), NOT a chmod 000 directory: this suite runs as root in some
// environments, where 000 is still readable and the test would silently pass for
// the wrong reason.
func TestAStoreThatCannotBeReadIsUnknownNotZero(t *testing.T) {
	root := t.TempDir()
	weird := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "nothing-here"), weird); err != nil {
		t.Fatal(err)
	}
	o := Options{}
	fillDefaults(&o)

	var s Store
	sizeStore(&s, weird, o)
	if s.Sizing != SizingUnknown {
		t.Fatalf("sizing = %q, want %q — an unreadable store must never report a size", s.Sizing, SizingUnknown)
	}
	if s.Bytes != 0 || sizeCell(s) != "unknown" {
		t.Errorf("unknown store rendered as %q (%d bytes); it must not render as a number", sizeCell(s), s.Bytes)
	}
	if s.Reason == "" {
		t.Error("an unknown store gave no reason; §5.5 requires the reason with the verdict")
	}

	// An ABSENT store is a different answer again: 0 bytes and NOT an error,
	// because a fresh machine has almost none of these.
	var missing Store
	sizeStore(&missing, filepath.Join(root, "not-created"), o)
	if missing.Sizing != SizingAbsent {
		t.Errorf("a missing store reported %q, want %q", missing.Sizing, SizingAbsent)
	}
	if sizeCell(missing) != "absent" {
		t.Errorf("a missing store rendered as %q, want \"absent\"", sizeCell(missing))
	}
}

// TestWalkCountsASingleFileStorePath: not every store path is a tree. Every
// *-stream-yolo-jail output in the nix store is one script, and treating that as
// unreadable reported a whole class as "0 B, partial" — a wrong number wearing a
// plausible label.
func TestWalkCountsASingleFileStorePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "stream-script")
	writeFile(t, file, 512)
	res, err := walkTree(file, time.Time{}, time.Minute, time.Now)
	if err != nil {
		t.Fatalf("walkTree on a regular file: %v", err)
	}
	if res.Bytes != 512 || res.Files != 1 {
		t.Errorf("walk = %d bytes / %d files, want 512 / 1", res.Bytes, res.Files)
	}
}

// TestAgeCutoffCountsOnlyOldFiles pins --age's accounting against the same mtime
// rule prune.PurgeCacheByAge applies (mtime >= cutoff is KEPT).
func TestAgeCutoffCountsOnlyOldFiles(t *testing.T) {
	root := t.TempDir()
	old, fresh := filepath.Join(root, "old"), filepath.Join(root, "fresh")
	writeFile(t, old, 1000)
	writeFile(t, fresh, 10)
	past := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	res, err := walkTree(root, cutoff, time.Minute, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 1010 {
		t.Errorf("total = %d, want 1010", res.Bytes)
	}
	if res.DeadBytes != 1000 || res.DeadFiles != 1 {
		t.Errorf("older-than = %d bytes / %d files, want 1000 / 1", res.DeadBytes, res.DeadFiles)
	}
}
