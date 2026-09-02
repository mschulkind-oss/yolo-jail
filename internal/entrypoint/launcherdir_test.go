package entrypoint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// launcherdir_test.go covers the BLOCKER/INSTALLER split: ~/.yolo/bin/block holds blockers and
// is first on PATH, ~/.yolo/bin/launch holds lazy installers and is last (after /bin).
//
// The defect that motivated the split: a pack declaring `program fzf` wrote
// ~/.yolo/bin/block/fzf, which preceded the image's working /bin/fzf, and the launcher execs
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
		"/home/agent/.yolo/bin/block",
		"/home/agent/.npm-global/bin",
		"/mise/shims",
		"/home/agent/go/bin",
		"/home/agent/.local/bin",
		"/bin",
		"/usr/bin",
		"/home/agent/.yolo/bin/launch",
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
	if idx(e.BlockDir()) != 0 {
		t.Error("the blocker dir must be FIRST: a shim that does not precede the real " +
			"binary intercepts nothing")
	}
	if idx(e.LaunchDir()) < idx("/bin") {
		t.Error("the launcher dir must come AFTER /bin: a lazy installer ordered earlier " +
			"shadows the image's own binary and then fails (defect 11.1)")
	}
	if idx(e.LaunchDir()) != len(got)-1 {
		t.Error("the launcher dir must be LAST — it is the fallback of last resort")
	}
}

// TestBootPathIsTheOnlyPathAuthority is the CALL SITE test for BootPath, and it is the
// thing every other BootPath test in this file is missing.
//
// The tests above exercise BootPath directly, so all of them stay green against a boot.go
// that never applies it — the callee-pinned/call-site-unpinned shape AGENTS.md says this
// repo has shipped five times. Main cannot be called from a test (it ends in execBash,
// which replaces the process), so the call site is pinned by reading the source, the way
// catalog_test.go pins the orphan catalog and bootlog_test.go pins the log wiring.
//
// It asserts a COUNT, not just a presence, because the defect it closes had both halves:
//
//   - delete the one `os.Setenv("PATH", BootPath(e))` in execBash and the agent inherits
//     the container's default PATH — no blockers, no launchers, no mise shims — while
//     every ordering assertion above still passes;
//   - add a SECOND, hand-spelled PATH write and the authority silently forks. That is not
//     hypothetical: boot.go carried exactly such a write for the `mise trust` subprocess,
//     it omitted e.LocalBin() from its first commit while claiming to "match", and it
//     outlived its subprocess by a month after 3a309da4 deleted trustWorkspaceConfigs.
//
// Parsed rather than substring-matched: this package's comments name os.Setenv and PATH
// freely (including the comment left where that second write used to be), so a text search
// would be satisfied by prose. A literal "PATH" key is the whole scope — a dynamic key
// (setEnvBoth's, or hydrateEnvFromUserEnvFile's loop over the user env file) is a different
// mechanism and lands BEFORE execBash either way, so BootPath still wins the last word.
func TestBootPathIsTheOnlyPathAuthority(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "entrypoint")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var viaBootPath, handSpelled []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 || !isSelector(call.Fun, "os", "Setenv") {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); !ok || lit.Kind != token.STRING ||
				lit.Value != `"PATH"` {
				return true
			}
			where := fset.Position(call.Pos()).String()
			if inner, ok := call.Args[1].(*ast.CallExpr); ok {
				if id, ok := inner.Fun.(*ast.Ident); ok && id.Name == "BootPath" {
					viaBootPath = append(viaBootPath, where)
					return true
				}
			}
			handSpelled = append(handSpelled, where)
			return true
		})
	}

	if len(handSpelled) > 0 {
		t.Errorf("PATH is set from something other than BootPath at %v — BootPath is the "+
			"single authority (its doc comment, AGENTS.md's \"PATH order (exact)\" line and "+
			"the .bashrc export all claim to mirror it). A second spelling drifts: the one "+
			"deleted in favor of this test omitted $HOME/.local/bin and said it matched.",
			handSpelled)
	}
	if len(viaBootPath) != 1 {
		t.Fatalf("found %d os.Setenv(\"PATH\", BootPath(...)) call(s) %v, want exactly 1 "+
			"(execBash's). Zero means the entrypoint computes the agent's PATH and never "+
			"applies it — the agent gets the container default, with no blocker dir, no "+
			"launcher dir and no mise shims, and every other BootPath test here still passes.",
			len(viaBootPath), viaBootPath)
	}
}

