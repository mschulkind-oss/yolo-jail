package entrypoint

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

// stagePackForBootstrap writes a REAL embedded manifest into a pack root the bootstrap can
// load. Named by pack because the shared-directory hook's sibling test stages `pi`.
//
// The real one, not a fixture: the tier of a path is what the PACK declares (§6 P2), so a
// test with its own invented manifest would pass while claude's `state` scopes said
// something else. This breaks if `.claude` stops being scope:workspace, if
// `.claude-shared-credentials` stops being scope:machine, or if the shared_credentials hook
// is dropped — each of which changes what the layout has to do.
func stagePackForBootstrap(t *testing.T, name string) string {
	t.Helper()
	p, err := embeddedPack(name)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(p.Root, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, name)
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
		"YOLO_PACK_ROOT":           stagePackForBootstrap(t, "claude"),
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
	if _, err := WriteHomeOverlayManifest(overlay, []string{".claude/skills", ".claude/CLAUDE.md"}); err != nil {
		t.Fatal(err)
	}

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
	// The pack is still selected — it is the one that stopped shipping the skill. (This boot
	// used to name no pack root, which passed only while the overlay followed ANY link: with
	// no pack, `~/.claude` is not this launch's layout link, and the install now refuses to
	// deliver through it rather than write into whichever sidecar it names.)
	e := DarwinEnvFrom(map[string]string{
		"HOME": home, "JAIL_HOME": home, "YOLO_HOST_DIR": ws,
		"YOLO_BLOCK_CONFIG": `[]`, "YOLO_MISE_TOOLS": `{}`,
		"YOLO_PACK_ROOT":        stagePackForBootstrap(t, "claude"),
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
	if !offers(t, err.Error(), "sudo", "rm", "-rf", home) {
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
			got := layoutRemedyPaths(t, msg)
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
// resetting it does not help) while not PRESCRIBING it. The words are a shell's
// (offeredCommands), so a path the message left unquoted comes back as the pieces the
// reader's shell would remove, and fails the comparison.
func layoutRemedyPaths(t *testing.T, msg string) []string {
	t.Helper()
	var out []string
	for _, argv := range offeredCommands(t, msg, "sudo", "rm") {
		if argv[0] == "sudo" {
			argv = argv[1:]
		}
		if len(argv) > 2 && argv[0] == "rm" && argv[1] == "-rf" {
			out = append(out, argv[2:]...)
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

// A HOME-ROOT host_files FILE GETS THE LINK PODMAN'S SKELETON GIVES IT, AND NOTHING ELSE DOES.
// The deciding call is config.HostFileEntry.StagingFor and the target SymlinkTarget, the two
// the skeleton reads (run.TestTheSkeletonsHostFileLinksAreTheMacosUserLayouts compares the two
// backends' output). A destination under a writable root, a new top-level directory and a
// directory entry are not home-root files and get no redirect; a layout with no sidecar gets
// none at all, since an install capture's flat staging home has no workspace tier to point it
// into. Applied, the link is laid DANGLING and the layout creates nothing under it: `once`
// seeds only a file it cannot stat, and the host_files step makes the directory through the
// path it checked.
func TestWithHostFileRedirectsLaysOnlyHomeRootFiles(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, sidecar := filepath.Join(base, "home"), filepath.Join(base, "ws", ".yolo", "home")
	npmrc := config.HostFileEntry{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly}
	plain := config.HostFileEntry{Path: "gitignore_global", Codec: "raw", HasContent: true, Mode: config.HostFileModeOnce}
	entries := []config.HostFileEntry{
		npmrc,
		plain,
		// A login rc file WriteLoginRC writes by path: no link (HT-D12).
		{Path: ".zshrc", Codec: "raw", HasContent: true, Mode: config.HostFileModeOnce},
		{Path: ".config/mytool/c.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
		{Path: "hf/one.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
		{Path: "themes", Source: "/host/themes", IsDir: true, Mode: config.HostFileModeCopy},
	}

	l := DeriveDarwinHomeLayout(home, sidecar, nil, nil).WithHostFileRedirects(entries, nil)
	want := []DarwinHomeLink{
		{Path: filepath.Join(home, ".npmrc"), Target: filepath.FromSlash(npmrc.SymlinkTarget())},
		{Path: filepath.Join(home, "gitignore_global"), Target: filepath.FromSlash(plain.SymlinkTarget())},
	}
	if !reflect.DeepEqual(l.HostFileRedirects, want) {
		t.Errorf("HostFileRedirects = %v, want %v", l.HostFileRedirects, want)
	}
	if got := (DarwinHomeLayout{Home: home}).WithHostFileRedirects(entries, nil).HostFileRedirects; got != nil {
		t.Errorf("a layout with no sidecar gained host_files redirects: %v", got)
	}

	if err := l.Apply(); err != nil {
		t.Fatal(err)
	}
	for _, ln := range want {
		if got, err := os.Readlink(ln.Path); err != nil || got != ln.Target {
			t.Errorf("%s -> %q (err %v), want %q", ln.Path, got, err, ln.Target)
		}
		if _, err := os.Stat(ln.Path); !os.IsNotExist(err) {
			t.Errorf("%s resolves after the layout (err %v); it must dangle until the "+
				"host_files step writes it, or `once` never seeds", ln.Path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(sidecar, "config", "yolo-home")); !os.IsNotExist(err) {
		t.Errorf("the layout created the host_files staging directory (err %v); it is the "+
			"host_files step's to create, through the path it checked", err)
	}
}

// TestDarwinLoginRCFilesAreTheFilesWriteLoginRCWrites: DarwinLoginRCFiles is the list
// WithHostFileRedirects keeps links away from, and WriteLoginRC spells the same names itself. A
// fourth rc file WriteLoginRC learns to write, missing from the list, would be one a workspace's
// host_files link sends that write through into another workspace's sidecar (HT-D12). So the
// writer is RUN, and the files it leaves in an empty home are compared with the list.
func TestDarwinLoginRCFilesAreTheFilesWriteLoginRCWrites(t *testing.T) {
	home := t.TempDir()
	if err := WriteLoginRC(NewEnv(map[string]string{"HOME": home})); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	var wrote []string
	for _, d := range ents {
		wrote = append(wrote, d.Name())
	}
	want := DarwinLoginRCFiles()
	slices.Sort(wrote)
	slices.Sort(want)
	if !slices.Equal(wrote, want) {
		t.Errorf("WriteLoginRC wrote %v at the home root; DarwinLoginRCFiles is %v", wrote, want)
	}
}

// TestASecondWorkspaceLayoutRepointsTheFirstsLinks keeps the measurement the macos-user account
// home's hold rests on (internal/cli/run's accounthomehold.go,
// docs/reference/macos-user-home-tiers.md#ht-d15): the one account home holds ONE link set, and a
// second workspace's layout repoints the first's core links even when the two select disjoint
// packs. If this ever stops holding — a per-workspace home, say — the hold that refuses a second
// workspace's launch while a session runs is what to revisit, and this test is the evidence.
func TestASecondWorkspaceLayoutRepointsTheFirstsLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "Users", "_yolojail")
	a := filepath.Join(base, "Shared", "a", ".yolo", "home")
	b := filepath.Join(base, "Shared", "b", ".yolo", "home")
	la := DeriveDarwinHomeLayout(home, a, []string{".claude"}, []string{".claude-shared-credentials"})
	if err := la.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := DeriveDarwinHomeLayout(home, b, []string{".codex"}, nil).Apply(); err != nil {
		t.Fatal(err)
	}
	var taken []string
	for _, ln := range la.Links {
		if got, _ := os.Readlink(ln.Path); got != ln.Target {
			rel, _ := filepath.Rel(home, ln.Path)
			taken = append(taken, rel)
		}
	}
	for _, core := range []string{".npm-global", ".local", "go", filepath.Join(".yolo", "bin"), ".config"} {
		if !slices.Contains(taken, core) {
			t.Errorf("workspace B's layout left A's %s in place; B took only %v", core, taken)
		}
	}
}

// --- cache_relocations (docs/plans/cache-relocation.md, the macos-user section) ------------

// relocWire is DarwinCacheRelocationsEnv's value for m.
func relocWire(m map[string]string) string { return DarwinCacheRelocationsWire(m) }

// relocEnv is an Env for home naming relocs, or none for a nil map.
func relocEnv(home string, relocs map[string]string) *Env {
	vars := map[string]string{"HOME": home, "JAIL_HOME": home}
	if relocs != nil {
		vars[DarwinCacheRelocationsEnv] = relocWire(relocs)
	}
	return DarwinEnvFrom(vars, home)
}

// THE REAL BOOT LAYS EACH LINK: ~/.cache/<subdir> → the target, beside the workspace tier, and
// records what it laid. Fails if the relocation half is dropped from InstallDarwinHomeLayout, or
// the layout step from RunDarwinBootstrap.
func TestDarwinBootstrapLaysEachCacheRelocationLink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "hf")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	home, _ := darwinBootstrapHome(t, map[string]string{
		DarwinCacheRelocationsEnv: relocWire(map[string]string{"huggingface": target}),
	})
	got, err := os.Readlink(filepath.Join(home, ".cache", "huggingface"))
	if err != nil || got != target {
		t.Fatalf("~/.cache/huggingface -> %q (%v), want a link to %s", got, err, target)
	}
	// A write through the link lands at the target, which is the whole feature.
	if err := os.WriteFile(filepath.Join(home, ".cache", "huggingface", "model"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "model")); err != nil {
		t.Errorf("a write through ~/.cache/huggingface did not land at the target: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".cache", darwinCacheRelocationManifest)); err != nil {
		t.Errorf("the links laid are not recorded: %v", err)
	}
}

// ONLY WHAT IT LAID GOES: a subdir no longer relocated loses the link yolo laid for it, while a
// link somebody else made in ~/.cache, and a yolo link since re-pointed, are left alone. With no
// relocation at all the last one goes, and so does the record.
func TestTheCacheRelocationStepRemovesOnlyTheLinksItLaid(t *testing.T) {
	home := t.TempDir()
	a, b, c := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b"), filepath.Join(t.TempDir(), "c")
	cache := filepath.Join(home, ".cache")
	if err := InstallDarwinCacheRelocations(relocEnv(home, map[string]string{"hf": a, "pw": b, "old": c})); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(c, filepath.Join(cache, "mine")); err != nil {
		t.Fatal(err)
	}
	// "old" was yolo's and has been re-pointed since: it is no longer the link yolo laid.
	if err := os.Remove(filepath.Join(cache, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, filepath.Join(cache, "old")); err != nil {
		t.Fatal(err)
	}
	if err := InstallDarwinCacheRelocations(relocEnv(home, map[string]string{"hf": a})); err != nil {
		t.Fatal(err)
	}
	link := func(sub string) string { got, _ := os.Readlink(filepath.Join(cache, sub)); return got }
	if link("hf") != a {
		t.Errorf("~/.cache/hf, still relocated, is %q", link("hf"))
	}
	if _, err := os.Lstat(filepath.Join(cache, "pw")); err == nil {
		t.Errorf("~/.cache/pw, no longer relocated, still links to %q", link("pw"))
	}
	if link("mine") != c || link("old") != a {
		t.Errorf("a link yolo did not lay was touched: mine -> %q, old -> %q", link("mine"), link("old"))
	}
	if err := InstallDarwinCacheRelocations(relocEnv(home, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(cache, "hf")); err == nil {
		t.Errorf("a launch with no relocation left ~/.cache/hf linked")
	}
	if _, err := os.Lstat(filepath.Join(cache, darwinCacheRelocationManifest)); err == nil {
		t.Errorf("a launch with no relocation left the record behind")
	}
	if link("mine") != c {
		t.Errorf("a launch with no relocation removed a link yolo did not lay")
	}
	for _, d := range []string{a, b, c} {
		if _, err := os.Lstat(d); err == nil {
			t.Errorf("a target %s was created by the bootstrap; only the host makes a target", d)
		}
	}
}

// NO MIGRATION (OQ-HT2): a real directory where a relocation's link belongs is refused, named
// with the copy into the target and the removal, and left exactly as it was — while every other
// relocation is still laid.
func TestTheCacheRelocationStepRefusesARealDirectoryAtALinkPath(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, ".cache")
	held := filepath.Join(cache, "huggingface")
	if err := os.MkdirAll(held, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(held, "model"), []byte("cached before"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, other := filepath.Join(t.TempDir(), "hf"), filepath.Join(t.TempDir(), "pw")
	err := InstallDarwinCacheRelocations(relocEnv(home, map[string]string{"huggingface": target, "pw": other}))
	if err == nil {
		t.Fatal("a real directory at a relocation's link path was not refused")
	}
	for _, want := range []string{held, target, "OQ-HT2", "cp -R " + held + "/. " + target + "/", "sudo rm -rf " + held} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if b, rerr := os.ReadFile(filepath.Join(held, "model")); rerr != nil || string(b) != "cached before" {
		t.Errorf("the refused directory was changed: %q (%v)", b, rerr)
	}
	if got, _ := os.Readlink(filepath.Join(cache, "pw")); got != other {
		t.Errorf("the other relocation was not laid beside the refusal: ~/.cache/pw -> %q", got)
	}
}

// A LINK AT A RELOCATION'S PATH IS A NAME, AND IS REPLACED: the profile names the target, so
// whoever pointed it elsewhere gained nothing, and the launch puts the relocation back.
func TestTheCacheRelocationStepReplacesALinkPointingElsewhere(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, ".cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "hf")
	if err := os.Symlink(t.TempDir(), filepath.Join(cache, "hf")); err != nil {
		t.Fatal(err)
	}
	if err := InstallDarwinCacheRelocations(relocEnv(home, map[string]string{"hf": target})); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(cache, "hf")); got != target {
		t.Errorf("~/.cache/hf -> %q, want %s", got, target)
	}
}

// ~/.cache ITSELF A LINK is refused, and nothing is laid through it: this step runs outside the
// sandbox, and every session's sandbox may write the account home.
func TestTheCacheRelocationStepRefusesALinkedCacheDir(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".cache")); err != nil {
		t.Fatal(err)
	}
	err := InstallDarwinCacheRelocations(relocEnv(home, map[string]string{"hf": filepath.Join(t.TempDir(), "hf")}))
	if err == nil || !strings.Contains(err.Error(), "sudo rm "+filepath.Join(home, ".cache")) {
		t.Fatalf("a linked ~/.cache was not refused with the link's removal: %v", err)
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) > 0 {
		t.Errorf("something was laid through the linked ~/.cache: %v", ents)
	}
}

// NOTHING TO DO, NOTHING MADE: no relocation and no ~/.cache creates no directory.
func TestNoRelocationCreatesNoCacheDir(t *testing.T) {
	home := t.TempDir()
	if err := InstallDarwinCacheRelocations(relocEnv(home, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".cache")); err == nil {
		t.Errorf("a launch with no relocation made ~/.cache")
	}
}

// A KEY THAT IS NOT ONE SEGMENT IS REFUSED, never joined into a path this step writes.
func TestTheCacheRelocationStepRefusesAKeyThatIsAPath(t *testing.T) {
	home := t.TempDir()
	for _, sub := range []string{"../escape", "a/b", "..", darwinCacheRelocationManifest} {
		if err := InstallDarwinCacheRelocations(relocEnv(home, map[string]string{sub: "/opt/x"})); err == nil {
			t.Errorf("the key %q was laid", sub)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "escape")); err == nil {
		t.Errorf("a `../` key wrote outside ~/.cache")
	}
}

// THE WIRE ROUND-TRIPS, and an unparseable one is an error rather than "none".
func TestTheCacheRelocationWireRoundTrips(t *testing.T) {
	in := map[string]string{"b": "/opt/b", "a": "/Volumes/D/a"}
	got, err := ParseDarwinCacheRelocations(DarwinCacheRelocationsWire(in))
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %v (%v), want %v", got, err, in)
	}
	if w := DarwinCacheRelocationsWire(in); w != `{"a":"/Volumes/D/a","b":"/opt/b"}` {
		t.Errorf("the wire is not sorted JSON: %s", w)
	}
	if _, err := ParseDarwinCacheRelocations("not json"); err == nil {
		t.Errorf("an unparseable wire read as none")
	}
	if m, err := ParseDarwinCacheRelocations(""); err != nil || len(m) != 0 {
		t.Errorf("an empty wire = %v, %v", m, err)
	}
}
