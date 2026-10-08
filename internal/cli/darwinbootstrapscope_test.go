package cli

// darwinbootstrapscope_test.go pins the scope check on `yolo internal darwin-bootstrap` —
// the one host-side generation that does not run inside run.Run, and therefore the one the
// launch-time workspace-scope guard cannot cover. Everything it goes on to create is a bare
// mkdir under a workspace it is TOLD about: the home overlay under <ws>/.yolo/home, the
// prism sidecars, the adoption archive and the staged bootstrap script.
//
// What it can promise is narrower than the launch guard's, and the test says so: the roots it
// compares against are the SANDBOX ACCOUNT's (darwinBootstrapScopeHome), whatever HOME the run
// was handed. That is the promise being pinned here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sandboxAccountHome stands a resolved temporary directory in for /Users/_yolojail.
func sandboxAccountHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	saved := darwinBootstrapScopeHome
	darwinBootstrapScopeHome = func() string { return home }
	t.Cleanup(func() { darwinBootstrapScopeHome = saved })
	return home
}

func TestDarwinBootstrapRefusesABoundaryWorkspace(t *testing.T) {
	home := sandboxAccountHome(t)
	// The sandbox home this invocation runs as, and the workspace it was handed.
	t.Setenv("HOME", home)
	t.Setenv("JAIL_HOME", home)

	for _, ws := range []string{home, filepath.Join(home, paths.GlobalStorageRel())} {
		t.Setenv("YOLO_DARWIN_WORKSPACE", ws)

		if rc := runDarwinBootstrap(nil); rc != 1 {
			t.Errorf("runDarwinBootstrap with workspace %q returned %d, want 1 (refused)", ws, rc)
		}
		if _, err := os.Stat(paths.WorkspaceStateDir(ws)); !os.IsNotExist(err) {
			t.Errorf("%s exists — the bootstrap generated into a directory that may not be a "+
				"workspace (stat err: %v)", paths.WorkspaceStateDir(ws), err)
		}
	}
}

// TestDarwinBootstrapAdmitsTheWorkspacesYoloItselfLaysOut is the scheduled macos-user job's
// refusals (runs 37522721810 through 37790269472), on Linux:
//
//   - a SECOND launch of a workspace, whose account home links ~/.config into that workspace's
//     own sidecar <ws>/.yolo/home/config, which by then holds a yolo-jail directory. Following
//     the link found "yolo's user config directory" inside the workspace;
//   - an install capture or fork build, which hands the bootstrap a staging HOME inside the
//     staging tree it names as the workspace. Checking HOME found "your home directory" there.
//
// Neither is the sandbox account's boundary, and both must be admitted. The account's own home
// and its state dir stay refused (the test above).
func TestDarwinBootstrapAdmitsTheWorkspacesYoloItselfLaysOut(t *testing.T) {
	home := sandboxAccountHome(t)

	relaunch := filepath.Join(resolvedDir(t), "ws")
	sidecarConfig := filepath.Join(paths.WorkspaceStateDir(relaunch), "home", "config")
	if err := os.MkdirAll(filepath.Join(sidecarConfig, "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sidecarConfig, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if b := darwinBootstrapScopeBreach(relaunch); b != nil {
		t.Errorf("a relaunched workspace whose sidecar the account's ~/.config links to was refused: %v", b)
	}

	capture := filepath.Join(resolvedDir(t), "yolo-captures", "somebin")
	if err := os.MkdirAll(filepath.Join(capture, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(capture, "home"))
	if b := darwinBootstrapScopeBreach(capture); b != nil {
		t.Errorf("a capture staging tree holding its own staging HOME was refused: %v", b)
	}

	for _, ws := range []string{home, filepath.Dir(home), filepath.Join(home, paths.GlobalStorageRel())} {
		if darwinBootstrapScopeBreach(ws) == nil {
			t.Errorf("workspace %q was admitted, but it is or contains the sandbox account's home, or sits in its state dir", ws)
		}
	}
}

// resolvedDir is t.TempDir with its symlinks resolved, minted resolved so no comparison
// against code that resolves them can forget (the darwin /var/folders class).
func resolvedDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
