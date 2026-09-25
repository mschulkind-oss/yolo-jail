package prune

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// errDedupChanged is a name that no longer holds the file that was hashed.
var errDedupChanged = errors.New("dedup: the file changed since it was hashed")

// linkBeneath replaces dup's name with a hardlink to canonical, link-to-temp-then-rename (never
// unlinking the original first), with every name resolved beneath the entries' roots.
//
// The two names are usually under DIFFERENT roots (two workspaces, or a workspace and the machine
// store), and os.Root links only within one, so the link is linkat(2) between two directory
// descriptors, each opened beneath its own root: a directory on the way that the jail swapped for
// a link leaving the root is refused at that open, and every name after it is resolved in the
// directory that was opened, not re-walked from a path. Both names are checked to still be the
// files that were hashed before the link, and the new name to be the canonical file after it, so
// a name swapped for a link or another file meanwhile is never linked or renamed over. None of
// these calls follows a symbolic link at the final name.
func linkBeneath(canonical, dup hashedEntry) error {
	cdir, err := canonical.root.Open(filepath.Dir(canonical.rel))
	if err != nil {
		return err
	}
	defer cdir.Close()
	ddir, err := dup.root.Open(filepath.Dir(dup.rel))
	if err != nil {
		return err
	}
	defer ddir.Close()
	cfd, dfd := int(cdir.Fd()), int(ddir.Fd())
	cname, dname := filepath.Base(canonical.rel), filepath.Base(dup.rel)
	if !sameRegularAt(cfd, cname, canonical) || !sameRegularAt(dfd, dname, dup) {
		return errDedupChanged
	}
	tmp := dname + ".yolo-dedup-tmp"
	if err := unix.Linkat(cfd, cname, dfd, tmp, 0); err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0) // clean partial
		return err
	}
	if !sameRegularAt(dfd, tmp, canonical) {
		_ = unix.Unlinkat(dfd, tmp, 0)
		return errDedupChanged
	}
	if err := unix.Renameat(dfd, tmp, dfd, dname); err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		return err
	}
	return nil
}

// sameRegularAt reports whether name in the directory dirfd is, without following a link at
// it, a regular file with want's identity.
func sameRegularAt(dirfd int, name string, want hashedEntry) bool {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}
	return uint32(st.Mode)&unix.S_IFMT == unix.S_IFREG && st.Ino == want.ino && uint64(st.Dev) == want.dev
}
