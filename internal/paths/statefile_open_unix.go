//go:build linux || darwin

package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

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
	for {
		fd, err = unix.Openat(int(anchor.Fd()), filepath.Base(name), openFlag, uint32(perm))
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}
