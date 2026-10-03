package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// acsurfaceroot_test.go covers the surface root on APPLE CONTAINER's home layout, where it is not
// a second path to the surfaces at all.
//
// # The two layouts the one argv meets
//
// The host act passes the same `--surface-root=/workspace/.yolo/home` on every container backend
// (captureJailArgv). On podman each surface is its own bind from `<ws>/.yolo/home/<Subtree>`, so
// `<root>/local` IS `$HOME/.local` through the workspace's mount. Apple Container binds
// `<ws>/.yolo/home` WHOLE at the home (appleContainerBaseMounts), so `$HOME/.local` is
// `<ws>/.yolo/home/.local` — the dotted HomeRel spelling — and `<root>/local` names a directory
// nothing creates (prepareWsState lays out no podman bind source on that backend).
//
// MEASURED on a Mac, 2026-10-02 (docs/research/macos-backend-performance.md §8): every Apple
// Container launch ran claude's, codex's and agy's installers and each "left nothing in the
// capture surfaces". The driver walked `<root>/local`, found no directory, skipped it as a surface
// that does not exist, and filed an empty delta.
//
// # Modelling it
//
// HOME is a symlink to `<ws>/.yolo/home` — one directory with two paths, as the home bind and the
// workspace bind give it on that backend — and the surface root is paths.WorkspaceHomeState(ws),
// the expression captureJailArgv spells. The installer writes through HOME, as it does in a jail.

// acHome builds the Apple Container fixture: a workspace whose .yolo/home holds the surfaces under
// their HOME spellings, and a HOME that is a second path to that directory. Returns (home, ws).
func acHome(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	state := paths.WorkspaceHomeState(ws)
	// What a booted Apple Container home holds before any installer: the boot's npm prefix,
	// ~/.local with yolo's own state in it, and $GOPATH (prepareWsState makes `go`, whose two
	// spellings agree).
	for _, rel := range []string{".npm-global/lib", ".local/share/yolo-jail", "go"} {
		must(t, os.MkdirAll(filepath.Join(state, filepath.FromSlash(rel)), 0o755))
	}
	must(t, os.MkdirAll(filepath.Join(state, ".codex"), 0o755))
	home := filepath.Join(base, "home-agent")
	must(t, os.Symlink(state, home))
	return home, ws
}

// acInstaller writes what the three shipped installers write: a version dir and a bin link under
// ~/.local (claude, agy), a file in the npm prefix, one in $GOPATH, and codex's standalone payload
// with its ~/.local/bin link.
const acInstaller = `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/share/vendor/1.2.3" "$HOME/.local/bin"
printf 'the vendor binary\n' > "$HOME/.local/share/vendor/1.2.3/vendor"
ln -s "$HOME/.local/share/vendor/1.2.3/vendor" "$HOME/.local/bin/vendor"
printf 'npm side\n' > "$HOME/.npm-global/lib/marker"
mkdir -p "$HOME/go/bin"
printf 'go side\n' > "$HOME/go/bin/marker"
mkdir -p "$HOME/.codex/packages/standalone/releases/1.0"
printf 'codex\n' > "$HOME/.codex/packages/standalone/releases/1.0/codex"
ln -s "$HOME/.codex/packages/standalone/releases/1.0/codex" "$HOME/.local/bin/codex"
`

// AN APPLE CONTAINER CAPTURE FINDS WHAT THE INSTALLER WROTE. The surface root the host passes
// does not reach `.npm-global`, `.local` or the codex payload on this layout, so the driver must
// not walk the paths it would name there: it reaches those surfaces through HOME, which is where
// the installer wrote them.
//
// Red before the fix: the tree held `go` alone, the one surface whose two spellings agree, and
// claude's, codex's and agy's installs each "left nothing in the capture surfaces".
func TestAnAppleContainerCaptureFindsWhatTheInstallerWrote(t *testing.T) {
	home, ws := acHome(t)
	out := filepath.Join(t.TempDir(), "staging-1")

	res, err := Run(Options{
		Home:        home,
		Out:         out,
		SurfaceRoot: paths.WorkspaceHomeState(ws),
		Command:     writeInstaller(t, acInstaller),
	})
	must(t, err)

	got := map[string]bool{}
	for _, p := range entryPaths(res.Manifest) {
		got[p] = true
	}
	for _, want := range []string{
		".local/share/vendor/1.2.3/vendor",
		".local/bin/vendor",
		".local/bin/codex",
		".npm-global/lib/marker",
		"go/bin/marker",
		".codex/packages/standalone/releases/1.0/codex",
	} {
		if !got[want] {
			t.Errorf("%s is not in the capture: the driver walked a surface root that is not a "+
				"second path to this home's surfaces\ncaptured: %v", want, entryPaths(res.Manifest))
		}
	}
	// THE DELTA LEFT THE HOME, rather than being copied out of it and left behind too.
	if _, err := os.Lstat(filepath.Join(home, ".local", "share", "vendor")); !os.IsNotExist(err) {
		t.Errorf("the captured version dir is still in the home: %v", err)
	}
	// yolo's own state stays excluded through the home door as through the other one.
	if got[".local/share/yolo-jail"] {
		t.Error("yolo's own state dir was captured")
	}
	if res.Manifest.Home != home {
		t.Errorf("Manifest.Home = %q, want the capture HOME %q", res.Manifest.Home, home)
	}
}

// A SURFACE ROOT WHOSE DIRECTORY EXISTS BUT IS NOT THE HOME'S is not used either. Existence is not
// the predicate: a directory can have the surface's podman name under the root and still not be
// the one the installer writes into, and walking it captures none of the installer's bytes. The
// driver uses a surface-root door only when it is the same directory the home reaches.
//
// Red before the fix: `.local/bin/vendor` was not captured, because the walk went through the
// empty stray `<root>/local`.
func TestASurfaceRootDirectoryThatIsNotTheHomesIsNotUsed(t *testing.T) {
	home, ws := acHome(t)
	state := paths.WorkspaceHomeState(ws)
	// A podman bind source with nothing in it, beside the dotted directory the home really uses.
	must(t, os.MkdirAll(filepath.Join(state, "local", "bin"), 0o755))
	out := filepath.Join(t.TempDir(), "staging-1")

	res, err := Run(Options{
		Home:        home,
		Out:         out,
		SurfaceRoot: state,
		Command: writeInstaller(t, "#!/bin/sh\nset -eu\nmkdir -p \"$HOME/.local/bin\"\n"+
			"printf x > \"$HOME/.local/bin/vendor\"\n"),
	})
	must(t, err)

	if paths := entryPaths(res.Manifest); !strings.Contains(strings.Join(paths, " "), ".local/bin/vendor") {
		t.Errorf("captured %v, want .local/bin/vendor: the walk went through a directory that "+
			"is not the home's .local", paths)
	}
	// And the stray was left alone: it is not the capture's to move.
	if _, err := os.Stat(filepath.Join(state, "local", "bin")); err != nil {
		t.Errorf("the unrelated directory under the surface root was disturbed: %v", err)
	}
}
