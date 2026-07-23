package prune

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSweepDanglingOutLinks(t *testing.T) {
	buildDir := t.TempDir()

	// A live target + a link to it (must be KEPT — an in-flight build's root).
	liveTarget := filepath.Join(buildDir, "live-store-path")
	if err := os.Mkdir(liveTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(liveTarget, filepath.Join(buildDir, "run-result-100")); err != nil {
		t.Fatal(err)
	}
	// A dangling out-link (target gone → REAPED).
	if err := os.Symlink(filepath.Join(buildDir, "gone"), filepath.Join(buildDir, "run-result-200")); err != nil {
		t.Fatal(err)
	}
	// A dangling NON-out-link (restore-result, roots/) → must be UNTOUCHED.
	if err := os.Symlink(filepath.Join(buildDir, "gone2"), filepath.Join(buildDir, "restore-result")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(buildDir, "roots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(buildDir, "gone3"), filepath.Join(buildDir, "roots", "deadbeef00000000")); err != nil {
		t.Fatal(err)
	}
	// A regular file named run-result-* (not a symlink) → must be UNTOUCHED.
	if err := os.WriteFile(filepath.Join(buildDir, "run-result-notlink"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry-run: reports the one dangling out-link, deletes nothing.
	got := SweepDanglingOutLinks(buildDir, false)
	want := []string{filepath.Join(buildDir, "run-result-200")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dry-run swept = %v, want %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(buildDir, "run-result-200")); err != nil {
		t.Error("dry-run must not delete the dangling link")
	}

	// Apply: removes only the dangling out-link.
	SweepDanglingOutLinks(buildDir, true)
	if _, err := os.Lstat(filepath.Join(buildDir, "run-result-200")); !os.IsNotExist(err) {
		t.Error("apply must remove the dangling out-link")
	}
	if _, err := os.Lstat(filepath.Join(buildDir, "run-result-100")); err != nil {
		t.Error("the live out-link must be kept")
	}
	if _, err := os.Lstat(filepath.Join(buildDir, "restore-result")); err != nil {
		t.Error("restore-result must never be swept")
	}
	if _, err := os.Lstat(filepath.Join(buildDir, "roots", "deadbeef00000000")); err != nil {
		t.Error("a roots/<sha16> GC root must never be swept by the out-link sweep")
	}
	if _, err := os.Lstat(filepath.Join(buildDir, "run-result-notlink")); err != nil {
		t.Error("a regular file named run-result-* must be kept")
	}
}

func TestSweepDanglingOutLinksNoDir(t *testing.T) {
	if got := SweepDanglingOutLinks(filepath.Join(t.TempDir(), "nope"), true); len(got) != 0 {
		t.Errorf("missing build dir → want empty, got %v", got)
	}
}
