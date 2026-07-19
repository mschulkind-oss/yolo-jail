package agentsmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoopholeFirst(t *testing.T) {
	cases := map[string]string{
		"audio. Second sentence here":    "audio",
		"single line no period":          "single line no period",
		"first line\nsecond line":        "first line",
		"trailing dots...":               "trailing dots",
		"":                               "",
		"PipeWire pass-through. More.\n": "PipeWire pass-through",
	}
	for in, want := range cases {
		if got := loopholeFirst(in); got != want {
			t.Errorf("loopholeFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeBriefing(t *testing.T) {
	if got := ComposeBriefing("body\n", ""); got != "body\n" {
		t.Errorf("no extra = %q", got)
	}
	if got := ComposeBriefing("body\n", "  extra  \n\n"); got != "body\n\n  extra\n" {
		t.Errorf("with extra = %q", got)
	}
}

func TestWriteBriefingBreaksHardlink(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	must(t, os.WriteFile(a, []byte("shared"), 0o644))
	must(t, os.Link(a, b)) // a and b now share an inode (nlink=2)

	// Writing b must break the link (fresh inode), leaving a untouched.
	must(t, WriteBriefing(b, "new b content"))
	if data, _ := os.ReadFile(a); string(data) != "shared" {
		t.Errorf("a clobbered through hardlink: %q", data)
	}
	if data, _ := os.ReadFile(b); string(data) != "new b content" {
		t.Errorf("b content = %q", data)
	}
	// Single-linked file: in-place write preserves the inode.
	ino1 := inodeOf(t, b)
	must(t, WriteBriefing(b, "again"))
	if inodeOf(t, b) != ino1 {
		t.Error("single-linked write should preserve inode")
	}
}

func TestWorkspaceIsYoloSourceTree(t *testing.T) {
	// A non-yolo dir.
	dir := t.TempDir()
	if WorkspaceIsYoloSourceTree(dir) {
		t.Error("empty dir is not a yolo source tree")
	}
	// The real repo root IS one.
	root := repoRoot(t)
	if !WorkspaceIsYoloSourceTree(root) {
		t.Error("repo root should be recognized as a yolo source tree")
	}
	// go.mod present but foreign module path -> false.
	must(t, os.MkdirAll(filepath.Join(dir, "cmd", "yolo"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "cmd", "yolo", "main.go"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n"), 0o644))
	if WorkspaceIsYoloSourceTree(dir) {
		t.Error("foreign module path should not match")
	}
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	must(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no syscall.Stat_t")
	}
	return st.Ino
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
