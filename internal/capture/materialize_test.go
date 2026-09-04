package capture

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// materialize_test.go measures the second verb: an admitted entry put into a home, and the
// three-step fallback that puts it there.
//
// TWO KINDS OF TEST LIVE HERE and they answer different questions.
//
//   - The PREMISE test (TestReflinkGivesTheDestinationItsOwnInode) measures the real ioctl on
//     whatever filesystem the test's temp dir is on, and skips when there is none. It cannot
//     measure the property that actually matters — reflink ACROSS MOUNTS, where link(2) fails
//     — because a unit test cannot make a second mount. That measurement was taken by hand
//     (see clone_linux.go's header: a `:ro` bind of a store and a rw bind of a home in a real
//     podman container, FICLONE OK where link(2) returned EXDEV) and is re-measured on every
//     run of integration/capturematerialize_test.go, which materializes through exactly those
//     two mounts.
//   - The CHAIN tests drive the fallback with the two O(1) arms replaced, because arranging a
//     real ext4 (no reflink) and a real second mount (no hardlink) inside `go test` would need
//     root and two loopback filesystems to assert a branch. What they pin is the ORDER, the
//     STICKINESS, and the loudness of the copy — none of which the filesystem decides.

// entryFixture admits a small entry into a fresh store and returns it, plus the store.
//
// It goes through Store.Stage + Store.AdmitEntry rather than laying out entries/<key>/ by
// hand, so the tree the test materializes from is one the real admit produced — frozen
// modes included, which is the whole reason materialize reads the manifest.
func entryFixture(t *testing.T, home, platform string, refs []AbsoluteRef) (*Store, *Entry) {
	t.Helper()
	store := &Store{Dir: t.TempDir()}
	staged, err := store.Stage("fixture")
	if err != nil {
		t.Fatal(err)
	}
	tree := TreeDir(staged)
	mk := func(rel string, perm fs.FileMode, body string) {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), perm); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
	}
	mk(".local/share/vendor/1.0/vendor", 0o755, "#!/bin/sh\necho vendor\n")
	mk(".local/share/vendor/1.0/data.txt", 0o644, "payload\n")
	if err := os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home+"/.local/share/vendor/1.0/vendor",
		filepath.Join(tree, ".local", "bin", "vendor")); err != nil {
		t.Fatal(err)
	}
	entries, gathered, err := describeTree(tree, home)
	if err != nil {
		t.Fatal(err)
	}
	if refs == nil {
		refs = gathered
	}
	if err := WriteManifest(staged, &Manifest{
		Schema: ManifestSchema, Home: home, Platform: platform,
		Surfaces: []string{".local"}, Excluded: []string{},
		Entries: entries, AbsoluteRefs: refs,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	return store, entry
}

// A materialize into a POPULATED home puts the vendor's files where the manifest says, with
// the modes the INSTALLER made rather than the read-only modes admit froze the entry to.
//
// The mode assertion is the one that would not survive a walk-the-tree implementation:
// Store.Admit chmods every entry file 0444/0555 so a hardlinked file cannot be written
// through, so the tree on disk no longer remembers the vendor's 0755. Only the manifest does.
func TestMaterializePutsAnEntryIntoAPopulatedHome(t *testing.T) {
	home := t.TempDir()
	// Pre-populate: a directory the capture also names, with a mode of its own and a file
	// in it that the capture does NOT name.
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "unrelated"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, entry := entryFixture(t, home, Platform(), nil)

	var errw bytes.Buffer
	res, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: &errw})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	bin := filepath.Join(home, ".local", "share", "vendor", "1.0", "vendor")
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("the vendor binary is not in the home: %v", err)
	}
	if !strings.Contains(string(body), "echo vendor") {
		t.Errorf("the materialized binary has the wrong bytes: %q", body)
	}
	fi, err := os.Lstat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the materialized binary is not executable: %v", fi.Mode())
	}
	// THE ONE OBSERVABLE DIFFERENCE BETWEEN THE ARMS. A hardlinked file IS the store's
	// inode, so its mode cannot be restored without unfreezing the store for every
	// workspace; a reflinked or copied one is its own inode and gets the vendor's 0755
	// back. Asserting per-arm rather than picking one keeps this test honest on both a
	// btrfs developer machine (reflink) and an ext4 runner (hardlink, same /tmp mount).
	switch res.Mechanism() {
	case "hardlink":
		if fi.Mode().Perm() != 0o555 {
			t.Errorf("a HARDLINKED file must keep the store's frozen mode, got %v", fi.Mode())
		}
	default:
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("a %s'd file must get the manifest's mode back, got %v",
				res.Mechanism(), fi.Mode())
		}
	}

	target, err := os.Readlink(filepath.Join(home, ".local", "bin", "vendor"))
	if err != nil {
		t.Fatalf("the symlink is not in the home: %v", err)
	}
	if target != home+"/.local/share/vendor/1.0/vendor" {
		t.Errorf("symlink target = %q, want the capture's verbatim", target)
	}

	// An existing directory keeps its own mode and its own contents.
	di, err := os.Lstat(filepath.Join(home, ".local", "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("materialize changed an existing home directory's mode to %v — it may add "+
			"to a home, not edit it", di.Mode())
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "unrelated")); err != nil {
		t.Errorf("materialize removed a file the capture does not name: %v", err)
	}

	if res.Files != 2 || res.Symlinks != 1 {
		t.Errorf("counts = %d files / %d symlinks, want 2/1", res.Files, res.Symlinks)
	}
	if res.Reflinked+res.Linked+res.Copied != res.Files {
		t.Errorf("the mechanism counters (%d/%d/%d) do not partition the %d files",
			res.Reflinked, res.Linked, res.Copied, res.Files)
	}
	if res.Copied == 0 && errw.Len() != 0 {
		t.Errorf("nothing was copied, so nothing should have been reported: %s", errw.String())
	}
}

