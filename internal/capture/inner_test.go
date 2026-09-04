package capture

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// installerScript is the fixture "installer": a shell script standing in for a vendor's
// opaque payload, doing the five things that make a capture non-trivial.
//
// It writes a NESTED NEW DIRECTORY (the version dir every installer of this class keeps), an
// EXECUTABLE inside it, an ABSOLUTE SYMLINK into the staging prefix (claude's
// ~/.local/bin/claude is one — measured in this jail), a RELATIVE symlink beside it that must
// NOT be reported as an absolute reference, a file in each of the other two surfaces, an
// OVERWRITE of a file the baseline already had, and two writes OUTSIDE every capture surface.
//
// It also records the INODE of everything it created, into a file outside the surfaces. That
// is the test's external oracle for "the move was a rename": the driver's own Renamed counter
// is the callee reporting on itself, while an inode the installer observed and the tree still
// has is a measurement neither half of the code controls. `ls -i` rather than `stat` because
// GNU and BSD stat spell it differently and this script is the portable half.
const installerScript = `#!/bin/sh
set -eu
inodes="$HOME/inodes.txt"
: > "$inodes"
record() { printf '%s %s\n' "$1" "$(ls -di "$HOME/$1" | awk '{print $1}')" >> "$inodes"; }

mkdir -p "$HOME/.local/share/vendor/1.2.3"
printf 'the vendor binary\n' > "$HOME/.local/share/vendor/1.2.3/vendor"
chmod 755 "$HOME/.local/share/vendor/1.2.3/vendor"
printf 'docs\n' > "$HOME/.local/share/vendor/1.2.3/README"
mkdir -p "$HOME/.local/bin"
ln -s "$HOME/.local/share/vendor/1.2.3/vendor" "$HOME/.local/bin/vendor"
ln -s ../share/vendor/1.2.3/README "$HOME/.local/bin/vendor-readme"

printf 'npm side\n' > "$HOME/.npm-global/lib/marker"
mkdir -p "$HOME/go/bin"
printf 'go side\n' > "$HOME/go/bin/marker"

# An overwrite of something the baseline already had: a delta is not only new paths.
printf 'rewritten by the installer\n' > "$HOME/.local/share/keep/thing"

# Outside every capture surface. Neither may be captured.
mkdir -p "$HOME/.config/vendor"
printf 'settings\n' > "$HOME/.config/vendor/settings"
printf 'stray\n' > "$HOME/stray"

record .local/share/vendor
record .local/share/vendor/1.2.3/vendor
record .local/share/vendor/1.2.3/README
record .local/bin/vendor
record .npm-global/lib/marker
record go/bin
record go/bin/marker
record .local/share/keep/thing
`

// wantEntries is the whole captured tree the fixture must produce: the six delta roots plus
// exactly the ancestor directories needed to hold them, and nothing else.
//
// Written out in full rather than computed, because "exactly this and nothing the baseline
// had" is the property under test and a computed expectation would compute it the same way
// the code does.
var wantEntries = []string{
	".local",
	".local/bin",
	".local/bin/vendor",
	".local/bin/vendor-readme",
	".local/share",
	".local/share/keep",
	".local/share/keep/thing",
	".local/share/vendor",
	".local/share/vendor/1.2.3",
	".local/share/vendor/1.2.3/README",
	".local/share/vendor/1.2.3/vendor",
	".npm-global",
	".npm-global/lib",
	".npm-global/lib/marker",
	"go",
	"go/bin",
	"go/bin/marker",
}

// fixtureHome builds a home that already LOOKS BOOTED — every capture surface populated, the
// way the entrypoint leaves them before an installer ever runs. This is the whole reason the
// driver exists: a host-side before/after diff cannot separate these bytes from the vendor's.
func fixtureHome(t *testing.T, home string) {
	t.Helper()
	write := func(rel, content string) {
		must(t, os.MkdirAll(filepath.Join(home, filepath.Dir(rel)), 0o755))
		must(t, os.WriteFile(filepath.Join(home, rel), []byte(content), 0o644))
	}
	write(".local/bin/preexisting", "from the boot\n")
	write(".local/share/keep/thing", "from the boot\n")
	write(".npm-global/lib/node_modules/pkg/index.js", "bootstrap npm package\n")
	write("go/pkg/mod/cache/thing", "a module cache entry\n")
	write(".config/preexisting", "generated config\n")
	must(t, os.MkdirAll(filepath.Join(home, ".npm-global", "lib"), 0o755))
}

