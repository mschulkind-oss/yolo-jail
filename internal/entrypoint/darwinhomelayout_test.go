package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darwinhomelayout_test.go covers the macos-user workspace tier
// (docs/design/macos-user-home-tiers.md, alternative A′).
//
// ⚠ READ §6's WARNING BEFORE ADDING TO THIS FILE. The obvious test for the SharedDirs
// mirror pins nothing: one that stubs the mirror and asserts a link resolves is satisfied
// by giving the STUB a mirroring body — zero production code, green. So the load-bearing
// test here (TestDarwinBootstrapLaysTheTierAndTheCredentialResolvesThroughIt) drives the
// REAL boot entry, RunDarwinBootstrap, against a real filesystem and reads the credential
// THROUGH the layout. Delete either call site — the layout step, or the mirror loop inside
// the deriver — and it fails.
//
// It runs on Linux, which is where it has to run: the whole backend is unrunnable in CI
// (no sandbox-exec, no _yolojail), and the `..` resolution this rests on is physical on
// both kernels — measured on Linux 2026-09-11, confirmed on macOS 26.5 the same day.

// stageClaudePack writes the REAL claude manifest into a pack root the bootstrap can load.
//
// The real one, not a fixture: the tier of a path is what the PACK declares (§6 P2), so a
// test with its own invented manifest would pass while claude's `state` scopes said
// something else. This breaks if `.claude` stops being scope:workspace, if
// `.claude-shared-credentials` stops being scope:machine, or if the shared_credentials hook
// is dropped — each of which changes what the layout has to do.
func stageClaudePack(t *testing.T) string {
	t.Helper()
	p, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(p.Root, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// darwinBootstrapHome runs the real native bootstrap against a temp home and a temp
// workspace, and returns both. `extra` overrides any env var.
func darwinBootstrapHome(t *testing.T, extra map[string]string) (home, ws string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	ws = filepath.Join(base, "workspace")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]string{
		"HOME":                     home,
		"JAIL_HOME":                home,
		"YOLO_HOST_DIR":            ws,
		"YOLO_BLOCK_CONFIG":        `[]`,
		"YOLO_MISE_TOOLS":          `{}`,
		"YOLO_PACK_ROOT":           stageClaudePack(t),
		"YOLO_DARWIN_WORKSPACE":    ws,
		DarwinHomeSidecarEnv:       filepath.Join(ws, ".yolo", "home"),
		"MISE_DATA_DIR":            filepath.Join(home, ".yolo", "mise"),
		"YOLO_DARWIN_HOME_OVERLAY": extra["YOLO_DARWIN_HOME_OVERLAY"],
	}
	for k, v := range extra {
		vars[k] = v
	}
	e := DarwinEnvFrom(vars, home)
	e.Stderr = &strings.Builder{}
	// The bootstrap's own error is deliberately NOT fatal to the test: a temp home can
	// fail an unrelated generator (no git, no node), and what these tests assert is the
	// LAYOUT. A layout failure shows up as a missing link below, named precisely.
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})
	return home, ws
}

// THE TEST §6 ASKS FOR. Two assertions, and each pins a different call site:
//
//   - ~/.claude is a symlink into the sidecar — fails if the layout step is deleted from
//     RunDarwinBootstrap (or moved below the generators that write through it);
//   - the credential file READS THROUGH that symlink to the account home's real one — fails
//     if the SharedDirs mirror is deleted, because the hook's relative `..` then resolves
//     physically into the sidecar, where nothing is.
//
// The second cannot stand alone: without the first, `..` resolves in the account home and
// the credential is reachable with no layout at all.
func TestDarwinBootstrapLaysTheTierAndTheCredentialResolvesThroughIt(t *testing.T) {
	home, ws := darwinBootstrapHome(t, nil)
	sidecar := filepath.Join(ws, ".yolo", "home")

	target, err := os.Readlink(filepath.Join(home, ".claude"))
	if err != nil {
		t.Fatalf("~/.claude is not a symlink into the workspace sidecar: %v\n"+
			"Every pack `state` dir at scope:workspace has to live under the workspace on "+
			"this backend too, or one machine's jails share one agent history.", err)
	}
	if want := filepath.Join(sidecar, "claude"); target != want {
		t.Fatalf("~/.claude -> %s, want %s", target, want)
	}

	// The machine tier's real file, written AFTER the boot so only this path holds the
	// bytes: reading them back through ~/.claude proves the whole chain resolved.
	cred := filepath.Join(home, ".claude-shared-credentials", ".credentials.json")
	if err := os.WriteFile(cred, []byte("machine-tier-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatalf("the shared credential does not resolve through the workspace tier: %v\n"+
			"The hook's symlink is relative BY DESIGN, so it resolves through whatever backs "+
			"the state dir; `..` is resolved physically, so it lands in %s and needs the "+
			"SharedDirs mirror there.", err, sidecar)
	}
	if string(got) != "machine-tier-token" {
		t.Errorf("read %q through the layout, want the machine tier's own bytes", got)
	}

	// And the mirror is a symlink OUT of the sidecar, not a copy: a copy would be a second
	// credential store, diverging on the first refresh.
	mirror, err := os.Readlink(filepath.Join(sidecar, ".claude-shared-credentials"))
	if err != nil || mirror != filepath.Join(home, ".claude-shared-credentials") {
		t.Errorf("the sidecar mirror is %q (err %v), want a symlink to the account home's dir",
			mirror, err)
	}
}

