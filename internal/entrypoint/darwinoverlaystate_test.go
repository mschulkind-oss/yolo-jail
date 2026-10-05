package entrypoint

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// darwinoverlaystate_test.go pins G36: installing the macos-user home overlay must replace
// ONLY the destinations the host staged, never a directory above or beside one.
//
// THE DEFECT. installOverlayTree descended through every symlink in the home and replaced
// the first REAL directory it met. Under the workspace-tier layout every agent's state
// directory is a symlink into <workspace>/.yolo/home, so for `.claude/skills` the first
// real directory is the destination itself — and for `.pi/agent/skills` it is
// `~/.pi/agent`, the whole of pi's state: its sign-in, its sessions and the settings the
// same boot had just generated. omp, agy and opencode have the same shape. Every
// macos-user launch deleted all of it and put back only the skills and the briefing.
//
// It runs on Linux because the layout it depends on is plain symlinks, and it drives the
// REAL boot entry against every shipped pack that declares a destination, so a pack added
// with the same shape is covered without anyone remembering to list it.

// contentDestinations is every home-relative skills and briefing destination a pack
// declares, read from the pack's own manifest — the two kinds the host lays into the
// overlay (run.buildMacosHomeOverlay).
func contentDestinations(p *packload.Pack) (skills, briefings []string) {
	for _, c := range p.Decl.Contributions() {
		if c.Into == "" {
			continue
		}
		switch c.Kind {
		case packdecl.KindSkills:
			skills = append(skills, c.Into)
		case packdecl.KindBriefing:
			briefings = append(briefings, c.Into)
		}
	}
	return skills, briefings
}

// stageWholePackForBootstrap copies a shipped pack's WHOLE tree — not just pack.json — into
// a pack root the bootstrap loads, so its derive scripts run and its config surfaces are
// generated exactly as a launch generates them. Those surfaces are part of what the wipe
// destroyed, so a fixture that could not produce them would under-report it.
func stageWholePackForBootstrap(t *testing.T, p *packload.Pack) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, p.Name), os.DirFS(p.Root)); err != nil {
		t.Fatal(err)
	}
	return root
}

// stageHostShapedOverlay lays an overlay out the way the host builder does: each skills
// destination a directory holding one skill, each briefing destination a file.
func stageHostShapedOverlay(t *testing.T, skills, briefings []string) string {
	t.Helper()
	overlay := filepath.Join(t.TempDir(), "home-overlay")
	var dests []string
	for _, d := range skills {
		writeTreeFile(t, filepath.Join(overlay, filepath.FromSlash(d), "delivered", "SKILL.md"), "delivered skill")
		dests = append(dests, d)
	}
	for _, d := range briefings {
		writeTreeFile(t, filepath.Join(overlay, filepath.FromSlash(d)), "delivered briefing")
		dests = append(dests, d)
	}
	if _, err := WriteHomeOverlayManifest(overlay, dests); err != nil {
		t.Fatal(err)
	}
	return overlay
}

// bootDarwinForPack runs the real native bootstrap for ONE staged pack. The bootstrap's own
// error is not fatal to the test, for darwinBootstrapHome's reason: a temp home on Linux
// fails generators unrelated to the overlay (no node, no git identity).
func bootDarwinForPack(t *testing.T, home, ws, packRoot, overlay string) *strings.Builder {
	t.Helper()
	var stderr strings.Builder
	e := DarwinEnvFrom(map[string]string{
		"HOME":                     home,
		"JAIL_HOME":                home,
		"YOLO_HOST_DIR":            ws,
		"YOLO_BLOCK_CONFIG":        `[]`,
		"YOLO_MISE_TOOLS":          `{}`,
		"YOLO_PACK_ROOT":           packRoot,
		"YOLO_DARWIN_WORKSPACE":    ws,
		DarwinHomeSidecarEnv:       filepath.Join(ws, ".yolo", "home"),
		"MISE_DATA_DIR":            filepath.Join(home, ".yolo", "mise"),
		"YOLO_DARWIN_HOME_OVERLAY": overlay,
	}, home)
	e.Stderr = &stderr
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})
	return &stderr
}

