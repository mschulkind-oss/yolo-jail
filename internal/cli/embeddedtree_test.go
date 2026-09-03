package cli

// embeddedtree_test.go pins where the process's materialized embedded pack tree is given
// back, and that this package makes no second copy of it.
//
// Both assertions exist because the leak they cover was invisible to every other test.
// `yolo` extracted the ~30-file embedded pack tree into /tmp and never removed it — once at
// package init (internal/config's hostFileWritableRoots is a package-level var whose
// initializer reaches packload.Embedded, so even `yolo --version` paid it) and a second
// time under `yolo-cli-packs-` for the config commands' surface merge. Measured in a live
// jail 2026-09-03: 625 directories, 109 MB, one per invocation of every command.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// yoloTempEntries lists what yolo left in dir. Any `yolo-` name counts, not just the two
// prefixes that leaked: a future call site that makes its own process-lifetime copy under
// a third prefix is the same bug and should trip the same test.
func yoloTempEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "yolo-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestMainReleasesTheEmbeddedPackTree pins the ONE line of wiring that stops the leak, in
// the place a real process exits through. It is the call-site half of AGENTS.md's rule:
// delete the release from cli.Main and this fails, which no assertion about
// packload.ReleaseEmbedded itself can do.
//
// `--version` on purpose, and it is the sharpest possible case: it is the cheapest path
// through Main and it touches no pack, yet it leaked a directory anyway — because the tree
// is materialized at INIT, before argv is parsed. The explicit Embedded() call below stands
// in for that init-time call, which has already run (into the real temp dir) by the time a
// test body can redirect TMPDIR.
func TestMainReleasesTheEmbeddedPackTree(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	packload.ReleaseEmbedded()
	t.Cleanup(packload.ReleaseEmbedded)
	if len(packload.Embedded()) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	if got := yoloTempEntries(t, tmp); len(got) != 1 {
		t.Fatalf("setup materialized %d trees in TMPDIR (%v), want 1 — without one this "+
			"test passes vacuously", len(got), got)
	}

	if rc := Main([]string{"yolo", "--version"}); rc != 0 {
		t.Fatalf("yolo --version rc = %d, want 0", rc)
	}

	if got := yoloTempEntries(t, tmp); len(got) != 0 {
		t.Errorf("cli.Main returned leaving %v behind in TMPDIR; every `yolo` invocation "+
			"then costs a permanent ~200 KB temp directory", got)
	}
}

// TestSurfaceManifestUsesTheProcessPackTree pins that the config commands' surface merge
// reads the process's ONE tree rather than extracting a second copy of its own. That copy
// is why `yolo config ls` left TWO directories behind, and being cached made it look free.
func TestSurfaceManifestUsesTheProcessPackTree(t *testing.T) {
	t.Cleanup(packload.ReleaseEmbedded)
	// Materialized BEFORE TMPDIR is redirected, so the observed dir stays empty unless
	// surfaceManifest extracts a tree of its own.
	if len(packload.Embedded()) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	allSurfacesOnce = sync.Once{}
	allSurfacesMan = nil
	t.Cleanup(func() {
		allSurfacesOnce = sync.Once{}
		allSurfacesMan = nil
	})

	m := surfaceManifest()
	if _, ok := m.Lookup("claude", "settings"); !ok {
		t.Fatal("surfaceManifest() lost the pack surfaces; it is no longer merging the " +
			"embedded packs at all, so the assertion below would pass vacuously")
	}
	if got := yoloTempEntries(t, tmp); len(got) != 0 {
		t.Errorf("surfaceManifest() extracted its own pack tree (%v); it must share the "+
			"one packload.Embedded materialized for the process", got)
	}
}
