package entrypoint

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// darwinoverlaylink.go is how the macos-user install DELIVERS a destination (installOverlayDestination)
// and how it knows a destination already holds this launch's delivery. Only macos-user installs a
// home overlay: a container backend binds each staged tree read-only at its destination and runs
// none of this.
//
// COPIES, NEVER HARD LINKS (MMP-D35, docs/design/macos-user-model-plan.md). Each file of a delivered
// destination is a COPY of the file at the same path in the staged overlay
// (macosuser.StagedHomeOverlay), made at the staged file's mode. The install never asks for a link
// from the staging root: a hard link is one inode under two names, and Seatbelt checks the path
// written through, so in run-as-you mode — where the sandbox runs as the staged file's owner — a
// link in the home would let the agent make the shared staged file writable and edit what other
// workspaces read. The deny-by-default session profile grants no file-link, and needs none.
//
//   - "IS THIS DESTINATION ALREADY THIS DELIVERY?" IS A CONTENT COMPARISON (overlayInstalledFrom):
//     every name the staged tree holds and no other, each directory no wider than delivered, each
//     symbolic link the same text, and each file a regular file of its own with the staged file's
//     mode, size and BYTES. Never an mtime, which the agent can set. So an unchanged relaunch
//     rewrites nothing and every delivered inode stays, while a destination the agent changed
//     between sessions — a file edited, chmodded, added, removed or swapped for a link — fails the
//     check and is replaced whole. A file that IS the staged file under a second name (a hard link
//     an earlier yolo made) fails it too, so the next install swaps that link for a copy.
//   - THE COST of an unchanged relaunch is a read of a few dozen small files, instead of a full
//     copy and a full removal of the previous one.
//
// The inherited user config is the exception to "replaced whole": it is never replaced blindly, and
// a copy of it is admitted by the host's delivery record instead (inheriteddelivery.go, issue #70).
//
// Every write goes through a descriptor of the directory being filled, one name at a time, with
// O_NOFOLLOW and O_EXCL where they apply, and that directory was itself opened beneath the os.Root
// the install checked (openOverlayParent). The staged source is read by path: the sandbox account
// cannot write the staging root.

// copyTreeStrictBeneath fills rel beneath dst, which must exist and be empty, with the tree src: a
// directory for each directory, the same link for each symbolic link, and a copy of each file. The
// first error ends it; the caller removes the partial tree.
func copyTreeStrictBeneath(src string, dst *os.Root, rel string) error {
	d, err := dst.Open(rel)
	if err != nil {
		return err
	}
	defer d.Close()
	return copyTreeInto(src, d)
}

// copyTreeInto is copyTreeStrictBeneath below an open directory.
func copyTreeInto(src string, dir *os.File) error {
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	fd := int(dir.Fd())
	for _, ent := range ents {
		name := ent.Name()
		from := filepath.Join(src, name)
		fi, err := os.Lstat(from)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(from)
			if err != nil {
				return err
			}
			if err := unix.Symlinkat(target, fd, name); err != nil {
				return &os.PathError{Op: "symlink", Path: filepath.Join(dir.Name(), name), Err: err}
			}
		case fi.IsDir():
			// THE OWNER KEEPS rwx on each directory: the staged overlay is sealed (a-w), and the
			// sandbox account owns these directories, so a sealed mode would leave it unable to fill
			// one or remove it at the next install (PPX-D45).
			if err := unix.Mkdirat(fd, name, uint32(fi.Mode().Perm()|0o700)); err != nil {
				return &os.PathError{Op: "mkdir", Path: filepath.Join(dir.Name(), name), Err: err}
			}
			sub, err := openDirAt(fd, name, filepath.Join(dir.Name(), name))
			if err != nil {
				return err
			}
			err = copyTreeInto(from, sub)
			sub.Close()
			if err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := copyFileAt(from, fi.Mode().Perm(), fd, name, filepath.Join(dir.Name(), name)); err != nil {
				return err
			}
		default:
			// A socket, fifo or device node: skipping it would be a partial copy reported as a
			// whole one (copyTreeStrict's rule).
			return fmt.Errorf("%s is a %s, which this copy cannot reproduce", from, fi.Mode().Type())
		}
	}
	return nil
}

// copyFileStrictBeneath is copyTreeStrictBeneath for a destination that is one file: rel, a name
// directly in dst, must not exist.
func copyFileStrictBeneath(src string, dst *os.Root, rel string, perm os.FileMode) error {
	d, err := dst.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return copyFileAt(src, perm, int(d.Fd()), rel, filepath.Join(d.Name(), rel))
}

// RefuseOverlayLinksForTest is a no-op kept so that other packages' tests, which forced the copy
// this way, still build: the overlay install makes no hard links (MMP-D35), so there is no link to
// refuse and every delivery already is a copy. The restore it returns does nothing. Nothing in
// production calls it.
func RefuseOverlayLinksForTest(error) (restore func()) {
	return func() {}
}

// copyFileAt creates name in the directory dirfd, which must not exist there, as a copy of the
// regular file from at perm.
func copyFileAt(from string, perm os.FileMode, dirfd int, name, display string) error {
	in, err := os.OpenFile(from, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	ofd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		uint32(perm))
	if err != nil {
		return &os.PathError{Op: "create", Path: display, Err: err}
	}
	out := os.NewFile(uintptr(ofd), display)
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	// The umask may have narrowed the create. The delivered mode is the staged one, which is what
	// the next install's comparison expects.
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// openDirAt opens the directory name in dirfd without following a link at it.
func openDirAt(dirfd int, name, display string) (*os.File, error) {
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: display, Err: err}
	}
	return os.NewFile(uintptr(fd), display), nil
}

