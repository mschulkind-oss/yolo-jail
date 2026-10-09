package entrypoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darwinoverlaylinks_test.go: THE BOOTSTRAP LAYS THE PATH THE PROFILE PROTECTS, or refuses.
//
// G14's content rules (macosuser.ResolveHomeReadonly) are a list of paths computed on the host
// before the bootstrap runs: the resolved workspace and account home, with everything below them
// joined as text through the layout's own links. That is the path the kernel sees only if every
// component below those two bases is a real directory or a link THIS launch laid. The sidecar is
// inside the workspace, which the agent can always write, so an earlier session whose profile did
// not cover these paths (a `packs: []` launch, or one with another pack selection) can leave a
// symbolic link anywhere in it. These tests plant one at each position and drive the REAL boot
// entry, so the assertions are about what a launch would do.

// bootFixture is one account home and one workspace, bootstrapped as often as a test likes.
type bootFixture struct {
	t                  *testing.T
	home, ws, sidecar  string
	packRoot           string // "" boots with no pack
	resolvedTempParent string
}

// newBootFixture mints a resolved home and workspace (AGENTS.md's darwin rule: resolve where the
// path is minted) for a launch selecting `pack`, or none when pack is "".
func newBootFixture(t *testing.T, pack string) *bootFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &bootFixture{t: t, home: filepath.Join(base, "home"), resolvedTempParent: base}
	if pack != "" {
		f.packRoot = stagePackForBootstrap(t, pack)
	}
	f.workspace("workspace")
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// workspace points the fixture at another workspace under the same base, sharing the home.
func (f *bootFixture) workspace(name string) {
	f.t.Helper()
	f.ws = filepath.Join(f.resolvedTempParent, name)
	f.sidecar = filepath.Join(f.ws, ".yolo", "home")
	if err := os.MkdirAll(f.ws, 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// boot runs the real native bootstrap with a fresh overlay tree holding `overlay`, and returns
// its error. Not asserted here: a temp home can fail an unrelated generator (no git, no node),
// so each test asserts on the substrings it is about.
//
// The tree is listed the way the host builder lists it (WriteHomeOverlayManifest), each file
// under a `skills` directory belonging to that skills destination and every other file being a
// destination of its own — the two shapes run.buildMacosHomeOverlayFor lays out.
func (f *bootFixture) boot(overlay map[string]string) error {
	f.t.Helper()
	tree := f.t.TempDir()
	var dests []string
	for rel, body := range overlay {
		writeTreeFile(f.t, filepath.Join(tree, filepath.FromSlash(rel)), body)
		dests = append(dests, overlayDestOf(rel))
	}
	if _, err := WriteHomeOverlayManifest(tree, dests); err != nil {
		f.t.Fatal(err)
	}
	vars := map[string]string{
		"HOME":                     f.home,
		"JAIL_HOME":                f.home,
		"YOLO_HOST_DIR":            f.ws,
		"YOLO_BLOCK_CONFIG":        `[]`,
		"YOLO_MISE_TOOLS":          `{}`,
		"YOLO_DARWIN_WORKSPACE":    f.ws,
		DarwinHomeSidecarEnv:       f.sidecar,
		"MISE_DATA_DIR":            filepath.Join(f.home, ".yolo", "mise"),
		"YOLO_DARWIN_HOME_OVERLAY": tree,
	}
	if f.packRoot != "" {
		vars["YOLO_PACK_ROOT"] = f.packRoot
	}
	e := DarwinEnvFrom(vars, f.home)
	e.Stderr = &strings.Builder{}
	return RunDarwinBootstrap(e, DarwinBootstrapOptions{})
}

// overlayDestOf is the destination a fixture overlay file belongs to: the `skills` directory
// above it when there is one (`.codex/skills/demo/SKILL.md` → `.codex/skills`), else the file.
func overlayDestOf(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts[:len(parts)-1] {
		if p == "skills" {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return rel
}

// requireNoContentFailure fails when the layout or the overlay step failed. Other generators may
// fail in a temp home; these two may not.
func requireNoContentFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, step := range []string{"darwin_home_layout", "install_home_overlay"} {
		if strings.Contains(err.Error(), step) {
			t.Fatalf("the %s step failed: %v", step, err)
		}
	}
}

// symlinkAt makes path a symbolic link to target, creating path's parent.
func symlinkAt(t *testing.T, path, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// assertAbsent fails when path exists (a link included).
func assertAbsent(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists (err %v): %s", path, err, why)
	}
}

// assertLinkTo fails unless path is still a symbolic link to target.
func assertLinkTo(t *testing.T, path, target, why string) {
	t.Helper()
	if got, err := os.Readlink(path); err != nil || got != target {
		t.Errorf("%s -> %q (err %v), want the link to %s left as it was: %s", path, got, err, target, why)
	}
}

// A LINK AT THE AGENT'S SIDECAR DIRECTORY ITSELF, planted between two launches — the shape the
// review reproduced. The first launch lays `~/.codex` -> <sidecar>/codex. An agent in a session
// that did not cover that path swaps <sidecar>/codex for a link to a directory of its choosing.
// The next launch's layout step refuses it, and the overlay step runs anyway (every boot step
// does): `~/.codex` is still the right link, so the old install walked through it and the planted
// link into the chosen directory, at a path no rule names. It must refuse too, deliver nothing
// through the link, and remove nothing — the directory it points at may hold real state.
func TestDarwinBootstrapRefusesALinkAtTheAgentsSidecarDirectory(t *testing.T) {
	f := newBootFixture(t, "codex")
	requireNoContentFailure(t, f.boot(map[string]string{
		".codex/skills/demo/SKILL.md": "first launch",
		".codex/AGENTS.md":            "first launch",
	}))

	evil := filepath.Join(f.ws, "evil")
	writeTreeFile(t, filepath.Join(evil, "history.jsonl"), "state reached through the link")
	link := filepath.Join(f.sidecar, "codex")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	symlinkAt(t, link, evil)

	err := f.boot(map[string]string{
		".codex/skills/demo/SKILL.md": "second launch",
		".codex/AGENTS.md":            "second launch",
	})
	if err == nil || !strings.Contains(err.Error(), "install_home_overlay") ||
		!strings.Contains(err.Error(), link) {
		t.Fatalf("the overlay step did not refuse the link at %s: %v", link, err)
	}
	assertAbsent(t, filepath.Join(evil, "skills"),
		"the skills were delivered through a link the layout did not lay, to a path no rule names")
	assertAbsent(t, filepath.Join(evil, "AGENTS.md"),
		"the briefing was delivered through a link the layout did not lay, to a path no rule names")
	assertLinkTo(t, link, evil, "the refusal names the link and removes nothing")
	if _, serr := os.Stat(filepath.Join(evil, "history.jsonl")); serr != nil {
		t.Errorf("the refusal touched what the link points at: %v", serr)
	}
}

// THE OVERLAY STEP'S OWN CHECK, where nothing after it would refuse. A link at the agent's sidecar
// directory that points somewhere ELSE IN THE SIDECAR resolves inside the install's roots, so the
// install's containment check accepts it, and the route from ~/.codex passes the layout's own link.
// Only linkedSidecarPaths, checked again by InstallHomeOverlay, keeps the delivery from landing at
// <sidecar>/elsewhere, a path no content rule names and the agent can therefore rewrite.
func TestDarwinBootstrapRefusesASidecarLinkThatStaysInsideTheSidecar(t *testing.T) {
	f := newBootFixture(t, "codex")
	requireNoContentFailure(t, f.boot(map[string]string{
		".codex/skills/demo/SKILL.md": "first launch",
		".codex/AGENTS.md":            "first launch",
	}))

	elsewhere := filepath.Join(f.sidecar, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.sidecar, "codex")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	symlinkAt(t, link, elsewhere)

	err := f.boot(map[string]string{
		".codex/skills/demo/SKILL.md": "second launch",
		".codex/AGENTS.md":            "second launch",
	})
	if err == nil || !strings.Contains(err.Error(), "install_home_overlay") {
		t.Fatalf("the overlay step did not refuse the link at %s: %v", link, err)
	}
	assertAbsent(t, filepath.Join(elsewhere, "skills"),
		"the skills were delivered through a sidecar link the layout did not lay")
	assertAbsent(t, filepath.Join(elsewhere, "AGENTS.md"),
		"the briefing was delivered through a sidecar link the layout did not lay")
	assertLinkTo(t, link, elsewhere, "the refusal names the link and removes nothing")
}

// THE LAYOUT STEP'S OWN REFUSAL, at every sidecar position it lays through: the sidecar's parent,
// the sidecar, a pack's state dir, and a core surface. Refused before anything is created,
// because MkdirAll follows a link and the layout would be laid wherever it points; and the remedy
// removes the LINK, never the account and never what the link points at.
func TestDarwinHomeLayoutRefusesALinkInTheWorkspaceSidecar(t *testing.T) {
	for _, at := range []string{
		".yolo",
		filepath.Join(".yolo", "home"),
		filepath.Join(".yolo", "home", "claude"),
		filepath.Join(".yolo", "home", "local"),
	} {
		t.Run(at, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
			sidecar := filepath.Join(ws, ".yolo", "home")
			evil := filepath.Join(ws, "evil")
			if err := os.MkdirAll(evil, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(ws, at)
			symlinkAt(t, link, evil)

			err = DeriveDarwinHomeLayout(home, sidecar, []string{".claude"}, nil).Apply()
			var linked *LinkedSidecarError
			if !errors.As(err, &linked) || !equalStrings(linked.Links, []string{link}) {
				t.Fatalf("Apply = %v, want a LinkedSidecarError naming exactly %s", err, link)
			}
			if !offers(t, err.Error(), "sudo", "rm", link) {
				t.Errorf("the refusal does not offer removing the link itself:\n%s", err)
			}
			if strings.Contains(err.Error(), "rm -rf") {
				t.Errorf("the refusal prescribes a recursive removal, which on a path that "+
					"resolves through the link would delete what it points at:\n%s", err)
			}
			entries, _ := os.ReadDir(evil)
			if len(entries) != 0 {
				t.Errorf("the layout was laid through the link: %s holds %v", evil, entries)
			}
			assertAbsent(t, filepath.Join(home, ".claude"), "a link was laid before the refusal")
			assertLinkTo(t, link, evil, "the refusal removes nothing")
		})
	}
}

// A LINK AT THE SKILLS DESTINATION. The overlay install used to descend through ANY link and merge
// into its target: the agent's planted skill stayed beside the delivered one, the next session
// loaded both, and both sat at a path the profile does not name. Past a layout link a link is an
// occupant like any other: replaced, and what it pointed at left alone.
func TestDarwinBootstrapReplacesALinkPlantedAtASkillsDestination(t *testing.T) {
	f := newBootFixture(t, "claude")
	evil := filepath.Join(f.ws, "evil")
	writeTreeFile(t, filepath.Join(evil, "planted", "SKILL.md"), "the agent's own skill")
	symlinkAt(t, filepath.Join(f.sidecar, "claude", "skills"), evil)

	requireNoContentFailure(t, f.boot(map[string]string{".claude/skills/demo/SKILL.md": "delivered"}))

	dest := filepath.Join(f.sidecar, "claude", "skills")
	if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("%s is not a real directory after the install (err %v): the delivery went "+
			"through the planted link", dest, err)
	}
	entries, err := os.ReadDir(filepath.Join(f.home, ".claude", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ent := range entries {
		names = append(names, ent.Name())
	}
	if !equalStrings(names, []string{"demo"}) {
		t.Errorf("~/.claude/skills lists %v, want only the delivered [demo]: a skill the agent "+
			"planted would be loaded by the next session", names)
	}
	assertAbsent(t, filepath.Join(evil, "demo"), "the delivered skill landed in the planted link's target")
	if _, serr := os.Stat(filepath.Join(evil, "planted", "SKILL.md")); serr != nil {
		t.Errorf("replacing the link deleted what it pointed at: %v", serr)
	}
}

// A LINK AT THE BRIEFING. os.WriteFile follows a link, so the briefing used to be written into the
// file of the agent's choosing. The link is removed and a regular file written in its place.
func TestDarwinBootstrapReplacesALinkPlantedAtABriefing(t *testing.T) {
	f := newBootFixture(t, "claude")
	evil := filepath.Join(f.ws, "evil.md")
	writeTreeFile(t, evil, "the agent's own file")
	symlinkAt(t, filepath.Join(f.sidecar, "claude", "CLAUDE.md"), evil)

	requireNoContentFailure(t, f.boot(map[string]string{".claude/CLAUDE.md": "delivered briefing"}))

	dest := filepath.Join(f.sidecar, "claude", "CLAUDE.md")
	if fi, err := os.Lstat(dest); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file after the install (err %v)", dest, err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "delivered briefing" {
		t.Errorf("%s = %q, want the delivered briefing", dest, got)
	}
	if got, _ := os.ReadFile(evil); string(got) != "the agent's own file" {
		t.Errorf("the briefing was written through the planted link: %s = %q", evil, got)
	}
}

// A LINK ABOVE THE SIDECAR. `<ws>/.yolo` and `<ws>/.yolo/home` are in the workspace too, and a link
// at either moves every sidecar path at once. Planted after a first launch, so `~/.claude` is
// already the right link and only the overlay step's own check stands between the delivery and
// the link's target.
func TestDarwinBootstrapRefusesALinkAboveTheSidecar(t *testing.T) {
	for _, at := range []string{".yolo", filepath.Join(".yolo", "home")} {
		t.Run(at, func(t *testing.T) {
			f := newBootFixture(t, "claude")
			requireNoContentFailure(t, f.boot(map[string]string{".claude/skills/demo/SKILL.md": "first"}))

			evil := filepath.Join(f.ws, "evil")
			link := filepath.Join(f.ws, at)
			// The agent's copy of what was there, so the chain resolves all the way down.
			if err := os.Rename(link, evil); err != nil {
				t.Fatal(err)
			}
			symlinkAt(t, link, evil)

			err := f.boot(map[string]string{".claude/skills/second/SKILL.md": "second"})
			if err == nil || !strings.Contains(err.Error(), "install_home_overlay") ||
				!strings.Contains(err.Error(), link) {
				t.Fatalf("the overlay step did not refuse the link at %s: %v", link, err)
			}
			var found []string
			_ = filepath.Walk(evil, func(p string, fi os.FileInfo, err error) error {
				if err == nil && filepath.Base(filepath.Dir(p)) == "second" {
					found = append(found, p)
				}
				return nil
			})
			if len(found) > 0 {
				t.Errorf("the skills were delivered through the link at %s: %v", link, found)
			}
		})
	}
}

// OQ-HT2 SAYS THE REFUSED PATH IS NEVER DELETED, and the overlay step used to delete it anyway. A
// pre-layout account home has a real ~/.claude; the layout step refuses and tells the reader to
// move what they want to keep. The overlay step runs after it regardless, and replaced ~/.claude
// wholesale, transcripts and all, because it was not a symlink. A path the layout owns is written
// only once it is the layout's link.
func TestDarwinBootstrapOverlayLeavesAPathTheLayoutRefusedAlone(t *testing.T) {
	f := newBootFixture(t, "claude")
	keep := filepath.Join(f.home, ".claude", "projects", "old.jsonl")
	writeTreeFile(t, keep, "a previous era's transcript")

	err := f.boot(map[string]string{".claude/skills/demo/SKILL.md": "delivered"})
	if err == nil || !offers(t, err.Error(), "sudo", "rm", "-rf", f.home) {
		t.Fatalf("the layout did not refuse the real ~/.claude: %v", err)
	}
	if !strings.Contains(err.Error(), "install_home_overlay") {
		t.Errorf("the overlay step did not report that it delivered nothing: %v", err)
	}
	if _, serr := os.Stat(keep); serr != nil {
		t.Errorf("the overlay deleted the directory the layout refused to touch: %v", serr)
	}
}

// ANOTHER LAUNCH'S LINK IN THE ACCOUNT HOME. The home is shared by every workspace, so a launch
// that delivers under `.claude` without selecting a pack that declares it finds `~/.claude` still
// pointing at the workspace that did. Following it wrote this launch's content into THAT
// workspace's sidecar. Replacing it would leave a real directory the other workspace's next
// launch refuses forever (OQ-HT2). The delivery refuses, naming the link.
func TestDarwinBootstrapWillNotDeliverThroughAnotherLaunchsLink(t *testing.T) {
	f := newBootFixture(t, "claude")
	requireNoContentFailure(t, f.boot(map[string]string{".claude/skills/demo/SKILL.md": "workspace A"}))
	aSkill := filepath.Join(f.sidecar, "claude", "skills", "demo", "SKILL.md")
	aLink := filepath.Join(f.sidecar, "claude")

	f.workspace("other")
	f.packRoot = ""
	err := f.boot(map[string]string{".claude/skills/demo/SKILL.md": "workspace B"})
	userLink := filepath.Join(f.home, ".claude")
	if err == nil || !strings.Contains(err.Error(), "install_home_overlay") ||
		!offers(t, err.Error(), "sudo", "rm", userLink) {
		t.Fatalf("the overlay step did not refuse another launch's link at %s: %v", userLink, err)
	}
	if got, _ := os.ReadFile(aSkill); string(got) != "workspace A" {
		t.Errorf("workspace A's delivered skill now reads %q: another workspace's launch wrote "+
			"into its sidecar through the shared account home", got)
	}
	assertLinkTo(t, userLink, aLink, "another launch's layout link is left alone")
}

// THE LAYOUT'S FILE LINKS ARE NOT WRITTEN OVER, OR THROUGH. `~/.claude.json` is a home-root
// redirect into the sidecar (paths.HomeFileRedirects), and the overlay follows only the layout's
// DIRECTORY links. Written through, a file would land at a sidecar path no content rule names;
// written over, it would be a real file where the next launch's layout needs its link, which
// OQ-HT2 refuses forever. No shipped pack delivers there; the rule is the account home's.
func TestDarwinBootstrapNeverWritesOverTheLayoutsFileLinks(t *testing.T) {
	f := newBootFixture(t, "claude")
	err := f.boot(map[string]string{
		".claude/skills/demo/SKILL.md": "delivered",
		".claude.json":                 "a file where the layout keeps a link",
	})
	redirect := filepath.Join(f.home, ".claude.json")
	if err == nil || !strings.Contains(err.Error(), redirect) {
		t.Fatalf("delivering over the layout's file link at %s did not refuse: %v", redirect, err)
	}
	if fi, lerr := os.Lstat(redirect); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is no longer the layout's link (err %v)", redirect, lerr)
	}
}
