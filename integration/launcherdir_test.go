package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launcherdir_test.go is the container-level proof of the blocker/installer PATH split.
// Three of its four assertions cannot be made anywhere else: the mount wiring for the
// second anchor, the writability of both anchors under a :ro /home/agent, and the actual
// resolution order a shell performs.
//
// TestPackProgramDoesNotShadowABakedBinary is the payoff — the defect the split removed.

// TestGeneratedDirsAreSplitWritableAndCorrectlyOrdered covers, in one launch:
//
//  1. PATH order — blockers first, launchers last (after /bin).
//  2. Blockers still block (`grep -r` → 127, `find` → 127).
//  3. YOLO_BYPASS_SHIMS=1 still lets the real tool through.
//  4. BOTH generated dirs are writable, which is the mount question: each is a bind-mount
//     anchor under a read-only /home/agent, so a missing `-v` for the new one makes the
//     boot fail EROFS. A unit test cannot see that at all.
func TestGeneratedDirsAreSplitWritableAndCorrectlyOrdered(t *testing.T) {
	requireJail(t)
	// Its own blocked_tools rather than tempProjectConfig: this test asserts on `find`
	// specifically, because `find` and `grep` are the only two blocks that get an
	// exec-fallthrough (realBin), which is what makes YOLO_BYPASS_SHIMS observable at all
	// — a block without a real binary behind it exits 0 doing nothing under the bypass.
	const cfg = `{
  "security": {
    "blocked_tools": [
      {"name": "grep", "message": "NO GREP -r", "suggestion": "use rg",
       "block_flags": ["--recursive", "-r", "-R", "-*[rR]*"]},
      {"name": "find", "message": "find is blocked", "suggestion": "use fd"}
    ]
  },
  "network": {"mode": "bridge"}
}`
	dir := writeProjectWithPacks(t, cfg, "claude")

	script := strings.Join([]string{
		`echo "=== PATH ==="`,
		`echo "$PATH"`,
		`echo "=== BLOCKED ==="`,
		// grep -r and find are blocked; both must exit 127 with a message.
		`grep -r foo . >/dev/null 2>&1; echo "grep_r_rc=$?"`,
		`find . -name x >/dev/null 2>&1; echo "find_rc=$?"`,
		// A non-recursive grep must still work (the argv filter, not a blanket block).
		`echo hay | grep hay >/dev/null 2>&1; echo "grep_plain_rc=$?"`,
		`echo "=== BYPASS ==="`,
		`YOLO_BYPASS_SHIMS=1 find . -maxdepth 0 >/dev/null 2>&1; echo "bypass_find_rc=$?"`,
		`echo "=== WRITABLE ==="`,
		`touch "$HOME/.yolo/bin/block/.probe" && echo "shims_writable=yes"`,
		`touch "$HOME/.yolo/bin/launch/.probe" && echo "launchers_writable=yes"`,
		`rm -f "$HOME/.yolo/bin/block/.probe" "$HOME/.yolo/bin/launch/.probe"`,
	}, "; ")

	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("probe script failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}

	// (1) PATH order.
	pathLine := strings.TrimSpace(section(r.stdout, "=== PATH ===", "=== BLOCKED ==="))
	dirs := strings.Split(pathLine, ":")
	if len(dirs) == 0 {
		t.Fatalf("no PATH in output:\n%s", r.stdout)
	}
	idx := func(want string) int {
		for i, d := range dirs {
			if d == want {
				return i
			}
		}
		return -1
	}
	shimIdx := idx("/home/agent/.yolo/bin/block")
	launcherIdx := idx("/home/agent/.yolo/bin/launch")
	binIdx := idx("/bin")
	if shimIdx != 0 {
		t.Errorf("the blocker dir must be first on PATH, got index %d in %v", shimIdx, dirs)
	}
	if launcherIdx < 0 {
		t.Fatalf("the launcher dir is missing from PATH: %v", dirs)
	}
	if binIdx < 0 || launcherIdx < binIdx {
		t.Errorf("the launcher dir (%d) must come AFTER /bin (%d): %v", launcherIdx, binIdx, dirs)
	}
	if launcherIdx != len(dirs)-1 {
		t.Errorf("the launcher dir must be last on PATH: %v", dirs)
	}

	// (2) Blockers still block, and the argv filter still lets plain usage through.
	blocked := section(r.stdout, "=== BLOCKED ===", "=== BYPASS ===")
	for _, want := range []string{"grep_r_rc=127", "find_rc=127", "grep_plain_rc=0"} {
		if !strings.Contains(blocked, want) {
			t.Errorf("missing %q in:\n%s", want, blocked)
		}
	}

	// (3) The documented escape hatch.
	bypass := section(r.stdout, "=== BYPASS ===", "=== WRITABLE ===")
	if !strings.Contains(bypass, "bypass_find_rc=0") {
		t.Errorf("YOLO_BYPASS_SHIMS=1 must reach the real find:\n%s", bypass)
	}

	// (4) Both anchors writable — the mount half of the change.
	writable := section(r.stdout, "=== WRITABLE ===", "")
	for _, want := range []string{"shims_writable=yes", "launchers_writable=yes"} {
		if !strings.Contains(writable, want) {
			t.Errorf("missing %q — a generated-script dir without its own rw bind fails "+
				"the boot EROFS under the :ro /home/agent:\n%s", want, writable)
		}
	}
}

// TestPackProgramDoesNotShadowABakedBinary is defect 11.1, from the outside.
//
// A pack declaring `program fzf` used to write ~/.yolo/bin/block/fzf, which PRECEDED the
// image's working /bin/fzf. The launcher execs $NPM_CONFIG_PREFIX/bin/fzf and never
// consults PATH, so with no npm install it printed "⚠ fzf not available" and exited 1 —
// declaring an honest dependency BROKE the tool. With the launcher dir ordered after /bin,
// the launcher is unreachable while /bin/fzf exists.
//
// fzf is a real baked package (flake.nix fullPackages), which is what makes this a
// regression test rather than a hypothetical.
func TestPackProgramDoesNotShadowABakedBinary(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	manifest := `{
  "name": "fzf-dep",
  "description": "declares fzf as a program dependency",
  "contributes": [
    {"kind": "program", "bin": "fzf", "via": "npm", "package": "fzf"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["file://`+pack+`"]}`)

	script := strings.Join([]string{
		`echo "=== RESOLVE ==="`,
		`command -v fzf`,
		`echo "=== VERSION ==="`,
		`fzf --version; echo "fzf_rc=$?"`,
		`echo "=== LAUNCHER ==="`,
		// The launcher IS generated (the pack asked for it) — it is just ordered where
		// nothing reaches it while a real fzf exists. Both halves matter: an absent
		// launcher would mean the pack's declaration was silently dropped.
		`test -x "$HOME/.yolo/bin/launch/fzf" && echo "launcher_present=yes"`,
		`test -e "$HOME/.yolo/bin/block/fzf" && echo "shim_present=yes" || echo "shim_present=no"`,
	}, "; ")

	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("a pack declaring `program fzf` broke fzf: rc %d\nstdout: %s\nstderr: %s",
			r.rc, r.stdout, r.stderr)
	}

	resolved := strings.TrimSpace(section(r.stdout, "=== RESOLVE ===", "=== VERSION ==="))
	if resolved != "/bin/fzf" {
		t.Errorf("fzf should resolve to the image's /bin/fzf, got %q — a pack's lazy "+
			"installer must not shadow a baked binary", resolved)
	}
	version := section(r.stdout, "=== VERSION ===", "=== LAUNCHER ===")
	if !strings.Contains(version, "fzf_rc=0") {
		t.Errorf("fzf --version failed:\n%s", version)
	}
	if strings.Contains(version, "not available") {
		t.Errorf("the lazy launcher ran and failed — it is still ahead of /bin:\n%s", version)
	}

	launcher := section(r.stdout, "=== LAUNCHER ===", "")
	if !strings.Contains(launcher, "launcher_present=yes") {
		t.Errorf("the pack's launcher should still be generated (just unreachable while "+
			"/bin/fzf exists):\n%s", launcher)
	}
	if !strings.Contains(launcher, "shim_present=no") {
		t.Errorf("a `program` launcher must NOT be written into the blocker dir:\n%s", launcher)
	}
}
