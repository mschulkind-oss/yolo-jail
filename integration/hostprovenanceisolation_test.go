package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostprovenanceisolation_test.go keeps the MACHINE's host-render marks out of a test
// that asks whether a `readsHost` layer is composed.
//
// isolateHome re-links the machine's whole yolo state dir into every isolated home
// (packHomeSharedStores), and the host-render provenance lives INSIDE it:
// render.Target.ProvenanceDir is GlobalStorageUnder(home)/host-provenance. The launcher
// labels a delivered host layer "yolo's own render" when that record exists
// (run.hostLayerIsRender → entrypoint.HostSurfaceRendered), and the entrypoint then
// composes the surface WITHOUT it, by design: a key yolo wrote must not come back as the
// user's. So on a machine whose real home was ever host-rendered, which the self-hosted Mac
// runner is (it runs as the maintainer's own account, runbooks/mac-actions-runner.md §0),
// the isolated home's hand-written settings.json is dropped for a mark that belongs to a
// DIFFERENT file, and the experiment reports the defect it exists to rule out.

// claudeSettingsSurface is the surface the launcher's label is asked about. Only the
// agent and name reach the provenance path.
var claudeSettingsSurface = manifest.Surface{Agent: "claude", Name: "settings", Path: "~/.claude/settings.json"}

// privateHostProvenance replaces home's LINK to the machine's state dir with a private
// directory that links back every entry EXCEPT host-provenance, so the launcher sees this
// home's own render history (none) while the image cache, the flake bundle and every other
// store stay the machine's, as isolateHome intends.
//
// A no-op when the state dir is not a link: a private dir already holds only what this
// test's own launches wrote. machineHome's tree is only read, never written.
func privateHostProvenance(t *testing.T, home, machineHome string) {
	t.Helper()
	store := paths.GlobalStorageUnder(home)
	fi, err := os.Lstat(store)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return
	}
	machineStore := paths.GlobalStorageUnder(machineHome)
	entries, err := os.ReadDir(machineStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Registered after the home's own t.TempDir, so it runs first and can clear what a
	// launch wrote with modes RemoveAll cannot unlink through. WalkDir does not follow the
	// links, so nothing of the machine's is chmod'd or removed.
	t.Cleanup(func() { removeWorkspaceTree(t, store) })
	hidden := filepath.Base(render.Host(home, nil, render.OwnershipUnstated).ProvenanceDir())
	for _, e := range entries {
		if e.Name() == hidden {
			continue
		}
		if err := os.Symlink(filepath.Join(machineStore, e.Name()), filepath.Join(store, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPrivateHostProvenanceHidesTheMachinesRenderMark runs under -short (no container): it
// is a harness invariant, like TestPackHomeSharesHostStores.
func TestPrivateHostProvenanceHidesTheMachinesRenderMark(t *testing.T) {
	machine := resolvedTempDir(t)
	machineStore := paths.GlobalStorageUnder(machine)
	mark := render.Host(machine, nil, render.OwnershipUnstated).ProvenancePath("claude", "settings")
	if mark == "" || !isUnder(mark, machineStore) {
		t.Fatalf("the host provenance path %q is not under the state dir %q, so isolateHome's "+
			"link does not carry it and this helper has nothing to hide", mark, machineStore)
	}
	cached := filepath.Join(machineStore, "cache", "probe")
	for _, f := range []string{mark, cached} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	home := resolvedTempDir(t)
	store := paths.GlobalStorageUnder(home)
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(machineStore, store); err != nil {
		t.Fatal(err)
	}
	// The leak itself, stated so a change to where provenance lives fails here rather than
	// silently making the helper moot.
	if !entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Fatal("precondition: through isolateHome's link, the machine's render mark is " +
			"expected to read as THIS home's; it does not, so the leak this helper closes is gone")
	}

	privateHostProvenance(t, home, machine)

	if entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Error("the isolated home still reads the machine's host-render mark for " +
			"claude/settings, so the launcher would label a hand-written settings.json " +
			"yolo's own render and compose the surface without it")
	}
	if b, err := os.ReadFile(filepath.Join(store, "cache", "probe")); err != nil || string(b) != "{}\n" {
		t.Errorf("the machine's cache/ no longer resolves through the isolated home (%v)", err)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("the machine's own provenance record was touched: %v", err)
	}
}

func isUnder(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel[:min(2, len(rel))] != ".."
}
