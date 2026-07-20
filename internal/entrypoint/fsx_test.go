package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteInPlacePreservesInode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "briefing.md")
	if err := WriteStringInPlace(p, "v1", 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStringInPlace(p, "version two, longer", 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// The inode must be preserved (truncate-in-place, not unlink+recreate) —
	// this is what keeps a file->file bind mount seeing refreshes.
	if !os.SameFile(before, after) {
		t.Error("WriteInPlace changed the inode — a bind mount would stop seeing refreshes")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "version two, longer" {
		t.Errorf("content = %q", got)
	}
}

func TestClearContentsKeepsDir(t *testing.T) {
	dir := t.TempDir()
	anchor := filepath.Join(dir, "skills")
	if err := os.MkdirAll(filepath.Join(anchor, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(anchor, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err := ClearContents(anchor); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(anchor)
	if err != nil {
		t.Fatalf("anchor dir was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("ClearContents replaced the dir — a mount anchor would detach")
	}
	entries, _ := os.ReadDir(anchor)
	if len(entries) != 0 {
		t.Errorf("dir not empty after clear: %v", entries)
	}
}

func TestClearContentsMissingDirOK(t *testing.T) {
	if err := ClearContents(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Errorf("clearing a missing dir should be a no-op, got %v", err)
	}
}

func TestEnsureRelativeSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "cur")

	// Fresh create.
	if err := EnsureRelativeSymlink("../target", link); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(link)
	if err != nil || got != "../target" {
		t.Fatalf("readlink = %q, %v; want ../target", got, err)
	}

	// Idempotent: same target -> no error, unchanged.
	if err := EnsureRelativeSymlink("../target", link); err != nil {
		t.Fatal(err)
	}

	// Retarget: different target -> replaced.
	if err := EnsureRelativeSymlink("../other", link); err != nil {
		t.Fatal(err)
	}
	got, _ = os.Readlink(link)
	if got != "../other" {
		t.Errorf("retarget readlink = %q, want ../other", got)
	}

	// Replace a non-symlink file.
	plain := filepath.Join(dir, "plain")
	os.WriteFile(plain, []byte("x"), 0o644)
	if err := EnsureRelativeSymlink("dest", plain); err != nil {
		t.Fatal(err)
	}
	got, _ = os.Readlink(plain)
	if got != "dest" {
		t.Errorf("replaced-file readlink = %q, want dest", got)
	}
}
