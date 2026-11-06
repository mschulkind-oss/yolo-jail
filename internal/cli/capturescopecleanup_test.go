package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// A path that deletes the approval record deletes every one of its parts (BB-D30, WW-D11): the
// config part, the scope part, the sources record and the loophole-grant part, named here by hand so that a part the
// cleanup's list forgets fails this. The record is the project's, keyed by the project key, not by
// the build jail's setup-qualified name (named-setups-plan.md SUP-D2).
func TestCaptureCleanupRemovesEveryApprovalPart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	key := runtime.FromWorkspace(ws)
	cname := runtime.JailName(ws, "podman-self")
	if err := os.MkdirAll(paths.ApprovalsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	parts := []string{
		filepath.Join(paths.ApprovalsDir(), key+".json"),
		filepath.Join(paths.ApprovalsDir(), key+".scope.json"),
		filepath.Join(paths.ApprovalsDir(), key+".scope-sources.json"),
		filepath.Join(paths.ApprovalsDir(), key+".loophole-grants.json"),
	}
	for _, p := range parts {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanupCaptureWorkspace(ws, cname)
	for _, p := range parts {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the capture cleanup", p)
		}
	}
}
