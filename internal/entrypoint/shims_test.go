package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateShimsPreservesAnchorAndClearsStale is the regression guard for the
// unblock-doesn't-take-effect bug (integration TestShimPersistence).
//
// The shim dir (~/.yolo-shims) is a BIND-MOUNT ANCHOR: its parent (/home/agent)
// is mounted read-only, so an os.RemoveAll of the anchor itself fails with
// EROFS top-down and leaves every stale child shim in place — a curl block from
// a previous run then survives a config that no longer blocks curl. The fix is
// to clear the dir's CONTENTS (ClearContents) rather than remove+recreate the
// anchor.
//
// This test reproduces the failure signal portably (no root/mount needed): a
// remove+recreate strategy assigns the shim dir a NEW inode across two
// GenerateShims calls, which is exactly what detaches the bind mount; a
// clear-contents strategy preserves the anchor inode. It also asserts the stale
// shim is gone once its tool is unblocked.
func TestGenerateShimsPreservesAnchorAndClearsStale(t *testing.T) {
	home := t.TempDir()

	// Run 1: curl is blocked → a curl shim is written.
	e1 := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"YOLO_BLOCK_CONFIG": `[{"name":"curl"}]`,
	})
	if err := GenerateShims(e1); err != nil {
		t.Fatal(err)
	}
	shimDir := e1.ShimDir()
	curlShim := filepath.Join(shimDir, "curl")
	if _, err := os.Stat(curlShim); err != nil {
		t.Fatalf("run 1 should create the curl shim: %v", err)
	}
	anchorBefore, err := os.Stat(shimDir)
	if err != nil {
		t.Fatal(err)
	}

	// Run 2: curl is unblocked (empty config) → the stale curl shim must be
	// removed and the anchor dir must be the SAME inode (not detached).
	e2 := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"YOLO_BLOCK_CONFIG": `[]`,
	})
	if err := GenerateShims(e2); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(curlShim); !os.IsNotExist(err) {
		t.Errorf("run 2 unblocked curl but the stale curl shim survived (err=%v) — "+
			"unblocking a tool has no effect", err)
	}
	anchorAfter, err := os.Stat(shimDir)
	if err != nil {
		t.Fatalf("shim-dir anchor was removed: %v", err)
	}
	if !os.SameFile(anchorBefore, anchorAfter) {
		t.Error("GenerateShims replaced the shim-dir anchor (new inode) — a bind " +
			"mount whose read-only parent forbids unlinking the anchor would keep " +
			"showing the stale contents")
	}
}
