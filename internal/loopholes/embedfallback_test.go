package loopholes

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	bundledloopholes "github.com/mschulkind-oss/yolo-jail/bundled_loopholes"
)

// repoBundledDir is the checkout's live bundled_loopholes (tests run in
// the package directory).
func repoBundledDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "bundled_loopholes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no checkout tree at %s: %v", dir, err)
	}
	return dir
}

// TestEmbedMatchesTree is the sync guard for embed.go's EXPLICIT directory
// list: every loophole directory on disk (a non-hidden subdir containing a
// manifest.jsonc — the loader's eligibility rule) must exist in the embed
// with byte-identical recursive contents, and vice versa. Adding a new
// bundled loophole without extending the go:embed directive fails here.
func TestEmbedMatchesTree(t *testing.T) {
	treeRoot := repoBundledDir(t)

	treeFiles := map[string][]byte{}
	entries, err := os.ReadDir(treeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		if _, err := os.Stat(filepath.Join(treeRoot, e.Name(), "manifest.jsonc")); err != nil {
			continue // not a loophole dir (e.g. __pycache__)
		}
		err := filepath.WalkDir(filepath.Join(treeRoot, e.Name()), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return werr
			}
			rel, rerr := filepath.Rel(treeRoot, p)
			if rerr != nil {
				return rerr
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			treeFiles[filepath.ToSlash(rel)] = b
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(treeFiles) == 0 {
		t.Fatal("found no loophole files in the checkout tree — test is broken")
	}

	embedFiles := map[string][]byte{}
	err = fs.WalkDir(bundledloopholes.FS, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		b, rerr := fs.ReadFile(bundledloopholes.FS, p)
		if rerr != nil {
			return rerr
		}
		embedFiles[p] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var treeNames, embedNames []string
	for k := range treeFiles {
		treeNames = append(treeNames, k)
	}
	for k := range embedFiles {
		embedNames = append(embedNames, k)
	}
	sort.Strings(treeNames)
	sort.Strings(embedNames)
	if !reflect.DeepEqual(treeNames, embedNames) {
		t.Fatalf("embed/tree file sets differ (extend embed.go's go:embed list?)\ntree:  %v\nembed: %v", treeNames, embedNames)
	}
	for _, name := range treeNames {
		if !bytes.Equal(treeFiles[name], embedFiles[name]) {
			t.Errorf("embed/tree bytes differ for %s", name)
		}
	}
}

// normalizeRoot returns a copy of l with root-derived absolute paths reduced
// to root-relative ones so copies loaded from different roots compare equal.
func normalizeRoot(l *Loophole, root string) Loophole {
	c := *l
	c.Path = strings.TrimPrefix(c.Path, root)
	c.CACert = strings.TrimPrefix(c.CACert, root)
	c.HostBindMount = append([]HostBindMount(nil), l.HostBindMount...)
	for i := range c.HostBindMount {
		c.HostBindMount[i].Host = strings.TrimPrefix(c.HostBindMount[i].Host, root)
	}
	return c
}

// TestMaterializedFallback simulates an installed binary outside any checkout:
// EVERY step of the shared resolver must miss — no valid YOLO_REPO_ROOT, cwd
// under an isolated temp (so the walk reaches / without a flake.nix+go.mod), an
// isolated HOME (no user-config repo_path), and the test binary has no
// share/yolo-jail sibling (exe-relative miss). BundledLoopholesDir must then
// serve the embedded copy, indistinguishable from loading the checkout tree.
func TestMaterializedFallback(t *testing.T) {
	// Resolve the checkout tree BEFORE chdir — repoBundledDir is cwd-relative.
	treeDir := repoBundledDir(t)

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// An empty YOLO_REPO_ROOT is rejected by the resolver (needs flake.nix or
	// go.mod), so step 1 misses instead of being trusted blindly.
	t.Setenv("YOLO_REPO_ROOT", t.TempDir())
	// Chdir out of the repo so the cwd-walk can't find /workspace's own
	// flake.nix+go.mod, and isolate HOME so no user config resolves.
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())

	got := BundledLoopholesDir()
	if got == "" {
		t.Fatal("BundledLoopholesDir returned empty")
	}
	if filepath.Base(filepath.Dir(got)) != "bundled-loopholes" {
		t.Fatalf("expected a materialized cache dir, got %s", got)
	}

	fromEmbed, orderEmbed := loadFromDir(got, SourceBundled)
	fromTree, orderTree := loadFromDir(treeDir, SourceBundled)
	if !reflect.DeepEqual(orderEmbed, orderTree) {
		t.Fatalf("loophole order differs: embed %v, tree %v", orderEmbed, orderTree)
	}
	if len(fromEmbed) == 0 {
		t.Fatal("materialized dir loaded zero loopholes")
	}
	for name, want := range fromTree {
		gotL, ok := fromEmbed[name]
		if !ok {
			t.Errorf("loophole %s missing from materialized copy", name)
			continue
		}
		// Path — and every path DERIVED from it (bind-mount sources like
		// audio/asound.conf, CA cert paths) — necessarily differs (cache dir
		// vs checkout) and differing is correct: the materialized copy must
		// reference its own files. Normalize those prefixes; everything else
		// must be identical.
		g, w := normalizeRoot(gotL, got), normalizeRoot(want, treeDir)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("loophole %s differs between materialized copy and tree:\n got %+v\nwant %+v", name, g, w)
		}
	}

	// Idempotency: a second resolution reuses the same extraction.
	if again := BundledLoopholesDir(); again != got {
		t.Errorf("second call returned %s, want %s", again, got)
	}
}
