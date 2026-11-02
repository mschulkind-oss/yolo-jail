package entrypoint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"golang.org/x/sys/unix"
)

// darwinoverlaylink.go is how the macos-user install DELIVERS a destination (installOverlayDestination)
// and how it knows a destination already holds this launch's delivery.
//
// HARD LINKS, NOT COPIES. Each file of a delivered destination is a hard link of the file at the
// same path in the staged overlay (macosuser.StagedHomeOverlay): the host user's, sealed a-w, in a
// root the Seatbelt profile never lets the sandbox write. So:
//
//   - THE BYTES CANNOT BE EDITED from the account home. The file is the host user's and read-only;
//     the sandbox account is not its owner and cannot chmod it. That is stronger than the copy it
//     replaces, whose only protection was the session profile's deny (the copy was the sandbox
//     account's own file).
//   - "IS THIS DESTINATION ALREADY THIS DELIVERY?" IS AN INODE COMPARISON (overlayInstalledFrom). A
//     file the sandbox account wrote has an inode of its own, never the staged file's, so an
//     unchanged relaunch checks every name and inode and replaces nothing, while a destination the
//     agent changed between sessions — a file replaced, one added or removed, a link swapped in —
//     fails the check and is replaced, exactly as every launch replaced it before. No stamp or
//     record the agent could forge is consulted.
//   - THE COST of an unchanged relaunch is a stat per entry instead of a full copy and a full
//     removal of the previous one.
//
// A LINK THE FILESYSTEM REFUSES IS A COPY. A workspace sidecar on another volume (EXDEV), a
// filesystem without hard links, or a kernel that refuses a link to another user's file (Linux's
// protected_hardlinks) gets the copy every launch made before; such a destination never passes the
// inode check, so it is replaced on every launch, as it was. The install says so in the boot log
// (copyNote). The inherited user config is the exception: it is never replaced blindly, and a
// copy of it is admitted by the host's delivery record instead (inheriteddelivery.go, issue #70).
//
// Every write goes through a descriptor of the directory being filled, one name at a time, with
// O_NOFOLLOW and O_EXCL where they apply, and that directory was itself opened beneath the os.Root
// the install checked (openOverlayParent). The staged source is read by path, as the copy read it:
// the sandbox account cannot write the staging root.

// linkTreeStrictBeneath fills rel beneath dst, which must exist and be empty, with the tree src: a
// directory for each directory, the same link for each symbolic link, and a hard link (or, refused
// one, a copy) of each file. The first error ends it; the caller removes the partial tree.
func linkTreeStrictBeneath(src string, dst *os.Root, rel string, onCopy copyNote) error {
	d, err := dst.Open(rel)
	if err != nil {
		return err
	}
	defer d.Close()
	return linkTreeInto(src, d, onCopy)
}

// linkTreeInto is linkTreeStrictBeneath below an open directory.
func linkTreeInto(src string, dir *os.File, onCopy copyNote) error {
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
			// THE OWNER KEEPS rwx on each directory, as the copy's did: the staged overlay is sealed
			// (a-w), and the sandbox account owns these directories, so a sealed mode would leave it
			// unable to fill one or remove it at the next install (PPX-D45).
			if err := unix.Mkdirat(fd, name, uint32(fi.Mode().Perm()|0o700)); err != nil {
				return &os.PathError{Op: "mkdir", Path: filepath.Join(dir.Name(), name), Err: err}
			}
			sub, err := openDirAt(fd, name, filepath.Join(dir.Name(), name))
			if err != nil {
				return err
			}
			err = linkTreeInto(from, sub, onCopy)
			sub.Close()
			if err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := linkOrCopyAt(from, fi.Mode().Perm(), fd, name, filepath.Join(dir.Name(), name), onCopy); err != nil {
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

// linkFileStrictBeneath is linkTreeStrictBeneath for a destination that is one file: rel, a name
// directly in dst, must not exist.
func linkFileStrictBeneath(src string, dst *os.Root, rel string, perm os.FileMode, onCopy copyNote) error {
	d, err := dst.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return linkOrCopyAt(src, perm, int(d.Fd()), rel, filepath.Join(d.Name(), rel), onCopy)
}

// overlayLinkat is linkat(2); a test swaps it to refuse, as a sidecar on another volume does.
var overlayLinkat = unix.Linkat

// RefuseOverlayLinksForTest makes every overlay hard link fail with err — as a sidecar on another
// volume, or a link the Seatbelt profile refuses (issue #70), does — until the returned restore
// runs. It is for another package's tests, which drive the real installer through Run; nothing
// in production calls it.
func RefuseOverlayLinksForTest(err error) (restore func()) {
	orig := overlayLinkat
	overlayLinkat = func(int, string, int, string, int) error { return err }
	return func() { overlayLinkat = orig }
}

// copyNote is told each staged file a refused link made a copy of, with the link's error, so the
// install can say so (a copy is never taken for already delivered, and an inherited config
// delivered as one needs the delivery record to be admitted again: issue #70). nil says nothing.
type copyNote func(from string, linkErr error)

// linkOrCopyAt makes name in the directory dirfd a hard link of the regular file from, and copies
// from there at perm when the link is refused for any reason but the name being taken, telling
// onCopy so.
func linkOrCopyAt(from string, perm os.FileMode, dirfd int, name, display string, onCopy copyNote) error {
	err := overlayLinkat(unix.AT_FDCWD, from, dirfd, name, 0)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return &os.PathError{Op: "link", Path: display, Err: err}
	}
	if onCopy != nil {
		onCopy(from, err)
	}
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
// regular file the SAME FILE (device and inode) as the staged one at the same path, every
// symbolic link the same text, every directory holding exactly the names the staged one holds.
// Anything else — a missing or extra name, a different type, a file with an inode of its own,
// an unreadable directory — is false, and the caller replaces the destination.
func overlayInstalledFrom(src string, srcInfo os.FileInfo, dir *os.Root, rel string) bool {
	if !srcInfo.IsDir() {
		cur, err := dir.Lstat(rel)
		return err == nil && sameEntry(src, srcInfo, cur, func() (string, error) { return dir.Readlink(rel) })
	}
	cur, err := dir.Lstat(rel)
	if err != nil || !cur.IsDir() || !dirModeIntact(uint32(cur.Mode().Perm()), 0o755) {
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
			if st.Mode&unix.S_IFMT != unix.S_IFREG || !sameInode(sfi, uint64(st.Dev), uint64(st.Ino)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// sameEntry compares one non-directory staged entry with the one installed.
func sameEntry(src string, srcInfo, cur os.FileInfo, readlink func() (string, error)) bool {
	switch {
	case srcInfo.Mode()&os.ModeSymlink != 0:
		if cur.Mode()&os.ModeSymlink == 0 {
			return false
		}
		a, err1 := os.Readlink(src)
		b, err2 := readlink()
		return err1 == nil && err2 == nil && a == b
	case srcInfo.Mode().IsRegular():
		return cur.Mode().IsRegular() && os.SameFile(srcInfo, cur)
	}
	return false
}

// sameInode reports whether fi is the file at device dev, inode ino.
func sameInode(fi os.FileInfo, dev, ino uint64) bool {
	sdev, sino, ok := fileIdentity(fi)
	return ok && sdev == dev && sino == ino
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

// fileIdentity is fi's device and inode, when fi came from a stat of this platform.
func fileIdentity(fi os.FileInfo) (dev, ino uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), true //nolint:unconvert // Dev and Ino differ in type by platform
}
