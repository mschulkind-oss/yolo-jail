package entrypoint

// hostcreatedfile_test.go pins HC-D4 (docs/design/host-computed-layer.md §7) at the render: a
// host apply that CREATES a file reports it as a change, in the dry run and in the --assert,
// through either of an owned host's mechanisms (hostMechanisms).
//
// The rmw arm's change predicate (hostSurfaceWouldChange) compares the file's decode, encoded,
// against the same decode after the fold, so that a purely layout difference cancels. A file
// that does not exist decodes to {} on both sides, and a surface whose layers add nothing folds
// to {} too — so the predicate said "no change" while the rmw writer went on to create the file.
// Measured on host pi under the retired `assert`, which ran every surface through rmw: the dry
// run said pi/models was `unchanged`, the --assert counted it among the destinations already in
// sync, and the file was created. pi/codex-models is the case left after HC-D1: it declares no
// layer, so a fresh home gets a `{}` file from every apply.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// piCodexModelsPath is the pi/codex-models surface's file under home.
func piCodexModelsPath(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, filepath.FromSlash(piCodexModelsRel(t)))
}

// hostRenderPiSurface renders the shipped pi pack at an owned host — with its surfaces re-declared
// `rmw` when rmw is set (declaredRMW), so the rmw arm's change predicate answers rather than
// stateful's — and returns the one surface's result.
func hostRenderPiSurface(t *testing.T, home string, rmw, observe bool, surface string) HostRenderResult {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	pi = declaredRMW(t, []*packload.Pack{pi}, "pi", rmw)[0]
	results, err := RenderHostPack(pi, home, render.OwnershipOwn, observe, nil, nil)
	if err != nil {
		t.Fatalf("RenderHostPack(pi): %v", err)
	}
	return resultFor(t, results, surface)
}

// A FILE THE APPLY WOULD CREATE IS A PENDING CHANGE, and once created it is not one any more:
// the second half is what keeps the fix from turning every launch gate into a prompt.
func TestAHostApplyThatCreatesAFileReportsItAsAChange(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) {
			home := t.TempDir()
			path := piCodexModelsPath(t, home)

			dry := hostRenderPiSurface(t, home, m.rmw, true, "pi/codex-models")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("the dry run touched %s (stat: %v)", path, err)
			}
			if !dry.WouldChange || dry.Action != "would render" {
				t.Errorf("dry run over a home with no %s: Action=%q WouldChange=%v; the --assert "+
					"creates the file, so it must report `would render`", path, dry.Action, dry.WouldChange)
			}

			wrote := hostRenderPiSurface(t, home, m.rmw, false, "pi/codex-models")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the --assert did not create %s: %v", path, err)
			}
			if !wrote.WouldChange {
				t.Errorf("the --assert that created %s reported WouldChange=false, so the apply counts "+
					"it among the destinations already in sync", path)
			}

			again := hostRenderPiSurface(t, home, m.rmw, true, "pi/codex-models")
			if again.WouldChange || again.Action != "unchanged" {
				t.Errorf("the dry run after the file exists: Action=%q WouldChange=%v, want unchanged — "+
					"a created file must stop reading as a change once it is there", again.Action,
					again.WouldChange)
			}
		})
	}
}

// A DANGLING LINK IS A FILE THE APPLY CREATES. A dotfiles-managed home links a config path to a
// target its repository has not created yet; the read follows the link, finds nothing, and
// decodes {} on both sides, and the write follows it too and creates the target. So the
// predicate has to ask the question the read and the write ask. Asked of the link itself
// (Lstat), it found something there, and the dry run said `unchanged` while the --assert
// counted the surface in sync and created the file.
func TestAHostApplyThroughADanglingLinkReportsItAsAChange(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) {
			home := t.TempDir()
			path := piCodexModelsPath(t, home)
			target := filepath.Join(t.TempDir(), "dotfiles", "codex-models.json")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}

			dry := hostRenderPiSurface(t, home, m.rmw, true, "pi/codex-models")
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("the dry run created %s through the link (stat: %v)", target, err)
			}
			if !dry.WouldChange || dry.Action != "would render" {
				t.Errorf("dry run through a dangling link: Action=%q WouldChange=%v; the --assert "+
					"creates the link's target, so it must report `would render`", dry.Action, dry.WouldChange)
			}

			wrote := hostRenderPiSurface(t, home, m.rmw, false, "pi/codex-models")
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("fixture premise: the --assert did not create the link's target %s: %v", target, err)
			}
			if !wrote.WouldChange {
				t.Errorf("the --assert that created %s through the link reported WouldChange=false, so "+
					"the apply counts it among the destinations already in sync", target)
			}
			if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the --assert replaced the link at %s instead of writing through it (%v)", path, err)
			}

			again := hostRenderPiSurface(t, home, m.rmw, true, "pi/codex-models")
			if again.WouldChange || again.Action != "unchanged" {
				t.Errorf("the dry run once the target exists: Action=%q WouldChange=%v, want unchanged",
					again.Action, again.WouldChange)
			}
		})
	}
}