// writeInstaller drops the fixture installer somewhere OUTSIDE the capture surfaces (its own
// bytes must not become part of the delta) and returns the argv that runs it.
func writeInstaller(t *testing.T, script string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "install.sh")
	must(t, os.WriteFile(path, []byte(script), 0o755))
	return []string{"/bin/sh", path}
}

// entryPaths is the manifest's paths, in manifest order (which is sorted).
func entryPaths(m *Manifest) []string {
	out := make([]string, 0, len(m.Entries))
	for _, e := range m.Entries {
		out = append(out, e.Path)
	}
	return out
}

func find(m *Manifest, path string) (ManifestEntry, bool) {
	for _, e := range m.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return ManifestEntry{}, false
}

// readInodes parses the fixture's `<rel> <inode>` record.
func readInodes(t *testing.T, home string) map[string]uint64 {
	t.Helper()
	f, err := os.Open(filepath.Join(home, "inodes.txt"))
	must(t, err)
	defer f.Close()
	out := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) != 2 {
			t.Fatalf("bad inode record %q", sc.Text())
		}
		n, perr := strconv.ParseUint(parts[1], 10, 64)
		must(t, perr)
		out[parts[0]] = n
	}
	must(t, sc.Err())
	return out
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	must(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	return st.Ino
}

