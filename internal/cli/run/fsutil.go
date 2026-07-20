package run

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// required for the staging path — mtime is stamped explicitly where it matters).
func copyFile2(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// src into dst, dereferencing symlinks (files and dirs).
func copyTree(src, dst string) error {
	info, err := os.Stat(src) // Stat follows symlinks (symlinks=False deref)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile2(src, dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		si, err := os.Stat(s)
		if err != nil {
			return err
		}
		if si.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
		} else {
			if err := copyFile2(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// randHex returns a 32-char hex string — the uuid4().hex analog for the unique
// nix-build-root.old.<hex> aside name (uniqueness is all that matters; two
// concurrent repopulates must never collide on one fixed .old target).
func randHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
