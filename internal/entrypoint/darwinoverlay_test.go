package entrypoint

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// darwinoverlay_test.go pins the overlay install's own rules (darwinoverlay.go): the
// destination list is required and never copied, a crash or a failed copy loses no agent
// state, a link AT a destination is never followed (replaced past a layout link, refused in
// the account home), and a link ABOVE one that the layout did not lay is refused. The G36
// reproducer, which drives the whole boot for every shipped pack, is in
// darwinoverlaystate_test.go; the G14 link plants, through the whole boot, are in
// darwinoverlaylinks_test.go.
//
// Every test here goes through InstallHomeOverlay, the entry RunDarwinBootstrap calls, so
// none of them can pass with the installer's call site removed.

// overlayFixture is a sandbox home laid out the way the workspace-tier layout lays pi's:
// ~/.pi is a symlink into the workspace sidecar, and ~/.pi/agent is a REAL directory
// holding the agent's state — the shape that G36 wiped. install selects the pi pack, so
// ~/.pi is this launch's own layout link (overlayLinks) and the install may pass through it.
type overlayFixture struct {
	home, sidecar, overlay string
}

func newOverlayFixture(t *testing.T) overlayFixture {
	t.Helper()
	base := t.TempDir()
	f := overlayFixture{
		home:    filepath.Join(base, "home"),
		sidecar: filepath.Join(base, "workspace", ".yolo", "home"),
		overlay: filepath.Join(base, "overlay"),
	}
	for _, d := range []string{f.home, filepath.Join(f.sidecar, "pi", "agent"), f.overlay} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(f.sidecar, "pi"), filepath.Join(f.home, ".pi")); err != nil {
		t.Fatal(err)
	}
	return f
}

// stage lays one skills destination and one briefing destination into the overlay and
// lists both, as the host builder does.
func (f overlayFixture) stage(t *testing.T) {
	t.Helper()
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "skills", "demo", "SKILL.md"), "new skill")
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "AGENTS.md"), "new briefing")
	if _, err := WriteHomeOverlayManifest(f.overlay, []string{".pi/agent/skills", ".pi/agent/AGENTS.md"}); err != nil {
		t.Fatal(err)
	}
}

func (f overlayFixture) install(t *testing.T) error {
	t.Helper()
	e := DarwinEnvFrom(map[string]string{
		"HOME":                     f.home,
		"JAIL_HOME":                f.home,
		DarwinHomeSidecarEnv:       f.sidecar,
		"YOLO_DARWIN_HOME_OVERLAY": f.overlay,
	}, f.home)
	e.Stderr = &strings.Builder{}
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	return InstallHomeOverlay(e, []*packload.Pack{pi})
}

func (f overlayFixture) agentState(t *testing.T) string {
	t.Helper()
	p := filepath.Join(f.home, ".pi", "agent", "auth.json")
	writeTreeFile(t, p, "pi's sign-in")
	return p
}

func requireFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s = %q (err %v), want %q", path, got, err, want)
	}
}

func requireAbsent(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists (err %v): %s", path, err, why)
	}
}

// An overlay with no destination list is REFUSED, and nothing in the home moves: without
// the list the install can only guess where a destination starts, and the guess is what
// deleted pi's state.
func TestOverlayInstallRefusesAnOverlayWithNoDestinationList(t *testing.T) {
	f := newOverlayFixture(t)
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "skills", "demo", "SKILL.md"), "new skill")
	state := f.agentState(t)

	err := f.install(t)
	if err == nil || !strings.Contains(err.Error(), HomeOverlayManifestName) {
		t.Fatalf("InstallHomeOverlay = %v, want a refusal naming %s", err, HomeOverlayManifestName)
	}
	requireFile(t, state, "pi's sign-in")
	requireAbsent(t, filepath.Join(f.home, ".pi", "agent", "skills"), "an unlisted overlay was installed anyway")
}

// The list is read, never delivered, and content the list does not name is not delivered
// either: the install walks the list, not the tree.
func TestOverlayInstallDeliversOnlyListedDestinationsAndNotTheList(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "unlisted.json"), "not a destination")

	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "skills", "demo", "SKILL.md"), "new skill")
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "AGENTS.md"), "new briefing")
	requireAbsent(t, filepath.Join(f.home, HomeOverlayManifestName), "the destination list was copied into the home")
	requireAbsent(t, filepath.Join(f.home, ".pi", "agent", "unlisted.json"), "a path the list does not name was delivered")
}

