package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestCopilotFooterScriptLeavesCopilotStateAlone pins where the copilot pack's footer script
// lands (docs/design/agent-footer.md §2.1). preparePackFiles' one-time migration from the
// pre-ledger mountpoints archives every unclaimed zero-byte file in the DIRECTORY that holds a
// single-file target. With the script directly in ~/.copilot, that directory was copilot's
// own state root, and a workspace's first launch after the footer shipped moved copilot's or
// the user's empty files out of it. So the script sits in a directory only yolo uses.
func TestCopilotFooterScriptLeavesCopilotStateAlone(t *testing.T) {
	copilot := officialPack(t, "copilot")
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			wsState := filepath.Join(t.TempDir(), ".yolo", "home")
			// podman binds <wsState>/copilot over ~/.copilot; Apple Container binds wsState
			// over the whole home, so there it is <wsState>/.copilot.
			stateDir := filepath.Join(wsState, "copilot")
			if rt == "container" {
				stateDir = filepath.Join(wsState, ".copilot")
			}
			if err := os.MkdirAll(stateDir, 0o755); err != nil {
				t.Fatal(err)
			}
			empty := filepath.Join(stateDir, "some-empty-state-file")
			if err := os.WriteFile(empty, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte(`{"yolo": true}`), 0o644); err != nil {
				t.Fatal(err)
			}

			archived := preparePackFiles([]*packload.Pack{copilot}, wsState, rt)
			if len(archived) != 0 {
				t.Errorf("the first launch with the copilot pack archived %v: copilot's own state is not a pack-file mountpoint", archived)
			}
			if !fileIsEmptyRegular(empty) {
				t.Errorf("copilot's empty state file %s is gone after preparePackFiles", empty)
			}
		})
	}
}
