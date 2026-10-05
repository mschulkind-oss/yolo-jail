package basehome

import (
	"strings"
	"testing"
)

// contains is local to this file: decls_test.go's `has` is in the EXTERNAL test package
// (basehome_test), and this test needs the internal fixture helpers.
func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestARetiredSharedPackageStoreIsNeverSwept covers pi's extension package store,
// `.pi-shared-npm`, the one member the machine tier ever had that was NOT a credential dir
// (docs/design/pi-extension-lifecycle.md §3.1). It left that tier on 2026-10-05 (XB-D14 of
// docs/design/pi-extension-store-builds.md): pi's npm prefix is per workspace again, and pi
// unshares the link a home kept to the store. The store itself stays in every base home that
// had it, full of `node_modules`, until a human deletes it, and a jail an older yolo launched
// can still have it mounted.
//
// So it must not become the one thing it was protected from being while it was shared: a move
// candidate, which would be the sweep offering to break every such jail's extensions at once
// from a report that named one workspace. Nor may it read as an UNKNOWN top-level directory,
// whose contents are classified with no declarations in hand and whose bytes `yolo check`
// reports as an incomplete detection. It is known as retired (Decls.RetiredSharedDirs, from
// pi's unshare_directory hook), and the launch is what says it can go.
//
// It reads the REAL manifests (ShippedDecls), so deleting pi's unshare_directory declaration
// fails it. A fixture Decls with the name typed in would pass either way.
func TestARetiredSharedPackageStoreIsNeverSwept(t *testing.T) {
	const store = ".pi-shared-npm"

	home := baseHomeFixture(t)
	// The store as a jail that ran pi before XB-D14 leaves it.
	writeFile(t, home, store+"/package.json", `{"name":"pi-extensions","private":true}`)
	writeFile(t, home, store+"/node_modules/pi-lens/index.js", "module.exports = {}\n")
	symlink(t, home, store+"/node_modules/.bin/pi-lens", "../pi-lens/index.js")
	// A per-workspace runtime artifact beside it, so the walk has something to find and the
	// "nothing under the store is a candidate" assertion below is not vacuous.
	writeFile(t, home, ".pi/agent/history.jsonl", "line\n")

	d := ShippedDecls()
	if len(d.Problems) != 0 {
		t.Fatalf("the shipped manifests must read cleanly: %v", d.Problems)
	}
	if !contains(d.RetiredSharedDirs, store) {
		t.Fatalf("RetiredSharedDirs %v no longer carries %s, so the sweep reads the store as an "+
			"unknown top-level directory", d.RetiredSharedDirs, store)
	}

	rep := Detect(home, d)
	if len(rep.Candidates) == 0 {
		t.Fatal("the walk found nothing at all, so every assertion below is vacuous")
	}
	if contains(rep.Roots, store) {
		t.Errorf("%s was walked as a root %v", store, rep.Roots)
	}
	if contains(rep.UnknownRoots, store) {
		t.Errorf("%s reads as an unknown top-level directory %v, so its contents are "+
			"classified with no declarations in hand", store, rep.UnknownRoots)
	}
	for _, e := range rep.Candidates {
		if underOrAt(e.Rel, store) {
			t.Errorf("%s (dir=%v, %s) is a move candidate inside the retired store",
				e.Rel, e.Dir, e.Class)
		}
	}
	for _, w := range rep.Warnings() {
		if strings.Contains(w, store) {
			t.Errorf("a warning names the retired store, which the launch reports instead: %q", w)
		}
	}
}
