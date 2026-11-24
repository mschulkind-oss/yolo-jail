//go:build linux || darwin

package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// openat is unix.Openat; a test replaces it to make the kernel's lost create race happen on
// a kernel that does not lose it.
var openat = unix.Openat

// fdIsLiveDir reports whether fd is a directory that is still linked. A removed directory
// stays open and stays a directory, with no links left, so a genuine ENOENT is not retried
// forever (the retries are also bounded).
func fdIsLiveDir(fd int) bool {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return false
	}
	return st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Nlink > 0
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
		if err == unix.ENOENT && flag&os.O_CREATE != 0 && tries < createRetries && fdIsLiveDir(int(anchor.Fd())) {
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
