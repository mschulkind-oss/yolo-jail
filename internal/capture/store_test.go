package capture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// stage builds a fixture capture in the store's own staging area and returns its path. Every test
// stages through Store.Stage rather than t.TempDir(), which is the same discipline the real driver
// is held to: admission is a rename, so the scratch tree has to be inside the store.
func stage(t *testing.T, s *Store, id string) string {
	t.Helper()
	dir, err := s.Stage(id)
	must(t, err)
	must(t, os.MkdirAll(filepath.Join(dir, "versions", "1.0.0"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "versions", "1.0.0", "claude"), []byte("binary bytes"), 0o755))
	must(t, os.Chmod(filepath.Join(dir, "versions", "1.0.0", "claude"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "versions", "1.0.0", "README"), []byte("docs"), 0o644))
	// An installer's tree contains absolute self-references — claude's ~/.local/bin/claude is
	// one, measured in this jail — so every fixture has one.
	must(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	must(t, os.Symlink("/home/agent/.local/versions/1.0.0/claude", filepath.Join(dir, "bin", "claude")))
	return dir
}

func ino(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	must(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	return st.Ino
}

// A fresh admit files the tree under its own content address, hands back a usable entry, and
// leaves every FILE in it unwritable while leaving every DIRECTORY writable.
//
// The file half is the trap the design names: a materialized file is a hardlink to this inode, so
// it is not a copy of the running program's bytes, it IS them — an installer that opens one for
// write corrupts every workspace on the machine at once. The directory half is why the freeze is
// files-only: GC has to be able to unlink a whole entry.
func TestAdmitStoresTheTreeAndFreezesItsFiles(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	staged := stage(t, s, "run-1")

	entry, err := s.Admit(staged)
	must(t, err)

	if entry.Root != filepath.Join(s.Dir, "entries", entry.Key) || entry.Tree != filepath.Join(entry.Root, "tree") {
		t.Errorf("layout drifted: root=%q tree=%q", entry.Root, entry.Tree)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Errorf("the staged tree must be consumed by the admit, not copied: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(entry.Tree, "versions", "1.0.0", "claude")); err != nil ||
		string(got) != "binary bytes" {
		t.Errorf("entry content = %q, %v", got, err)
	}
	// The symlink survives as a symlink, with its target intact — materialize has to reproduce
	// the installer's own links, not what they pointed at on the capture machine.
	link, err := os.Readlink(filepath.Join(entry.Tree, "bin", "claude"))
	must(t, err)
	if link != "/home/agent/.local/versions/1.0.0/claude" {
		t.Errorf("symlink target = %q", link)
	}

	must(t, filepath.Walk(entry.Tree, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			if fi.Mode().Perm()&0o200 == 0 {
				t.Errorf("%s: directories must stay writable or GC cannot unlink the entry (mode %v)",
					path, fi.Mode())
			}
		case fi.Mode().IsRegular():
			if fi.Mode().Perm()&0o222 != 0 {
				t.Errorf("%s: a CAS file is the running program's bytes — admit must drop w "+
					"(mode %v)", path, fi.Mode())
			}
		}
		return nil
	}))
	// Freezing must not have cost the exec bit: the whole tree is meant to be run from.
	fi, err := os.Stat(filepath.Join(entry.Tree, "versions", "1.0.0", "claude"))
	must(t, err)
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the freeze ate the exec bit: mode %v", fi.Mode())
	}

	got, err := s.Resolve(entry.Key)
	must(t, err)
	if got.Tree != entry.Tree {
		t.Errorf("Resolve after Admit = %q, want %q", got.Tree, entry.Tree)
	}
}

// THE property the whole CAS rests on: an admit interrupted before its completion marker lands
// reads as ABSENT, not as an entry, and the next admit REBUILDS it from scratch rather than
// adopting whatever the dead run left behind.
//
// A torn entry is an installer's half-written state. Resolving it would materialize a broken
// program into a home; merging into it would produce a tree that no single installer run ever
// produced while the key claims otherwise.
func TestTornAdmitReadsAsAbsentAndIsRedone(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	entry, err := s.Admit(stage(t, s, "run-1"))
	must(t, err)
	key := entry.Key

	// Tear it exactly the way a kill -9 between the rename and the marker write would: the
	// tree is there, damaged, and the marker is not.
	must(t, os.Remove(filepath.Join(entry.Root, completeMarker)))
	must(t, os.Chmod(filepath.Join(entry.Tree, "versions", "1.0.0"), 0o755))
	must(t, os.Remove(filepath.Join(entry.Tree, "versions", "1.0.0", "README")))
	must(t, os.WriteFile(filepath.Join(entry.Tree, "half-written-junk"), []byte("x"), 0o644))

	if _, err := s.Resolve(key); !errors.Is(err, ErrNotCaptured) {
		t.Fatalf("a torn entry must resolve as ABSENT, got %v", err)
	}

	redone, err := s.Admit(stage(t, s, "run-2"))
	must(t, err)
	if redone.Key != key {
		t.Errorf("the redo must land on the same key (the content is the same): %q vs %q", redone.Key, key)
	}
	if _, err := s.Resolve(key); err != nil {
		t.Errorf("the redo must produce a complete entry: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(redone.Tree, "half-written-junk")); !os.IsNotExist(err) {
		t.Error("the redo must clear the torn tree wholesale — a leftover file means the new " +
			"entry does not match the key that addresses it")
	}
	if _, err := os.Stat(filepath.Join(redone.Tree, "versions", "1.0.0", "README")); err != nil {
		t.Errorf("the redo must restore the full tree: %v", err)
	}
	// And the rebuilt entry really is what its key says it is.
	digest, err := treedigest.Of(redone.Tree)
	must(t, err)
	if Key(digest) != key {
		t.Errorf("the redone tree digests to %q, which is not its address %q", Key(digest), key)
	}
}

// Re-admitting an identical tree returns the entry already on disk WITHOUT rebuilding it. The
// inode is the assertion: a workspace may already hold one of these by hardlink, and swapping it
// out would be a silent version change under a running program.
func TestAdmitIsIdempotentAndKeepsTheExistingInodes(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	first, err := s.Admit(stage(t, s, "run-1"))
	must(t, err)
	before := ino(t, filepath.Join(first.Tree, "versions", "1.0.0", "claude"))

	staged := stage(t, s, "run-2")
	second, err := s.Admit(staged)
	must(t, err)
	if second.Key != first.Key {
		t.Fatalf("identical trees must share a key: %q vs %q", second.Key, first.Key)
	}
	if after := ino(t, filepath.Join(second.Tree, "versions", "1.0.0", "claude")); after != before {
		t.Error("a repeat admit must leave the existing entry's inodes alone — a materialized " +
			"hardlink in some workspace is pointing at them")
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Error("a repeat admit must still consume the duplicate staged tree, not leak it into " +
			"staging/ forever")
	}

	// Different bytes, different address.
	other := stage(t, s, "run-3")
	must(t, os.WriteFile(filepath.Join(other, "versions", "1.0.0", "README"), []byte("v2 docs"), 0o644))
	third, err := s.Admit(other)
	must(t, err)
	if third.Key == first.Key {
		t.Error("a changed tree must get a different key")
	}
}

// Resolve is the MATERIALIZE path: it runs per workspace, mid-launch, and must never turn into a
// download. A miss is ErrNotCaptured (so the launcher's branch can fall through to today's
// download) carrying a message that names the act which would fix it.
func TestResolveIsOfflineAndNamesTheCaptureAct(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	_, err := s.Resolve("0123456789abcdef")
	if !errors.Is(err, ErrNotCaptured) {
		t.Fatalf("a miss must be ErrNotCaptured, got %v", err)
	}
	if !strings.Contains(err.Error(), "yolo capture") {
		t.Errorf("the miss must name the fetch act, got %q", err)
	}
	// A failed resolve is a pure read: it must not create the entry it did not find, or the
	// next admit would see a torn entry it has to clear.
	if _, statErr := os.Stat(filepath.Join(s.Dir, "entries")); statErr == nil {
		t.Error("Resolve created entries/ — resolution must not write")
	}
}

// Admitting from outside the store is REFUSED rather than handled. Inside one filesystem the
// admit is a rename; from /tmp on another it silently becomes a 1.2 GB copy — the exact cost this
// subsystem exists to delete — so the cheap-looking mistake is made unrepresentable.
func TestAdmitRefusesAStagedTreeOutsideTheStore(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	outside := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outside, "f"), []byte("x"), 0o644))

	_, err := s.Admit(outside)
	if err == nil {
		t.Fatal("admitting a tree from outside the store must fail")
	}
	if !strings.Contains(err.Error(), "os.Rename") {
		t.Errorf("the refusal must say why, got %q", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.Dir, "entries")); statErr == nil {
		t.Error("the refused admit created entries/")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "f")); statErr != nil {
		t.Error("the refused admit must leave the caller's tree alone")
	}
}