// THE ORDERING ASSERTION. Every generator has to write THROUGH the layout, which is only
// true if the layout was laid before the first of them — and ~/.yolo/bin is the case that
// forces "above genStep #1" rather than "before the pack hooks", because GenerateShims is
// genStep #1 and writes into it.
//
// So this checks the OUTPUT of four different generators, each in the sidecar rather than the
// account home: the blocker anchor (generate_shims), the mise config (generate_mise_config),
// the pack's config surfaces (ConfigurePackSurfaces), and ~/.claude.json, which reaches the
// sidecar only through the home-root file redirect the container keeps for the same reason.
func TestDarwinBootstrapGeneratorsWriteThroughTheLayout(t *testing.T) {
	home, ws := darwinBootstrapHome(t, nil)
	sidecar := filepath.Join(ws, ".yolo", "home")

	for _, rel := range []string{
		filepath.Join("yolo-bin", "block"),       // genStep #1 wrote through ~/.yolo/bin
		filepath.Join("config", "mise"),          // ~/.config/mise/config.toml
		filepath.Join("claude", "settings.json"), // a pack config surface
		filepath.Join("claude", "claude.json"),   // via the ~/.claude.json redirect
	} {
		if _, err := os.Stat(filepath.Join(sidecar, rel)); err != nil {
			t.Errorf("%s is not in the workspace sidecar: %v", rel, err)
		}
	}
	// And none of it is a real path in the shared account home — which is what it would be
	// if the layout ran after the generator that wrote it.
	for _, rel := range []string{".yolo/bin", ".config", ".claude"} {
		fi, err := os.Lstat(filepath.Join(home, rel))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("~/%s is a real path in the account home (err %v) — a generator got "+
				"there first, which is what laying the layout above genStep #1 prevents", rel, err)
		}
	}
}

