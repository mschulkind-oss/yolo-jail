package run

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// The host launcher's writes INTO the workspace overlay (wsState, `<workspace>/.yolo/home`) are
// made beneath an os.Root, because the jail can write every path the writes land on. Apple
// Container binds wsState over the whole jail home, and podman binds each selected pack's
// declared state dir from it, so a jail can put a symlink at any of those paths or at any
// directory above one. A plain path write on the next launch follows it: a copy truncates and
// overwrites whatever host file the link names, and a MkdirAll through a linked directory
// creates the mountpoint outside the overlay.
//
// os.Root resolves every component itself and refuses one that leaves the root, including a
// component swapped for a link between two calls, on Linux and on macOS alike (the os.Root doc
// names js as the one GOOS where that race is open). It follows a link only when the link is
// relative and stays inside; an absolute one, which is what a link the jail makes to a
// /home/agent/... path is, is refused. So the checks below decide only whether the write is
// DELIVERED; containment holds whatever the jail does.

// copyFileBeneath copies src to rel below root, creating rel's missing parent directories.
// A non-regular file already at rel (a symlink the jail planted, even one pointing inside the
// root) is removed and replaced by a regular file rather than written through. A rel that
// leaves the root through a symlinked directory is refused and nothing is written.
func copyFileBeneath(src, root, rel string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	r, err := openRootCreating(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := mkdirParentBeneath(r, rel); err != nil {
		return err
	}
	if fi, err := r.Lstat(rel); err == nil && !fi.Mode().IsRegular() {
		if err := r.Remove(rel); err != nil {
			return err
		}
	}
	out, err := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// mountpointBeneath creates an empty directory (kind "dir") or an empty regular file at rel
// below root when nothing is there, creating rel's missing parents, and reports whether
// nothing was there. Something already at rel, or a rel that leaves the root through a
// symlinked directory, reports false and creates nothing. The file is created with O_EXCL, so
// it is never written through a link either.
func mountpointBeneath(root, rel, kind string) (absent bool) {
	r, err := openRootCreating(root)
	if err != nil {
		return false
	}
	defer r.Close()
	if _, err := r.Lstat(rel); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err := mkdirParentBeneath(r, rel); err != nil {
		return false
	}
	if kind == "dir" {
		_ = r.Mkdir(rel, 0o755)
		return true
	}
	if f, err := r.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
		_ = f.Close()
	}
	return true
}

// openRootCreating opens root as an os.Root, creating it first when it is missing — the
// overlay's own path is the launcher's, not the jail's, and the plain writes these replace
// created it the same way.
func openRootCreating(root string) (*os.Root, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return os.OpenRoot(root)
}

func mkdirParentBeneath(r *os.Root, rel string) error {
	dir := filepath.Dir(filepath.Clean(rel))
	if dir == "." {
		return nil
	}
	return r.MkdirAll(dir, 0o755)
}