// THE PREMISE, measured rather than assumed: a reflinked file is a SEPARATE INODE holding the
// same bytes.
//
// That is the property that downgrades install-capture.md's sharpest trap. The plan warns
// that "a hardlinked CAS file is the running program's bytes", so an installer that opens one
// for write corrupts every workspace at once — true of a hardlink, and structurally false of
// a reflink, which copies on write. Skipped, with the filesystem named, where the kernel says
// no: that skip is itself the measurement that ext4 forces the copy arm.
func TestReflinkGivesTheDestinationItsOwnInode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 1<<16), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := reflinkOne(src, dst, 0o644); err != nil {
		if errors.Is(err, errCloneUnsupported) {
			t.Skipf("no reflink on %s (%s) — materialize takes the hardlink or copy arm here",
				dir, orUnknown(fsName(dir)))
		}
		t.Fatalf("reflink: %v", err)
	}
	si, di := statT(t, src), statT(t, dst)
	if si.Ino == di.Ino {
		t.Errorf("the reflinked file shares the source's inode (%d) — then it would be a "+
			"hardlink, and a write through it would reach the store", si.Ino)
	}
	if di.Nlink != 1 {
		t.Errorf("the reflinked file has nlink %d, want 1", di.Nlink)
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(dst)
	if !bytes.Equal(a, b) {
		t.Errorf("the reflinked file does not hold the source's bytes")
	}
	// The measurement slice 5 has to account for: a REFERENCED entry file whose nlink is
	// still 1. See install-capture.md's slice-5 correction.
	if statT(t, src).Nlink != 1 {
		t.Errorf("the SOURCE's nlink moved to %d — a reflink must not bump it, which is "+
			"exactly why st_nlink cannot be the GC's reference oracle", statT(t, src).Nlink)
	}
}

func statT(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	return st
}

// The chain falls REFLINK → HARDLINK → COPY, retires an arm exactly once, and says out loud
// which filesystems forced the copy.
//
// Stickiness is asserted by counting calls: an arm that answered "not here" must not be asked
// again, or a 1.2 GB tree of thousands of files pays one failed ioctl per file for an answer
// the pair of filesystems already gave.
func TestMaterializeFallsThroughToHardlinkThenCopy(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, home, Platform(), nil)

	reflinks, links := 0, 0
	restore := forceChain(t,
		func(src, dst string, perm fs.FileMode) error {
			reflinks++
			return errCloneUnsupported
		},
		func(src, dst string) error {
			links++
			return &os.LinkError{Op: "link", Old: src, New: dst, Err: syscall.EXDEV}
		})
	defer restore()

	var errw bytes.Buffer
	res, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: &errw})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	if res.Copied != 2 || res.Reflinked != 0 || res.Linked != 0 {
		t.Errorf("counters = %d reflinked / %d linked / %d copied, want 0/0/2",
			res.Reflinked, res.Linked, res.Copied)
	}
	if reflinks != 1 {
		t.Errorf("reflink was attempted %d times — an arm that reported 'not here' must be "+
			"retired for the run, not retried per file", reflinks)
	}
	if links != 1 {
		t.Errorf("hardlink was attempted %d times, want 1 (same reason)", links)
	}
	if res.ReflinkRetired == "" || res.LinkRetired == "" {
		t.Errorf("the retirement reasons were not recorded: %+v", res)
	}
	// The copy is LOUD and names the filesystems. A silent copy is indistinguishable from
	// the property this subsystem promises.
	report := errw.String()
	for _, want := range []string{"COPIED", entry.Key, home, "2 of 2"} {
		if !strings.Contains(report, want) {
			t.Errorf("the copy report does not mention %q:\n%s", want, report)
		}
	}
	if fs := fsName(home); fs != "" && !strings.Contains(report, fs) {
		t.Errorf("the copy report does not name this home's filesystem (%s):\n%s", fs, report)
	}
	// The bytes still arrived.
	if body, err := os.ReadFile(filepath.Join(home, ".local", "share", "vendor", "1.0", "data.txt")); err != nil {
		t.Errorf("the copy arm did not place the file: %v", err)
	} else if string(body) != "payload\n" {
		t.Errorf("the copied file has the wrong bytes: %q", body)
	}
}

