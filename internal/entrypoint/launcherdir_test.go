package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// launcherdir_test.go covers the BLOCKER/INSTALLER split: ~/.yolo-shims holds blockers and
// is first on PATH, ~/.yolo-launchers holds lazy installers and is last (after /bin).
//
// The defect that motivated the split: a pack declaring `program fzf` wrote
// ~/.yolo-shims/fzf, which preceded the image's working /bin/fzf, and the launcher execs
// only $NPM_CONFIG_PREFIX/bin/fzf — it never consults PATH — so declaring the dependency
// honestly BROKE the tool. With the installer dir after /bin, that is unrepresentable.

// TestBootPathOrdersBlockersFirstAndInstallersLast pins the PATH invariant AGENTS.md
// documents ("PATH order (exact)"). It is the whole mechanism: if the launcher dir ever
// moves before /bin, 11.1 comes back and nothing else in this file would notice.
func TestBootPathOrdersBlockersFirstAndInstallersLast(t *testing.T) {
	e := NewEnv(map[string]string{
		"JAIL_HOME":         "/home/agent",
		"NPM_CONFIG_PREFIX": "/home/agent/.npm-global",
		"GOPATH":            "/home/agent/go",
		"MISE_DATA_DIR":     "/mise",
	})
	got := strings.Split(BootPath(e), ":")
	want := []string{
		"/home/agent/.yolo-shims",
		"/home/agent/.npm-global/bin",
		"/mise/shims",
		"/home/agent/go/bin",
		"/home/agent/.local/bin",
		"/bin",
		"/usr/bin",
		"/home/agent/.yolo-launchers",
	}
	if len(got) != len(want) {
		t.Fatalf("BootPath has %d entries, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BootPath[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	idx := func(dir string) int {
		for i, d := range got {
			if d == dir {
				return i
			}
		}
		t.Fatalf("%q missing from PATH %v", dir, got)
		return -1
	}
	// The two load-bearing relations, asserted by name so a failure says WHICH rule broke.
	if idx(e.ShimDir()) != 0 {
		t.Error("the blocker dir must be FIRST: a shim that does not precede the real " +
			"binary intercepts nothing")
	}
	if idx(e.LauncherDir()) < idx("/bin") {
		t.Error("the launcher dir must come AFTER /bin: a lazy installer ordered earlier " +
			"shadows the image's own binary and then fails (defect 11.1)")
	}
	if idx(e.LauncherDir()) != len(got)-1 {
		t.Error("the launcher dir must be LAST — it is the fallback of last resort")
	}
}

// TestBashrcPathMatchesBootPathOrder: the .bashrc export is the PATH an agent's interactive
// (and `bash -lc`) shell actually gets, so it has to carry the same split. It is a separate
// string from BootPath, which is exactly why it needs pinning — the two drifted apart
// silently before (execBash and .bashrc order $HOME/.local/bin differently to this day).
func TestBashrcPathMatchesBootPathOrder(t *testing.T) {
	e := NewEnv(map[string]string{"JAIL_HOME": "/home/agent", "MISE_DATA_DIR": "/mise"})
	rc := Bashrc(e)

	var pathLine string
	for _, line := range strings.Split(rc, "\n") {
		if strings.HasPrefix(line, "export PATH=") {
			pathLine = line
			break
		}
	}
	if pathLine == "" {
		t.Fatalf("no PATH export in the generated .bashrc:\n%s", rc)
	}
	if !strings.HasPrefix(pathLine, `export PATH="$SHIM_DIR:`) {
		t.Errorf("the blocker dir must be first in the .bashrc PATH: %q", pathLine)
	}
	if !strings.HasSuffix(pathLine, `:/bin:/usr/bin:$LAUNCHER_DIR"`) {
		t.Errorf("the launcher dir must come last, after /bin:/usr/bin: %q", pathLine)
	}
	// Both vars must actually be defined, or the export silently expands to empty
	// components and the whole split is inert.
	if !strings.Contains(rc, `SHIM_DIR="${HOME}/.yolo-shims"`) {
		t.Error(".bashrc must define SHIM_DIR")
	}
	if !strings.Contains(rc, `LAUNCHER_DIR="${HOME}/.yolo-launchers"`) {
		t.Error(".bashrc must define LAUNCHER_DIR")
	}
}

// TestLauncherDirIsSeparateFromShimDir: the generators must write to different dirs. This
// is the structural half of the fix — with one dir, ordering cannot express "blockers
// early, installers late" at all.
func TestLauncherDirIsSeparateFromShimDir(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home})
	if e.ShimDir() == e.LauncherDir() {
		t.Fatal("ShimDir and LauncherDir must be different directories")
	}

	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}

	// pnpm is the always-generated lazy installer: it must land in the LAUNCHER dir and
	// must NOT appear in the blocker dir.
	if _, err := os.Stat(filepath.Join(e.LauncherDir(), "pnpm")); err != nil {
		t.Errorf("pnpm launcher should be in the launcher dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.ShimDir(), "pnpm")); !os.IsNotExist(err) {
		t.Errorf("pnpm launcher must NOT be written into the blocker dir (err=%v)", err)
	}
}