// The content overlay is delivered LAST and used to RemoveAll its top-level entry, which
// for a skills destination of `.claude/skills` is the whole of ~/.claude — the credential
// symlink the hooks had just written, the transcripts, and (under this layout) the sidecar
// link itself, replaced by a real directory that the NEXT boot refuses to link over.
//
// So: the layout survives the overlay, existing state under it survives, the delivered
// content arrives, and content a pack stopped shipping still disappears.
func TestDarwinBootstrapOverlayDeliversWithoutEatingTheLayout(t *testing.T) {
	base := t.TempDir()
	overlay := filepath.Join(base, "overlay")
	writeTreeFile(t, filepath.Join(overlay, ".claude", "skills", "demo", "SKILL.md"), "demo skill")
	writeTreeFile(t, filepath.Join(overlay, ".claude", "CLAUDE.md"), "the briefing")

	home, ws := darwinBootstrapHome(t, map[string]string{"YOLO_DARWIN_HOME_OVERLAY": overlay})
	sidecar := filepath.Join(ws, ".yolo", "home")

	if _, err := os.Readlink(filepath.Join(home, ".claude")); err != nil {
		t.Fatalf("the overlay replaced the workspace-tier link with a real directory: %v\n"+
			"A bind mount lands AT its destination and leaves the parent alone; the copy that "+
			"stands in for it here has to do the same.", err)
	}
	for rel, want := range map[string]string{
		filepath.Join("claude", "skills", "demo", "SKILL.md"): "demo skill",
		filepath.Join("claude", "CLAUDE.md"):                  "the briefing",
	} {
		got, err := os.ReadFile(filepath.Join(sidecar, rel))
		if err != nil || string(got) != want {
			t.Errorf("%s in the sidecar = %q (err %v), want %q", rel, got, err, want)
		}
	}

	// Second boot: a skill the pack stopped shipping must go, and state beside it must not.
	stale := filepath.Join(sidecar, "claude", "skills", "gone", "SKILL.md")
	writeTreeFile(t, stale, "removed from the pack")
	kept := filepath.Join(sidecar, "claude", "projects", "session.jsonl")
	writeTreeFile(t, kept, "a transcript")
	e := DarwinEnvFrom(map[string]string{
		"HOME": home, "JAIL_HOME": home, "YOLO_HOST_DIR": ws,
		"YOLO_BLOCK_CONFIG": `[]`, "YOLO_MISE_TOOLS": `{}`,
		"YOLO_DARWIN_WORKSPACE": ws, DarwinHomeSidecarEnv: sidecar,
		"YOLO_DARWIN_HOME_OVERLAY": overlay,
	}, home)
	e.Stderr = &strings.Builder{}
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a skills dir the pack stopped shipping survived the overlay (err %v) — "+
			"the destination is authoritative, exactly as a bind is", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the agent's own state under the destination's PARENT was destroyed: %v", err)
	}
}

// writeTreeFile writes body at path, creating parents.
func writeTreeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The deriver's table IS the container's mount table, which is the whole point: a directory
// bound from <ws>/.yolo/home on podman and left in the shared account home here is the
// drift A′ exists to end. Checked as a set of pairs rather than a golden string so the
// failure names the missing surface.
func TestDeriveDarwinHomeLayoutMirrorsTheContainerBindTable(t *testing.T) {
	home, sidecar := "/Users/_yolojail", "/Users/Shared/yolo/proj/.yolo/home"
	l := DeriveDarwinHomeLayout(home, sidecar, []string{".claude", ".codex"},
		[]string{".claude-shared-credentials"})

	got := map[string]string{}
	for _, ln := range l.Links {
		got[ln.Path] = ln.Target
	}
	for path, want := range map[string]string{
		home + "/.npm-global": sidecar + "/npm-global",
		home + "/.local":      sidecar + "/local",
		home + "/go":          sidecar + "/go",
		home + "/.yolo/bin":   sidecar + "/yolo-bin",
		home + "/.config":     sidecar + "/config",
		home + "/.claude":     sidecar + "/claude",
		home + "/.codex":      sidecar + "/codex",
	} {
		if got[path] != want {
			t.Errorf("%s -> %q, want %q", path, got[path], want)
		}
	}
	if len(l.Links) != 7 {
		t.Errorf("the layout links %d paths, want the 5 the podman argv binds plus the 2 "+
			"pack-declared state dirs: %v", len(l.Links), l.Links)
	}
	// The machine tier never moves — it is mirrored INTO the sidecar, pointing home.
	if len(l.Mirrors) != 1 || l.Mirrors[0].Path != sidecar+"/.claude-shared-credentials" ||
		l.Mirrors[0].Target != home+"/.claude-shared-credentials" {
		t.Errorf("mirrors = %v, want the machine-scope dir mirrored into the sidecar", l.Mirrors)
	}
	// Every link target is a directory the apply creates FIRST: a link to a missing dir
	// dangles, and MkdirAll through a dangling symlink fails — which is how the first
	// generator writing through ~/.yolo/bin would fail the boot.
	dirs := map[string]bool{}
	for _, d := range l.Dirs {
		dirs[d] = true
	}
	for _, ln := range l.Links {
		if !dirs[ln.Target] {
			t.Errorf("%s is linked but never created", ln.Target)
		}
	}
	if !dirs[home+"/.claude-shared-credentials"] {
		t.Error("the machine-scope dir the mirror points at is never created")
	}
}