// A second install REPLACES both kinds of destination — a skill the host stopped staging
// goes, a briefing is rewritten — and the state beside them stays.
func TestOverlayInstallReplacesThePreviousDeliveryAndKeepsTheStateBesideIt(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	state := f.agentState(t)
	writeTreeFile(t, filepath.Join(f.home, ".pi", "agent", "skills", "gone", "SKILL.md"), "stale")
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "AGENTS.md"), "second briefing")

	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	requireFile(t, state, "pi's sign-in")
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "AGENTS.md"), "second briefing")
	requireAbsent(t, filepath.Join(f.home, ".pi", "agent", "skills", "gone"), "a skill the host stopped staging lingered")
	for _, leftover := range []string{".skills" + overlayStagedSuffix, ".skills" + overlayAsideSuffix,
		".AGENTS.md" + overlayStagedSuffix, ".AGENTS.md" + overlayAsideSuffix} {
		requireAbsent(t, filepath.Join(f.home, ".pi", "agent", leftover), "an install left its working copy behind")
	}
}

// A CRASH between the two renames of a directory leaves the previous delivery under the
// aside name and no destination at all; a crash while copying leaves a half-built staged
// copy. The next install must clear both and deliver — and neither state ever involved the
// agent's own files.
func TestOverlayInstallRecoversFromACrashedInstall(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	state := f.agentState(t)
	agentDir := filepath.Join(f.home, ".pi", "agent")
	writeTreeFile(t, filepath.Join(agentDir, ".skills"+overlayAsideSuffix, "old", "SKILL.md"), "previous delivery")
	writeTreeFile(t, filepath.Join(agentDir, ".skills"+overlayStagedSuffix, "half", "SKILL.md"), "half copied")

	if err := f.install(t); err != nil {
		t.Fatalf("an install after a crash failed: %v", err)
	}
	requireFile(t, state, "pi's sign-in")
	requireFile(t, filepath.Join(agentDir, "skills", "demo", "SKILL.md"), "new skill")
	requireAbsent(t, filepath.Join(agentDir, "skills", "half"), "a crashed install's half copy was delivered")
	requireAbsent(t, filepath.Join(agentDir, ".skills"+overlayAsideSuffix), "a crashed install's aside copy lingered")
	requireAbsent(t, filepath.Join(agentDir, ".skills"+overlayStagedSuffix), "a crashed install's staged copy lingered")
}

// A copy that FAILS part-way leaves the previous delivery exactly as it was — never a
// partial one, never an empty destination — and removes its own half-built copy.
func TestOverlayInstallFailureKeepsThePreviousDeliveryWhole(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	state := f.agentState(t)
	// A FIFO in the staged tree: copyTreeStrict refuses what it cannot reproduce, which is
	// a copy failure no file permission can fake when the test runs as root.
	if err := syscall.Mkfifo(filepath.Join(f.overlay, ".pi", "agent", "skills", "demo", "pipe"), 0o644); err != nil {
		t.Skipf("cannot make a FIFO here: %v", err)
	}
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "skills", "demo", "SKILL.md"), "never delivered")

	if err := f.install(t); err == nil {
		t.Fatal("an install whose copy failed reported success")
	}
	agentDir := filepath.Join(f.home, ".pi", "agent")
	requireFile(t, state, "pi's sign-in")
	requireFile(t, filepath.Join(agentDir, "skills", "demo", "SKILL.md"), "new skill")
	requireAbsent(t, filepath.Join(agentDir, ".skills"+overlayStagedSuffix), "a failed install left its half-built copy")
}

