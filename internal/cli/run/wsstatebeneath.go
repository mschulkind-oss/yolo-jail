package run

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// HOST CODE TOUCHES JAIL-WRITABLE STATE ONLY BENEATH AN os.Root (docs/reference/jail-home.md,
// "Host code touches jail-writable state only beneath an os.Root").
//
// The host launcher's reads, writes and copies in the workspace overlay (wsState,
// `<workspace>/.yolo/home`) are made beneath an os.Root, because the jail can write every path
// they land on. Apple Container binds wsState over the whole jail home; podman binds each
// selected pack's declared state dir from it; and on both, the workspace bind puts the whole of
// `<workspace>/.yolo`, wsState included, at /workspace/.yolo in the jail. So a jail can put a
// symlink at any path the launcher touches, at any directory above one, or at wsState or
// `.yolo` themselves. A plain path operation on the next launch follows it: a copy truncates
// and overwrites whatever host file the link names, a read copies a host file into the jail's
// state, and a MkdirAll through a linked directory creates the mountpoint outside the overlay.
// Podman also resolves a bind SOURCE on the host, so a link left at one binds whatever it
// points to into the next jail.
//
// os.Root resolves every component itself and refuses one that leaves the root, including a
// component swapped for a link between two calls, on Linux and on macOS alike (the os.Root doc
// names js as the one GOOS where that race is open). It follows a link only when the link is
// relative and stays inside; an absolute one, which is what a link the jail makes to a
// /home/agent/... path is, is refused. So the checks below decide only whether the write is
// DELIVERED; containment holds whatever the jail does.
//
// The ROOT is opened refusing a link at it (openStateRoot), because os.OpenRoot follows one:
// the containment is only as good as the directory it is opened on.

// linkedStateRootError is the refusal of a jail-writable root that is a symbolic link: the
// same refusal paths.OpenStateDirRoot returns for `.yolo` and the machine store's dirs.
type linkedStateRootError = paths.LinkedStateDirError

// linkedWorkspaceState returns `<workspace>/.yolo` or `<workspace>/.yolo/home` when either is
// a symbolic link, and "" when neither is. Both are reachable from the jail through the
// workspace bind, so either can be a link the jail left for the next launch: every host write
// below it would follow it, and podman would bind whatever its bind sources then resolve to.
// Run refuses such a launch (its first check after the workspace-scope guard), and
// prepareWsState writes nothing below one.
func linkedWorkspaceState(workspace string) string {
	for _, p := range []string{paths.WorkspaceStateDir(workspace), paths.WorkspaceHomeState(workspace)} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return p
		}
	}
	return ""
}

// openStateRoot opens dir, a jail-writable directory such as wsState, as an os.Root, creating
// it (and its parent) when missing. It refuses, with a linkedStateRootError naming the path,
// when dir or its parent is a symbolic link: for wsState those are `.yolo/home` and `.yolo`,
// the two components the jail can replace. The parent is opened first and dir beneath it, and
// each open is checked against the directory that was Lstat'ed, so a link swapped in between
// the check and the open is refused too.
func openStateRoot(dir string) (*os.Root, error) {
	dir = filepath.Clean(dir)
	parent, err := openDirRefusingLink(filepath.Dir(dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openChildRefusingLink(parent, filepath.Base(dir), dir)
}

// openDirRefusingLink opens dir as an os.Root, creating it with MkdirAll when it is missing,
// and refusing when dir itself is a symbolic link (paths.OpenStateDirRoot, whose refusal is
// the same text as linkedStateRootError's). The components above dir are followed: they are
// host paths the jail cannot write (the workspace's own, or the machine store's).
func openDirRefusingLink(dir string) (*os.Root, error) {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return paths.OpenStateDirRoot(dir)
}

// openChildRefusingLink opens name, a direct child of parent, as an os.Root, creating it when
// missing and refusing when it is a symbolic link; full names it in the refusal.
func openChildRefusingLink(parent *os.Root, name, full string) (*os.Root, error) {
	fi, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := parent.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		fi, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &linkedStateRootError{Path: full}
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
		return nil, &linkedStateRootError{Path: full}
	}
	return r, nil
}

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
	r, err := openStateRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return writeBeneath(r, rel, info.Mode().Perm(), 0, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}

// writeFileBeneath is os.WriteFile below root, on copyFileBeneath's terms: perm applies to a
// file it creates, a non-regular file at rel is replaced, and a rel that leaves the root is
// refused.
func writeFileBeneath(root, rel string, data []byte, perm os.FileMode) error {
	r, err := openStateRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return writeBeneath(r, rel, perm, 0, writeBytes(data))
}

// writeFileBeneathMode is writeFileBeneath that also sets mode on the file it wrote, whether it
// created it or not — through the open file, never a path, so no link is chmodded.
func writeFileBeneathMode(root, rel string, data []byte, mode os.FileMode) error {
	r, err := openStateRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return writeBeneath(r, rel, mode, mode, writeBytes(data))
}

func writeBytes(data []byte) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}
}

