package cli

// embeddedtree_test.go pins that `yolo` touches the embedded pack tree only when a command
// reads a pack, gives it back on the way out, and makes no second copy of it.
//
// Each assertion exists because the leak it covers was invisible to every other test.
// `yolo` extracted the embedded pack tree into /tmp and never removed it — once at package
// init (internal/config's hostFileWritableRoots was a package-level var whose initializer
// reached packload.Embedded, so even `yolo --version` paid it) and a second time under
// `yolo-cli-packs-` for the config commands' surface merge. Measured in a live jail
// 2026-09-03: 625 directories, 109 MB, one per invocation of every command; and 2026-09-23:
// 281 more, from the exits that skip cli.Main's deferred release.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// cliLoadedAtInit is evaluated during this test binary's package initialization, after
// every package cli links has initialized — so a package-level initializer ANYWHERE in the
// CLI's dependency graph reaching the embedded packs makes it true.
var cliLoadedAtInit = packload.EmbeddedLoaded()

// yoloTempEntries lists what yolo left in dir. Any `yolo-` name counts, not just the
// prefixes that leaked: a future call site that makes its own process-lifetime copy under
// another prefix is the same bug and should trip the same test.
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

func isolatedEmbedded(t *testing.T) (cache, tmp string) {
	t.Helper()
	cache, tmp = t.TempDir(), t.TempDir()
	home := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Cleanup(packload.OverrideEmbeddedCacheDir(cache))
	return cache, tmp
}

// TestVersionCreatesNoPackTree: `--version` reads no pack, so it must create nothing — no
// cache tree, no temp tree, no hash — which is only true because nothing materializes the
// packs at init any more.
func TestVersionCreatesNoPackTree(t *testing.T) {
	if cliLoadedAtInit {
		t.Fatal("the embedded packs were loaded during package init: a package-level " +
			"initializer in the CLI's dependency graph reaches packload.Embedded, so every " +
			"`yolo` invocation writes a pack tree before argv is parsed")
	}
	cache, tmp := isolatedEmbedded(t)

	if rc := Main([]string{"yolo", "--version"}); rc != 0 {
		t.Fatalf("yolo --version rc = %d, want 0", rc)
	}
	if packload.EmbeddedLoaded() {
		t.Error("`yolo --version` loaded the embedded packs")
	}
	for _, dir := range []string{cache, tmp} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("`yolo --version` created %d entries in %s", len(entries), dir)
		}
	}
}

// TestMainReleasesTheEmbeddedPackTree pins the ONE line of wiring that gives the tree back
// in the place a real process exits through — delete the release from cli.Main and this
// fails, which no assertion about packload.ReleaseEmbedded itself can do. Both shapes: a
// FALLBACK tree is deleted; the shared CACHE tree is kept and only its lease freed.
func TestMainReleasesTheEmbeddedPackTree(t *testing.T) {
	t.Run("fallback tree is deleted", func(t *testing.T) {
		_, tmp := isolatedEmbedded(t)
		t.Cleanup(packload.OverrideEmbeddedCacheDir(""))
		if len(packload.Embedded()) == 0 {
			t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
		}
		if got := yoloTempEntries(t, tmp); len(got) != 1 {
			t.Fatalf("setup materialized %v in TMPDIR, want 1 — without one this passes vacuously", got)
		}
		if rc := Main([]string{"yolo", "--version"}); rc != 0 {
			t.Fatalf("yolo --version rc = %d, want 0", rc)
		}
		if got := yoloTempEntries(t, tmp); len(got) != 0 {
			t.Errorf("cli.Main returned leaving %v behind in TMPDIR", got)
		}
	})
	t.Run("cache tree is kept, lease freed", func(t *testing.T) {
		isolatedEmbedded(t)
		if len(packload.Embedded()) == 0 {
			t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
		}
		root, fallback := packload.EmbeddedLocation()
		if fallback {
			t.Fatalf("setup fell back: %v", packload.EmbeddedProblems())
		}
		if rc := Main([]string{"yolo", "--version"}); rc != 0 {
			t.Fatalf("yolo --version rc = %d, want 0", rc)
		}
		if _, err := os.Stat(root); err != nil {
			t.Errorf("cli.Main deleted the shared cache tree: %v", err)
		}
		st, unlock, _ := packload.ProbeLease(root)
		if st != packload.LeaseFree {
			t.Errorf("after cli.Main the tree's lease is %v, want free — the process kept it pinned", st)
		} else {
			unlock()
		}
	})
}

// TestSurfaceManifestUsesTheProcessPackTree pins that the config commands' surface merge
// reads the process's ONE tree rather than extracting a second copy of its own. That copy
// is why `yolo config ls` left TWO directories behind, and being cached made it look free.
func TestSurfaceManifestUsesTheProcessPackTree(t *testing.T) {
	isolatedEmbedded(t)
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