// A symlink AT a destination PAST A LAYOUT LINK is replaced, never followed. Following it
// would replace whatever it names — here a directory of the agent's — and deliver where no
// content rule points. In the sidecar the layout lays no links, so one there is an occupant
// like any other (G14): the install moves the LINK aside and unlinks it, and what it named is
// left exactly as it was.
func TestOverlayInstallReplacesASymlinkAtADestinationAndNeverWhatItNames(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	precious := filepath.Join(f.sidecar, "pi", "sessions")
	writeTreeFile(t, filepath.Join(precious, "s.jsonl"), "a transcript")
	link := filepath.Join(f.home, ".pi", "agent", "skills")
	if err := os.Symlink(precious, link); err != nil {
		t.Fatal(err)
	}

	if err := f.install(t); err != nil {
		t.Fatalf("InstallHomeOverlay = %v, want the link at ~/.pi/agent/skills replaced", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("~/.pi/agent/skills is not a real directory after the install (err %v)", err)
	}
	requireFile(t, filepath.Join(link, "demo", "SKILL.md"), "new skill")
	requireFile(t, filepath.Join(precious, "s.jsonl"), "a transcript")
	requireAbsent(t, filepath.Join(precious, "demo"), "the delivery went through the link into what it named")
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "AGENTS.md"), "new briefing")
}

// A symlink AT a destination IN THE ACCOUNT HOME is refused, not followed and not replaced.
// Replacing it would put a real path where a layout link may belong — another launch's, for
// a pack this one does not select — which that launch's next boot refuses (OQ-HT2).
func TestOverlayInstallRefusesASymlinkAtADestinationInTheAccountHome(t *testing.T) {
	f := newOverlayFixture(t)
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "AGENTS.md"), "new briefing")
	writeTreeFile(t, filepath.Join(f.overlay, ".other", "AGENTS.md"), "not delivered")
	if _, err := WriteHomeOverlayManifest(f.overlay, []string{".pi/agent/AGENTS.md", ".other/AGENTS.md"}); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(f.home, "notes.md")
	writeTreeFile(t, precious, "the agent's own file")
	link := filepath.Join(f.home, ".other", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, link); err != nil {
		t.Fatal(err)
	}

	err := f.install(t)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") || !strings.Contains(err.Error(), link) {
		t.Fatalf("InstallHomeOverlay = %v, want a refusal naming the link at %s", err, link)
	}
	requireFile(t, precious, "the agent's own file")
	if got, err := os.Readlink(link); err != nil || got != precious {
		t.Errorf("the link at the destination was changed: %q, %v", got, err)
	}
	// The other destination is still delivered: one refusal does not cost the rest.
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "AGENTS.md"), "new briefing")
}

// A symlink ABOVE a destination, past the layout's own link, is refused: the layout lays no
// link in the sidecar, so one there was planted, and delivering through it lands where no
// content rule points — here, outside the jail's own directories altogether, which the install
// runs with the sandbox account's full reach to write. Nothing is written through it, and it
// is left in place for the reader to remove.
func TestOverlayInstallRefusesALinkAboveADestinationThatTheLayoutDidNotLay(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	outside := filepath.Join(t.TempDir(), "another-workspace", "pi-agent")
	writeTreeFile(t, filepath.Join(outside, "skills", "theirs", "SKILL.md"), "someone else's")
	agentDir := filepath.Join(f.sidecar, "pi", "agent")
	if err := os.RemoveAll(agentDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, agentDir); err != nil {
		t.Fatal(err)
	}

	err := f.install(t)
	if err == nil || !offers(t, err.Error(), "sudo", "rm", agentDir) {
		t.Fatalf("InstallHomeOverlay = %v, want a refusal naming the link at %s", err, agentDir)
	}
	requireFile(t, filepath.Join(outside, "skills", "theirs", "SKILL.md"), "someone else's")
	requireAbsent(t, filepath.Join(outside, "AGENTS.md"), "the briefing was written through the link")
	if got, err := os.Readlink(agentDir); err != nil || got != outside {
		t.Errorf("the refused link was changed: %q, %v", got, err)
	}
}

