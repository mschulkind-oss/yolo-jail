package prune

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The two names an in-flight delivery used to have in cache/images (before the
// delta archive moved deliveries to paths.ImageDeliveryDir), which a machine can
// still hold. Spelled out here rather than imported because internal/image imports
// nothing from this package and the point of the test is that THIS package
// cannot tell them apart from a corpse: filepath.Ext on either is ".tmp".
const (
	testOCIArchiveName    = "abc123.oci-archive.tmp"
	testDockerArchiveName = "abc123.docker-archive.tmp"
)

// writeAged writes a file of n bytes at dir/name and backdates its mtime by age.
func writeAged(t *testing.T, dir, name string, n int, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	must(t, os.WriteFile(p, make([]byte, n), 0o644))
	if age > 0 {
		past := time.Now().Add(-age)
		must(t, os.Chtimes(p, past, past))
	}
	return p
}

// TestPruneImageCacheTmpGraceFloor: a *.tmp under cache/images is no longer
// necessarily a crash leftover — since C9 an archive-delivering backend (Apple
// Container, podman-on-macOS) writes its transient image archive there under a
// name of exactly that shape. So the sweep has an age grace floor: a recent .tmp
// is a delivery in flight and is spared; one past the floor is the corpse this
// sweep exists for.
func TestPruneImageCacheTmpGraceFloor(t *testing.T) {
	// A fresh archive of each spelling — this is the file a concurrent launch is
	// copying into, or loading back out of, right now.
	t.Run("a recent .tmp survives", func(t *testing.T) {
		dir := t.TempDir()
		oci := writeAged(t, dir, testOCIArchiveName, 64, 0)
		docker := writeAged(t, dir, testDockerArchiveName, 64, 0)

		bytes, files := PruneImageCache(dir, 3, true)
		if files != 0 || bytes != 0 {
			t.Errorf("swept (%d bytes, %d files), want (0, 0) — an in-flight archive is inside the grace floor", bytes, files)
		}
		for _, p := range []string{oci, docker} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s was removed: an in-flight image delivery's archive must survive housekeeping", filepath.Base(p))
			}
		}
	})

	// The floor is a floor, not a reprieve: past it, the bytes come back.
	t.Run("an old .tmp is swept", func(t *testing.T) {
		dir := t.TempDir()
		old := writeAged(t, dir, testOCIArchiveName, 128, 48*time.Hour)

		bytes, files := PruneImageCache(dir, 3, true)
		if files != 1 || bytes != 128 {
			t.Errorf("swept (%d bytes, %d files), want (128, 1)", bytes, files)
		}
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Error("a .tmp older than the grace floor is a crashed delivery and must be reclaimed")
		}
	})

	// The exact boundary the constant names, so a change to its VALUE is a change
	// to this test rather than a silent widening of the window.
	t.Run("the floor is imageCacheTmpGraceFloor", func(t *testing.T) {
		dir := t.TempDir()
		young := writeAged(t, dir, "young.tmp", 16, imageCacheTmpGraceFloor/2)
		old := writeAged(t, dir, "old.tmp", 16, imageCacheTmpGraceFloor+time.Minute)

		_, files := PruneImageCache(dir, 3, true)
		if files != 1 {
			t.Errorf("swept %d files, want 1 (only the entry past the floor)", files)
		}
		if _, err := os.Stat(young); err != nil {
			t.Error("an entry younger than the floor must be spared")
		}
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Error("an entry past the floor must be swept")
		}
	})

	// keep governs the TARS. A spared .tmp is spared because of its age, and no
	// keep value may talk the sweep into deleting an archive in flight — the
	// housekeeping caller (run.reapImageTars) passes keep=0 on podman.
	t.Run("keep=0 does not override the floor", func(t *testing.T) {
		dir := t.TempDir()
		fresh := writeAged(t, dir, testDockerArchiveName, 32, 0)

		_, files := PruneImageCache(dir, 0, true)
		if files != 0 {
			t.Errorf("swept %d files at keep=0, want 0", files)
		}
		if _, err := os.Stat(fresh); err != nil {
			t.Error("keep=0 must not reach an in-flight archive")
		}
	})

	// Dry run keeps its contract on the new branch too: an old .tmp is reported
	// and left on disk.
	t.Run("dry run reports an old .tmp without deleting it", func(t *testing.T) {
		dir := t.TempDir()
		old := writeAged(t, dir, testOCIArchiveName, 256, 48*time.Hour)

		bytes, files := PruneImageCache(dir, 3, false)
		if files != 1 || bytes != 256 {
			t.Errorf("dry run = (%d bytes, %d files), want (256, 1)", bytes, files)
		}
		if _, err := os.Stat(old); err != nil {
			t.Error("apply=false must not touch disk")
		}
	})

	// The floor belongs to the .tmp branch alone. Tars are a frozen backlog
	// nothing writes any more, so age is not what selects them — keep is — and a
	// floor applied to both would strand the newest tars beyond keep forever.
	t.Run("the floor does not reach the tars", func(t *testing.T) {
		dir := t.TempDir()
		writeAged(t, dir, "a.tar", 10, 0)
		writeAged(t, dir, "b.tar", 20, time.Minute)
		writeAged(t, dir, "c.tar", 40, 2*time.Minute)

		bytes, files := PruneImageCache(dir, 1, true)
		if files != 2 || bytes != 60 {
			t.Errorf("tar sweep = (%d bytes, %d files), want (60, 2) — recent tars past keep are still dropped", bytes, files)
		}
		if _, err := os.Stat(filepath.Join(dir, "a.tar")); err != nil {
			t.Error("the newest tar is the one keep=1 retains")
		}
	})
}

