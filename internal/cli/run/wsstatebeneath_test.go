package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostFileBody stands in for a host file the jail wants overwritten, such as the user's shell rc.
const hostFileBody = "export IMPORTANT=1\n"

// outsideFile is a host file outside every workspace overlay, holding hostFileBody.
func outsideFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "zshrc")
	if err := os.WriteFile(p, []byte(hostFileBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func symlinkAt(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// footerLikePack is a fixture shaped like the omp pack's footer (docs/design/agent-footer.md
// §2.1): one file in a subdirectory of its own, under a workspace-scoped state dir, so podman
// prepares its mountpoint in the overlay and Apple Container copies it there. It returns the
// pack and its one target.
func footerLikePack(t *testing.T) (*packload.Pack, packFilesTarget) {
	t.Helper()
	p := workspaceFilesPack(t, "footer-like", "index.js", ".oh-omp/agent/extensions/yolo-footer/index.js", ".oh-omp")
	targets := packFilesTargets([]*packload.Pack{p})
	if len(targets) != 1 || !isFile(targets[0].Src) {
		t.Fatalf("the fixture pack resolves to %+v, want one single-file target", targets)
	}
	return p, targets[0]
}

// TestACMaterializeNeverWritesThroughALinkTheJailLeft: on Apple Container the workspace
// overlay is the jail's own home, so a jail can leave a symlink at a path the next launch
// copies a file to, or at a directory above it. The copy must land in the overlay and
// nowhere else: a link at the file is replaced, and a link above it is refused.
func TestACMaterializeNeverWritesThroughALinkTheJailLeft(t *testing.T) {
	_, target := footerLikePack(t)
	footer := mustReadFile(t, target.Src)

	t.Run("a link at the file names a host file", func(t *testing.T) {
		wsState := t.TempDir()
		host := outsideFile(t)
		dest := filepath.Join(wsState, filepath.FromSlash(target.Dest))
		symlinkAt(t, host, dest)

		acMaterialize(target.Src, target.Dest, wsState)

		if got := mustReadFile(t, host); got != hostFileBody {
			t.Fatalf("the launch wrote through the jail's link: the host file now holds %d bytes of "+
				"%s", len(got), target.Dest)
		}
		fi, err := os.Lstat(dest)
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s after the copy: %v %v, want a regular file replacing the link", dest, fi, err)
		}
		if got := mustReadFile(t, dest); got != footer {
			t.Errorf("%s holds %q, want the pack's footer", dest, got)
		}
	})

	t.Run("a link at the file names another overlay file", func(t *testing.T) {
		wsState := t.TempDir()
		other := filepath.Join(wsState, "keep.txt")
		if err := os.WriteFile(other, []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(wsState, filepath.FromSlash(target.Dest))
		symlinkAt(t, other, dest)

		acMaterialize(target.Src, target.Dest, wsState)

		if got := mustReadFile(t, other); got != "mine\n" {
			t.Errorf("the copy went through the link into %s: %q", other, got)
		}
		if got := mustReadFile(t, dest); got != footer {
			t.Errorf("%s holds %q, want the pack's footer", dest, got)
		}
	})

	t.Run("a link above the file names a host directory", func(t *testing.T) {
		wsState := t.TempDir()
		hostDir := t.TempDir()
		// The footer's own directory, yolo-footer/, replaced by a link to a host directory.
		symlinkAt(t, hostDir, filepath.Join(wsState, filepath.FromSlash(filepath.Dir(target.Dest))))

		acMaterialize(target.Src, target.Dest, wsState)

		if entries, _ := os.ReadDir(hostDir); len(entries) != 0 {
			t.Errorf("the launch wrote %s into a host directory through the jail's link: %v",
				filepath.Base(target.Dest), entries)
		}
	})
}

// TestAppleContainerFilesDeliveryStaysInTheOverlay is the same rule through the launch's own
// call site: assembleRunCmd delivers a single-file `files` contribution on Apple Container by
// copying it into the overlay (packFilesMountArgs), and a link the jail left there must not
// carry the copy onto a host file.
func TestAppleContainerFilesDeliveryStaysInTheOverlay(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.IsMacOS, o.IsLinux = true, false

	wsState := filepath.Join(ws, ".yolo", "home")
	host := outsideFile(t)
	symlinkAt(t, host, filepath.Join(wsState, ".claude", "file-suggestion.sh"))
	pack := filesPack(t, "onefile", "file-suggestion.sh", ".claude/file-suggestion.sh", nil)
	in := relocationInput(t, "container", wsState, nil)
	in.packs = append(in.packs, pack)

	o.assembleRunCmd(in)

	if got := mustReadFile(t, host); got != hostFileBody {
		t.Fatalf("the launch copied the pack's file onto a host file through the jail's link: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(wsState, ".claude", "file-suggestion.sh")); got != "single-file tree\n" {
		t.Errorf("the pack's file was not delivered into the overlay: %q", got)
	}
}

// TestPreparePackFilesMakesNoMountpointOutsideTheOverlay is the podman half: preparePackFiles
// creates an empty mountpoint for a single-file target under a declared state dir, as omp's footer
// is under ~/.oh-omp, so the jail can replace a directory on the way with a link to a host
// directory. The mountpoint, and any directory above it, must not appear there.
func TestPreparePackFilesMakesNoMountpointOutsideTheOverlay(t *testing.T) {
	pack, target := footerLikePack(t)
	rel, ok := packFilesWorkspaceRel(target.Dest, packload.WritableDirs([]*packload.Pack{pack}), "podman")
	if !ok {
		t.Fatalf("podman does not prepare the fixture's %s in the overlay", target.Dest)
	}
	for _, linked := range []string{filepath.Dir(rel), filepath.Dir(filepath.Dir(filepath.Dir(rel)))} {
		t.Run(linked, func(t *testing.T) {
			wsState := filepath.Join(t.TempDir(), ".yolo", "home")
			hostDir := t.TempDir()
			symlinkAt(t, hostDir, filepath.Join(wsState, linked))

			preparePackFiles([]*packload.Pack{pack}, wsState, "podman")

			if entries, _ := os.ReadDir(hostDir); len(entries) != 0 {
				t.Errorf("preparePackFiles created %v in a host directory through the jail's link at %s",
					entries, linked)
			}
		})
	}
	t.Run("no link", func(t *testing.T) {
		wsState := filepath.Join(t.TempDir(), ".yolo", "home")
		preparePackFiles([]*packload.Pack{pack}, wsState, "podman")
		if !fileIsEmptyRegular(filepath.Join(wsState, rel)) {
			t.Errorf("preparePackFiles no longer creates the mountpoint at %s", rel)
		}
	})
}