// isSelector reports whether expr is the selector pkg.sel (e.g. os.Setenv), written against
// the plain identifier: internal/entrypoint imports os unaliased everywhere, and an alias
// would be a change worth failing on.
func isSelector(expr ast.Expr, pkg, sel string) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != sel {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == pkg
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
	if !strings.HasPrefix(pathLine, `export PATH="$BLOCK_DIR:`) {
		t.Errorf("the blocker dir must be first in the .bashrc PATH: %q", pathLine)
	}
	if !strings.HasSuffix(pathLine, `:/bin:/usr/bin:$LAUNCH_DIR"`) {
		t.Errorf("the launcher dir must come last, after /bin:/usr/bin: %q", pathLine)
	}
	// Both vars must actually be defined, or the export silently expands to empty
	// components and the whole split is inert.
	if !strings.Contains(rc, `BLOCK_DIR="${HOME}/.yolo/bin/block"`) {
		t.Error(".bashrc must define BLOCK_DIR")
	}
	if !strings.Contains(rc, `LAUNCH_DIR="${HOME}/.yolo/bin/launch"`) {
		t.Error(".bashrc must define LAUNCH_DIR")
	}
}

// TestLaunchDirIsSeparateFromBlockDir: the generators must write to different dirs. This
// is the structural half of the fix — with one dir, ordering cannot express "blockers
// early, installers late" at all.
func TestLaunchDirIsSeparateFromBlockDir(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home})
	if e.BlockDir() == e.LaunchDir() {
		t.Fatal("BlockDir and LaunchDir must be different directories")
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
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "pnpm")); err != nil {
		t.Errorf("pnpm launcher should be in the launcher dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.BlockDir(), "pnpm")); !os.IsNotExist(err) {
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

	blocker := filepath.Join(e.BlockDir(), "pnpm")
	launcher := filepath.Join(e.LaunchDir(), "pnpm")

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
	if !strings.Contains(string(launcherBody), "Lazy-install launcher") {
		t.Errorf("launcher body looks wrong:\n%s", launcherBody)
	}

	// And the blocker wins because of PATH position, not because the launcher is missing.
	path := strings.Split(BootPath(e), ":")
	shimIdx, launcherIdx := -1, -1
	for i, d := range path {
		switch d {
		case e.BlockDir():
			shimIdx = i
		case e.LaunchDir():
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

	first, err := os.ReadFile(filepath.Join(e.LaunchDir(), "shellcheck"))
	if err != nil {
		t.Fatalf("the first program's launcher is missing: %v", err)
	}
	if !strings.Contains(string(first), "shellcheck-bin") {
		t.Errorf("shellcheck launcher should install its npm package:\n%s", first)
	}
	second, err := os.ReadFile(filepath.Join(e.LaunchDir(), "shfmt"))
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
// ~/.yolo/bin/launch is a bind-mount ANCHOR just like ~/.yolo/bin/block (mounted from
// <ws>/.yolo/home/yolo-bin under a read-only /home/agent), so the same two properties
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
	stale := filepath.Join(e1.LaunchDir(), "tmptool")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("run 1 should write the pack's launcher: %v", err)
	}
	anchorBefore, err := os.Stat(e1.LaunchDir())
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
	anchorAfter, err := os.Stat(e2.LaunchDir())
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
		if _, err := os.Stat(filepath.Join(e.LaunchDir(), name)); err != nil {
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
		body, err := os.ReadFile(filepath.Join(e.BlockDir(), name))
		if err != nil {
			t.Fatalf("%s blocker: %v", name, err)
		}
		if !strings.Contains(string(body), `if [ -z "$YOLO_BYPASS_SHIMS" ]; then`) {
			t.Errorf("%s blocker lost the YOLO_BYPASS_SHIMS escape hatch:\n%s", name, body)
		}
	}
}

// TestPackWithTraversalBinRefusesToBoot: a pack bin carrying ".." names a launcher path
// outside the launch anchor (filepath.Join(LaunchDir, bin) — ~/.bashrc in the jail's
// persistent home is the canonical target). LoadJailPacks makes manifest problems fatal,
// so such a pack never generates anything; the test pins both halves — the refusal names
// the bin, and no file appears anywhere under the home.
func TestPackWithTraversalBinRefusesToBoot(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "evil")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"evil","contributes":[` +
		`{"kind":"program","bin":"sub/../../pwn","via":"npm","package":"pwn"}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	err := GenerateAgentLaunchers(e)
	if err == nil || !strings.Contains(err.Error(), "bare program name") {
		t.Fatalf("err = %v, want the manifest refusal to reach the caller", err)
	}
	var escaped []string
	filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "pwn" {
			escaped = append(escaped, p)
		}
		return nil
	})
	if len(escaped) > 0 {
		t.Errorf("a traversal pack bin escaped the launch dir: %v", escaped)
	}
}
