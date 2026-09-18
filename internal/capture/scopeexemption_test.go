package capture

// scopeexemption_test.go pins the one workspace yolo itself puts inside its own state dir.
//
// THE REGRESSION IT CAUGHT. The launch-time workspace-scope guard refuses a workspace
// INSIDE ~/.local/share/yolo-jail, and `yolo capture`'s scratch workspace is exactly that:
// Store.Stage mints <CapturesDir>/staging/<id> and capturehost.go launches the ordinary run
// pipeline against it. That is deliberate and load-bearing — paths.CapturesDir's docstring
// states why it is not /tmp (admission into the store is an os.Rename, and a scratch tree on
// another filesystem turns it into a full copy of the gigabytes the subsystem exists to stop
// copying) — so the guard, shipped without this exemption, refused every `yolo capture`.
//
// It lives HERE rather than in internal/paths because this package owns the layout: the test
// asks the real Store for the real path, so a change to either the staging layout or the
// exemption fails it. A hand-joined "staging" literal in the predicate's own test would
// agree with itself forever.

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestTheCaptureScratchWorkspaceIsNotAScopeBreach(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)

	store := &Store{Dir: paths.CapturesDir()}
	staging := store.StagingDir("claude")

	if breach := paths.WorkspaceScopeBreach(staging); breach != nil {
		t.Fatalf("the capture scratch workspace %q must be launchable, got: %s",
			staging, breach.What())
	}
}

// TestTheCaptureStoreItselfIsStillProtected is the other side of the exemption: it covers
// yolo's own scratch trees, not the state dir at large. A workspace at the state dir, or one
// in a sibling of captures/, is still the breach it was.
func TestTheCaptureStoreItselfIsStillProtected(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)

	for _, ws := range []string{
		paths.GlobalStorage(),
		filepath.Join(paths.GlobalStorage(), "packs"),
		filepath.Join(paths.GlobalStorage(), "home"),
	} {
		if paths.WorkspaceScopeBreach(ws) == nil {
			t.Errorf("%q is inside yolo's state dir and is not a capture scratch tree; it "+
				"must still breach", ws)
		}
	}
}
