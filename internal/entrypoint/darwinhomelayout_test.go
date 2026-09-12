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
	// TWO of each, deliberately. The writable half was already pinned for multiplicity
	// (the len check below); the SHARED half was pinned with a one-element list, so
	// `if len(sharedDirs) > 1 { sharedDirs = sharedDirs[:1] }` before the mirror loop
	// passed the whole short suite — measured 2026-09-12. packs/agy declares a second
	// machine-scope dir (.gemini-shared-credentials), so `packs: ["claude","agy"]` on
	// this backend is exactly the case that gap covered.
	l := DeriveDarwinHomeLayout(home, sidecar, []string{".claude", ".codex"},
		[]string{".claude-shared-credentials", ".gemini-shared-credentials"})

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
	// The machine tier never moves — it is mirrored INTO the sidecar, pointing home, and
	// EVERY machine-scope dir is, not just the first one.
	gotMirror := map[string]string{}
	for _, m := range l.Mirrors {
		gotMirror[m.Path] = m.Target
	}
	for _, dir := range []string{".claude-shared-credentials", ".gemini-shared-credentials"} {
		if gotMirror[sidecar+"/"+dir] != home+"/"+dir {
			t.Errorf("%s is not mirrored into the sidecar (mirrors = %v)", dir, l.Mirrors)
		}
	}
	if len(l.Mirrors) != 2 {
		t.Errorf("the layout mirrors %d paths, want one per machine-scope dir: %v",
			len(l.Mirrors), l.Mirrors)
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
	for _, dir := range []string{".claude-shared-credentials", ".gemini-shared-credentials"} {
		if !dirs[home+"/"+dir] {
			t.Errorf("%s, the machine-scope dir a mirror points at, is never created", dir)
		}
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

// A LAUNCH MUST NOT BE BRICKED BY A LINK THE PREVIOUS ONE LEFT, and this is the failure that
// found the rule. `.claude.json → .claude/claude.json` is in paths.HomeFileRedirects, which is
// CORE, while `.claude` itself is a symlink only when a PACK declares it. So a launch whose
// packs do not declare `.claude` still needed ~/.claude to exist as a directory — and if a
// previous launch's workspace had been deleted, ~/.claude was a DANGLING symlink, where
// MkdirAll fails with `mkdir …: file exists` (Stat fails, mkdir refuses). The layout generator
// then failed, the boot refused, and nothing repaired it: every later launch without that pack
// hit the same wall, with no remedy in the message.
//
// MEASURED 2026-09-12: three of the six macos-user integration twins failed exactly here on
// their first hardware run — each configures no packs, and an earlier test in the same run had
// pointed ~/.claude at a workspace it then deleted.
//
// The rule the fix encodes is P2's: the layout manages what THIS launch declares. A redirect
// whose directory the layout does not lay is not laid either — ~/.claude.json means nothing
// without a ~/.claude to hold it — and a stale link nothing declares is left alone rather than
// removed, because removing it would either destroy a live sidecar's link or turn it into the
// real directory OQ-HT2 then refuses forever.
func TestDarwinHomeLayoutSurvivesAStaleLinkFromADeletedWorkspace(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	gone := filepath.Join(base, "deleted-ws", ".yolo", "home")
	// The state a deleted workspace leaves: a link yolo wrote, pointing nowhere.
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(gone, "claude"), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}

	// A launch in a NEW workspace whose packs declare nothing (`packs: []` is the default,
	// and the launch says so) — so `.claude` is not in this layout at all.
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	if err := DeriveDarwinHomeLayout(home, sidecar, nil, nil).Apply(); err != nil {
		t.Fatalf("a stale link from a deleted workspace bricked the launch: %v", err)
	}
	// The redirect that needed it is not laid, and the stale link is not touched.
	if _, err := os.Lstat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("~/.claude.json was laid with no ~/.claude to hold it (err=%v)", err)
	}
	if got, _ := os.Readlink(filepath.Join(home, ".claude")); got != filepath.Join(gone, "claude") {
		t.Errorf("the stale link was rewritten to %q; a link this launch does not declare "+
			"is left alone", got)
	}
	// The redirects whose directories ARE core (.config) are unaffected.
	if got, err := os.Readlink(filepath.Join(home, ".gitconfig")); err != nil ||
		got != filepath.Join(".config", "git", "config") {
		t.Errorf(".gitconfig -> %q (err=%v), want the core .config redirect intact", got, err)
	}
}