// The WORKSPACE SIDECAR itself, and the `.yolo` above it, may not be links. Both are in the
// workspace, which the sandbox account can write, and the layout's links name the sidecar by
// path, so a link at either would carry every destination under it — here, all of them — to
// wherever it points, and the containment check would accept that as "the sidecar". The
// launcher refuses a launch with such a link; one here was made after that check. Through the
// entry, G14's check (LinkedSidecarError) is the refusal that speaks; the install's own roots
// refuse the same link as well, pinned directly by TestOverlayInstallRootsRefuseALinkedSidecar.
func TestOverlayInstallRefusesASidecarThatIsALink(t *testing.T) {
	for _, at := range []string{"home", ".yolo"} {
		t.Run(at, func(t *testing.T) {
			f := newOverlayFixture(t)
			f.stage(t)
			linked := f.sidecar
			if at == ".yolo" {
				linked = filepath.Dir(f.sidecar)
			}
			elsewhere := filepath.Join(t.TempDir(), "another-workspace", filepath.Base(linked))
			if err := os.MkdirAll(filepath.Dir(elsewhere), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(linked, elsewhere); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, linked); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, elsewhere)

			err := f.install(t)
			if err == nil || !strings.Contains(err.Error(), "symbolic link") || !strings.Contains(err.Error(), linked) {
				t.Fatalf("InstallHomeOverlay = %v, want a refusal naming the link at %s", err, linked)
			}
			after := snapshotTree(t, elsewhere)
			if len(after) != len(before) {
				t.Errorf("the install wrote through the linked %s: %v, was %v", at, after, before)
			}
			for rel, was := range before {
				if after[rel] != was {
					t.Errorf("through the linked %s, %s was %q and is now %q", at, rel, was, after[rel])
				}
			}
		})
	}
}

// The install's roots are opened refusing a link at the sidecar or the `.yolo` above it
// (openSidecarRoot), the second line behind G14's check at InstallHomeOverlay, which always
// refuses such a link first and so hides this one from every test through the entry.
func TestOverlayInstallRootsRefuseALinkedSidecar(t *testing.T) {
	for _, at := range []string{"home", ".yolo"} {
		t.Run(at, func(t *testing.T) {
			f := newOverlayFixture(t)
			linked := f.sidecar
			if at == ".yolo" {
				linked = filepath.Dir(f.sidecar)
			}
			elsewhere := filepath.Join(t.TempDir(), filepath.Base(linked))
			if err := os.Rename(linked, elsewhere); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, linked); err != nil {
				t.Fatal(err)
			}
			roots, err := openOverlayInstallRoots(f.home, f.sidecar)
			if err == nil {
				closeOverlayRoots(roots)
				t.Fatalf("the install's roots opened through the link at %s", linked)
			}
		})
	}
}

