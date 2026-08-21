package run

// packstagedfallback_test.go pins the LAUNCH call site of the staged-tree fallback.
//
// packsrc.Store.Resolve owns the rule (docs/design/storage-and-config.md §10, OQ-SC1
// ruled option (i)), and internal/packsrc/store_test.go pins the rule itself. This file
// exists for the OTHER half of the failure that produced it: the rule had been written
// once already, in `yolo check`, and the launcher never learned it — so the fix and its
// test were both real while `yolo run` still refused the launch. A test that pins the
// callee while the call site is unpinned is not a test (AGENTS.md, Testing), and the
// question to ask of one is whether it fails if the call site is deleted. Delete
// `entry.Slug()` or the `o.Getenv` thread from packRoot and this goes red; the packsrc
// test alone would not.
//
// No container is involved: staging is host-side work, and a fake YOLO_PACK_ROOT is
// exactly what the jail's mount looks like from Go's point of view.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deliveredPackTree writes the tree an OUTER launcher staged and returns the root to
// name in YOLO_PACK_ROOT plus a getenv that names it.
func deliveredPackTree(t *testing.T, name string) (string, func(string) string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"),
		[]byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, func(k string) string {
		if k == "YOLO_PACK_ROOT" {
			return root
		}
		return ""
	}
}

// The measured defect: inside a jail whose config selects a local pack, a nested launch
// was refused with `packs: matt-core: local pack /home/matt/.dotfiles/packs/matt-core is
// not a directory` — because every pack address in an inherited config names a HOST path.
// That breaks the nested-verification workflow AGENTS.md mandates, for the person the
// instruction is written for.
func TestStagePacksUsesTheDeliveredTreeWhenTheSourceIsNotVisible(t *testing.T) {
	home := packHome(t)
	// A source path that does not exist, exactly as a host path looks from in-jail.
	missing := filepath.Join(t.TempDir(), "matt-core")
	writeUserPacks(t, home, `["file://`+missing+`"]`)

	_, getenv := deliveredPackTree(t, "matt-core")
	o, _ := stagingOptions(t)
	o.Getenv = getenv

	stagingRoot, loaded, _, err := o.stagePacks("yolo-test-staged-fallback")
	if err != nil {
		t.Fatalf("a launch whose packs were all delivered must not be refused: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "matt-core" {
		t.Fatalf("want the delivered pack loaded, got %d pack(s)", len(loaded))
	}
	// Staged for THIS launch out of the delivered copy, not merely resolved: the nested
	// jail mounts its own staging root, so the content has to have been copied in.
	if body, rerr := os.ReadFile(filepath.Join(stagingRoot, "matt-core", "marker.txt")); rerr != nil ||
		strings.TrimSpace(string(body)) != "matt-core" {
		t.Errorf("the delivered pack's content did not reach this launch's staging root: %v / %q",
			rerr, body)
	}
}

// THE ANTI-VACUITY CONTROL: with nothing delivered, the launch must still refuse. This
// is what stops the fix above from becoming "never refuse an unresolvable pack", and it
// is the property that makes the FILESYSTEM the predicate rather than "am I in a jail".
func TestStagePacksStillRefusesWhenNothingWasDeliveredEither(t *testing.T) {
	home := packHome(t)
	missing := filepath.Join(t.TempDir(), "matt-core")
	writeUserPacks(t, home, `["file://`+missing+`"]`)

	o, _ := stagingOptions(t)
	o.Getenv = func(string) string { return "" } // an ordinary host: no staged tree

	if _, _, _, err := o.stagePacks("yolo-test-staged-fallback-none"); err == nil {
		t.Error("a pack that is neither resolvable nor delivered is broken, and stagePacks " +
			"is fail-closed (A12)")
	}
}
