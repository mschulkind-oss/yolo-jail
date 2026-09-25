package paths

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// THE FILES THE HOST WRITES DIRECTLY UNDER <workspace>/.yolo are opened beneath an os.Root on
// `.yolo`, never by plain path (docs/reference/jail-home.md, "Host code in jail-writable
// state"). `.yolo` is jail-writable: both container backends bind the workspace at /workspace
// and hide nothing under it. So the last jail can leave a symbolic link at any name here, and
// the next launch's plain os.WriteFile, or O_APPEND open, follows it onto a host file of the
// jail's choosing. The launch log's tee appended launch output, which quotes the jail-writable
// workspace config, to whatever the link named (~/.bashrc, say), and a config snapshot
// truncated the file it named and wrote config JSON into it.
//
// internal/cli/run/wsstatebeneath.go holds the same rule for the workspace overlay below
// `.yolo`, and opens its roots through OpenStateDirRoot.

// LinkedStateDirError is the refusal of a jail-writable directory that is a symbolic link.
type LinkedStateDirError struct{ Path string }

func (e *LinkedStateDirError) Error() string {
	return "refusing to write beneath " + e.Path + ": it is a symbolic link, and the jail can " +
		"write it, so every write below it would land wherever the link points (remove the " +
		"link; yolo recreates the directory)"
}

// OpenStateDirRoot opens dir, an existing jail-writable directory, as an os.Root, refusing
// with a *LinkedStateDirError naming it when dir is a symbolic link: os.OpenRoot follows one,
// and the containment is only as good as the directory it is opened on. The open is checked
// against the directory that was Lstat'ed, so a link swapped in between is refused too. The
// components ABOVE dir are followed: they are host paths the jail cannot write (the
// workspace's own, or the machine store's).
func OpenStateDirRoot(dir string) (*os.Root, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &LinkedStateDirError{Path: dir}
	}
	if !fi.IsDir() {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: syscall.ENOTDIR}
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if st, err := r.Stat("."); err != nil || !os.SameFile(fi, st) {
		r.Close()
		return nil, &LinkedStateDirError{Path: dir}
	}
	return r, nil
}

// OpenStateSubdirRoot opens name, an EXISTING direct child of parent (a root on a jail-writable
// directory), as an os.Root, refusing with a *LinkedStateDirError naming full when name is a
// symbolic link: parent.OpenRoot follows a link that stays inside parent, and the jail can put
// one there. The open is checked against the directory that was Lstat'ed, so a link swapped in
// between is refused too. It creates nothing: a missing name is an fs.ErrNotExist.
func OpenStateSubdirRoot(parent *os.Root, name, full string) (*os.Root, error) {
	fi, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &LinkedStateDirError{Path: full}
	}
	if !fi.IsDir() {
		return nil, &fs.PathError{Op: "open", Path: full, Err: syscall.ENOTDIR}
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	if st, err := r.Stat("."); err != nil || !os.SameFile(fi, st) {
		r.Close()
		return nil, &LinkedStateDirError{Path: full}
	}
	return r, nil
}

// OpenWorkspaceStateSubdir opens name, an existing directory directly under
// <workspace>/.yolo (the overlay "home", the sidecar dir "prism"), as an os.Root, refusing a
// link at `.yolo` or at name with a *LinkedStateDirError naming it. It creates nothing, and
// is for host code that only reads, or writes only into state that already exists, such as
// the capture at jail exit and `yolo prune`.
func OpenWorkspaceStateSubdir(workspace, name string) (*os.Root, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	dir := WorkspaceStateDir(workspace)
	parent, err := OpenStateDirRoot(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return OpenStateSubdirRoot(parent, name, filepath.Join(dir, name))
}

// ReadRegularFileBeneath reads name below r only when it is a regular file: a link at name,
// dangling or not, is not read (r refuses one that leaves it; this refuses one that stays), and
// the opened file is checked to be regular, O_NONBLOCK keeping a FIFO swapped in meanwhile from
// hanging the open. A missing name is an fs.ErrNotExist.
func ReadRegularFileBeneath(r *os.Root, name string) ([]byte, error) {
	f, err := OpenRegularFileBeneath(r, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// OpenRegularFileBeneath opens name below r for reading, on ReadRegularFileBeneath's terms, for
// a caller that streams the file (a hash) or needs its identity (the opened file's Stat).
func OpenRegularFileBeneath(r *os.Root, name string) (*os.File, error) {
	return openRegularBeneath(r, name, os.O_RDONLY, 0)
}

// WriteRegularFileBeneath is os.WriteFile of name below r, on OpenWorkspaceStateFile's terms: a
// regular file already there is truncated and rewritten in place, keeping its inode; anything
// else there (a link the jail left, dangling or not) is removed and replaced by a regular file;
// and a name that leaves r through a linked directory is refused. name's parent must exist.
func WriteRegularFileBeneath(r *os.Root, name string, data []byte, perm fs.FileMode) error {
	f, err := openRegularBeneath(r, name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// OpenWorkspaceStateFile opens name, a file directly under <workspace>/.yolo, with flag and
// perm (os.OpenFile's meanings), beneath a root on `.yolo`. The directory is created through
// EnsureWorkspaceStateDir, so the scope refusal and the .gitignore apply. Anything at name
// that is not a regular file (a link the jail left there, dangling or not) is removed first,
// so an O_CREATE creates a real file rather than following the link; and the opened file is
// checked to be a regular file, O_NONBLOCK keeping a FIFO swapped in meanwhile from hanging
// the open. name must be a single path component.
func OpenWorkspaceStateFile(workspace, name string, flag int, perm fs.FileMode) (*os.File, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	dir, err := EnsureWorkspaceStateDir(workspace)
	if err != nil {
		return nil, err
	}
	r, err := OpenStateDirRoot(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return openRegularBeneath(r, name, flag, perm)
}

// WriteWorkspaceStateFile is os.WriteFile of name directly under <workspace>/.yolo, on
// OpenWorkspaceStateFile's terms: a regular file already there is truncated and rewritten in
// place, and anything else there is replaced.
func WriteWorkspaceStateFile(workspace, name string, data []byte, perm fs.FileMode) error {
	f, err := OpenWorkspaceStateFile(workspace, name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// openRegularBeneath opens name below r as a regular file, removing a non-regular file there
// first when flag creates.
func openRegularBeneath(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
	if fi, err := r.Lstat(name); err == nil && !fi.Mode().IsRegular() {
		if flag&os.O_CREATE == 0 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EINVAL}
		}
		if err := r.Remove(name); err != nil {
			return nil, err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	f, err := r.OpenFile(name, flag|syscall.O_NONBLOCK, perm)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EINVAL}
	}
	return f, nil
}
