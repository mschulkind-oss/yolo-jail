package prune

// hostarchive_test.go pins the host-render archive sweep. The archive is an UNDO buffer
// living in the user's own state dir, so the tests care most about what prune declines to
// touch: a directory it cannot account for, and the newest generations a user would actually
// reach for.

import (
	"os"
	"path/filepath"
	"testing"
)

// writeGeneration creates an archive generation holding one file of the given size.
func writeGeneration(t *testing.T, root, stamp string, size int) {
	t.Helper()
	dir := filepath.Join(root, stamp, "matt-core", "some-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func generationNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Keeps the newest N, removes the rest, oldest first. Generation names are the render's own
// timestamp stamps, so lexical order is chronological order.
func TestPruneHostArchiveKeepsNewest(t *testing.T) {
	root := t.TempDir()
	for _, stamp := range []string{
		"20260801-010000", "20260801-020000", "20260801-030000",
		"20260801-040000", "20260802-010000",
	} {
		writeGeneration(t, root, stamp, 100)
	}

	bytes, removed, names := PruneHostArchive(root, 2, true)
	if removed != 3 {
		t.Errorf("removed = %d, want 3 (5 generations, keep 2)", removed)
	}
	if bytes != 300 {
		t.Errorf("bytes = %d, want 300", bytes)
	}
	want := []string{"20260801-010000", "20260801-020000", "20260801-030000"}
	if len(names) != 3 {
		t.Fatalf("names = %v, want the 3 oldest %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("names[%d] = %q, want %q (oldest first)", i, names[i], n)
		}
	}
	// The two newest survive — those are the ones a user would reach for.
	left := generationNames(t, root)
	if len(left) != 2 {
		t.Errorf("surviving generations = %v, want the 2 newest", left)
	}
}

// Dry-run reports without touching disk, matching every other prune section.
func TestPruneHostArchiveDryRunTouchesNothing(t *testing.T) {
	root := t.TempDir()
	for _, stamp := range []string{"20260801-010000", "20260801-020000", "20260801-030000"} {
		writeGeneration(t, root, stamp, 50)
	}
	bytes, removed, _ := PruneHostArchive(root, 1, false)
	if removed != 2 || bytes != 100 {
		t.Errorf("dry run should REPORT 2 removals / 100 bytes, got %d / %d", removed, bytes)
	}
	if left := generationNames(t, root); len(left) != 3 {
		t.Errorf("dry run deleted something: %v", left)
	}
}

// A directory whose name is not a generation stamp is LEFT ALONE. Prune must not delete what
// it cannot account for — this lives in the user's state dir, and a stray dir here is more
// likely something a human put there than something yolo forgot.
func TestPruneHostArchiveIgnoresUnrecognizedDirs(t *testing.T) {
	root := t.TempDir()
	for _, stamp := range []string{"20260801-010000", "20260801-020000"} {
		writeGeneration(t, root, stamp, 10)
	}
	for _, stray := range []string{"my-backup", "20260801", "notes.txt-dir"} {
		if err := os.MkdirAll(filepath.Join(root, stray), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, removed, _ := PruneHostArchive(root, 1, true); removed != 1 {
		t.Errorf("removed = %d, want 1 (only real generations are candidates)", removed)
	}
	for _, stray := range []string{"my-backup", "20260801", "notes.txt-dir"} {
		if _, err := os.Stat(filepath.Join(root, stray)); err != nil {
			t.Errorf("prune deleted an unrecognized dir %q it cannot account for: %v", stray, err)
		}
	}
}

// Nothing to do is the common case: no archive at all (nobody has run apply --host), or
// fewer generations than the keep count.
func TestPruneHostArchiveNoopCases(t *testing.T) {
	if b, n, _ := PruneHostArchive(filepath.Join(t.TempDir(), "absent"), 3, true); b != 0 || n != 0 {
		t.Errorf("a missing archive should be a no-op, got %d bytes / %d gens", b, n)
	}
	root := t.TempDir()
	writeGeneration(t, root, "20260801-010000", 10)
	if b, n, _ := PruneHostArchive(root, 3, true); b != 0 || n != 0 {
		t.Errorf("fewer generations than keep should be a no-op, got %d / %d", b, n)
	}
	if left := generationNames(t, root); len(left) != 1 {
		t.Errorf("no-op deleted something: %v", left)
	}
}

// ── The bucket sweep (V3) ─────────────────────────────────────────────────────────────────

// EVERY BUCKET is swept, and the legacy one is a bucket like any other. This is the migration
// assertion: `archive/skills` used to be the whole archive, so a user upgrading has real
// generations sitting there — a sweep that only knew the NEW bucket names would strand them
// forever, which for an undo buffer means an unbounded leak in the user's own state dir.
func TestPruneHostArchiveBucketsSweepsLegacyAndNewBuckets(t *testing.T) {
	root := t.TempDir()
	// `skills` is the historical path (it held every kind's copies); `files` and `retired` are
	// the buckets the V3 render introduced.
	for _, bucket := range []string{"skills", "files", "retired"} {
		for _, stamp := range []string{"20260801-010000", "20260801-020000", "20260801-030000"} {
			writeGeneration(t, filepath.Join(root, bucket), stamp, 100)
		}
	}

	bytes, removed, names := PruneHostArchiveBuckets(root, 1, true)
	// Two of three generations go in EACH bucket: the keep count is per bucket, because each
	// one is its own undo buffer and an apply that touched only skills has no business evicting
	// the generation holding the user's replaced `files` copy.
	if removed != 6 {
		t.Errorf("removed = %d, want 6 (3 buckets × 2 doomed generations)", removed)
	}
	if bytes != 600 {
		t.Errorf("bytes = %d, want 600", bytes)
	}
	want := []string{
		"files/20260801-010000", "files/20260801-020000",
		"retired/20260801-010000", "retired/20260801-020000",
		"skills/20260801-010000", "skills/20260801-020000",
	}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			// Bucket-qualified, because one apply archives into several buckets under ONE
			// stamp — a bare list would print the same name three times with no way to tell
			// which copy went.
			t.Errorf("names[%d] = %q, want %q", i, names[i], w)
		}
	}
	for _, bucket := range []string{"skills", "files", "retired"} {
		if left := generationNames(t, filepath.Join(root, bucket)); len(left) != 1 {
			t.Errorf("bucket %q kept %v, want the single newest", bucket, left)
		}
	}
}

// A bucket holding NO generation is left alone, and so is a stray file at the archive root.
// Same rule as the stamp check one level down: prune does not delete what it cannot account
// for, and this directory is in the user's own state dir.
func TestPruneHostArchiveBucketsLeavesUnaccountedEntriesAlone(t *testing.T) {
	root := t.TempDir()
	writeGeneration(t, filepath.Join(root, "skills"), "20260801-010000", 10)
	writeGeneration(t, filepath.Join(root, "skills"), "20260801-020000", 10)
	if err := os.MkdirAll(filepath.Join(root, "my-own-backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, removed, _ := PruneHostArchiveBuckets(root, 1, true); removed != 1 {
		t.Errorf("removed = %d, want 1 (only real generations inside buckets are candidates)", removed)
	}
	for _, keep := range []string{"my-own-backup", "README"} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Errorf("prune removed %q, which it cannot account for: %v", keep, err)
		}
	}
}

// A missing archive root is the normal case — nobody has run `apply --host` yet.
func TestPruneHostArchiveBucketsMissingRootIsANoop(t *testing.T) {
	b, n, names := PruneHostArchiveBuckets(filepath.Join(t.TempDir(), "absent"), 3, true)
	if b != 0 || n != 0 || names != nil {
		t.Errorf("a missing archive root should be a no-op, got %d bytes / %d gens / %v", b, n, names)
	}
}

// keep=0 clears the archive entirely — a legitimate "reclaim everything" request, and the
// boundary most likely to be off by one.
func TestPruneHostArchiveKeepZeroClearsAll(t *testing.T) {
	root := t.TempDir()
	for _, stamp := range []string{"20260801-010000", "20260801-020000"} {
		writeGeneration(t, root, stamp, 25)
	}
	if _, removed, _ := PruneHostArchive(root, 0, true); removed != 2 {
		t.Errorf("removed = %d, want 2 (keep=0 clears the archive)", removed)
	}
	if left := generationNames(t, root); len(left) != 0 {
		t.Errorf("keep=0 should leave no generations: %v", left)
	}
}
