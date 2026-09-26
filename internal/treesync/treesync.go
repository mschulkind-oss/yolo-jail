// Package treesync makes one directory tree hold another's content WITHOUT replacing what
// did not change: the non-destructive re-stage a tree needs once a live jail has bound it.
//
// It exists because the per-workspace staging trees under AGENTS_DIR/<cname> — the staged
// packs a podman jail binds at /ctx/packs, the `files` trees and loophole module dirs bound
// from inside them, the skills trees bound at each skills destination — are rebuilt by EVERY
// invocation, an attach to a running jail included. They used to be rebuilt by clearing them
// and copying again, and a bind mount captures an inode, not a path: a directory removed and
// recreated under a live bind leaves the jail looking at the removed one, which is empty. So
// every attach, with a config identical to the one the jail booted with, emptied that jail's
// directory binds into its own staged tree, and a reader walking one mid-copy saw half of it.
//
// Sync is the other way to arrive at the same content. The rules, each what a bind into dst
// requires:
//
//  1. dst itself is never removed or recreated — the packstage rule 3 inode invariant, for
//     every tree this is pointed at. It is created (0755) when absent.
//  2. A directory present on both sides is recursed into, never replaced, so a bind of it
//     keeps seeing it and its updated entries.
//  3. A regular file whose bytes and mode already match is not touched: its inode, and a
//     single-file bind of it, survive.
//  4. A file that differs is written to a temporary sibling and renamed over the old one, so a
//     concurrent reader sees the old bytes or the new ones and never a truncated file.
//  5. An entry of another type, or one src does not have, is removed.
//
// MODES follow the rule both staging copiers already apply (run.copyTree for an embedded pack,
// packstage.copyFile for a configured one): 0755 for a file carrying any execute bit in src,
// 0644 otherwise, and 0755 for a directory. So a tree synced from a staged source is
// byte-for-byte and mode-for-mode what copying it would have produced.
//
// src must hold only regular files and directories, which every staged tree does (packstage
// resolves in-pack symlinks and refuses escaping ones; the skills copy dereferences). Anything
// else in src is an error rather than a guess.
package treesync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Sync makes dst hold exactly src's files and directories under the package rules, and reports
// whether it changed anything in dst.
func Sync(src, dst string) (bool, error) {
	sfi, err := os.Stat(src)
	if err != nil {
		return false, err
	}
	if !sfi.IsDir() {
		return false, fmt.Errorf("sync source %s is not a directory", src)
	}
	changed := false
	dfi, err := os.Lstat(dst)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return false, err
		}
		changed = true
	case err != nil:
		return false, err
	case !dfi.IsDir():
		// Rule 1 forbids replacing dst, so a dst that is not a directory is the caller's
		// problem to resolve, not this function's to delete.
		return false, fmt.Errorf("sync destination %s is not a directory", dst)
	}
	c, err := syncDir(src, dst)
	return changed || c, err
}

func syncDir(src, dst string) (bool, error) {
	srcEntries, err := os.ReadDir(src)
	if err != nil {
		return false, err
	}
	changed := false
	want := make(map[string]bool, len(srcEntries))
	for _, e := range srcEntries {
		name := e.Name()
		want[name] = true
		s, d := filepath.Join(src, name), filepath.Join(dst, name)
		sfi, err := os.Lstat(s)
		if err != nil {
			return changed, err
		}
		switch {
		case sfi.IsDir():
			c, err := ensureDir(d)
			changed = changed || c
			if err != nil {
				return changed, err
			}
			c, err = syncDir(s, d)
			changed = changed || c
			if err != nil {
				return changed, err
			}
		case sfi.Mode().IsRegular():
			c, err := syncFile(s, d, sfi)
			changed = changed || c
			if err != nil {
				return changed, err
			}
		default:
			return changed, fmt.Errorf("sync source %s is neither a regular file nor a directory", s)
		}
	}
	// Removals LAST, after every addition and update: a reader of a tree being re-synced to a
	// different content sees the new entries arrive before the old ones go.
	dstEntries, err := os.ReadDir(dst)
	if err != nil {
		return changed, err
	}
	for _, e := range dstEntries {
		if want[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// ensureDir makes d a directory at 0755, keeping an existing directory (rule 2) and replacing
// anything else that sits at its path (rule 5).
func ensureDir(d string) (bool, error) {
	dfi, err := os.Lstat(d)
	if err == nil && dfi.IsDir() {
		if dfi.Mode().Perm() == 0o755 {
			return false, nil
		}
		return true, os.Chmod(d, 0o755)
	}
	if err == nil {
		if err := os.RemoveAll(d); err != nil {
			return false, err
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Mkdir(d, 0o755); err != nil {
		return false, err
	}
	// Mkdir's mode is filtered by the umask; the staged mode is not the caller's umask's to
	// decide.
	return true, os.Chmod(d, 0o755)
}

// syncFile makes d a regular file with s's bytes, at the staged mode, touching nothing when it
// already is one (rule 3) and replacing it by rename otherwise (rule 4).
func syncFile(s, d string, sfi os.FileInfo) (bool, error) {
	mode := os.FileMode(0o644)
	if sfi.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	dfi, err := os.Lstat(d)
	switch {
	case err == nil && dfi.Mode().IsRegular():
		if dfi.Mode().Perm() == mode && dfi.Size() == sfi.Size() {
			same, err := sameBytes(s, d)
			if err != nil {
				return false, err
			}
			if same {
				return false, nil
			}
		}
	case err == nil:
		// A directory or a link where a file belongs: a rename cannot replace a directory, and
		// the entry is going regardless (rule 5).
		if err := os.RemoveAll(d); err != nil {
			return false, err
		}
	case !os.IsNotExist(err):
		return false, err
	}
	return true, replaceFile(s, d, mode)
}

// replaceFile writes s's bytes to a temporary sibling of d and renames it over d.
func replaceFile(s, d string, mode os.FileMode) (err error) {
	in, err := os.Open(s)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(d), "."+filepath.Base(d)+".yolo-sync-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes 0600; the staged mode is set explicitly, as both copiers do, because a
	// create mode is filtered by the umask.
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), d)
}

// sameBytes reports whether two files hold identical bytes, streaming so a large staged
// binary costs two buffers rather than two copies of itself.
func sameBytes(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	bufA, bufB := make([]byte, 64*1024), make([]byte, 64*1024)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		endA := errors.Is(errA, io.EOF) || errors.Is(errA, io.ErrUnexpectedEOF)
		endB := errors.Is(errB, io.EOF) || errors.Is(errB, io.ErrUnexpectedEOF)
		if errA != nil && !endA {
			return false, errA
		}
		if errB != nil && !endB {
			return false, errB
		}
		if endA || endB {
			return endA && endB, nil
		}
	}
}