// A LAYOUT PATH IS PASSED ONLY WHILE IT IS THE LINK THIS LAUNCH LAID, TO THE TARGET IT LAID (G14,
// HT-D6). Here ~/.pi is a link at the right path to the wrong target, inside this workspace's own
// sidecar — so containment would accept it, and only the target comparison keeps the delivery
// from landing at a sidecar path no content rule names.
func TestOverlayInstallPassesALayoutPathOnlyToTheTargetTheLayoutLaid(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	link := filepath.Join(f.home, ".pi")
	elsewhere := filepath.Join(f.sidecar, "elsewhere")
	if err := os.MkdirAll(filepath.Join(elsewhere, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}

	err := f.install(t)
	if err == nil || !strings.Contains(err.Error(), "not the layout's link to "+filepath.Join(f.sidecar, "pi")) {
		t.Fatalf("InstallHomeOverlay = %v, want a refusal naming the target the layout laid", err)
	}
	requireAbsent(t, filepath.Join(elsewhere, "agent", "skills"), "the skills were delivered through a link to the wrong target")
	requireAbsent(t, filepath.Join(elsewhere, "agent", "AGENTS.md"), "the briefing was delivered through a link to the wrong target")
}

// CONTAINMENT, beyond the layout check. The route check (overlayLinks.route) refuses a link the
// layout did not lay, by path, before the directory is resolved — so a link that is already
// there never reaches the containment check. One swapped in just after the route passed does:
// the directory is resolved once, and a directory that resolves outside the sandbox home and
// the workspace sidecar is refused rather than opened, so nothing is written there.
func TestOverlayInstallRefusesAParentThatResolvesOutOfTheJail(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	outside := filepath.Join(t.TempDir(), "another-workspace", "pi-agent")
	writeTreeFile(t, filepath.Join(outside, "skills", "theirs", "SKILL.md"), "someone else's")
	agentDir := filepath.Join(f.sidecar, "pi", "agent")
	swapped := false
	withOverlayInstallHook(t, func(dest, window string) {
		if swapped || window != "routed" {
			return
		}
		swapped = true
		if err := os.RemoveAll(agentDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, agentDir); err != nil {
			t.Fatal(err)
		}
	})

	err := f.install(t)
	if !swapped {
		t.Fatal("the install never reached the routed window")
	}
	if err == nil || !strings.Contains(err.Error(), "outside the sandbox home") {
		t.Fatalf("InstallHomeOverlay = %v, want a refusal naming the escape", err)
	}
	requireFile(t, filepath.Join(outside, "skills", "theirs", "SKILL.md"), "someone else's")
	requireAbsent(t, filepath.Join(outside, "AGENTS.md"), "the briefing was written through the escaping link")
}

// The destination list refuses what is not a path strictly below the home, on both sides:
// the host may not write one, and a list naming one is refused before anything moves.
func TestHomeOverlayDestinationsMustLieBelowTheHome(t *testing.T) {
	for _, bad := range []string{"", ".", "/etc/passwd", "../escape", ".pi/../../escape",
		HomeOverlayManifestName, HomeOverlayManifestName + "/x"} {
		if _, err := WriteHomeOverlayManifest(t.TempDir(), []string{bad}); err == nil {
			t.Errorf("WriteHomeOverlayManifest accepted %q", bad)
		}
	}

	// The overlay one level deeper than the home, so `../escape` names a real SOURCE beside
	// it and a distinct DESTINATION beside the home: without the check the copy would land.
	f := newOverlayFixture(t)
	f.overlay = filepath.Join(t.TempDir(), "staged", "overlay")
	writeTreeFile(t, filepath.Join(f.overlay, HomeOverlayManifestName), `{"destinations":["../escape"]}`)
	writeTreeFile(t, filepath.Join(filepath.Dir(f.overlay), "escape"), "outside the overlay")
	if err := f.install(t); err == nil {
		t.Fatal("a list naming a path above the home was installed")
	}
	requireAbsent(t, filepath.Join(filepath.Dir(f.home), "escape"), "a destination above the home was written")
}

// A destination INSIDE another is dropped from the list as read: the outer destination's
// tree already carries it, and installing both would replace the outer one's copy twice.
// That holds however the list sorts: `-` and `.` sort before `/`, so a SIBLING of the outer
// destination (`.a/skills-extra`) can sit between it and a destination inside it.
func TestHomeOverlayNestedDestinationsInstallOnce(t *testing.T) {
	for _, c := range []struct {
		name  string
		dests []string
		want  string
	}{
		{"adjacent", []string{".x/skills/README.md", ".x/skills", ".x/skills", ".y/AGENTS.md"}, ".x/skills,.y/AGENTS.md"},
		{"a sibling sorts between", []string{".a/skills", ".a/skills-extra", ".a/skills/sub"}, ".a/skills,.a/skills-extra"},
		{"two siblings and a deeper one", []string{".a/skills", ".a/skills.d", ".a/skills-x", ".a/skills/sub/deeper"}, ".a/skills,.a/skills-x,.a/skills.d"},
	} {
		t.Run(c.name, func(t *testing.T) {
			overlay := t.TempDir()
			wrote, err := WriteHomeOverlayManifest(overlay, c.dests)
			if err != nil {
				t.Fatal(err)
			}
			got, err := readHomeOverlayManifest(overlay)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != c.want {
				t.Errorf("destinations = %v, want %s", got, c.want)
			}
			// The list the host returns for the profile is the one the install reads (HT-D7).
			if strings.Join(wrote, ",") != c.want {
				t.Errorf("WriteHomeOverlayManifest returned %v, want %s", wrote, c.want)
			}
		})
	}
}

// The same rule seen from the install RunDarwinBootstrap runs: a destination inside another
// is not installed on its own, even with a sibling sorting between the two.
func TestOverlayInstallDoesNotInstallANestedDestinationTwice(t *testing.T) {
	f := newOverlayFixture(t)
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "skills", "sub", "SKILL.md"), "nested skill")
	writeTreeFile(t, filepath.Join(f.overlay, ".pi", "agent", "skills-extra", "x", "SKILL.md"), "sibling skill")
	// Written by hand, as a list the host's writer never produces (it drops the nested one
	// itself), so this pins the READ side's drop.
	writeTreeFile(t, filepath.Join(f.overlay, HomeOverlayManifestName),
		`{"destinations":[".pi/agent/skills",".pi/agent/skills-extra",".pi/agent/skills/sub"]}`)
	var installed []string
	withOverlayInstallHook(t, func(dest, window string) {
		if window == "opened" {
			installed = append(installed, dest)
		}
	})

	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	if strings.Join(installed, ",") != ".pi/agent/skills,.pi/agent/skills-extra" {
		t.Errorf("installed %v, want [.pi/agent/skills .pi/agent/skills-extra]: a destination "+
			"inside another was installed on its own", installed)
	}
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "skills", "sub", "SKILL.md"), "nested skill")
	requireFile(t, filepath.Join(f.home, ".pi", "agent", "skills-extra", "x", "SKILL.md"), "sibling skill")
}

