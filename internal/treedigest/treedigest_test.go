package treedigest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func of(t *testing.T, root string) string {
	t.Helper()
	d, err := Of(root)
	must(t, err)
	return d
}

// TestCanonicalFormIsPinnedByteForByte builds the digest input BY HAND and compares. Every other
// test here checks that one property changes the digest; this one states what the digest IS.
//
// It is worth pinning literally because the canonical form is now a CONTRACT between two
// subsystems that never call each other: hostskills decides "is this the same skill?" with it, and
// internal/capture derives a CAS key with it (Key over this digest). A change to the stream — an
// extra field, a different separator, following a symlink — silently re-keys every capture on
// every machine while both packages keep working, so the format is the thing to make loud.
func TestCanonicalFormIsPinnedByteForByte(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "sub", "x"), []byte("hi"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "z.sh"), []byte("#!\n"), 0o755))
	must(t, os.Chmod(filepath.Join(root, "z.sh"), 0o755)) // explicit: WriteFile's mode is umasked
	must(t, os.Symlink("/tgt", filepath.Join(root, "l")))

	// Depth-first, each directory's children sorted by name (l, sub, z.sh), the root itself
	// recorded as ".", file bytes immediately after their header line with no separator, and
	// only the EXEC bits in the mode field.
	want := sha256.Sum256([]byte(
		"d .\n" +
			"l l /tgt\n" +
			"d sub\n" +
			"f sub/x 0\n" + "hi" +
			"f z.sh 111\n" + "#!\n"))
	if got := of(t, root); got != hex.EncodeToString(want[:]) {
		t.Errorf("digest = %q, want %q — the canonical form changed; see the package comment "+
			"and remember that internal/capture's CAS keys are derived from it",
			got, hex.EncodeToString(want[:]))
	}
}

// A symlink is recorded BY ITS TARGET STRING and never followed — the property the whole design
// rests on, and the one that a naive "just hash the files" rewrite would lose.
func TestSymlinkTargetsAreRecordedNotFollowed(t *testing.T) {
	root := t.TempDir()

	// A DANGLING link digests fine. Following it could only fail, so this is the cheapest
	// possible proof that nothing follows: an installer's tree routinely contains absolute
	// links whose targets do not exist until the tree is materialized.
	dangling := filepath.Join(root, "dangling")
	must(t, os.MkdirAll(dangling, 0o755))
	must(t, os.Symlink("/nowhere/at/all", filepath.Join(dangling, "link")))
	if _, err := Of(dangling); err != nil {
		t.Fatalf("a dangling symlink must not fail the walk (that would mean it was followed): %v", err)
	}

	// Two trees whose links point at DIFFERENT places differ, even when the two places hold
	// identical bytes: the target is the identity, not what is at the end of it.
	must(t, os.WriteFile(filepath.Join(root, "one"), []byte("same"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "two"), []byte("same"), 0o644))
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	must(t, os.MkdirAll(a, 0o755))
	must(t, os.MkdirAll(b, 0o755))
	must(t, os.Symlink(filepath.Join(root, "one"), filepath.Join(a, "link")))
	must(t, os.Symlink(filepath.Join(root, "two"), filepath.Join(b, "link")))
	if of(t, a) == of(t, b) {
		t.Error("links to different paths holding identical bytes must digest differently — " +
			"the digest must record the target, not follow it")
	}
}

// The exec bit is part of the identity: a captured install whose binary lost +x is not the same
// install, and a skill that ships a script the user made executable is not the same skill.
func TestExecBitChangesTheDigest(t *testing.T) {
	root := t.TempDir()
	tree := func(name string, mode os.FileMode) string {
		dir := filepath.Join(root, name)
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "run"), []byte("body"), mode))
		return dir
	}
	plain, exec := tree("plain", 0o644), tree("exec", 0o755)
	if of(t, plain) == of(t, exec) {
		t.Error("+x must change the digest")
	}
	// The NON-exec permission bits are deliberately not in it: capture chmods entry files
	// read-only at admit, and that must not re-key an entry against its own manifest.
	must(t, os.Chmod(filepath.Join(plain, "run"), 0o444))
	if of(t, plain) == of(t, exec) {
		t.Error("read-only is not +x")
	}
	other := tree("other", 0o644)
	if of(t, plain) != of(t, other) {
		t.Error("dropping the write bit must NOT change the digest — internal/capture freezes " +
			"entry files at admit and the entry must still match its own key")
	}
}

// The walk sorts, so the digest cannot depend on the order the filesystem happens to hand entries
// back — the same tree built in the opposite order is the same tree.
func TestDigestIsIndependentOfCreationOrder(t *testing.T) {
	root := t.TempDir()
	build := func(name string, names []string) string {
		dir := filepath.Join(root, name)
		must(t, os.MkdirAll(dir, 0o755))
		for _, n := range names {
			must(t, os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644))
		}
		return dir
	}
	forward := build("forward", []string{"a", "b", "c", "d"})
	backward := build("backward", []string{"d", "c", "b", "a"})
	if of(t, forward) != of(t, backward) {
		t.Error("creation order must not reach the digest")
	}
	// And a re-walk of one tree is stable.
	first := of(t, forward)
	if again := of(t, forward); first != again {
		t.Errorf("the digest must be stable across a re-walk: %q then %q", first, again)
	}
}

// OfSkipping omits a skipped path ENTIRELY — its name as well as its content. That is the
// documented semantic (it matches hostskills' copyTreeExcept, which never creates the excluded
// directory at all), and it is only observable by comparing against a tree where the path was
// never there.
func TestSkippingOmitsNamesNotJustContent(t *testing.T) {
	root := t.TempDir()
	withSkipped := filepath.Join(root, "with")
	must(t, os.MkdirAll(filepath.Join(withSkipped, "keep"), 0o755))
	must(t, os.WriteFile(filepath.Join(withSkipped, "keep", "f"), []byte("keep me"), 0o644))
	must(t, os.MkdirAll(filepath.Join(withSkipped, "skipme"), 0o755))
	must(t, os.WriteFile(filepath.Join(withSkipped, "skipme", "f"), []byte("junk"), 0o644))

	without := filepath.Join(root, "without")
	must(t, os.MkdirAll(filepath.Join(without, "keep"), 0o755))
	must(t, os.WriteFile(filepath.Join(without, "keep", "f"), []byte("keep me"), 0o644))

	got, err := OfSkipping(withSkipped, map[string]bool{"skipme": true})
	must(t, err)
	if got != of(t, without) {
		t.Error("a skipped path must be omitted name and all, so the digest equals that of a " +
			"tree which never had it")
	}
	// Sanity: without the skip set the two differ, or the assertion above proves nothing.
	if of(t, withSkipped) == of(t, without) {
		t.Fatal("the two fixtures must differ when nothing is skipped")
	}
}

// A missing root is an error rather than a digest of nothing — a caller that mistyped a path must
// not get a plausible-looking hash back.
func TestMissingRootIsAnError(t *testing.T) {
	if _, err := Of(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("digesting a missing root must fail")
	}
}
