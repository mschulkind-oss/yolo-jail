package paths

// statefilebeneath_test.go pins the beneath-root helpers the host uses for EXISTING
// jail-writable state (OpenWorkspaceStateSubdir, OpenStateSubdirRoot, and the
// Read/Open/WriteRegularFileBeneath trio) directly: a link at `.yolo`, at the subdir, or at the
// file name is refused or replaced, never followed, whether it names an existing host file or
// directory or nothing. The call sites are pinned where they live
// (internal/cli/configcapturelinks_test.go, internal/prune/jailwritable_test.go,
// internal/oauthbroker/credslink_test.go).

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWorkspaceStateSubdirRefusesALinkAndCreatesNothing(t *testing.T) {
	for _, at := range []string{".yolo", filepath.Join(".yolo", "home")} {
		t.Run(at+"/a link to an existing host directory", func(t *testing.T) {
			ws := stateFileWorkspace(t)
			host := t.TempDir()
			if err := os.MkdirAll(filepath.Join(host, "home"), 0o755); err != nil {
				t.Fatal(err)
			}
			beneathSymlink(t, host, filepath.Join(ws, at))
			var linked *LinkedStateDirError
			if r, err := OpenWorkspaceStateSubdir(ws, "home"); !errors.As(err, &linked) ||
				linked.Path != filepath.Join(ws, at) {
				if r != nil {
					r.Close()
				}
				t.Errorf("OpenWorkspaceStateSubdir = %v, want a LinkedStateDirError naming %s", err, at)
			}
		})
		t.Run(at+"/a dangling link", func(t *testing.T) {
			ws := stateFileWorkspace(t)
			dir := t.TempDir()
			beneathSymlink(t, filepath.Join(dir, "created"), filepath.Join(ws, at))
			if r, err := OpenWorkspaceStateSubdir(ws, "home"); err == nil {
				r.Close()
				t.Error("opened a root through a dangling link")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("the open created %v through the dangling link", entries)
			}
		})
	}
	t.Run("a missing subdir is not created", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		if _, err := OpenWorkspaceStateSubdir(ws, "home"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("OpenWorkspaceStateSubdir(missing) = %v, want fs.ErrNotExist", err)
		}
		if _, err := os.Lstat(filepath.Join(WorkspaceStateDir(ws), "home")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the open created the subdir: %v", err)
		}
	})
	t.Run("one component only", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		for _, name := range []string{"", ".", "..", "home/x", "../x"} {
			if r, err := OpenWorkspaceStateSubdir(ws, name); err == nil {
				r.Close()
				t.Errorf("OpenWorkspaceStateSubdir accepted %q", name)
			}
		}
	})
	t.Run("an ordinary subdir opens", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		if err := os.MkdirAll(filepath.Join(WorkspaceStateDir(ws), "home"), 0o755); err != nil {
			t.Fatal(err)
		}
		r, err := OpenWorkspaceStateSubdir(ws, "home")
		if err != nil {
			t.Fatalf("OpenWorkspaceStateSubdir(ordinary): %v", err)
		}
		r.Close()
	})
}

// A link at the file name is never read, including a RELATIVE one that stays inside the root,
// which os.Root itself would follow.
func TestReadRegularFileBeneathNeverReadsALink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target func(t *testing.T, dir string) string
	}{
		{"a link to an existing host file", func(t *testing.T, _ string) string {
			p := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(p, []byte("host secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"a relative link inside the root", func(t *testing.T, dir string) string {
			if err := os.WriteFile(filepath.Join(dir, "other"), []byte("host secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			return "other"
		}},
		{"a dangling link", func(t *testing.T, _ string) string {
			return filepath.Join(t.TempDir(), "gone")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			beneathSymlink(t, tc.target(t, dir), filepath.Join(dir, "name"))
			r, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if data, err := ReadRegularFileBeneath(r, "name"); err == nil || strings.Contains(string(data), "host secret") {
				t.Errorf("ReadRegularFileBeneath read through the link: %q, %v", data, err)
			}
			if f, err := OpenRegularFileBeneath(r, "name"); err == nil {
				f.Close()
				t.Error("OpenRegularFileBeneath opened through the link")
			}
			if fi, err := os.Lstat(filepath.Join(dir, "name")); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
				t.Errorf("a read removed the link (%v)", err)
			}
		})
	}
}

func TestWriteRegularFileBeneathReplacesALink(t *testing.T) {
	t.Run("a link to an existing host file", func(t *testing.T) {
		dir := t.TempDir()
		host := filepath.Join(t.TempDir(), "bashrc")
		if err := os.WriteFile(host, []byte("# the host's own\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		beneathSymlink(t, host, filepath.Join(dir, "name"))
		writeBeneathRoot(t, dir, "name", "{}\n")
		if got, _ := os.ReadFile(host); string(got) != "# the host's own\n" {
			t.Errorf("the write followed the link into the host file: %q", got)
		}
		assertRegularHolding(t, filepath.Join(dir, "name"), "{}\n")
	})
	t.Run("a dangling link", func(t *testing.T) {
		dir, hostDir := t.TempDir(), t.TempDir()
		beneathSymlink(t, filepath.Join(hostDir, "created"), filepath.Join(dir, "name"))
		writeBeneathRoot(t, dir, "name", "{}\n")
		if entries, _ := os.ReadDir(hostDir); len(entries) != 0 {
			t.Errorf("the write created %v through the dangling link", entries)
		}
		assertRegularHolding(t, filepath.Join(dir, "name"), "{}\n")
	})
	t.Run("a regular file is rewritten in place", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "name")
		if err := os.WriteFile(p, []byte("a much longer previous body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, _ := os.Lstat(p)
		writeBeneathRoot(t, dir, "name", "{}\n")
		assertRegularHolding(t, p, "{}\n")
		if after, _ := os.Lstat(p); !os.SameFile(before, after) {
			t.Error("the rewrite replaced the file rather than rewriting it in place")
		}
	})
}

func beneathSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func writeBeneathRoot(t *testing.T, dir, name, body string) {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := WriteRegularFileBeneath(r, name, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteRegularFileBeneath: %v", err)
	}
}

func assertRegularHolding(t *testing.T, p, body string) {
	t.Helper()
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file (%v)", p, err)
	}
	if got, _ := os.ReadFile(p); string(got) != body {
		t.Errorf("%s holds %q, want %q", p, got, body)
	}
}
