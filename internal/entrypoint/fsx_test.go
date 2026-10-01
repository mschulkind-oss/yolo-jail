package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE KERNEL ESCAPES A MOUNT POINT'S SPACE. /proc/self/mountinfo writes a space in a path as
// \040 (and a tab, a newline and a backslash as \011, \012, \134), so a mount at a path with
// a space in it was compared escaped against the path unescaped and never matched: the render
// went on to write a surface through the bind mount it is meant to leave alone.
func TestAMountPointWithASpaceIsAMountPoint(t *testing.T) {
	const mountinfo = "22 1 0:21 / /proc rw,nosuid - proc proc rw\n" +
		"605 590 0:44 /src /home/agent/.config/Some\\040App/config.json ro - btrfs /dev/x rw\n" +
		"606 590 0:44 /src /home/agent/back\\134slash ro - btrfs /dev/x rw\n"
	for _, p := range []string{"/proc", "/home/agent/.config/Some App/config.json", `/home/agent/back\slash`} {
		if !mountPointIn(mountinfo, p) {
			t.Errorf("%q is a mount point in\n%s", p, mountinfo)
		}
	}
	for _, p := range []string{"/home/agent/.config/Some", `/home/agent/.config/Some\040App/config.json`, "/home"} {
		if mountPointIn(mountinfo, p) {
			t.Errorf("%q is not a mount point in\n%s", p, mountinfo)
		}
	}
}

// IsMountPoint reads the real table: every mount point this process can see is one. Delete the
// read and this fails. Only the unescaped lines are taken, so the expectation is the kernel's
// own spelling rather than this package's decoding of it; the escapes are
// TestAMountPointWithASpaceIsAMountPoint's.
func TestIsMountPointReadsTheProcessMountTable(t *testing.T) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Skip("no /proc/self/mountinfo on this OS")
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 5 && !strings.Contains(f[4], `\`) {
			p := f[4]
			if !IsMountPoint(p) {
				t.Errorf("IsMountPoint(%q) = false for a line of the process's own table:\n%s", p, line)
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("the process's mount table lists no mount point")
	}
}

func TestWriteInPlacePreservesInode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "briefing.md")
	if err := WriteStringInPlace(p, "v1", 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStringInPlace(p, "version two, longer", 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// The inode must be preserved (truncate-in-place, not unlink+recreate) —
	// this is what keeps a file->file bind mount seeing refreshes.
	if !os.SameFile(before, after) {
		t.Error("WriteInPlace changed the inode — a bind mount would stop seeing refreshes")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "version two, longer" {
		t.Errorf("content = %q", got)
	}
}

func TestClearContentsKeepsDir(t *testing.T) {
	dir := t.TempDir()
	anchor := filepath.Join(dir, "skills")
	if err := os.MkdirAll(filepath.Join(anchor, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(anchor, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err := ClearContents(anchor); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(anchor)
	if err != nil {
		t.Fatalf("anchor dir was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("ClearContents replaced the dir — a mount anchor would detach")
	}
	entries, _ := os.ReadDir(anchor)
	if len(entries) != 0 {
		t.Errorf("dir not empty after clear: %v", entries)
	}
}

func TestClearContentsMissingDirOK(t *testing.T) {
	if err := ClearContents(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Errorf("clearing a missing dir should be a no-op, got %v", err)
	}
}

func TestEnsureRelativeSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "cur")

	// Fresh create.
	if err := EnsureRelativeSymlink("../target", link); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(link)
	if err != nil || got != "../target" {
		t.Fatalf("readlink = %q, %v; want ../target", got, err)
	}

	// Idempotent: same target -> no error, unchanged.
	if err := EnsureRelativeSymlink("../target", link); err != nil {
		t.Fatal(err)
	}

	// Retarget: different target -> replaced.
	if err := EnsureRelativeSymlink("../other", link); err != nil {
		t.Fatal(err)
	}
	got, _ = os.Readlink(link)
	if got != "../other" {
		t.Errorf("retarget readlink = %q, want ../other", got)
	}

	// Replace a non-symlink file.
	plain := filepath.Join(dir, "plain")
	os.WriteFile(plain, []byte("x"), 0o644)
	if err := EnsureRelativeSymlink("dest", plain); err != nil {
		t.Fatal(err)
	}
	got, _ = os.Readlink(plain)
	if got != "dest" {
		t.Errorf("replaced-file readlink = %q, want dest", got)
	}
}