// filesUnder lists every regular file below dir (following the home's layout links at the
// top, as the agent does), relative to dir.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(real, p)
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out
}

// underAny reports whether rel (home-relative) is a destination or lies below one.
func underAny(rel string, dests []string) bool {
	for _, d := range dests {
		d = filepath.FromSlash(d)
		if rel == d || strings.HasPrefix(rel, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// THE G36 REPRODUCER. For every shipped pack that declares a skills or briefing destination:
// boot once to lay the layout and generate the pack's config, plant agent state BESIDE each
// destination and in every directory ABOVE it, then boot again with that pack's overlay.
// Everything that was not a destination must survive — the planted state and every file the
// first boot generated — the delivered content must arrive, and a skill the host stopped
// staging must still disappear.
func TestDarwinOverlayInstallKeepsAgentStateBesideAndAboveEveryDestination(t *testing.T) {
	packs, err := embeddedPackSet()
	if err != nil {
		t.Fatal(err)
	}
	covered := 0
	for _, p := range packs {
		skills, briefings := contentDestinations(p)
		if len(skills)+len(briefings) == 0 {
			continue
		}
		covered++
		t.Run(p.Name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			ws := filepath.Join(base, "workspace")
			for _, d := range []string{home, ws} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			packRoot := stageWholePackForBootstrap(t, p)
			dests := append(append([]string{}, skills...), briefings...)

			// Boot 1: no overlay. Lays the layout and writes the pack's surfaces.
			bootDarwinForPack(t, home, ws, packRoot, "")

			// The files boot 1 left in each destination's parent that are not themselves
			// part of a destination: the pack's generated config, above all.
			generated := map[string]bool{}
			for _, d := range dests {
				parent := filepath.Dir(filepath.Join(home, filepath.FromSlash(d)))
				for _, f := range filesUnder(t, parent) {
					relHome, _ := filepath.Rel(home, filepath.Join(parent, f))
					if !underAny(relHome, dests) {
						generated[relHome] = true
					}
				}
			}

			// Plant agent state: beside every destination (a file and a directory in its
			// parent) and in every directory between the home and that parent.
			planted := map[string]bool{}
			for _, d := range dests {
				dst := filepath.Join(home, filepath.FromSlash(d))
				parent := filepath.Dir(dst)
				planted[filepath.Join(parent, "auth.json")] = true
				planted[filepath.Join(parent, "sessions", "2026-09-27.jsonl")] = true
				for dir := filepath.Dir(parent); dir != home && strings.HasPrefix(dir, home); dir = filepath.Dir(dir) {
					planted[filepath.Join(dir, "state-above.json")] = true
				}
			}
			for pl := range planted {
				writeTreeFile(t, pl, "agent state")
			}
			// A skill a previous launch delivered and the host no longer stages.
			for _, d := range skills {
				writeTreeFile(t, filepath.Join(home, filepath.FromSlash(d), "gone", "SKILL.md"), "stale")
			}

			// Boot 2: with the overlay.
			stderr := bootDarwinForPack(t, home, ws, packRoot, stageHostShapedOverlay(t, skills, briefings))

			for pl := range planted {
				if _, err := os.Stat(pl); err != nil {
					rel, _ := filepath.Rel(home, pl)
					t.Errorf("agent state ~/%s did not survive the overlay install: %v\n"+
						"The overlay replaces its destinations (%v) and nothing else — a bind "+
						"mount lands at its destination and leaves the parent alone.",
						rel, err, dests)
				}
			}
			// Lstat, not Stat: some of what the boot writes is a LINK (the shared-credential
			// and shared-directory hooks), and a link whose machine-tier target this temp
			// home never had still has to be there.
			for rel := range generated {
				if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
					t.Errorf("~/%s, which the boot itself generated, was deleted by the "+
						"overlay install: %v", rel, err)
				}
			}
			for _, d := range skills {
				dst := filepath.Join(home, filepath.FromSlash(d))
				if got, err := os.ReadFile(filepath.Join(dst, "delivered", "SKILL.md")); err != nil || string(got) != "delivered skill" {
					t.Errorf("the staged skill did not arrive at ~/%s: %q, %v\n%s", d, got, err, stderr)
				}
				if _, err := os.Stat(filepath.Join(dst, "gone")); !os.IsNotExist(err) {
					t.Errorf("a skill the host no longer stages survived at ~/%s/gone (err %v): "+
						"the destination is authoritative, exactly as a bind is", d, err)
				}
			}
			for _, d := range briefings {
				if got, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(d))); err != nil || string(got) != "delivered briefing" {
					t.Errorf("the staged briefing did not arrive at ~/%s: %q, %v\n%s", d, got, err, stderr)
				}
			}
			// And the pack's workspace-tier link is still a link: replacing it with a real
			// directory makes the next boot refuse (OQ-HT2).
			for _, dir := range packload.WritableDirs([]*packload.Pack{p}) {
				if fi, err := os.Lstat(filepath.Join(home, dir)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
					t.Errorf("~/%s is no longer the workspace-tier symlink (err %v)", dir, err)
				}
			}
		})
	}
	if covered == 0 {
		t.Fatal("no shipped pack declares a skills or briefing destination — the enumeration " +
			"read nothing, so this test would pass having checked nothing")
	}
}