// The three home-root files the container keeps as symlinks into a per-workspace dir are
// laid here too, from the same list (paths.HomeFileRedirects). ~/.claude.json carries a
// projects.<workspace> map, so leaving it in the shared home is the same cross-workspace
// race in a file rather than a directory.
func TestDarwinHomeLayoutRedirectsTheHomeRootFiles(t *testing.T) {
	base := t.TempDir()
	home, sidecar := filepath.Join(base, "home"), filepath.Join(base, "ws", ".yolo", "home")
	l := DeriveDarwinHomeLayout(home, sidecar, []string{".claude"}, nil)
	if err := l.Apply(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ file, want string }{
		{".claude.json", filepath.Join(sidecar, "claude", "claude.json")},
		{".gitconfig", filepath.Join(sidecar, "config", "git", "config")},
		{".bashrc", filepath.Join(sidecar, "config", "bashrc")},
	} {
		// Written through the symlink, then read at the sidecar path it must land at.
		if err := os.WriteFile(filepath.Join(home, tc.file), []byte("x"), 0o644); err != nil {
			t.Fatalf("writing ~/%s through the redirect: %v", tc.file, err)
		}
		if _, err := os.Stat(tc.want); err != nil {
			t.Errorf("~/%s did not land at %s: %v", tc.file, tc.want, err)
		}
	}
}

// NO MIGRATION (OQ-HT2): a real directory where a link belongs is never removed, renamed or
// copied. The launch refuses and names every offender at once, because the fix is one `rm`
// of an account the user is being told to reset anyway.
func TestDarwinHomeLayoutRefusesToReplaceRealDirectories(t *testing.T) {
	base := t.TempDir()
	home, sidecar := filepath.Join(base, "home"), filepath.Join(base, "ws", ".yolo", "home")
	keep := filepath.Join(home, ".claude", "projects", "old.jsonl")
	writeTreeFile(t, keep, "a previous era's transcript")
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := DeriveDarwinHomeLayout(home, sidecar, []string{".claude"}, nil).Apply()
	if err == nil {
		t.Fatal("applying over a pre-layout home must refuse, not migrate")
	}
	for _, want := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".config")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s:\n%s", want, err)
		}
	}
	if !strings.Contains(err.Error(), "rm -rf "+home) {
		t.Errorf("the refusal does not name the reset:\n%s", err)
	}
	if _, statErr := os.Stat(keep); statErr != nil {
		t.Errorf("the refusal deleted what it refused to migrate: %v", statErr)
	}
}

// Applied on every launch, so it has to be idempotent — and a link yolo itself wrote to a
// STALE sidecar is repointed rather than refused (the workspace moved; nothing is lost).
func TestDarwinHomeLayoutIsIdempotentAndRepointsItsOwnLinks(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	first := filepath.Join(base, "a", ".yolo", "home")
	if err := DeriveDarwinHomeLayout(home, first, []string{".claude"}, nil).Apply(); err != nil {
		t.Fatal(err)
	}
	writeTreeFile(t, filepath.Join(first, "claude", "kept.json"), "state")
	if err := DeriveDarwinHomeLayout(home, first, []string{".claude"}, nil).Apply(); err != nil {
		t.Fatalf("a second apply of the same layout must be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "kept.json")); err != nil {
		t.Errorf("the second apply lost the state under the link: %v", err)
	}

	second := filepath.Join(base, "b", ".yolo", "home")
	if err := DeriveDarwinHomeLayout(home, second, []string{".claude"}, nil).Apply(); err != nil {
		t.Fatalf("repointing a layout link at another workspace must work: %v", err)
	}
	if got, _ := os.Readlink(filepath.Join(home, ".claude")); got != filepath.Join(second, "claude") {
		t.Errorf("~/.claude -> %s, want the new workspace's sidecar", got)
	}
}

// NO SIDECAR, NO LAYOUT — and this is not a degraded mode. An install capture bootstraps a
// throwaway staging home whose contract is that everything the installer writes lands under
// it; its delta walk does not follow symlinks, so a layout there would record an empty
// install. The launcher names the sidecar, the capture planner does not.
func TestInstallDarwinHomeLayoutWithoutASidecarWritesNothing(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home})
	if err := InstallDarwinHomeLayout(e, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a bootstrap with no sidecar laid %d paths: %v", len(entries), entries)
	}
}
