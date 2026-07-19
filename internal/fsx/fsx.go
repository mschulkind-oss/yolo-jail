// Package fsx codifies the filesystem incident history from the Python code in
// Go, so idiomatic-Go habits (tmp+rename onto bind mounts, rmtree of mount
// anchors) can't reintroduce documented breakages (§3 internal/fsx).
//
// The load-bearing rules:
//
//   - WriteInPlace: truncate + write the SAME inode; NEVER tmp+rename. A
//     file->file bind mount is pinned to the inode it captured at container
//     start, so an atomic rename swaps in a new inode the mount can't see, and
//     running jails silently stop seeing refreshes (docs/design/agent-briefings.md,
//     "treat as load-bearing"). Go's os.WriteFile already truncates in place —
//     this wrapper exists to NAME the invariant and to be the single audited
//     write path (a lint rule bans rename-based writes outside fsx).
//   - ClearContents: empty a directory's CONTENTS without removing the dir
//     itself — the dir is a mount anchor; removing it detaches the mount
//     (2026-07-04 regression).
//     recreate" pattern.
//   - Relative symlinks: created RELATIVE and compared as raw link strings
//     (ensure_global_storage creates relative symlinks; the drift/golden
//     compares readlink targets as strings, not resolved paths).
//
// Source of truth: docs/design/agent-briefings.md + the storage/prune incident
// history in src/.
package fsx

import (
	"os"
	"path/filepath"
)

// WriteInPlace writes data to path by TRUNCATING the existing file in place
// (never tmp+rename), preserving the inode a bind mount may be pinned to.
// Creates the file (mode perm) if absent. This is the ONLY sanctioned write
// path for bind-mount-visible files.
func WriteInPlace(path string, data []byte, perm os.FileMode) error {
	// os.WriteFile opens with O_WRONLY|O_CREATE|O_TRUNC — truncates the
	// existing inode, does not unlink+recreate. That is exactly the required
	// semantic; we wrap it to make the invariant explicit and greppable.
	return os.WriteFile(path, data, perm)
}

// WriteStringInPlace is the string convenience form of WriteInPlace.
func WriteStringInPlace(path, data string, perm os.FileMode) error {
	return WriteInPlace(path, []byte(data), perm)
}

// ClearContents removes every entry INSIDE dir but leaves dir itself in place
// (it may be a mount anchor). A missing dir is not an error (nothing to clear).
func ClearContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// EnsureRelativeSymlink makes linkPath a symlink whose RAW target is exactly
// target (a relative string, as ensure_global_storage creates them). If
// linkPath already exists as a symlink with the identical raw target, it is a
// no-op; if it exists with a different target (or as a non-symlink), it is
// replaced. The stored target is compared as a raw string — never resolved —
// matching the Python golden's readlink comparison.
func EnsureRelativeSymlink(target, linkPath string) error {
	if cur, err := os.Readlink(linkPath); err == nil {
		if cur == target {
			return nil
		}
		if err := os.Remove(linkPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		// linkPath exists but isn't a symlink (Readlink returns EINVAL) — or
		// another error. Remove a non-symlink so we can create the link;
		// propagate genuine errors.
		if _, statErr := os.Lstat(linkPath); statErr == nil {
			if err := os.Remove(linkPath); err != nil {
				return err
			}
		}
	}
	return os.Symlink(target, linkPath)
}

// SameInode reports whether two paths refer to the same (dev, inode) — used by
// callers guarding "only unlink the socket file WE bound" style checks.
func SameInode(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}