// THE INHERITED USER SCOPE'S FILE on macos-user (OQ-LP9, run.macosUserInheritedScope): the host
// lays the generated ~/.config/yolo-jail/config.jsonc into the overlay as a destination of its
// own, and the install replaces that one file and nothing beside it — R8's property, which the
// container gets from a single-file bind: the directory around it stays the agent's, so a
// --user-layer file it wrote there and the conventional local pack (~/.config/yolo-jail/local)
// both survive the launch. It lands through the ~/.config layout link, in the workspace's sidecar.
func TestDarwinOverlayInstallsTheInheritedUserScopeBesideTheAgentsOwnFiles(t *testing.T) {
	packs, err := embeddedPackSet()
	if err != nil {
		t.Fatal(err)
	}
	var claude *packload.Pack
	for _, p := range packs {
		if p.Name == "claude" {
			claude = p
		}
	}
	if claude == nil {
		t.Fatal("no shipped claude pack")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	ws := filepath.Join(base, "workspace")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	packRoot := stageWholePackForBootstrap(t, claude)
	const dest = ".config/yolo-jail/config.jsonc"

	// Boot 1 lays the layout; then the agent writes its own files beside the destination, and a
	// previous launch's copy of the file is there to be replaced.
	bootDarwinForPack(t, home, ws, packRoot, "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	layer := filepath.Join(dir, "layer.jsonc")
	local := filepath.Join(dir, "local", "pack.json")
	writeTreeFile(t, layer, `{"packs": ["mine"]}`)
	writeTreeFile(t, local, `{"name": "local"}`)
	writeTreeFile(t, filepath.Join(home, filepath.FromSlash(dest)), "previous launch's user scope")

	// Boot 2 with the overlay carrying the file, as a file destination.
	stderr := bootDarwinForPack(t, home, ws, packRoot, stageHostShapedOverlay(t, nil, []string{dest}))

	if got, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(dest))); err != nil || string(got) != "delivered briefing" {
		t.Errorf("the inherited user scope did not arrive at ~/%s: %q, %v\n%s", dest, got, err, stderr)
	}
	sidecarCopy := filepath.Join(ws, ".yolo", "home", "config", "yolo-jail", "config.jsonc")
	if got, err := os.ReadFile(sidecarCopy); err != nil || string(got) != "delivered briefing" {
		t.Errorf("the file is not in the workspace's sidecar at %s (%q, %v): it did not land "+
			"through the ~/.config layout link", sidecarCopy, got, err)
	}
	for _, kept := range []string{layer, local} {
		if _, err := os.Stat(kept); err != nil {
			rel, _ := filepath.Rel(home, kept)
			t.Errorf("~/%s, the agent's own file beside the inherited user scope, did not survive "+
				"the install: %v", rel, err)
		}
	}
}
