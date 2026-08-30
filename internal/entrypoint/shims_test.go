package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateShimsPreservesAnchorAndClearsStale is the regression guard for the
// unblock-doesn't-take-effect bug (integration TestShimPersistence).
//
// The shim dir (~/.yolo/bin/block) is a BIND-MOUNT ANCHOR: its parent (/home/agent)
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
	shimDir := e1.BlockDir()
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

// TestRemoveRetiredGeneratedDirsEmptiesButKeepsThem is the OQ-6 migration.
//
// After the rename, ~/.yolo-shims and ~/.yolo-launchers are on no PATH but still hold
// executables named after real tools — including a `grep` blocker that would start
// intercepting again the moment either directory got back onto a PATH. They are emptied.
//
// CONTENTS-ONLY, and that is not cosmetic here: on a host whose launcher has not been
// upgraded yet, those directories are still live bind-mount ANCHORS. Removing the
// directory itself would fail EROFS against the read-only /home/agent, or succeed and
// detach the mount.
func TestRemoveRetiredGeneratedDirsEmptiesButKeepsThem(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home})

	for _, name := range retiredGeneratedDirs {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "grep"), []byte("#!/bin/sh\nexit 127\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	before, err := os.Stat(filepath.Join(home, ".yolo-shims"))
	if err != nil {
		t.Fatal(err)
	}

	removeRetiredGeneratedDirs(e)

	for _, name := range retiredGeneratedDirs {
		dir := filepath.Join(home, name)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s was REMOVED, not emptied — on an un-upgraded host that is a live "+
				"mount anchor: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "grep")); !os.IsNotExist(err) {
			t.Errorf("%s/grep survived — it would intercept again if the dir reached a PATH", name)
		}
	}
	after, err := os.Stat(filepath.Join(home, ".yolo-shims"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the retired dir was recreated (new inode) — a live bind would detach")
	}
}

// TestRemoveRetiredGeneratedDirsOnAFreshHomeIsQuiet: absent is the normal case.
func TestRemoveRetiredGeneratedDirsOnAFreshHomeIsQuiet(t *testing.T) {
	home := t.TempDir()
	removeRetiredGeneratedDirs(NewEnv(map[string]string{"JAIL_HOME": home}))
}
