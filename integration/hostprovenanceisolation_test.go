package integration

import (
	"os"
	"path/filepath"
	"slices"
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

// privateHostProvenance replaces home's LINK to a state dir with a private directory that
// links back every entry of that state dir EXCEPT host-provenance, so the launcher sees this
// home's own render history (none) while the image cache, the flake bundle and every other
// store stay where isolateHome pointed them.
//
// A no-op when the state dir is not a link: a private dir already holds only what this
// test's own launches wrote. The linked tree is only read, never written.
func privateHostProvenance(t *testing.T, home string) {
	t.Helper()
	privateStateEntries(t, home,
		filepath.Base(render.Host(home, nil, render.OwnershipUnstated).ProvenanceDir()))
}

// privateStateEntries is privateHostProvenance for any set of the state dir's top-level
// entries: home's link to a state dir becomes a private directory linking back every entry
// except those named in hidden, which this home then has none of until a test or a launch makes
// its own. The Apple Container login-seed experiment hides `home`, the store that holds the
// seed (paths.GlobalHome), so the seed it plants is never the machine's.
//
// THE ENTRIES ARE THE LINK'S OWN TARGET'S, never the machine's store read by name. In a non-short
// run the link points at this run's own store (runstore_test.go), which keeps the loophole state
// root and its ownership record private; linking the machine's entries back would hand the launch
// both, and a claude-only launch would then retire the machine's state for every other pack the
// record names (TestPrivateStateEntriesKeepsARunStorePrivate).
func privateStateEntries(t *testing.T, home string, hidden ...string) {
	t.Helper()
	store := paths.GlobalStorageUnder(home)
	fi, err := os.Lstat(store)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return
	}
	linked, err := filepath.EvalSymlinks(store)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(linked)
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
	// links, so nothing of the linked store's is chmod'd or removed.
	t.Cleanup(func() { removeWorkspaceTree(t, store) })
	for _, e := range entries {
		if slices.Contains(hidden, e.Name()) {
			continue
		}
		if err := os.Symlink(filepath.Join(linked, e.Name()), filepath.Join(store, e.Name())); err != nil {
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

	privateHostProvenance(t, home)

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

// TestPrivateStateEntriesKeepsARunStorePrivate runs under -short: when the isolated home's
// state dir links to this run's OWN store (runstore_test.go), which is every non-short run on
// the machine's home, the private directory must link back that store's entries and not the
// machine's. Linking the machine's would hand the launch the machine's loophole state root and
// ownership record, the pair the run store keeps private: a claude-only launch would then
// retire the machine's state for every other pack its record names. On the self-hosted Mac
// that is the maintainer's own account's state.
func TestPrivateStateEntriesKeepsARunStorePrivate(t *testing.T) {
	machine := resolvedTempDir(t)
	machineStore := paths.GlobalStorageUnder(machine)
	for _, d := range []string{filepath.Join(machineStore, "state", "aws-auth"), filepath.Join(machineStore, "cache")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(machineStore, "pack-loophole-owners.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir, err := makeRunStore(machine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(runDir)) })
	if err := os.MkdirAll(filepath.Join(runDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}

	home := resolvedTempDir(t)
	store := paths.GlobalStorageUnder(home)
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runDir, store); err != nil {
		t.Fatal(err)
	}

	privateStateEntries(t, home, "home")

	for _, name := range []string{"state", "pack-loophole-owners.json"} {
		if _, err := os.Lstat(filepath.Join(store, name)); err == nil {
			t.Errorf("the private state dir links the machine's %s, though the home it replaced "+
				"linked this run's own store, which has none", name)
		}
	}
	for _, name := range []string{"cache", "agents"} {
		if _, err := os.Stat(filepath.Join(store, name)); err != nil {
			t.Errorf("the run store's %s no longer resolves through the private state dir: %v", name, err)
		}
	}
}

func isUnder(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel[:min(2, len(rel))] != ".."
}
