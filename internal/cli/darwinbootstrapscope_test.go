package cli

// darwinbootstrapscope_test.go pins the scope check on `yolo internal darwin-bootstrap` —
// the one host-side generation that does not run inside run.Run, and therefore the one the
// launch-time workspace-scope guard cannot cover. Everything it goes on to create is a bare
// mkdir under a workspace it is TOLD about: the home overlay under <ws>/.yolo/home, the
// prism sidecars, the adoption archive and the staged bootstrap script.
//
// What it can promise is narrower than the launch guard's, and the test says so: the check
// runs after HOME has been rebound to the sandbox home, so the roots it compares against are
// the SANDBOX identity's. That is the promise being pinned here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestDarwinBootstrapRefusesABoundaryWorkspace(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	// The sandbox home this invocation runs as, and the workspace it was handed. HOME and
	// JAIL_HOME agree so the function's own rebind is a no-op and the roots are this home's.
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