// overlayInstalledFrom reports whether rel beneath dir is already exactly the staged src: every
// regular file a copy with the staged file's mode and bytes (sameCopyAt), every symbolic link the
// same text, every directory holding exactly the names the staged one holds. Anything else — a
// missing or extra name, a different type, different bytes or mode, the staged file itself under a
// second name, an unreadable entry — is false, and the caller replaces the destination.
func overlayInstalledFrom(src string, srcInfo os.FileInfo, dir *os.Root, rel string) bool {
	cur, err := dir.Lstat(rel)
	if err != nil {
		return false
	}
	switch {
	case srcInfo.Mode()&os.ModeSymlink != 0:
		if cur.Mode()&os.ModeSymlink == 0 {
			return false
		}
		a, err1 := os.Readlink(src)
		b, err2 := dir.Readlink(rel)
		return err1 == nil && err2 == nil && a == b
	case srcInfo.Mode().IsRegular():
		parent, err := dir.Open(".")
		if err != nil {
			return false
		}
		defer parent.Close()
		return sameCopyAt(src, srcInfo, int(parent.Fd()), rel)
	case !srcInfo.IsDir():
		return false
	}
	if !cur.IsDir() || !dirModeIntact(uint32(cur.Mode().Perm()), 0o755) {
		return false
	}
	// Opened beneath its parent WITHOUT following a link, so a destination swapped for a link
	// after the Lstat above is not walked as if it were the directory.
	parent, err := dir.Open(".")
	if err != nil {
		return false
	}
	defer parent.Close()
	d, err := openDirAt(int(parent.Fd()), rel, rel)
	if err != nil {
		return false
	}
	defer d.Close()
	return dirInstalledFrom(src, d)
}

// dirModeIntact reports whether a delivered directory's permission bits are no wider than the
// install gave it (want). The sandbox account owns these directories and could widen one between
// sessions; a narrower mode is the umask's doing and is left alone.
func dirModeIntact(perm, want uint32) bool {
	return perm&0o7777&^want == 0
}

// dirInstalledFrom is overlayInstalledFrom for an open destination directory.
func dirInstalledFrom(src string, dir *os.File) bool {
	want, err := os.ReadDir(src)
	if err != nil {
		return false
	}
	got, err := dir.Readdirnames(-1)
	if err != nil || len(got) != len(want) {
		return false
	}
	sort.Strings(got)
	fd := int(dir.Fd())
	for i, ent := range want { // os.ReadDir sorts by name
		name := ent.Name()
		if got[i] != name {
			return false
		}
		from := filepath.Join(src, name)
		sfi, err := os.Lstat(from)
		if err != nil {
			return false
		}
		var st unix.Stat_t
		if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return false
		}
		switch {
		case sfi.Mode()&os.ModeSymlink != 0:
			if st.Mode&unix.S_IFMT != unix.S_IFLNK {
				return false
			}
			a, err1 := os.Readlink(from)
			b, err2 := readlinkAt(fd, name)
			if err1 != nil || err2 != nil || a != b {
				return false
			}
		case sfi.IsDir():
			if st.Mode&unix.S_IFMT != unix.S_IFDIR ||
				!dirModeIntact(uint32(st.Mode)&0o7777, uint32(sfi.Mode().Perm()|0o700)) {
				return false
			}
			sub, err := openDirAt(fd, name, filepath.Join(dir.Name(), name))
			if err != nil {
				return false
			}
			ok := dirInstalledFrom(from, sub)
			sub.Close()
			if !ok {
				return false
			}
		case sfi.Mode().IsRegular():
			if st.Mode&unix.S_IFMT != unix.S_IFREG || !sameCopyAt(from, sfi, fd, name) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// sameCopyAt reports whether name in the directory dirfd is a delivered copy of the staged regular
// file from (whose Lstat is sfi) that still matches it: a regular file, opened without following a
// link, that is NOT the staged file itself, with the staged file's permission bits and size and
// byte-for-byte the same content. Timestamps are never consulted.
func sameCopyAt(from string, sfi os.FileInfo, dirfd int, name string) bool {
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	got := os.NewFile(uintptr(fd), name)
	defer got.Close()
	gi, err := got.Stat()
	if err != nil || !gi.Mode().IsRegular() || os.SameFile(gi, sfi) ||
		gi.Mode().Perm() != sfi.Mode().Perm() || gi.Size() != sfi.Size() {
		return false
	}
	want, err := os.OpenFile(from, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer want.Close()
	if wi, err := want.Stat(); err != nil || !os.SameFile(wi, sfi) {
		return false
	}
	return sameReaderContent(want, got)
}

// sameReaderContent reports whether a and b yield the same bytes to their ends.
func sameReaderContent(a, b io.Reader) bool {
	const chunk = 32 << 10
	ba, bb := make([]byte, chunk), make([]byte, chunk)
	for {
		na, ea := io.ReadFull(a, ba)
		nb, eb := io.ReadFull(b, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false
		}
		aDone := ea == io.EOF || ea == io.ErrUnexpectedEOF
		bDone := eb == io.EOF || eb == io.ErrUnexpectedEOF
		if (ea != nil && !aDone) || (eb != nil && !bDone) {
			return false
		}
		if aDone || bDone {
			return aDone && bDone
		}
	}
}

// readlinkAt reads the symbolic link name in dirfd.
func readlinkAt(dirfd int, name string) (string, error) {
	for size := 256; ; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(dirfd, name, buf)
		if err != nil {
			return "", err
		}
		if n < size {
			return string(buf[:n]), nil
		}
	}
}