// writeBeneath writes a regular file at rel below r. A non-regular file already at rel is
// removed first; a regular one is truncated and rewritten IN PLACE, keeping its inode, because
// a file bind pins the inode it captured (jail-home.md, "No rename-writes to mount-visible
// files"). perm applies when the file is created; mode, when not zero, is set on the open file.
func writeBeneath(r *os.Root, rel string, perm, mode os.FileMode, fill func(io.Writer) error) error {
	if err := mkdirParentBeneath(r, rel); err != nil {
		return err
	}
	if fi, err := r.Lstat(rel); err == nil && !fi.Mode().IsRegular() {
		if err := r.Remove(rel); err != nil {
			return err
		}
	}
	out, err := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := fill(out); err != nil {
		out.Close()
		return err
	}
	if mode != 0 {
		if err := out.Chmod(mode); err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}

// mountpointBeneath creates an empty directory (kind "dir") or an empty regular file at rel
// below root when nothing is there, creating rel's missing parents, and reports whether
// nothing was there. Something already at rel, or a rel that leaves the root through a
// symlinked directory, reports false and creates nothing. The file is created with O_EXCL, so
// it is never written through a link either.
func mountpointBeneath(root, rel, kind string) (absent bool) {
	r, err := openStateRoot(root)
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

func mkdirParentBeneath(r *os.Root, rel string) error {
	dir := filepath.Dir(filepath.Clean(rel))
	if dir == "." {
		return nil
	}
	return r.MkdirAll(dir, 0o755)
}

// mkdirAllBeneath is os.MkdirAll of rel below root: a link on the way that stays inside the
// root is followed, and one that leaves it refuses the mkdir.
func mkdirAllBeneath(root, rel string) error {
	r, err := openStateRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.MkdirAll(rel, 0o755)
}

// printReplacedLinks names each link a bind-source helper replaced beneath wsState (each rel is
// relative to it), so a launch never replaces one silently: the jail may have left it there,
// and what the user expected to find behind it is not there any more.
func printReplacedLinks(out printer, wsState string, replaced []string) {
	for _, rel := range replaced {
		out.print("[yellow]Replaced a symbolic link at " + filepath.Join(wsState, rel) +
			": it is a bind source, and podman binds whatever a link there points to. The " +
			"jail can write " + wsState + " (it is under /workspace), so the link may be its.[/yellow]")
	}
}

// ensureBindSourceDir is bindSourceDirBeneath for a caller holding only the root's path. It is
// best-effort, as the MkdirAll it replaces was: podman names a bind source it cannot find.
func ensureBindSourceDir(root, rel string) (replaced []string) {
	r, err := openStateRoot(root)
	if err != nil {
		return nil
	}
	defer r.Close()
	replaced, _ = bindSourceDirBeneath(r, rel, 0o755)
	return replaced
}

// bindSourceDirBeneath makes every component of rel below r a REAL directory, creating the
// missing ones with perm, and returns the components it found as symbolic links and replaced.
//
// It is for podman BIND SOURCES, and replacing rather than refusing is the point: podman
// resolves a bind source on the host, so a link left at one, or above one, binds whatever it
// points to into the jail, and refusing only the host's own mkdir would still hand the link to
// podman. A link at a bind source is never the jail's ordinary use of its home: the jail sees
// the source as the mountpoint itself, which it cannot replace, and reaches it only through
// /workspace/.yolo/home. Removing the link loses nothing but the link. A component that is a
// regular file is left alone and reported as an error.
func bindSourceDirBeneath(r *os.Root, rel string, perm os.FileMode) (replaced []string, err error) {
	cur := ""
	for _, seg := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		fi, err := r.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return replaced, err
		case fi.Mode()&fs.ModeSymlink != 0:
			if err := r.Remove(cur); err != nil {
				return replaced, err
			}
			replaced = append(replaced, cur)
		case fi.IsDir():
			continue
		default:
			return replaced, &fs.PathError{Op: "mkdir", Path: cur, Err: syscall.ENOTDIR}
		}
		if err := r.Mkdir(cur, perm); err != nil && !errors.Is(err, fs.ErrExist) {
			return replaced, err
		}
	}
	return replaced, nil
}