// withOverlayInstallHook sets overlayInstallHook for one test.
func withOverlayInstallHook(t *testing.T, hook func(dest, window string)) {
	t.Helper()
	overlayInstallHook = hook
	t.Cleanup(func() { overlayInstallHook = nil })
}

// snapshotTree records every entry below dir, links as links, with each regular file's
// content and each link's target, so a test can say that NOTHING there changed.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "link to " + target
		case d.IsDir():
			out[rel] = "dir"
		default:
			b, _ := os.ReadFile(p)
			out[rel] = "file " + string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// THE CONTAINMENT RULE HOLDS AGAINST A CONCURRENT SESSION, not only against a link that was
// there before the install began. The launch lock is released before the agent runs, and the
// account home is shared by every workspace, so an agent can re-point a directory while
// another launch installs into it. Here the "agent" moves the destination's directory aside
// and puts a link to another workspace in its place at each moment that matters: just after
// the layout check passed its path and before the directory is resolved, just after the
// install checked that directory and before it opened it, just after it opened it, and just
// before it swaps the new copy in. The other workspace also holds a copy under the
// install's own working-copy name, which an install that renames by path would move into
// place.
//
// Nothing outside the home and the sidecar may change, and the agent state in the directory
// that was moved must survive.
func TestOverlayInstallStaysInsideTheJailWhenADirectoryIsSwappedMidInstall(t *testing.T) {
	for _, window := range []string{"routed", "checked", "opened", "staged"} {
		for _, swapAt := range []string{".pi/agent/skills", ".pi/agent/AGENTS.md"} {
			t.Run(window+" "+swapAt, func(t *testing.T) {
				f := newOverlayFixture(t)
				f.stage(t)
				// A previous launch already delivered, as on every launch but the first.
				if err := f.install(t); err != nil {
					t.Fatal(err)
				}
				state := f.agentState(t)
				outside := filepath.Join(t.TempDir(), "another-workspace", "pi-agent")
				writeTreeFile(t, filepath.Join(outside, "skills", "theirs", "SKILL.md"), "someone else's")
				writeTreeFile(t, filepath.Join(outside, "AGENTS.md"), "their briefing")
				writeTreeFile(t, filepath.Join(outside, ".skills"+overlayStagedSuffix, "planted", "SKILL.md"), "planted")
				writeTreeFile(t, filepath.Join(outside, ".AGENTS.md"+overlayStagedSuffix), "planted briefing")
				before := snapshotTree(t, outside)

				agentDir := filepath.Join(f.sidecar, "pi", "agent")
				moved := agentDir + ".moved"
				swapped := false
				withOverlayInstallHook(t, func(dest, w string) {
					if swapped || dest != swapAt || w != window {
						return
					}
					swapped = true
					if err := os.Rename(agentDir, moved); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, agentDir); err != nil {
						t.Fatal(err)
					}
				})

				// The install's own result is not the point: a destination whose directory is
				// swapped before it is opened is refused, and one opened before the swap
				// finishes in the directory the agent moved.
				_ = f.install(t)
				if !swapped {
					t.Fatalf("the install never reached the %q window for %s", window, swapAt)
				}
				after := snapshotTree(t, outside)
				for rel, was := range before {
					if after[rel] != was {
						t.Errorf("outside the jail, %s was %q and is now %q", rel, was, after[rel])
					}
				}
				for rel, is := range after {
					if _, ok := before[rel]; !ok {
						t.Errorf("outside the jail, the install created %s (%q)", rel, is)
					}
				}
				rel, _ := filepath.Rel(filepath.Join(f.home, ".pi", "agent"), state)
				requireFile(t, filepath.Join(moved, rel), "pi's sign-in")
			})
		}
	}
}
