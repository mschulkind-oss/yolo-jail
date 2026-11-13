package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generation reaper keeps a bundle a live helper executes from only if the REAL process
// table names that helper's file (PH-D8). This reads the real table, on Linux and on darwin
// (kern.procargs2), and requires the running test binary to be in it, symlinks resolved: a
// reader that reports ok but misses its own caller would let a sweep delete a running helper's
// bundle. It is also the Mac witness W-PH5's check (docs/reference/pinned-host-helpers.md).
func TestProcessExesNamesTheRunningBinary(t *testing.T) {
	exes, ok := ProcessExes()
	if !ok {
		t.Fatal("ProcessExes could not read this user's process table")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	for _, exe := range exes {
		exe = strings.TrimSuffix(exe, " (deleted)")
		if exe == want {
			return
		}
		if r, err := filepath.EvalSymlinks(exe); err == nil && r == want {
			return
		}
	}
	t.Fatalf("ProcessExes (%d entries) does not name the running test binary %s", len(exes), want)
}