// TestPruneImageCacheDeclinesWhatItCannotStat: "I could not stat it" and "it is
// reclaimable" are different facts, and only one of them is safe to act on. The
// age floor makes this sharper than it was — the sweep now NEEDS the mtime to
// decide, so the tempting wrong branch is "cannot tell how old it is, delete
// it", which would put the sweep straight back on top of an in-flight delivery.
func TestPruneImageCacheDeclinesWhatItCannotStat(t *testing.T) {
	dir := t.TempDir()
	// Old enough that age alone would condemn it: the only thing sparing it is
	// the refusal to act on an answer the sweep could not get.
	blind := writeAged(t, dir, testOCIArchiveName, 512, 48*time.Hour)
	visible := writeAged(t, dir, "other.tmp", 8, 48*time.Hour)

	orig := imageCacheLstat
	t.Cleanup(func() { imageCacheLstat = orig })
	imageCacheLstat = func(p string) (os.FileInfo, error) {
		if p == blind {
			return nil, errors.New("simulated lstat failure")
		}
		return orig(p)
	}

	bytes, files := PruneImageCache(dir, 3, true)
	if files != 1 || bytes != 8 {
		t.Errorf("swept (%d bytes, %d files), want (8, 1) — only the entry it could stat", bytes, files)
	}
	if _, err := os.Stat(blind); err != nil {
		t.Error("an entry the sweep could not stat must be left alone, not removed")
	}
	if _, err := os.Stat(visible); !os.IsNotExist(err) {
		t.Error("the statable old entry should still have been swept")
	}
}

// TestPruneImageCacheSkipsNonRegularEntries: a symlink or a directory named
// *.tmp is neither a crash leftover nor an archive, and removing one would
// follow a name into something the sweep never sized.
func TestPruneImageCacheSkipsNonRegularEntries(t *testing.T) {
	dir := t.TempDir()
	target := writeAged(t, dir, "real-file", 16, 48*time.Hour)
	link := filepath.Join(dir, "link.tmp")
	must(t, os.Symlink(target, link))
	subdir := filepath.Join(dir, "dir.tmp")
	must(t, os.MkdirAll(subdir, 0o755))
	past := time.Now().Add(-48 * time.Hour)
	must(t, os.Chtimes(subdir, past, past))

	_, files := PruneImageCache(dir, 3, true)
	if files != 0 {
		t.Errorf("swept %d files, want 0 (neither a symlink nor a dir is a tmp archive)", files)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a *.tmp symlink must be left alone")
	}
	if _, err := os.Stat(subdir); err != nil {
		t.Error("a *.tmp directory must be left alone")
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("the symlink's target must never be followed and removed")
	}
}
