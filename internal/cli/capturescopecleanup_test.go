package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A path that deletes the approval record deletes every one of its parts (BB-D30, WW-D11): the
// config part, the scope part and the sources record, named here by hand so that a part the
// cleanup's list forgets fails this.
func TestCaptureCleanupRemovesEveryApprovalPart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := "yolo-capture-test"
	if err := os.MkdirAll(paths.ApprovalsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	parts := []string{
		filepath.Join(paths.ApprovalsDir(), cname+".json"),
		filepath.Join(paths.ApprovalsDir(), cname+".scope.json"),
		filepath.Join(paths.ApprovalsDir(), cname+".scope-sources.json"),
	}
	for _, p := range parts {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanupCaptureWorkspace(filepath.Join(t.TempDir(), "ws"), cname)
	for _, p := range parts {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the capture cleanup", p)
		}
	}
}
