package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
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

// TestGenerateShimsSkipsPathyToolNames: the shim path is filepath.Join(BlockDir, name),
// and blocked_tools arrives from the assembled config — whose workspace half is
// agent-editable (/workspace is bind-mounted rw). A name carrying ".." would write an
// executable outside the block anchor into the jail's PERSISTENT home (~/.bashrc is the
// canonical target). ValidateConfig refuses such a name upstream; this is the
// writer-side half.
func TestGenerateShimsSkipsPathyToolNames(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"YOLO_BLOCK_CONFIG": `[{"name":"sub/../../pwn"},{"name":"curl"}]`,
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	var escaped []string
	filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "pwn" {
			escaped = append(escaped, p)
		}
		return nil
	})
	if len(escaped) > 0 {
		t.Errorf("a pathy blocked-tool name escaped the block dir: %v", escaped)
	}
	if _, err := os.Stat(filepath.Join(e.BlockDir(), "curl")); err != nil {
		t.Error("the well-formed shim should still be written")
	}
}

// A BLOCKER WHOSE REPLACEMENT IS ABSENT MUST NOT BE GENERATED. The defaults block
// `grep -r` and `find` and point at `rg` and `fd` — sound on the container
// backends, which BAKE both, and false on macos-user, which bakes nothing.
// Measured 2026-09-04 on a real Mac launch whose `packages:` held only `just` and
// `fzf`: the shims were generated, `grep -r` exited 127, and the suggestion named a
// binary that did not exist. The agent lost the capability AND was sent nowhere.
func TestShimsSkipABlockerWhoseReplacementIsMissing(t *testing.T) {
	home := t.TempDir()
	var warnings strings.Builder
	// An empty PATH for the agent: nothing is present, so nothing may be blocked.
	e := NewEnv(map[string]string{
		"JAIL_HOME":              home,
		"YOLO_DARWIN_LOGIN_PATH": t.TempDir(),
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"m","suggestion":"s",` +
			`"replacement":"rg-not-installed","block_flags":["-r"]}]`,
	})
	e.Stderr = &warnings

	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.BlockDir(), "grep")); err == nil {
		t.Error("generated a grep blocker whose replacement is not on PATH — the agent " +
			"loses grep -r and is pointed at a binary that does not exist")
	}
	if !strings.Contains(warnings.String(), "rg-not-installed") {
		t.Errorf("skipped the blocker without naming the missing replacement:\n%s", warnings.String())
	}
}

// The converse, or the gate would disable blocking everywhere: with the replacement
// PRESENT, the blocker is generated as before.
func TestShimsBlockWhenTheReplacementIsPresent(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "rg-installed"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{
		"JAIL_HOME":              home,
		"YOLO_DARWIN_LOGIN_PATH": binDir,
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"m","suggestion":"s",` +
			`"replacement":"rg-installed","block_flags":["-r"]}]`,
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.BlockDir(), "grep")); err != nil {
		t.Errorf("did not block grep even though its replacement is on PATH: %v", err)
	}
}

// An entry with NO `replacement` always generates — which is every custom entry any
// user has ever written. The gate is opt-in by declaration, so no existing config
// changes behaviour.
func TestShimsAlwaysBlockAnEntryDeclaringNoReplacement(t *testing.T) {
	e := NewEnv(map[string]string{
		"JAIL_HOME":              t.TempDir(),
		"YOLO_DARWIN_LOGIN_PATH": t.TempDir(), // nothing on PATH at all
		"YOLO_BLOCK_CONFIG":      `[{"name":"curl","message":"no network archaeology"}]`,
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.BlockDir(), "curl")); err != nil {
		t.Errorf("an entry with no declared replacement was not blocked: %v", err)
	}
}
