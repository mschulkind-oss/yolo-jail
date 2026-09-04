package capture

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// surfaceroot_test.go covers the two things slice 3 added to the driver: the SECOND PATH to
// the capture surfaces (Options.SurfaceRoot) and the exclusion of yolo's own state dir.
//
// # Modelling two paths to one directory without a bind mount
//
// Under podman each surface is its own bind, and the same directory is reachable both at
// `$HOME/.local` and at `/workspace/.yolo/home/local` — only the second sharing a mount with
// the scratch dir. A unit test cannot make a bind mount, but it does not need to: what the
// driver sees is one directory with two paths, and a SYMLINK on the HOME side produces
// exactly that. The real directories live under a "state" root (the workspace's
// `.yolo/home`), and `$HOME/.local` &co. are symlinks into it — which is also why the fixture
// installer, writing through `$HOME`, lands its bytes in the state root without knowing.
//
// The symlinks are on the home side deliberately. With a SurfaceRoot the driver never
// touches `$HOME/<HomeRel>` at all, so a symlink there is invisible to it and visible to the
// installer — and that asymmetry is what makes the test fail if SurfaceRoot stops being
// honoured: reached through HOME, `.local` lstats as a SYMLINK, WalkDir records one entry and
// descends into nothing, and the delta comes out empty.

// twoPathHome builds the fixture: real surface dirs under a state root, plus a HOME whose
// surface entries are symlinks into it. Returns (home, stateRoot).
func twoPathHome(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	state := filepath.Join(base, "state")
	must(t, os.MkdirAll(home, 0o755))
	must(t, os.MkdirAll(state, 0o755))
	for _, s := range paths.HomeSurfaces() {
		real := filepath.Join(state, filepath.FromSlash(s.Subtree))
		must(t, os.MkdirAll(real, 0o755))
		link := filepath.Join(home, filepath.FromSlash(s.HomeRel))
		must(t, os.MkdirAll(filepath.Dir(link), 0o755))
		must(t, os.Symlink(real, link))
	}
	return home, state
}

// twoPathInstaller writes the same shapes the slice-2 fixture does — a new version dir, an
// absolute self-referencing symlink, a file in each surface — plus a write into yolo's OWN
// state dir, and records every inode it created on STDOUT.
//
// The inode record is the external oracle for "the move was a rename". It is the installer's
// own observation of a number neither the driver nor the assertions control, so an inode the
// tree still has cannot have come from a copy.
const twoPathInstaller = `#!/bin/sh
set -eu
record() { printf 'INODE %s %s\n' "$1" "$(ls -di "$HOME/$1" | awk '{print $1}')"; }

mkdir -p "$HOME/.local/share/vendor/1.2.3"
printf 'the vendor binary\n' > "$HOME/.local/share/vendor/1.2.3/vendor"
chmod 755 "$HOME/.local/share/vendor/1.2.3/vendor"
mkdir -p "$HOME/.local/bin"
ln -s "$HOME/.local/share/vendor/1.2.3/vendor" "$HOME/.local/bin/vendor"
mkdir -p "$HOME/.npm-global/lib"
printf 'npm side\n' > "$HOME/.npm-global/lib/marker"
mkdir -p "$HOME/go/bin"
printf 'go side\n' > "$HOME/go/bin/marker"

# YOLO'S OWN STATE, inside the .local surface. Written AFTER the baseline walk, exactly as a
# late boot step or a launcher's receipt append would be.
mkdir -p "$HOME/.local/share/yolo-jail/captures/entries"
printf 'yolo state\n' > "$HOME/.local/share/yolo-jail/marker"

record .local/share/vendor
record .local/share/vendor/1.2.3/vendor
record .local/bin/vendor
record .npm-global/lib/marker
record go/bin
`

// stdoutInodes parses the fixture's `INODE <rel> <ino>` lines out of captured stdout.
func stdoutInodes(t *testing.T, s string) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || f[0] != "INODE" {
			continue
		}
		n, err := strconv.ParseUint(f[2], 10, 64)
		must(t, err)
		out[f[1]] = n
	}
	if len(out) == 0 {
		t.Fatalf("the fixture recorded no inodes; stdout was %q", s)
	}
	return out
}