// The MIDDLE arm is reachable: reflink off, hardlink on.
func TestMaterializeUsesHardlinkWhenReflinkCannot(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, home, Platform(), nil)

	restore := forceChain(t,
		func(src, dst string, perm fs.FileMode) error { return errCloneUnsupported },
		os.Link)
	defer restore()

	var errw bytes.Buffer
	res, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: &errw})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if res.Linked != 2 || res.Copied != 0 {
		t.Fatalf("counters = %d linked / %d copied, want 2/0 (store and home are both under "+
			"the test's temp root, so link(2) works)", res.Linked, res.Copied)
	}
	if errw.Len() != 0 {
		t.Errorf("nothing was copied, so nothing should have been reported: %s", errw.String())
	}
	// A hardlink IS the store's inode — the trap install-capture.md names, and the reason
	// this arm is second rather than first.
	src := statT(t, filepath.Join(entry.Tree, ".local", "share", "vendor", "1.0", "vendor"))
	dst := statT(t, filepath.Join(home, ".local", "share", "vendor", "1.0", "vendor"))
	if src.Ino != dst.Ino {
		t.Errorf("the hardlink arm did not share the store's inode (%d vs %d)", src.Ino, dst.Ino)
	}
}

// forceChain replaces the two O(1) arms and returns the undo. See the file comment for why
// the mechanisms are seams here and measured for real elsewhere.
func forceChain(t *testing.T, rl func(string, string, fs.FileMode) error, hl func(string, string) error) func() {
	t.Helper()
	oldR, oldH := reflinkOne, hardlinkOne
	reflinkOne, hardlinkOne = rl, hl
	return func() { reflinkOne, hardlinkOne = oldR, oldH }
}

// An entry captured under ANOTHER HOME is refused, and the refusal says which of the two
// refusals it is — "not relocatable, because X" or "the rewrite is not built yet".
//
// Unreachable on the container backends (capture home and materialize home are both
// /home/agent) and the whole of macos-user's problem, so this is the guard that keeps a
// not-yet-built rewrite from being skipped rather than a feature.
func TestMaterializeRefusesAnEntryCapturedUnderAnotherHome(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, "/some/other/home", Platform(), []AbsoluteRef{{
		Path: ".local/bin/vendor", Kind: RefSymlinkTarget,
		Value: "/some/other/home/.local/share/vendor/1.0/vendor",
	}})

	_, err := Materialize(MaterializeOptions{Entry: entry, Home: home})
	if !errors.Is(err, ErrNotRelocatable) {
		t.Fatalf("materialize into a different home = %v, want ErrNotRelocatable", err)
	}
	if !strings.Contains(err.Error(), "/some/other/home") || !strings.Contains(err.Error(), home) {
		t.Errorf("the refusal names neither home: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(statErr) {
		t.Errorf("the refusal still wrote into the home: %v", statErr)
	}
}

// A capture from another PLATFORM is refused by the mechanism itself, not only by whatever
// chose it. §6.3: "captures are per-platform (and only for platforms we can run)".
func TestMaterializeRefusesAnotherPlatform(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, home, "plan9/mips", nil)

	_, err := Materialize(MaterializeOptions{Entry: entry, Home: home})
	if err == nil || !strings.Contains(err.Error(), "plan9/mips") {
		t.Fatalf("materialize of a foreign-platform entry = %v, want a refusal naming it", err)
	}
}

// A stale FILE at a captured path is replaced; a DIRECTORY there is refused rather than
// removed, because os.RemoveAll on the home's side of that disagreement is how a materialize
// would delete a user's data.
func TestMaterializeReplacesAFileAndRefusesADirectory(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, home, Platform(), nil)
	stale := filepath.Join(home, ".local", "share", "vendor", "1.0", "data.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(MaterializeOptions{Entry: entry, Home: home}); err != nil {
		t.Fatalf("materialize over a stale file: %v", err)
	}
	if body, _ := os.ReadFile(stale); string(body) != "payload\n" {
		t.Errorf("the stale file was not replaced: %q", body)
	}

	home2 := t.TempDir()
	_, entry2 := entryFixture(t, home2, Platform(), nil)
	blocked := filepath.Join(home2, ".local", "share", "vendor", "1.0", "data.txt")
	if err := os.MkdirAll(filepath.Join(blocked, "someone-elses-stuff"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Materialize(MaterializeOptions{Entry: entry2, Home: home2})
	if err == nil || !strings.Contains(err.Error(), "refusing to remove") {
		t.Fatalf("materialize over a directory = %v, want a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(blocked, "someone-elses-stuff")); err != nil {
		t.Errorf("the refusal still removed the directory's contents: %v", err)
	}
}
