// embeddedlifetime_test.go pins the LIFETIME of the process's materialized embedded pack
// tree — one directory, reused, and given back on the way out.
//
// It is here because the leak these assertions exist for was invisible to every other
// test: Embedded() worked perfectly, and the only symptom was that /tmp grew by one
// ~200 KB directory per `yolo` invocation, forever (measured in a live jail 2026-09-03:
// 625 directories, 109 MB, one per invocation of every command since the feature shipped).
//
// EXTERNAL package so it can import internal/packreg, which registers the embedded packs.
// Without it Embedded() is empty and every assertion below passes vacuously — see
// agentsurfaces_test.go's header for why an in-package test cannot.
package packload_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs
)

// isolatedTempDir points os.MkdirTemp at a fresh directory nothing else in this test
// binary writes to, and releases whatever tree an earlier test (or this package's
// init-time consumers) already materialized, so the NEXT Embedded() call lands here.
func isolatedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	packload.ReleaseEmbedded()
	// The next test in this binary gets a live tree, not Roots pointing into a t.TempDir
	// the framework has already deleted.
	t.Cleanup(packload.ReleaseEmbedded)
	return dir
}

func embeddedDirsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "yolo-embedded") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestEmbeddedMaterializesOneTreePerProcess is the "speedup" half: the tree is ~30 files
// and every extra copy is both wasted work and (before ReleaseEmbedded existed) a second
// directory nobody removed. Three call sites made their own; now there is one.
func TestEmbeddedMaterializesOneTreePerProcess(t *testing.T) {
	tmp := isolatedTempDir(t)

	first := packload.Embedded()
	if len(first) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	second := packload.Embedded()

	if got := embeddedDirsIn(t, tmp); len(got) != 1 {
		t.Fatalf("Embedded() called twice made %d trees (%v), want exactly 1", len(got), got)
	}
	if filepath.Dir(first[0].Root) != filepath.Dir(second[0].Root) {
		t.Errorf("two Embedded() calls returned packs from different trees: %s vs %s",
			first[0].Root, second[0].Root)
	}
}

// TestEmbeddedRootsAreReadableForTheProcessGuards the part a leak fix is most likely to
// break: Pack.Root is a HANDLE, and callers read files out of it long after Embedded()
// returned (skills trees, briefing sources, a contribution's `from` path). A cleanup that
// ran any earlier than the process's exit path would leave those callers reading a deleted
// directory — so the tree is asserted present and populated, not merely named.
func TestEmbeddedRootsAreReadableForTheProcess(t *testing.T) {
	isolatedTempDir(t)

	packs := packload.Embedded()
	if len(packs) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	for _, p := range packs {
		if _, err := os.Stat(filepath.Join(p.Root, "pack.json")); err != nil {
			t.Errorf("pack %s: %v — Pack.Root must stay readable for the whole process",
				p.Name, err)
		}
	}
}

// TestReleaseEmbeddedRemovesTheTreeThenRematerializes pins both halves of the contract the
// exit-path callers depend on: the directory is really gone (the leak), and a later call
// gets a fresh live tree rather than dangling Roots (which is what makes calling this at an
// exit path safe — a process that runs Main twice, as the unit tests do, must not be handed
// paths that were deleted under it).
func TestReleaseEmbeddedRemovesTheTreeThenRematerializes(t *testing.T) {
	tmp := isolatedTempDir(t)

	first := packload.Embedded()
	if len(first) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	root := filepath.Dir(first[0].Root)

	packload.ReleaseEmbedded()

	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("%s still exists after ReleaseEmbedded (stat err %v); the process's temp "+
			"tree is what leaked", root, err)
	}
	if got := embeddedDirsIn(t, tmp); len(got) != 0 {
		t.Errorf("ReleaseEmbedded left %v behind in %s", got, tmp)
	}

	// Released, not poisoned.
	again := packload.Embedded()
	if len(again) != len(first) {
		t.Fatalf("Embedded() after ReleaseEmbedded returned %d packs, want %d — a released "+
			"tree must re-materialize, or a second Main in one process gets nothing",
			len(again), len(first))
	}
	if _, err := os.Stat(filepath.Join(again[0].Root, "pack.json")); err != nil {
		t.Errorf("re-materialized pack %s: %v", again[0].Name, err)
	}

	// Idempotent: the exec-shaped exit paths both defer it and call it explicitly.
	packload.ReleaseEmbedded()
	packload.ReleaseEmbedded()
}