// And the same launch WITH the pack heals it: `.claude` is in Links, so ensureLayoutSymlink
// repoints the stale link at this workspace before the redirect step needs it. This is the
// pairing that makes the test above a statement about DECLARATION rather than about dangling
// links — one input differs, and it is the pack.
func TestDarwinHomeLayoutRepointsAStaleLinkThePackStillDeclares(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	gone := filepath.Join(base, "deleted-ws", ".yolo", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(gone, "claude"), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}

	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	if err := DeriveDarwinHomeLayout(home, sidecar, []string{".claude"}, nil).Apply(); err != nil {
		t.Fatalf("a launch that declares .claude must repoint the stale link: %v", err)
	}
	if got, _ := os.Readlink(filepath.Join(home, ".claude")); got != filepath.Join(sidecar, "claude") {
		t.Errorf("~/.claude -> %q, want this workspace's sidecar", got)
	}
	// And the redirect it gates is laid, and writes through to the new sidecar.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing ~/.claude.json through the redirect: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sidecar, "claude", "claude.json")); err != nil {
		t.Errorf("~/.claude.json did not land in the new sidecar: %v", err)
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

// THE SOURCING, which is a design constraint rather than an implementation detail and was
// not pinned by anything. §6 P2 of the design doc says the tier of a path is THE PACK'S
// DECLARATION on this backend too, and the file header above repeats it — but every test
// here passes its tier lists in by hand or stages the one shipped pack whose dirs any
// hardcoded list would also name. Measured 2026-09-12: replacing
// `packload.WritableDirs(packs), packload.SharedDirs(packs)` in InstallDarwinHomeLayout
// with literal slices of the six shipped workspace dirs and the one shared dir left
// `go test -short ./...` fully green. The consequence is that a pack added tomorrow gets
// no link and no mirror, silently.
//
// This test uses a pack whose declared dirs no hardcoded list could contain.
func TestTheHomeLayoutsTiersComeFromThePackDeclaration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sourcing-probe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "name": "sourcing-probe",
	  "contributes": [
	    {"kind": "state", "at": ".probe-workspace-state", "scope": "workspace"},
	    {"kind": "state", "at": ".probe-machine-state", "scope": "machine",
	     "because": "pins that the machine tier is read from here"}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	home, sidecar := filepath.Join(base, "home"), filepath.Join(base, "ws", ".yolo", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	e := DarwinEnvFrom(map[string]string{
		"HOME":               home,
		"YOLO_PACK_ROOT":     root,
		DarwinHomeSidecarEnv: sidecar,
	}, home)
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 1 {
		t.Fatalf("staged 1 pack, loaded %d", len(packs))
	}
	if err := InstallDarwinHomeLayout(e, packs); err != nil {
		t.Fatal(err)
	}

	// scope: workspace → a link in the account home pointing into the sidecar.
	got, err := os.Readlink(filepath.Join(home, ".probe-workspace-state"))
	if err != nil {
		t.Fatalf("the pack's scope:workspace dir got no link, so the tier lists are not "+
			"coming from the pack declaration: %v", err)
	}
	if want := filepath.Join(sidecar, "probe-workspace-state"); got != want {
		t.Errorf("~/.probe-workspace-state -> %q, want %q", got, want)
	}
	// scope: machine → stays in the account home, mirrored back into the sidecar.
	got, err = os.Readlink(filepath.Join(sidecar, ".probe-machine-state"))
	if err != nil {
		t.Fatalf("the pack's scope:machine dir got no mirror, so the machine tier is not "+
			"coming from the pack declaration either: %v", err)
	}
	if want := filepath.Join(home, ".probe-machine-state"); got != want {
		t.Errorf("mirror -> %q, want %q", got, want)
	}
}

// THE REFUSAL'S REMEDY HAS TO REACH THE PATH IT NAMES — the first of the two defects a
// mutation pass found on 2026-09-12 and left unfixed (macos-user-home-tiers.md §10; the
// runbook's item 10, docs/plans/runbooks/macos-user-manual-checks.md).
//
// THE DEFECT. `Apply` collected every occupied path into ONE list and always prescribed
// `sudo rm -rf <account home>`. A Link's path is in the account home, so that works. A
// MIRROR's path is in the WORKSPACE SIDECAR, which that command does not touch — so the
// refusal for an occupied mirror told the reader to destroy the machine tier (their
// credentials, the one thing the mirror exists to keep reachable) and then get the
// identical refusal on the next launch, because the offending directory was never in the
// account. Refusing is correct; the remedy was the bug.
//
// HOW IT IS ASSERTED, and why not on the prose: the message is checked by collecting the
// COMMANDS it offers and requiring each to name a path that is actually occupied. That
// survives rewording, and it fails the moment the two groups are merged back into one —
// a merged message offers `rm -rf <home>` for a sidecar path, which is precisely the
// defect. The account home is still expected to be NAMED in the mirror case, because the
// warning "the account reset does not touch these" is the sentence that stops a reader
// running it from memory; naming it and prescribing it are different acts.
func TestDarwinHomeLayoutRefusalPrescribesARemedyThatReachesTheOccupiedPath(t *testing.T) {
	// A pack of its own, so the mirror path comes from a DECLARATION rather than from a
	// list this test also writes — the same sourcing rule
	// TestTheHomeLayoutsTiersComeFromThePackDeclaration pins.
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "remedy-probe")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "name": "remedy-probe",
	  "contributes": [
	    {"kind": "state", "at": ".probe-workspace-state", "scope": "workspace"},
	    {"kind": "state", "at": ".probe-machine-state", "scope": "machine",
	     "because": "its mirror lands in the sidecar, which is the tier under test"}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// occupy makes `rel` a real directory under `root`, which is what every case here
	// needs and what the layout must refuse to replace.
	occupy := func(t *testing.T, root, rel string) string {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name string
		// occupied returns the paths to make real, and the remedies the refusal must
		// then offer — in the order Apply reports them (account home first).
		setup func(t *testing.T, home, sidecar string) (wantRemedies []string)
	}{
		{
			// The pre-A′ account: only the account home is occupied, so the account reset
			// IS the remedy. This is the case that worked before and must keep working.
			name: "only the account home is occupied",
			setup: func(t *testing.T, home, sidecar string) []string {
				occupy(t, home, ".probe-workspace-state")
				return []string{home}
			},
		},
		{
			// THE DEFECT'S CASE. A container-era launch bound /home/agent at the sidecar,
			// so the agent's own `scope: machine` dir landed as a REAL directory inside
			// <ws>/.yolo/home — where this backend now needs a mirror.
			name: "only the workspace sidecar is occupied",
			setup: func(t *testing.T, home, sidecar string) []string {
				return []string{occupy(t, sidecar, ".probe-machine-state")}
			},
		},
		{
			// Both at once: one refusal, both remedies, neither standing in for the other.
			name: "both tiers are occupied",
			setup: func(t *testing.T, home, sidecar string) []string {
				occupy(t, home, ".probe-workspace-state")
				return []string{home, occupy(t, sidecar, ".probe-machine-state")}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			sidecar := filepath.Join(base, "ws", ".yolo", "home")
			for _, d := range []string{home, sidecar} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			wantRemedies := tc.setup(t, home, sidecar)

			e := DarwinEnvFrom(map[string]string{
				"HOME":               home,
				"YOLO_PACK_ROOT":     packRoot,
				DarwinHomeSidecarEnv: sidecar,
			}, home)
			packs, err := LoadJailPacks(e)
			if err != nil {
				t.Fatal(err)
			}
			// The REAL boot entry, not Apply directly: the remedy is only worth anything
			// if it reaches a launch, and the tier lists have to come from the pack.
			err = InstallDarwinHomeLayout(e, packs)
			if err == nil {
				t.Fatal("a real directory where a layout symlink belongs must refuse " +
					"(OQ-HT2: nothing is migrated, copied or renamed) — this applied cleanly")
			}
			msg := err.Error()

			for _, p := range wantRemedies {
				if !strings.Contains(msg, p) {
					t.Errorf("the refusal never names %s, so the reader cannot tell which "+
						"path is in the way:\n%s", p, msg)
				}
			}
			got := layoutRemedyPaths(msg)
			if !equalStrings(got, wantRemedies) {
				t.Errorf("the refusal offers these commands:\n  rm -rf %v\nand the paths it "+
					"must be able to fix are:\n  %v\n\nA remedy naming a path that is not "+
					"occupied cannot fix anything, and one MISSING for an occupied path "+
					"leaves that launch refusing forever. The sidecar case is the one that "+
					"regressed before: `rm -rf <account home>` does not touch "+
					"<workspace>/.yolo/home, so following it destroys the machine tier and "+
					"changes nothing.\nFull refusal:\n%s", got, wantRemedies, msg)
			}
		})
	}
}

// layoutRemedyPaths returns the path argument of every `rm -rf` the refusal OFFERS — the
// indented command lines, not a path merely mentioned in prose. The distinction is the
// test's whole subject: the mirror case has to NAME the account home (to warn that
// resetting it does not help) while not PRESCRIBING it.
func layoutRemedyPaths(msg string) []string {
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		if line == strings.TrimLeft(line, " \t") {
			continue // not an indented command line
		}
		trimmed := strings.TrimSpace(line)
		for _, prefix := range []string{"sudo rm -rf ", "rm -rf "} {
			if rest, ok := strings.CutPrefix(trimmed, prefix); ok {
				out = append(out, rest)
				break
			}
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
