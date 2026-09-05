package hostskills

// archive.go retires a delivered skill by MOVING it aside rather than deleting it.
//
// Why not delete: the authority to remove a tier-B entry comes from the manifest, which is
// weak evidence by nature (see manifest.go). A stale record plus rm is unrecoverable data
// loss in the user's own home; a stale record plus mv is a file in a printed location. The
// asymmetry in cost is the whole argument — being wrong about ownership should cost the user
// one `mv` back, not their work.
//
// Why not leave it in place: then a skill removed from a pack keeps being loaded by the
// agent forever, and "the pack no longer ships this" would have no way to take effect. That
// was the compromise a flat merge forced, and it is worse than either alternative.
//
// The archive is UNBOUNDED by design here and pruned elsewhere (`yolo prune`), deliberately:
// a destructive cleanup must not be a side effect of a render. Reclaiming disk is a thing
// the user asks for, not something an `apply` decides.

import (
	"fmt"
	"os"
	"path/filepath"
)

// ArchiveRoot is the archive dir under the state dir. Callers pass it in (rather than this
// package reading paths.GlobalStorage itself) so tests can point it at a temp dir and the
// package stays free of a dependency on path layout.
type ArchiveRoot string

// Archive moves src into the archive under stamp/pack/<basename>, returning the path it
// landed at so the caller can PRINT it. An archive the user cannot find is the same as a
// deletion from their point of view, so the returned path is not optional decoration.
//
// stamp is supplied by the caller rather than computed here: this package must stay
// deterministic for tests, and the repo's workflow scripts forbid non-deterministic clocks
// in some contexts. A caller with no meaningful stamp can pass a fixed one; collisions are
// handled below.
func Archive(root ArchiveRoot, stamp, pack, src string) (string, error) {
	dest := filepath.Join(string(root), stamp, pack, filepath.Base(src))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	// A second archive of the same name in the same stamp must not clobber the first —
	// that would defeat the point of archiving instead of deleting. Suffix until free.
	final := dest
	for i := 2; ; i++ {
		if _, err := os.Lstat(final); os.IsNotExist(err) {
			break
		}
		final = fmt.Sprintf("%s.%d", dest, i)
	}
	if err := os.Rename(src, final); err != nil {
		// A cross-device rename fails (the state dir and the home may be different
		// filesystems), so fall back to copy+remove. Copy FIRST and only remove once the
		// copy is intact: the reverse order can lose the file if the copy fails.
		if cerr := copyTree(src, final); cerr != nil {
			return "", fmt.Errorf("archive %s: rename failed (%v) and copy failed: %w", src, err, cerr)
		}
		if rerr := os.RemoveAll(src); rerr != nil {
			return final, fmt.Errorf("archived %s to %s but could not remove the original: %w",
				src, final, rerr)
		}
	}
	return final, nil
}

// copyTree copies a file or directory tree, preserving the exec bit (a skill may ship a
// script, and an archived copy the user restores must still run).
//
// IT MATERIALIZES SYMLINKS, all of them, and that is the delivered form (delivered.go): the
// destination is a real agent home whose tools must be able to read the skill, and a link
// copied verbatim would either resolve against the wrong parent (a relative one) or turn
// yolo's own output into a pointer at the user's dotfiles repo — so their next `git mv` would
// leave the destination full of exactly the dangling links dangling.go exists to clean up.
func copyTree(src, dst string) error { return copyTreeExcept(src, dst, nil) }

// copyTreeExcept is copyTree skipping the given absolute source paths (and their subtrees).
// The only caller that passes any is the flat plugin delivery — see Request.excludePaths for
// why omitting content is the honest option there and nowhere else.
func copyTreeExcept(src, dst string, exclude []string) error {
	return copyTreeInto(src, dst, exclude, followedDirs{})
}

// copyTreeInto is copyTreeExcept carrying the cycle guard down the descent. See followedDirs:
// following a directory link is what makes an endless walk possible at all.
func copyTreeInto(src, dst string, exclude []string, walked followedDirs) error {
	for _, ex := range exclude {
		if filepath.Clean(ex) == filepath.Clean(src) {
			return nil
		}
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	linked := fi.Mode()&os.ModeSymlink != 0
	if linked {
		// STAT, not Lstat, and both halves matter. A symlinked DIRECTORY is a legitimate skill
		// — collectSkills admits one deliberately, "the tools follow them" — and Lstat's
		// IsDir is false for it, so the copy fell through to os.ReadFile and refused the
		// whole delivery with `is a directory`. A symlinked FILE was copied (ReadFile
		// follows) but took its mode from the LINK, whose 0o777 made every one of them
		// executable at the destination.
		resolved, serr := os.Stat(src)
		if serr != nil {
			return serr
		}
		fi = resolved
	}
	if fi.IsDir() {
		if linked {
			leave, cerr := walked.enter(src)
			if cerr != nil {
				return cerr
			}
			defer leave()
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTreeInto(filepath.Join(src, e.Name()),
				filepath.Join(dst, e.Name()), exclude, walked); err != nil {
				return err
			}
		}
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
