//go:build linux || darwin

package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// openat is unix.Openat; a test replaces it to make the kernel's lost create race happen on
// a kernel that does not lose it.
var openat = unix.Openat

// fstatFD is unix.Fstat; a test replaces it with an APFS stand-in, which reports a removed
// directory with a directory mode and a link count of 2.
var fstatFD = unix.Fstat

// fdIsLiveDir reports whether fd, the open directory Root holds for the directory rel names
// under r, is still the directory at that name. It compares the descriptor's (dev, ino) with
// those of the name as r resolves it from outside (rootDirOutside). The link count and the
// directory mode are not used: on macOS APFS fstat of a removed directory's descriptor still
// reports nlink=2 and a directory mode (measured on macOS 26.5 on 2026-11-24), so only the name tells a removed
// directory from a live one. A descriptor on a removed directory matches nothing, and so a genuine ENOENT is not retried (the retries are also bounded).
func fdIsLiveDir(r *os.Root, rel string, fd int) bool {
	var st unix.Stat_t
	if err := fstatFD(fd, &st); err != nil {
		return false
	}
	out, err := rootDirOutside(r, rel)
	if err != nil {
		return false
	}
	o, ok := out.Sys().(*syscall.Stat_t)
	return ok && uint64(o.Dev) == uint64(st.Dev) && uint64(o.Ino) == uint64(st.Ino)
}

// openNoFollowLeaf opens the final component relative to the directory Root actually
// holds. os.Root.OpenFile retries a symlink which stays inside its root, even when
// O_NOFOLLOW is supplied, so it cannot enforce this leaf policy.
func openNoFollowLeaf(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
	if name == "" || filepath.Clean(name) != name || filepath.Base(name) == "." || filepath.Base(name) == ".." {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: fs.ErrInvalid}
	}
	if perm&^0o777 != 0 {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: errors.New("unsupported file mode")}
	}
	parent, err := r.OpenRoot(filepath.Dir(name))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	anchor, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer anchor.Close()
	// Truncation happens only after the opened inode has been checked as regular.
	openFlag := flag&^os.O_TRUNC | unix.O_NOFOLLOW | unix.O_CLOEXEC
	var fd int
	for tries := 0; ; {
		fd, err = openat(int(anchor.Fd()), filepath.Base(name), openFlag, uint32(perm))
		if err == unix.EINTR {
			continue
		}
		// A creating open that reports ENOENT while its directory is still there lost a race
		// the kernel should not have lost: see retryCreateENOENT.
		if err == unix.ENOENT && flag&os.O_CREATE != 0 && tries < createRetries && fdIsLiveDir(r, filepath.Dir(name), int(anchor.Fd())) {
			tries++
			continue
		}
		break
	}
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}