// bindSourceFileBeneath is bindSourceDirBeneath for a SINGLE-FILE bind source: it creates an
// empty regular file at rel when nothing is there, and replaces a symbolic link there with one
// (reporting replaced). A regular file already there is left exactly as it is. It replaced
// touchFile, whose O_CREATE followed a dangling link and created its target, and which left a
// link to an existing file in place for podman to bind.
func bindSourceFileBeneath(r *os.Root, rel string) (replaced bool, err error) {
	if dir := filepath.Dir(filepath.Clean(rel)); dir != "." {
		if _, err := bindSourceDirBeneath(r, dir, 0o755); err != nil {
			return false, err
		}
	}
	fi, err := r.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, err
	case fi.Mode()&fs.ModeSymlink != 0:
		if err := r.Remove(rel); err != nil {
			return false, err
		}
		replaced = true
	default:
		return false, nil
	}
	f, err := r.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return replaced, err
	}
	return replaced, f.Close()
}

// readRegularFileIn reads name in dir, a jail-writable directory such as `.yolo`, only when
// neither dir nor name is a symbolic link and name is a regular file: the jail can put a link
// at either, and a host read through it would hand the jail whatever host file it names. It
// creates nothing; a missing dir or file is an ordinary error.
func readRegularFileIn(dir, name string) ([]byte, error) {
	dfi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if dfi.Mode()&fs.ModeSymlink != 0 {
		return nil, &linkedStateRootError{Path: dir}
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if st, err := r.Stat("."); err != nil || !os.SameFile(dfi, st) {
		return nil, &linkedStateRootError{Path: dir}
	}
	fi, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "read", Path: filepath.Join(dir, name), Err: syscall.EINVAL}
	}
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !os.SameFile(fi, st) {
		return nil, &fs.PathError{Op: "read", Path: filepath.Join(dir, name), Err: syscall.EINVAL}
	}
	return io.ReadAll(f)
}

// readDirBeneath lists rel below r, sorted by name, or nil when it cannot be read.
func readDirBeneath(r *os.Root, rel string) []fs.DirEntry {
	f, err := r.Open(rel)
	if err != nil {
		return nil
	}
	defer f.Close()
	entries, _ := f.ReadDir(-1)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries
}

// copyFileIfMissing copies the regular file s below src to d below dst when NOTHING is at d —
// anything already there, a dangling link included, counts as an existing target — keeping
// the source's permission bits. Neither side is followed through a link: s must Lstat as a
// regular file and the opened file must be that same file, and d is created O_EXCL.
func copyFileIfMissing(src *os.Root, s string, dst *os.Root, d string) error {
	fi, err := src.Lstat(s)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return &fs.PathError{Op: "copy", Path: s, Err: syscall.EINVAL}
	}
	if _, err := dst.Lstat(d); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := mkdirParentBeneath(dst, d); err != nil {
		return err
	}
	in, err := src.OpenFile(s, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	if st, err := in.Stat(); err != nil || !st.Mode().IsRegular() || !os.SameFile(fi, st) {
		return &fs.PathError{Op: "copy", Path: s, Err: syscall.EINVAL}
	}
	out, err := dst.OpenFile(d, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = dst.Remove(d)
		return err
	}
	return out.Close()
}

// copyLinkIfMissing recreates the symbolic link s below src at d below dst when nothing is at
// d. The link is copied as a link, never followed: the jail wrote it, and it resolves where
// the jail reads it, not on the host.
func copyLinkIfMissing(src *os.Root, s string, dst *os.Root, d string) error {
	target, err := src.Readlink(s)
	if err != nil {
		return err
	}
	if _, err := dst.Lstat(d); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := mkdirParentBeneath(dst, d); err != nil {
		return err
	}
	return dst.Symlink(target, d)
}

// copyTreeBeneath copies the host tree src to rel below r, carrying copyTree's mode rule (a
// source's exec bit and nothing else; packs.go says why). rel must not exist: the caller
// removes it first, so nothing the jail left there is written through.
func copyTreeBeneath(src string, r *os.Root, rel string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		sub, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(rel, sub)
		if d.IsDir() {
			return r.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if fi, statErr := d.Info(); statErr == nil && fi.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		return writeBeneath(r, target, mode, mode, writeBytes(data))
	})
}