// TestBlockedAndDeclaredToolGetsBothAndBlockerWins is the trap-2 test.
//
// The generators used to coordinate through the shared directory: GenerateAgentLaunchers
// skipped a name a blocked-tool shim already owned, so a tool that was BOTH blocked and
// declared as a pack `program` simply never got a launcher. With two dirs that collision
// cannot happen, and the semantics change: it now gets a blocker (early) AND a launcher
// (late), and the blocker wins by POSITION rather than by the launcher being absent.
//
// Pinning it matters because the two outcomes are indistinguishable from the user's seat
// (the tool is blocked either way) but not from the code's: a future refactor that
// "restored" the skip would silently lose the installer for a tool that gets unblocked
// later in the same jail's life.
func TestBlockedAndDeclaredToolGetsBothAndBlockerWins(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()

	// A pack declaring `program pnpm` — the same name the pkg-manager launcher uses, and
	// a name we simultaneously BLOCK below.
	packDir := filepath.Join(packRoot, "blocky")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"blocky","contributes":[` +
		`{"kind":"program","bin":"pnpm","via":"npm","package":"pnpm"}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": packRoot,
		"YOLO_BLOCK_CONFIG": `[{"name":"pnpm","message":"pnpm is blocked in this project",` +
			`"suggestion":"Use npm"}]`,
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}

	blocker := filepath.Join(e.ShimDir(), "pnpm")
	launcher := filepath.Join(e.LauncherDir(), "pnpm")

	blockerBody, err := os.ReadFile(blocker)
	if err != nil {
		t.Fatalf("a blocked tool must still get its blocker: %v", err)
	}
	if !strings.Contains(string(blockerBody), "exit 127") {
		t.Errorf("blocker should exit 127:\n%s", blockerBody)
	}
	launcherBody, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatalf("a pack-declared program must get its launcher even when the tool is "+
			"ALSO blocked — the dirs cannot collide, so there is nothing to skip: %v", err)
	}
	if !strings.Contains(string(launcherBody), "Lazy-update launcher") {
		t.Errorf("launcher body looks wrong:\n%s", launcherBody)
	}

	// And the blocker wins because of PATH position, not because the launcher is missing.
	path := strings.Split(BootPath(e), ":")
	shimIdx, launcherIdx := -1, -1
	for i, d := range path {
		switch d {
		case e.ShimDir():
			shimIdx = i
		case e.LauncherDir():
			launcherIdx = i
		}
	}
	if shimIdx < 0 || launcherIdx < 0 {
		t.Fatalf("both dirs must be on PATH: %v", path)
	}
	if shimIdx > launcherIdx {
		t.Error("the blocker must precede the launcher on PATH, or a blocked tool that a " +
			"pack also declares would resolve to the installer and run unblocked")
	}
}

// TestPackWithTwoProgramsGetsTwoLaunchers is the jail half of finding 11.2.
//
// InstallContribution() used to `return` inside its loop, so a pack declaring `fd` AND
// `fzf` got a launcher for `fd` only — while DepRequirements() (the host path) returned
// both. One declaration, two answers, and the jail was the side dropping data silently.
//
// `program` is CombineExclusive by BIN NAME, not per pack, so two different bins from one
// pack were never a collision; and with the launcher dir ordered after /bin, N launchers
// carry no more shadowing risk than one. Both flavors are covered, because the nested loop
// has to pick a template per contribution rather than once per pack.
func TestPackWithTwoProgramsGetsTwoLaunchers(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "twotools")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"twotools","contributes":[` +
		`{"kind":"program","bin":"shellcheck","via":"npm","package":"shellcheck-bin"},` +
		`{"kind":"program","bin":"shfmt","via":"installer","url":"https://example/shfmt.sh"}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}

	first, err := os.ReadFile(filepath.Join(e.LauncherDir(), "shellcheck"))
	if err != nil {
		t.Fatalf("the first program's launcher is missing: %v", err)
	}
	if !strings.Contains(string(first), "shellcheck-bin") {
		t.Errorf("shellcheck launcher should install its npm package:\n%s", first)
	}
	second, err := os.ReadFile(filepath.Join(e.LauncherDir(), "shfmt"))
	if err != nil {
		t.Fatalf("the SECOND program's launcher is missing — only the first `program` per "+
			"pack installed, so a pack needing two tools silently got one: %v", err)
	}
	if !strings.Contains(string(second), "https://example/shfmt.sh") {
		t.Errorf("shfmt launcher should carry its installer URL:\n%s", second)
	}
}