// One capture, end to end: the delta is exactly what the installer wrote, the surfaces are
// exactly prune's dedupe set, the absolute self-reference is recorded, and the bytes MOVED
// rather than being copied.
//
// This is also the test that pins the SURFACE SET's call site. Delete the
// `d.surfaces = paths.HomeSurfaces()` default and the driver walks nothing, so the delta is
// empty and this goes red — the surface set is not a constant the code merely agrees with,
// it is the thing that decides what a capture is.
func TestRunCapturesTheInstallersDeltaAndNothingElse(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(t.TempDir(), "staging-1")
	fixtureHome(t, home)

	var stderr bytes.Buffer
	res, err := Run(Options{
		Home:    home,
		Out:     out,
		Command: writeInstaller(t, installerScript),
		Stderr:  &stderr,
	})
	must(t, err)

	// 1. The delta is exactly the installer's output plus its ancestor dirs.
	if got := entryPaths(res.Manifest); !equalStrings(got, wantEntries) {
		t.Errorf("captured tree =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(wantEntries, "\n  "))
	}

	// 2. Nothing the baseline already had came along, in either direction: the boot's own
	//    files stay in the home and stay out of the tree.
	for _, rel := range []string{".local/bin/preexisting", ".npm-global/lib/node_modules/pkg/index.js", "go/pkg/mod/cache/thing"} {
		if _, err := os.Lstat(filepath.Join(res.Tree, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s was captured; it is the BOOT's, not the installer's", rel)
		}
		if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s left the home: %v", rel, err)
		}
	}

	// 3. Writes outside every capture surface are neither captured nor disturbed.
	for _, rel := range []string{".config/vendor/settings", "stray"} {
		if _, err := os.Lstat(filepath.Join(res.Tree, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s is outside every capture surface and must not be captured", rel)
		}
		if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s should still be in the home: %v", rel, err)
		}
	}

	// 4. The surfaces walked are the three prune dedupes, home-relative and in order.
	if got, want := res.Manifest.Surfaces, []string{".npm-global", ".local", "go"}; !equalStrings(got, want) {
		t.Errorf("Surfaces = %v, want %v", got, want)
	}

	// 5. The delta is not only NEW paths: a baseline file the installer rewrote is in it,
	//    with the installer's content.
	if got, err := os.ReadFile(filepath.Join(res.Tree, ".local", "share", "keep", "thing")); err != nil ||
		string(got) != "rewritten by the installer\n" {
		t.Errorf("the overwritten baseline file = %q, %v", got, err)
	}

	// 6. Kinds, modes and symlink targets survive.
	if e, ok := find(res.Manifest, ".local/share/vendor/1.2.3/vendor"); !ok || e.Kind != KindFile || e.Mode != "0755" {
		t.Errorf("the installed binary = %+v (want a 0755 file)", e)
	}
	if e, ok := find(res.Manifest, ".local/bin/vendor"); !ok || e.Kind != KindSymlink ||
		e.Target != filepath.Join(home, ".local/share/vendor/1.2.3/vendor") {
		t.Errorf("the absolute symlink = %+v", e)
	}
	if e, ok := find(res.Manifest, ".local/bin/vendor-readme"); !ok || e.Target != "../share/vendor/1.2.3/README" {
		t.Errorf("the relative symlink = %+v", e)
	}

	// 7. The absolute reference into the staging prefix is recorded, and ONLY it — the
	//    relative link beside it is already relocatable and reporting it would hand slice
	//    6 a rewrite it must not make.
	want := []AbsoluteRef{{
		Path: ".local/bin/vendor", Kind: RefSymlinkTarget,
		Value: filepath.Join(home, ".local/share/vendor/1.2.3/vendor"),
	}}
	if len(res.Manifest.AbsoluteRefs) != 1 || res.Manifest.AbsoluteRefs[0] != want[0] {
		t.Errorf("AbsoluteRefs = %+v, want %+v", res.Manifest.AbsoluteRefs, want)
	}
	if res.Manifest.Home != home {
		t.Errorf("Manifest.Home = %q, want the capture home %q", res.Manifest.Home, home)
	}

	// 8. THE MOVE IS A RENAME. Every inode the installer observed is the inode the tree
	//    now has — a copy would have produced new ones. Measured against the installer's
	//    own record, not against the driver's counter.
	for rel, ino := range readInodes(t, home) {
		if got := inodeOf(t, filepath.Join(res.Tree, filepath.FromSlash(rel))); got != ino {
			t.Errorf("%s: inode %d in the tree, %d when the installer made it — it was COPIED", rel, got, ino)
		}
	}
	if res.Copied != 0 {
		t.Errorf("Copied = %d, want 0 on one filesystem", res.Copied)
	}

	// 9. A WHOLE new directory moves in ONE rename rather than one per file. Six delta
	//    roots: two symlinks, the rewritten file, the npm marker, and the two new
	//    directories (.local/share/vendor and go/bin) taken whole. Descending into those
	//    two instead would make it seven.
	if res.Renamed != 6 {
		t.Errorf("Renamed = %d, want 6 delta roots (a new directory moves whole)", res.Renamed)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected driver output: %q", stderr.String())
	}
}

// The manifest lands BESIDE the tree, never inside it, and is what Store.Admit consumes plus
// what it does not: TreeDir(out) is the admit argument and the manifest is its sibling.
func TestRunWritesTheManifestBesideTheTreeAndAdmitTakesIt(t *testing.T) {
	home := t.TempDir()
	store := &Store{Dir: t.TempDir()}
	out, err := store.Stage("run-1")
	must(t, err)
	fixtureHome(t, home)

	res, err := Run(Options{Home: home, Out: out, Command: writeInstaller(t, installerScript)})
	must(t, err)

	if res.Tree != filepath.Join(out, "tree") {
		t.Errorf("Tree = %q, want %s/tree", res.Tree, out)
	}
	if _, err := os.Stat(filepath.Join(res.Tree, ManifestName)); !os.IsNotExist(err) {
		t.Errorf("the manifest is inside the tree materialize hardlinks wholesale: %v", err)
	}
	onDisk, err := ReadManifest(out)
	must(t, err)
	if !equalStrings(entryPaths(onDisk), entryPaths(res.Manifest)) {
		t.Errorf("the manifest on disk disagrees with the one returned")
	}
	// The proto-entry is admit-shaped: the driver's output is the store's input.
	entry, err := store.Admit(res.Tree)
	must(t, err)
	if _, err := os.Stat(filepath.Join(entry.Tree, ".local", "share", "vendor", "1.2.3", "vendor")); err != nil {
		t.Errorf("the admitted entry is missing the captured binary: %v", err)
	}
}

// crossMountOut returns a scratch dir on a DIFFERENT MOUNT from home, or skips.
//
// /dev/shm is the portable-enough second mount on Linux; the probe rename is what actually
// decides, because "a different path" is not "a different mount" and the whole point of this
// test is the case where the kernel says EXDEV.
func crossMountOut(t *testing.T, home string) string {
	t.Helper()
	base, err := os.MkdirTemp("/dev/shm", "yolo-capture-")
	if err != nil {
		t.Skipf("no second mount to test the cross-mount path: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	probe := filepath.Join(home, ".exdev-probe")
	must(t, os.WriteFile(probe, []byte("x"), 0o644))
	if err := os.Rename(probe, filepath.Join(base, "probe")); err == nil {
		_ = os.Remove(filepath.Join(base, "probe"))
		t.Skip("the temp dir and /dev/shm are the same mount here")
	}
	_ = os.Remove(probe)
	return filepath.Join(base, "staging-1")
}

// A scratch dir on another MOUNT still produces a correct capture — by copying, loudly.
//
// The plan says "same filesystem, so rename". MEASURED 2026-09-04 in this jail: two bind
// mounts of ONE btrfs, identical st_dev, still fail rename with EXDEV, because the kernel
// compares the MOUNT. Under podman every capture surface is its own bind, so a scratch dir
// that is not inside one of them hits this by default — which makes the fallback the
// difference between a working capture and a launch that dies mid-install, and makes the
// warning the difference between paying for the bytes twice and knowing that you are.
func TestRunCopiesLoudlyWhenTheScratchDirIsOnAnotherMount(t *testing.T) {
	home := t.TempDir()
	fixtureHome(t, home)
	out := crossMountOut(t, home)

	var stderr bytes.Buffer
	res, err := Run(Options{
		Home: home, Out: out, Command: writeInstaller(t, installerScript), Stderr: &stderr,
	})
	must(t, err)

	if got := entryPaths(res.Manifest); !equalStrings(got, wantEntries) {
		t.Errorf("cross-mount capture = %v, want the same tree as a same-mount one", got)
	}
	if res.Copied != 6 || res.Renamed != 0 {
		t.Errorf("Copied/Renamed = %d/%d, want 6/0 across a mount boundary", res.Copied, res.Renamed)
	}
	if !strings.Contains(stderr.String(), "COPIED") {
		t.Errorf("a copy is the cost this subsystem exists to delete and must be reported; stderr = %q", stderr.String())
	}
	// New inodes — which is what makes the same-mount test's inode equality mean
	// something rather than being true by accident.
	for rel, ino := range readInodes(t, home) {
		if got := inodeOf(t, filepath.Join(res.Tree, filepath.FromSlash(rel))); got == ino {
			t.Errorf("%s kept inode %d across a mount boundary, which is impossible", rel, ino)
		}
	}
	// Content, modes and symlinks survive the copy path too.
	if got, err := os.ReadFile(filepath.Join(res.Tree, ".local", "share", "vendor", "1.2.3", "vendor")); err != nil ||
		string(got) != "the vendor binary\n" {
		t.Errorf("copied binary = %q, %v", got, err)
	}
	if e, ok := find(res.Manifest, ".local/share/vendor/1.2.3/vendor"); !ok || e.Mode != "0755" {
		t.Errorf("the copy lost the exec bit: %+v", e)
	}
	if e, ok := find(res.Manifest, ".local/bin/vendor"); !ok || e.Kind != KindSymlink {
		t.Errorf("the copy followed a symlink instead of reproducing it: %+v", e)
	}
}

// A failed installer captures NOTHING. Half an install filed as a package is worse than no
// package: the store's whole torn-write discipline exists to keep one off disk, and it cannot
// help if the driver hands it a tree from a run that died.
func TestRunRefusesToCaptureAFailedInstaller(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(t.TempDir(), "staging-1")
	fixtureHome(t, home)

	_, err := Run(Options{
		Home: home, Out: out,
		Command: writeInstaller(t, "#!/bin/sh\nmkdir -p \"$HOME/.local/half\"\nexit 3\n"),
	})
	if err == nil {
		t.Fatal("a failed installer must fail the capture")
	}
	if !strings.Contains(err.Error(), "installer") {
		t.Errorf("error should name the installer: %v", err)
	}
	if _, serr := os.Stat(ManifestPath(out)); !os.IsNotExist(serr) {
		t.Errorf("a failed capture must leave no manifest: %v", serr)
	}
}

// A scratch dir inside a capture surface would capture itself. Refused up front rather than
// filtered out of the walk, because a filter is a rule the manifest's reader cannot see.
func TestRunRefusesAnOutDirInsideACaptureSurface(t *testing.T) {
	home := t.TempDir()
	fixtureHome(t, home)
	_, err := Run(Options{
		Home:    home,
		Out:     filepath.Join(home, ".local", "share", "captures", "staging-1"),
		Command: writeInstaller(t, "#!/bin/sh\ntrue\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "capture itself") {
		t.Fatalf("want a refusal naming self-capture, got %v", err)
	}
}

// A surface the installer CREATES is captured — but the surface ROOT itself is never moved
// whole, because on the container backends it is a bind MOUNTPOINT and renaming one is EBUSY.
func TestRunCapturesASurfaceTheInstallerCreatesWithoutMovingItsRoot(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(t.TempDir(), "staging-1")

	res, err := Run(Options{
		Home: home, Out: out,
		Command: writeInstaller(t, "#!/bin/sh\nset -eu\nmkdir -p \"$HOME/go/bin\"\nprintf x > \"$HOME/go/bin/tool\"\n"),
	})
	must(t, err)

	if got, want := entryPaths(res.Manifest), []string{"go", "go/bin", "go/bin/tool"}; !equalStrings(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(home, "go")); err != nil {
		t.Errorf("the surface root was moved out of the home; on a bind mount that is EBUSY: %v", err)
	}
	if res.Renamed != 1 {
		t.Errorf("Renamed = %d, want 1 (go/bin whole, its root descended into)", res.Renamed)
	}
}

// The surface set is not the driver's own opinion: it is paths.HomeSurfaces(), the same list
// prune dedupes per workspace. A fourth spelling of it is the bug this pins.
func TestSurfacesAreThePathsHomeSurfaces(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(t.TempDir(), "staging-1")
	fixtureHome(t, home)

	res, err := Run(Options{Home: home, Out: out, Command: writeInstaller(t, "#!/bin/sh\ntrue\n")})
	must(t, err)

	want := make([]string, 0, len(paths.HomeSurfaces()))
	for _, s := range paths.HomeSurfaces() {
		want = append(want, s.HomeRel)
	}
	if !equalStrings(res.Manifest.Surfaces, want) {
		t.Errorf("Surfaces = %v, want paths.HomeSurfaces() = %v", res.Manifest.Surfaces, want)
	}
}

// Bad inputs are refused before an installer runs, because running one is the irreversible
// half: it downloads, it writes, and on macos-user it runs under a profile built for one
// staging dir.
func TestRunRefusesBadInputs(t *testing.T) {
	home := t.TempDir()
	ok := writeInstaller(t, "#!/bin/sh\ntrue\n")
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"no home", Options{Out: "/tmp/x", Command: ok}, "no HOME"},
		{"relative home", Options{Home: "rel", Out: "/tmp/x", Command: ok}, "absolute"},
		{"no out", Options{Home: home, Command: ok}, "no output directory"},
		{"relative out", Options{Home: home, Out: "rel", Command: ok}, "absolute"},
		{"no command", Options{Home: home, Out: "/tmp/x"}, "no installer command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// A scratch dir that already holds a tree is refused: two runs merged into one entry would be
// a package no single installer run ever produced, filed under an address that says otherwise.
func TestRunRefusesANonEmptyScratchTree(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(t.TempDir(), "staging-1")
	must(t, os.MkdirAll(filepath.Join(TreeDir(out), "leftover"), 0o755))

	_, err := Run(Options{Home: home, Out: out, Command: writeInstaller(t, "#!/bin/sh\ntrue\n")})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("want a refusal naming the non-empty scratch tree, got %v", err)
	}
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
