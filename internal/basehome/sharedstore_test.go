package basehome

import (
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

// TestASharedPackageStoreIsNeverSwept covers the third member of the machine tier, which is
// the first one that is NOT a credential dir: pi's extension package store
// (`.pi-shared-npm`, docs/design/pi-extension-lifecycle.md §3.1).
//
// The exclusion is structural — rule 1 says the walk cannot descend into a shared dir even
// when handed it as a root — so this test asserts a property the code already has rather than
// one it needs new logic for. It is here because the property is INHERITED rather than
// stated: nothing else fails if `.pi-shared-npm` stops arriving in Decls.SharedDirs, and the
// consequence would be a sweep proposing to archive a `node_modules` tree that every jail on
// the machine reads — breaking all of them at once, from a report that named one workspace.
//
// It reads the REAL manifests (ShippedDecls), so deleting the pack's `scope: "machine"`
// declaration fails it. A fixture Decls with the name typed in would pass either way.
func TestASharedPackageStoreIsNeverSwept(t *testing.T) {
	const store = ".pi-shared-npm"

	home := baseHomeFixture(t)
	// The store as a jail that has run pi leaves it.
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
	if !contains(d.SharedDirs, store) {
		t.Fatalf("SharedDirs %v no longer carries %s, so the walk treats it as ordinary "+
			"state and every launch's store is a move candidate", d.SharedDirs, store)
	}

	rep := Detect(home, d)
	if len(rep.Candidates) == 0 {
		t.Fatal("the walk found nothing at all, so every assertion below is vacuous")
	}
	if contains(rep.Roots, store) {
		t.Errorf("%s was walked as a root %v", store, rep.Roots)
	}
	// The unknown-top-level sweep must recognise it. An unrecognised top-level directory is
	// reported as classified WITHOUT its declarations, which is the route by which a live
	// host's `.pi-lens` was found — and for this one it would mean the store's whole tree
	// reading as runtime.
	if contains(rep.UnknownRoots, store) {
		t.Errorf("%s reads as an unknown top-level directory %v, so its contents are "+
			"classified with no declarations in hand", store, rep.UnknownRoots)
	}
	for _, e := range rep.Candidates {
		if underOrAt(e.Rel, store) {
			t.Errorf("%s (dir=%v, %s) is a move candidate inside the shared store",
				e.Rel, e.Dir, e.Class)
		}
	}

	// RULE 1, the belt-and-braces half: handed the store as a walk ROOT — which is what a
	// future declaration naming it in both tiers would do — it must be EXCLUDED at admission
	// rather than descended into.
	forced := d
	forced.StateDirs = append(append([]string(nil), d.StateDirs...), store)
	forcedRep := Detect(home, forced)
	if !contains(forcedRep.Excluded, store) {
		t.Errorf("handed %s as a root it was not excluded (Excluded %v, Roots %v)",
			store, forcedRep.Excluded, forcedRep.Roots)
	}
	for _, e := range forcedRep.Candidates {
		if underOrAt(e.Rel, store) {
			t.Errorf("%s became a candidate once the store was handed in as a root", e.Rel)
		}
	}
}