// TestFetchedPackKeepsNpmInstallAndLosesOnlyTheInstaller: the origin gate is PER
// contribution, asserted at the generator rather than only at packload, because this is the
// path that actually writes an executable. A pack mixing an npm install with a
// curl-to-shell installer must not have the URL smuggled through beside the npm one.
func TestFetchedPackKeepsNpmInstallAndLosesOnlyTheInstaller(t *testing.T) {
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "mixed")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"mixed","contributes":[` +
		`{"kind":"program","bin":"safe","via":"npm","package":"safe-pkg"},` +
		`{"kind":"program","bin":"sharp","via":"installer","url":"https://evil/sh"}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// The JAIL loader trusts the staged tree (the host already applied the gate), so drive
	// the gate directly through a fetched-origin load to assert the per-contribution split.
	p, probs := packload.LoadDir(packDir, "mixed", false)
	if len(probs) != 0 {
		t.Fatalf("manifest problems: %v", probs)
	}
	granted, refused := p.HonoredInstalls()
	if len(granted) != 1 || granted[0].Bin != "safe" {
		t.Errorf("the npm install must survive: %+v", granted)
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "https://evil/sh") {
		t.Errorf("exactly the installer must be refused, naming its URL: %v", refused)
	}
}

// TestGenerateLaunchersPreserveAnchorAndClearStale is the launcher-dir twin of
// TestGenerateShimsPreservesAnchorAndClearsStale.
//
// ~/.yolo-launchers is a bind-mount ANCHOR just like ~/.yolo-shims (mounted from
// <ws>/.yolo/home/yolo-launchers under a read-only /home/agent), so the same two properties
// are required and for the same reasons: an os.RemoveAll of the anchor fails EROFS on the
// read-only parent and leaves stale children, and a remove+recreate assigns a new inode
// that detaches the mount. Reproduced portably: same-inode across two runs, plus a dropped
// pack's launcher actually disappearing.
func TestGenerateLaunchersPreserveAnchorAndClearStale(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()

	packDir := filepath.Join(packRoot, "tmptool")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tmptool","contributes":[` +
		`{"kind":"program","bin":"tmptool","via":"npm","package":"tmptool"}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 1: the pack is mounted → its launcher is written.
	e1 := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if err := GenerateAgentLaunchers(e1); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(e1.LauncherDir(), "tmptool")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("run 1 should write the pack's launcher: %v", err)
	}
	anchorBefore, err := os.Stat(e1.LauncherDir())
	if err != nil {
		t.Fatal(err)
	}

	// Run 2: the pack is dropped (nothing mounted) → the stale launcher must go, and the
	// anchor inode must survive.
	e2 := NewEnv(map[string]string{"JAIL_HOME": home})
	if err := GenerateAgentLaunchers(e2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a dropped pack's launcher survived (err=%v) — the tool keeps lazily "+
			"installing itself forever", err)
	}
	anchorAfter, err := os.Stat(e2.LauncherDir())
	if err != nil {
		t.Fatalf("launcher-dir anchor was removed: %v", err)
	}
	if !os.SameFile(anchorBefore, anchorAfter) {
		t.Error("GenerateAgentLaunchers replaced the launcher-dir anchor (new inode) — a " +
			"bind mount whose read-only parent forbids unlinking the anchor would keep " +
			"showing the stale contents")
	}
}

// TestPackageManagerLaunchersSurviveTheAgentReset: ordering constraint made explicit.
// GenerateAgentLaunchers owns the contents-only reset and runs FIRST; if the pkg-manager
// generator also cleared the dir it would delete the pack launchers just written, and if
// the two ran in the other order the pnpm launcher would be the casualty.
func TestPackageManagerLaunchersSurviveTheAgentReset(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"toolpack","via":"npm","package":"toolpack"}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})

	// Boot order (boot.go / darwin.go): shims, agent launchers, pkg-manager launchers.
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"toolpack", "pnpm"} {
		if _, err := os.Stat(filepath.Join(e.LauncherDir(), name)); err != nil {
			t.Errorf("%s launcher missing after the full boot sequence: %v", name, err)
		}
	}
}

// TestBlockerBypassEnvIsUnaffectedBySplit: YOLO_BYPASS_SHIMS=1 is the documented escape
// hatch for installers and scripts, and it is a property of the BLOCKER body — nothing
// about moving the installers to a second dir may weaken it. Pins that every generated
// blocker still guards its refusal on the var (both flavors: argv-filter and
// unconditional).
func TestBlockerBypassEnvIsUnaffectedBySplit(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"grep -r is blocked",` +
			`"suggestion":"Use rg","block_flags":["-r"]},` +
			`{"name":"curl","message":"curl is blocked"}]`,
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"grep", "curl"} {
		body, err := os.ReadFile(filepath.Join(e.ShimDir(), name))
		if err != nil {
			t.Fatalf("%s blocker: %v", name, err)
		}
		if !strings.Contains(string(body), `if [ -z "$YOLO_BYPASS_SHIMS" ]; then`) {
			t.Errorf("%s blocker lost the YOLO_BYPASS_SHIMS escape hatch:\n%s", name, body)
		}
	}
}