// Stage hands back an EMPTY dir even when a previous run died inside it. A redo that inherited a
// dead run's files would admit a tree no installer run ever produced.
func TestStageClearsLeftoverScratch(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	dir, err := s.Stage("run-1")
	must(t, err)
	if !strings.HasPrefix(dir, s.Dir+string(filepath.Separator)) {
		t.Fatalf("staging %q must be inside the store %q", dir, s.Dir)
	}
	must(t, os.WriteFile(filepath.Join(dir, "leftover"), []byte("x"), 0o644))

	again, err := s.Stage("run-1")
	must(t, err)
	entries, err := os.ReadDir(again)
	must(t, err)
	if len(entries) != 0 {
		t.Errorf("Stage must clear leftover scratch, found %v", entries)
	}

	// An id is a single path segment, so no caller can address a directory outside staging/.
	for _, bad := range []string{"", ".", "..", "a/b", "../escape"} {
		if _, err := s.Stage(bad); err == nil {
			t.Errorf("Stage(%q) must be refused", bad)
		}
	}
}

// One key convention across the repo's content-addressed directories, not two. `entries/3f2a…`
// and `build/roots/3f2a…` mean the same kind of thing because they are computed the same way.
func TestKeyIsTheImageStoreKeyConvention(t *testing.T) {
	const sample = "sha256:whatever-canonical-digest"
	if got, want := Key(sample), image.ImageStoreKey(sample); got != want {
		t.Errorf("Key = %q, want image.ImageStoreKey's %q", got, want)
	}
	if len(Key(sample)) != 16 {
		t.Errorf("key length = %d, want 16", len(Key(sample)))
	}
}
