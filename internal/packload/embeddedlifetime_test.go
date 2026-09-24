// embeddedlifetime_test.go pins the LIFETIME of the embedded pack tree with the REAL
// embedded packs: one shared, content-addressed tree per build, adopted by every process
// and never deleted by a release; a per-process fallback that is.
//
// It is here because the leak these assertions exist for was invisible to every other
// test: Embedded() worked perfectly, and the only symptom was that /tmp grew by one
// ~250 KB directory per `yolo` invocation, forever (measured in a live jail 2026-09-03:
// 625 directories, 109 MB; and again 2026-09-23: 281, after the exit-path releases — the
// paths that skip a defer kept leaking).
//
// EXTERNAL package so it can import internal/packreg, which registers the embedded packs.
// Without it Embedded() is empty and every assertion below passes vacuously — see
// agentsurfaces_test.go's header for why an in-package test cannot.
package packload_test

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs
)

// isolated points the cache base and TMPDIR at fresh dirs nothing else in this binary
// writes to; the override releases whatever tree an earlier test held, so the NEXT
// Embedded() lands here. Returns (base, tmp).
func isolated(t *testing.T) (string, string) {
	t.Helper()
	// Resolved where minted: the loader resolves the base through symlinks, and darwin's
	// t.TempDir() is under the /var -> /private/var symlink.
	resolve := func() string {
		d, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base, tmp := resolve(), resolve()
	t.Setenv("TMPDIR", tmp)
	t.Cleanup(packload.OverrideEmbeddedCacheDir(base))
	return base, tmp
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func mustEmbedded(t *testing.T) []*packload.Pack {
	t.Helper()
	packs := packload.Embedded()
	if len(packs) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	return packs
}

// TestEmbeddedUsesOneTreePerBuild: the tree is under <base>/<hash>, two calls share it, and
// nothing is written per process anywhere — not in TMPDIR, not beside it in the base.
func TestEmbeddedUsesOneTreePerBuild(t *testing.T) {
	base, tmp := isolated(t)
	sum, ok := packload.EmbeddedHash()
	if !ok {
		t.Fatal("EmbeddedHash() unknown with the packs registered")
	}

	first := mustEmbedded(t)
	second := mustEmbedded(t)
	if first[0].Root != second[0].Root {
		t.Errorf("two Embedded() calls returned different trees: %s vs %s", first[0].Root, second[0].Root)
	}
	if want := filepath.Join(base, sum, first[0].Name); first[0].Root != want {
		t.Errorf("Pack.Root = %s, want %s", first[0].Root, want)
	}
	if got := entries(t, base); len(got) != 1 || got[0] != sum {
		t.Errorf("base holds %v, want exactly [%s]", got, sum)
	}
	if got := entries(t, tmp); len(got) != 0 {
		t.Errorf("TMPDIR holds %v, want nothing: a usable cache leaves nothing per process", got)
	}
}

// TestEmbeddedRootsAreReadableForTheProcess guards the part a lifetime change is most likely
// to break: Pack.Root is a HANDLE, and callers read files out of it long after Embedded()
// returned (skills trees, briefing sources, a contribution's `from` path).
func TestEmbeddedRootsAreReadableForTheProcess(t *testing.T) {
	isolated(t)
	for _, p := range mustEmbedded(t) {
		if _, err := os.Stat(filepath.Join(p.Root, "pack.json")); err != nil {
			t.Errorf("pack %s: %v — Pack.Root must stay readable for the whole process", p.Name, err)
		}
	}
}

// TestReleaseEmbeddedKeepsTheSharedTreeAndReadopts: a release gives back this process's
// LEASE, never the tree other processes of the build are using, and a later call re-adopts
// the very same tree — released, not poisoned; idempotent.
func TestReleaseEmbeddedKeepsTheSharedTreeAndReadopts(t *testing.T) {
	base, _ := isolated(t)
	first := mustEmbedded(t)
	root, _ := packload.EmbeddedLocation()
	if st, _, _ := packload.ProbeLease(root); st != packload.LeaseHeld {
		t.Errorf("an adopted tree's lease probed %v, want held", st)
	}

	packload.ReleaseEmbedded()
	if r, _ := packload.EmbeddedLocation(); r != "" || packload.EmbeddedLoaded() {
		t.Errorf("after release: location %q loaded=%v, want forgotten", r, packload.EmbeddedLoaded())
	}
	if _, err := os.Stat(filepath.Join(first[0].Root, "pack.json")); err != nil {
		t.Fatalf("ReleaseEmbedded deleted the shared tree: %v", err)
	}
	if st, unlock, _ := packload.ProbeLease(root); st != packload.LeaseFree {
		t.Errorf("after release the lease probed %v, want free", st)
	} else {
		unlock()
	}

	again := mustEmbedded(t)
	if again[0].Root != first[0].Root || len(again) != len(first) {
		t.Errorf("re-adopt: %d packs at %s, want %d at %s", len(again), again[0].Root, len(first), first[0].Root)
	}
	if got := entries(t, base); len(got) != 1 {
		t.Errorf("re-adopt changed the base: %v", got)
	}
	packload.ReleaseEmbedded()
	packload.ReleaseEmbedded()
}

// TestReleaseEmbeddedRemovesTheFallbackTree: with no usable cache location the tree is the
// process's own, in TMPDIR, and ReleaseEmbedded removes it — the leak the exit-path releases
// exist for.
func TestReleaseEmbeddedRemovesTheFallbackTree(t *testing.T) {
	_, tmp := isolated(t)
	t.Cleanup(packload.OverrideEmbeddedCacheDir(""))

	first := mustEmbedded(t)
	root, fallback := packload.EmbeddedLocation()
	if !fallback || filepath.Dir(root) != tmp {
		t.Fatalf("tree at %s (fallback=%v), want a fallback in %s", root, fallback, tmp)
	}
	packload.ReleaseEmbedded()
	if got := entries(t, tmp); len(got) != 0 {
		t.Errorf("ReleaseEmbedded left %v behind in %s", got, tmp)
	}
	again := mustEmbedded(t)
	if len(again) != len(first) {
		t.Fatalf("Embedded() after release returned %d packs, want %d", len(again), len(first))
	}
	if _, err := os.Stat(filepath.Join(again[0].Root, "pack.json")); err != nil {
		t.Errorf("re-materialized pack %s: %v", again[0].Name, err)
	}
	packload.ReleaseEmbedded()
	packload.ReleaseEmbedded()
}

// TestConcurrentFirstPopulatesLeaveOneTree races several PROCESSES at an empty base. Each
// must end up reading the same tree, and the base must hold that one tree and no stray
// .tmp- dir from a loser.
func TestConcurrentFirstPopulatesLeaveOneTree(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		t.Skip("helper process")
	}
	base, tmp := isolated(t)
	sum, _ := packload.EmbeddedHash()

	const n = 6
	roots := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestEmbeddedHelperProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), helperEnv+"="+base)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("helper %d: %v\n%s", i, err, out)
				return
			}
			sc := bufio.NewScanner(bytes.NewReader(out))
			for sc.Scan() {
				if r, ok := strings.CutPrefix(sc.Text(), "ROOT="); ok {
					roots[i] = r
				}
			}
		}(i)
	}
	wg.Wait()

	for i, r := range roots {
		if r != filepath.Join(base, sum) {
			t.Errorf("helper %d read %q, want the one tree %s", i, r, filepath.Join(base, sum))
		}
	}
	if got := entries(t, base); len(got) != 1 || got[0] != sum {
		t.Errorf("after %d racing first-populates base holds %v, want exactly [%s]", n, got, sum)
	}
	if got := entries(t, tmp); len(got) != 0 {
		t.Errorf("TMPDIR holds %v after the race", got)
	}
}

const helperEnv = "YOLO_TEST_EMBEDDED_HELPER_BASE"

// TestEmbeddedHelperProcess is the child of TestConcurrentFirstPopulatesLeaveOneTree.
func TestEmbeddedHelperProcess(t *testing.T) {
	base := os.Getenv(helperEnv)
	if base == "" {
		t.Skip("only runs as a helper process")
	}
	t.Cleanup(packload.OverrideEmbeddedCacheDir(base))
	mustEmbedded(t)
	root, fallback := packload.EmbeddedLocation()
	if fallback {
		t.Fatalf("helper fell back: %v", packload.EmbeddedProblems())
	}
	os.Stdout.WriteString("ROOT=" + root + "\n")
}