// A capture reached through a SECOND PATH to the surfaces moves the delta by RENAME and
// still reports it home-relative.
//
// This is the podman case in miniature, and the assertion that matters is the inode one: the
// scratch dir is on the same mount as the state root and on a different one from nothing —
// what a SurfaceRoot buys is that the driver uses the path where that is true.
//
// It fails if the call site is deleted. Drop SurfaceRoot from surfacePath/abs and the driver
// walks `$HOME/.local`, which here is a symlink: WalkDir does not descend a symlinked root,
// the baseline records one unchanged symlink, and the captured tree comes out EMPTY.
func TestSurfaceRootIsTheDoorTheDriverUses(t *testing.T) {
	home, state := twoPathHome(t)
	out := filepath.Join(t.TempDir(), "staging-1")
	var stdout, stderr strings.Builder

	res, err := Run(Options{
		Home:        home,
		Out:         out,
		SurfaceRoot: state,
		Command:     writeInstaller(t, twoPathInstaller),
		Stdout:      &stdout,
		Stderr:      &stderr,
	})
	must(t, err)

	want := []string{
		".local", ".local/bin", ".local/bin/vendor",
		".local/share", ".local/share/vendor", ".local/share/vendor/1.2.3",
		".local/share/vendor/1.2.3/vendor",
		".npm-global", ".npm-global/lib", ".npm-global/lib/marker",
		"go", "go/bin", "go/bin/marker",
	}
	if got := entryPaths(res.Manifest); !equalStrings(got, want) {
		t.Errorf("captured tree =\n  %s\nwant\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	// HOME-RELATIVE, and the HOME is the real one — the door the driver used is an
	// implementation detail of the move and must not reach the manifest, because a tree is
	// materialized into a home and slice 6 rewrites absolute references against that home.
	if res.Manifest.Home != home {
		t.Errorf("Manifest.Home = %q, want the capture HOME %q, not the surface root", res.Manifest.Home, home)
	}
	if got, want := res.Manifest.Surfaces, []string{".npm-global", ".local", "go"}; !equalStrings(got, want) {
		t.Errorf("Surfaces = %v, want the home-relative spellings %v", got, want)
	}
	wantRef := AbsoluteRef{
		Path: ".local/bin/vendor", Kind: RefSymlinkTarget,
		Value: filepath.Join(home, ".local/share/vendor/1.2.3/vendor"),
	}
	if len(res.Manifest.AbsoluteRefs) != 1 || res.Manifest.AbsoluteRefs[0] != wantRef {
		t.Errorf("AbsoluteRefs = %+v, want %+v", res.Manifest.AbsoluteRefs, wantRef)
	}
	// THE MOVE WAS A RENAME — measured against the installer's own record.
	for rel, ino := range stdoutInodes(t, stdout.String()) {
		if got := inodeOf(t, filepath.Join(res.Tree, filepath.FromSlash(rel))); got != ino {
			t.Errorf("%s: inode %d in the tree, %d when the installer made it — COPIED", rel, got, ino)
		}
	}
	if res.Copied != 0 {
		t.Errorf("Copied = %d, want 0: the scratch dir and the surface root are one mount", res.Copied)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected driver output: %q", stderr.String())
	}
	// The delta really did leave the home, through the other door.
	if _, err := os.Lstat(filepath.Join(state, "local", "share", "vendor")); !os.IsNotExist(err) {
		t.Errorf("the moved delta is still in the surface: %v", err)
	}
}

// YOLO'S OWN STATE DIR IS NEVER PART OF A CAPTURE, even though `.local` is a capture surface
// and yolo writes into `.local/share/yolo-jail` between the baseline and the end of the
// install (a launcher's receipt append is the obvious case).
//
// It fails if the call site is deleted: remove the `d.excluded(rel)` guard from visit and
// `.local/share/yolo-jail/marker` appears in the tree, since the baseline walk skipped it and
// every path under it therefore reads as new.
func TestYoloStateIsExcludedFromTheDelta(t *testing.T) {
	home, state := twoPathHome(t)
	out := filepath.Join(t.TempDir(), "staging-1")

	res, err := Run(Options{
		Home: home, Out: out, SurfaceRoot: state,
		Command: writeInstaller(t, twoPathInstaller),
	})
	must(t, err)

	for _, rel := range []string{
		".local/share/yolo-jail",
		".local/share/yolo-jail/marker",
		".local/share/yolo-jail/captures/entries",
	} {
		if _, err := os.Lstat(filepath.Join(res.Tree, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s was captured — yolo's own state must never reach a capture entry "+
				"and from there every workspace on the machine", rel)
		}
	}
	// Excluded, not consumed: the state stays where yolo put it.
	if _, err := os.Lstat(filepath.Join(state, "local", "share", "yolo-jail", "marker")); err != nil {
		t.Errorf("the exclusion MOVED yolo's state instead of leaving it alone: %v", err)
	}
	// And the manifest says so, rather than leaving a reader to wonder why a whole subtree
	// of a walked surface is absent.
	if got, want := res.Manifest.Excluded, DefaultExcludes(); !equalStrings(got, want) {
		t.Errorf("Manifest.Excluded = %v, want %v", got, want)
	}
	if got, want := DefaultExcludes(), []string{paths.GlobalStorageRel()}; !equalStrings(got, want) {
		t.Errorf("DefaultExcludes() = %v, want the state dir %v — a second spelling of it "+
			"is an exclusion that stops matching when the layout moves", got, want)
	}
}

// The NEGATIVE CONTROL for the test above: with the exclusion turned off, the same fixture
// puts yolo's state in the tree.
//
// Without this, "no yolo-jail dir in the tree" would also pass for a driver that captured
// nothing at all, or for a fixture that never wrote there.
func TestAnEmptyExcludeListCapturesEverythingTheSurfacesHold(t *testing.T) {
	home, state := twoPathHome(t)
	out := filepath.Join(t.TempDir(), "staging-1")

	res, err := Run(Options{
		Home: home, Out: out, SurfaceRoot: state,
		Excludes: []string{},
		Command:  writeInstaller(t, twoPathInstaller),
	})
	must(t, err)

	if _, err := os.Lstat(filepath.Join(res.Tree, ".local", "share", "yolo-jail", "marker")); err != nil {
		t.Errorf("with no exclusions the state dir must be captured like anything else, "+
			"or the test above is asserting nothing: %v", err)
	}
	if len(res.Manifest.Excluded) != 0 {
		t.Errorf("Excluded = %v, want empty", res.Manifest.Excluded)
	}
}

// The platform is the DRIVER's, recorded in the manifest, because it is the only place it
// can be observed: a capture made from a Mac through podman runs in a Linux jail, and a
// host-side answer would be wrong in exactly the way that looks right.
func TestManifestRecordsTheCapturePlatform(t *testing.T) {
	home, state := twoPathHome(t)
	out := filepath.Join(t.TempDir(), "staging-1")

	res, err := Run(Options{
		Home: home, Out: out, SurfaceRoot: state,
		Command: writeInstaller(t, "#!/bin/sh\nset -eu\nmkdir -p \"$HOME/go/bin\"\nprintf x > \"$HOME/go/bin/t\"\n"),
	})
	must(t, err)

	if res.Manifest.Platform != Platform() {
		t.Errorf("Manifest.Platform = %q, want %q", res.Manifest.Platform, Platform())
	}
	if !strings.Contains(Platform(), "/") {
		t.Errorf("Platform() = %q, want <GOOS>/<GOARCH>", Platform())
	}
}

// An out dir inside the surface reached through the SURFACE ROOT is refused too. The same
// directory has two paths here, and a self-capture through the second one is just as
// unbounded as through the first — it merely would not have looked it.
func TestRunRefusesAnOutDirInsideTheSurfaceRootView(t *testing.T) {
	home, state := twoPathHome(t)
	_, err := Run(Options{
		Home:        home,
		SurfaceRoot: state,
		Out:         filepath.Join(state, "local", "scratch"),
		Command:     writeInstaller(t, "#!/bin/sh\ntrue\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "capture itself") {
		t.Fatalf("want a refusal naming self-capture, got %v", err)
	}
}
