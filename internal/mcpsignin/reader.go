// Package mcpsignin reads native bundle identity without composing MCP servers.
package mcpsignin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// FileReader is a confined, effect-free read of a retained pack or delivered tree.
type FileReader interface{ ReadFile(string) ([]byte, error) }

const maxIdentityBytes = 4 << 20

// DiskReader pins the trusted tree root. Every component below it is opened
// O_NOFOLLOW, so an in-root symlink or replacement race cannot supply identity.
type DiskReader struct{ root *os.File }

func OpenDisk(root string) (*DiskReader, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &DiskReader{root: os.NewFile(uintptr(fd), root)}, nil
}
func (r *DiskReader) Close() error { return r.root.Close() }
func (r *DiskReader) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
		return nil, fs.ErrInvalid
	}
	fd, err := unix.Dup(int(r.root.Fd()))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, &fs.PathError{Op: "read", Path: name, Err: e}
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", name)
	}
	return readBounded(f)
}

// FSReader accepts an immutable filesystem original or selected filtered view.
// Directory entries classify links before opening; mutable disk uses DiskReader.
type FSReader struct{ FS fs.FS }

func (r FSReader) ReadFile(name string) ([]byte, error) {
	if r.FS == nil || !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
		return nil, fs.ErrInvalid
	}
	dir := "."
	parts := strings.Split(name, "/")
	for i, part := range parts {
		entries, err := fs.ReadDir(r.FS, dir)
		if err != nil {
			return nil, err
		}
		found := false
		for _, entry := range entries {
			if entry.Name() != part {
				continue
			}
			found = true
			if entry.Type()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("%s: symbolic link refused", name)
			}
			if i < len(parts)-1 && !entry.IsDir() {
				return nil, fmt.Errorf("%s: not a directory", name)
			}
			if i == len(parts)-1 && !entry.Type().IsRegular() {
				return nil, fmt.Errorf("%s: not a regular file", name)
			}
		}
		if !found {
			return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
		}
		if dir == "." {
			dir = part
		} else {
			dir += "/" + part
		}
	}
	f, err := r.FS.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return readBounded(f)
}
func readBounded(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxIdentityBytes+1))
	if err == nil && len(data) > maxIdentityBytes {
		err = fmt.Errorf("identity file exceeds %d bytes", maxIdentityBytes)
	}
	return data, err
}
