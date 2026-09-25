package paths

// statefile_test.go pins OpenWorkspaceStateFile's terms directly: a link at the file name is
// replaced rather than followed, whether it names an existing host file or nothing, a linked
// `.yolo` is refused with an error naming it, and a name reaching below `.yolo` is refused.
// The call sites are pinned where they live (internal/cli/run/wsstatefiles_test.go and
// internal/config/snapshotlink_test.go).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateFileWorkspace is an ordinary workspace below a temp home, so the scope refusal passes.
func stateFileWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(scopeHome(t), "code", "project")
	if err := os.MkdirAll(WorkspaceStateDir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestWriteWorkspaceStateFileReplacesALinkAtTheName(t *testing.T) {
	t.Run("a link to an existing host file", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		host := filepath.Join(t.TempDir(), "bashrc")
		if err := os.WriteFile(host, []byte("# the host's own\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		leaf := filepath.Join(WorkspaceStateDir(ws), "config-boot.json")
		if err := os.Symlink(host, leaf); err != nil {
			t.Fatal(err)
		}

		if err := WriteWorkspaceStateFile(ws, "config-boot.json", []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("WriteWorkspaceStateFile: %v", err)
		}
		if got, _ := os.ReadFile(host); string(got) != "# the host's own\n" {
			t.Errorf("the write followed the link into the host file: %q", got)
		}
		if fi, err := os.Lstat(leaf); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("the link was not replaced by a regular file (%v)", err)
		}
		if got, _ := os.ReadFile(leaf); string(got) != "{}\n" {
			t.Errorf("the state file holds %q", got)
		}
	})
	t.Run("a dangling link", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		hostDir := t.TempDir()
		leaf := filepath.Join(WorkspaceStateDir(ws), "launch.log")
		if err := os.Symlink(filepath.Join(hostDir, "created"), leaf); err != nil {
			t.Fatal(err)
		}

		f, err := OpenWorkspaceStateFile(ws, "launch.log", os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatalf("OpenWorkspaceStateFile: %v", err)
		}
		f.Close()
		if entries, _ := os.ReadDir(hostDir); len(entries) != 0 {
			t.Errorf("the open created %v through the dangling link", entries)
		}
	})
	t.Run("an existing regular file is rewritten in place", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		leaf := filepath.Join(WorkspaceStateDir(ws), "config-boot.json")
		if err := os.WriteFile(leaf, []byte("a much longer previous body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := WriteWorkspaceStateFile(ws, "config-boot.json", []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(leaf); string(got) != "{}\n" {
			t.Errorf("the rewrite left %q", got)
		}
	})
	t.Run("without O_CREATE a link is refused, not removed", func(t *testing.T) {
		ws := stateFileWorkspace(t)
		leaf := filepath.Join(WorkspaceStateDir(ws), "launch.log")
		if err := os.Symlink("/etc/hostname", leaf); err != nil {
			t.Fatal(err)
		}
		if f, err := OpenWorkspaceStateFile(ws, "launch.log", os.O_RDONLY, 0); err == nil {
			f.Close()
			t.Fatal("a read-only open went through the link")
		}
		if fi, err := os.Lstat(leaf); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("a read-only open removed the link (%v)", err)
		}
	})
}

func TestOpenWorkspaceStateFileRefusesALinkedStateDir(t *testing.T) {
	ws := filepath.Join(scopeHome(t), "code", "project")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	hostDir := t.TempDir()
	if err := os.Symlink(hostDir, WorkspaceStateDir(ws)); err != nil {
		t.Fatal(err)
	}

	f, err := OpenWorkspaceStateFile(ws, "launch.log", os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err == nil {
		f.Close()
		t.Fatal("opened a state file beneath a linked .yolo")
	}
	var linked *LinkedStateDirError
	if !errors.As(err, &linked) || !strings.Contains(err.Error(), WorkspaceStateDir(ws)) {
		t.Errorf("the refusal does not name the linked directory: %v", err)
	}
	if entries, _ := os.ReadDir(hostDir); len(entries) != 0 {
		t.Errorf("the refused open still wrote %v into the link's target", entries)
	}
}

func TestOpenWorkspaceStateFileTakesOneComponent(t *testing.T) {
	ws := stateFileWorkspace(t)
	for _, name := range []string{"", ".", "..", "home/x", "../x", "/etc/passwd"} {
		if f, err := OpenWorkspaceStateFile(ws, name, os.O_RDWR|os.O_CREATE, 0o644); err == nil {
			f.Close()
			t.Errorf("OpenWorkspaceStateFile accepted %q", name)
		}
	}
}

func TestOpenStateDirRootRefusesALinkAndANonDirectory(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	var linked *LinkedStateDirError
	if _, err := OpenStateDirRoot(link); !errors.As(err, &linked) || linked.Path != link {
		t.Errorf("OpenStateDirRoot(link) = %v, want a LinkedStateDirError naming it", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStateDirRoot(file); err == nil {
		t.Error("OpenStateDirRoot opened a regular file")
	}
	r, err := OpenStateDirRoot(dir)
	if err != nil {
		t.Fatalf("OpenStateDirRoot(ordinary dir): %v", err)
	}
	r.Close()
}
