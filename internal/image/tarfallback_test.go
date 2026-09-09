package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What survives of P4 — "no reclaimer may delete an artifact an in-flight launch
// is between steps on" (docs/design/minimal-disk-footprint.md §5 P4). The racer
// is `yolo prune --apply`'s keep-N tail-drop, which deletes cache/images/*.tar by
// mtime and knows nothing about which tar a launch is mid-flight on.
//
// THE LARGER HALF OF THIS FILE IS GONE, AND THAT IS A REDUCTION IN EXPOSURE
// RATHER THAN A DROPPED TEST. Three tests here drove the Apple Container
// materialize-then-convert pair, whose window between `fileExists(cacheFile)`
// and a converter subprocess opening that same path WAS the whole remaining
// exposure once C3 removed podman's file. C9 deletes that pair: the copy writes
// a TEMPORARY archive under a name `newestTars` cannot match, and removes it
// itself (deliverToAppleContainer), so no reclaimer can see the file and there
// is no two-step window to race. A test for a race that cannot be represented
// would be a test of its own fixture.
//
// What is left is the ONE reader a reclaimer can still race: the degraded
// fallback's LISTING of legacy tars, which is a listing by construction and
// therefore always one instruction behind the directory.

// TestCachedTarFallbackSkipsATarEvictedAfterListing pins the CROSS-BACKEND half
// of P4. newestTars is a LISTING, and §10 step 2 is explicit that the fallback
// reader "is cross-backend and needs the guard on every backend" — since C3 the
// podman arm streams, so this loop is the only place podman still holds a tar
// path at all.
//
// What the guard buys here is narrower than on the converter path, and the test
// says so rather than overclaiming: the loop already recovers from a vanished
// candidate by trying the next one, so the outcome is unchanged. What changes is
// that the launch stops ANNOUNCING a load of a file that is gone and stops
// handing a ghost path to `podman load -i`, whose failure would then be reported
// as this image's failure rather than as someone else's reclaim.
//
// Delete the `if !fileExists(tarFile) { … continue }` guard and this fails: the
// evicted tar is handed to the loader and announced as being loaded.
func TestCachedTarFallbackSkipsATarEvictedAfterListing(t *testing.T) {
	bd := withBuildDir(t)
	cacheImages := filepath.Join(filepath.Dir(bd), "cache", "images")
	if err := os.MkdirAll(cacheImages, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two candidates. newer.tar is tried first; it is the one whose load triggers
	// the concurrent reclaim that takes older.tar out from under the loop.
	newer := filepath.Join(cacheImages, "newer.tar")
	older := filepath.Join(cacheImages, "older.tar")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("tar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := mustStat(t, older).ModTime()
	if err := os.Chtimes(older, old.Add(-time.Hour), old.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var ghosts []string // paths handed to the loader that did not exist
	var loaded []string
	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "podman",
		SkipBuild:      true, // degraded launch: this is the branch that reads tars
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return "", nil },
		Run: func(argv []string) (int, bool) {
			if len(argv) >= 4 && argv[1] == "load" && argv[2] == "-i" {
				p := argv[3]
				loaded = append(loaded, filepath.Base(p))
				if !fileExists(p) {
					// `podman load -i <missing>` cannot succeed; recording it is the
					// point — production must never get here.
					ghosts = append(ghosts, filepath.Base(p))
					return 125, true
				}
				if filepath.Base(p) == "newer.tar" {
					// THE RACE: prune's keep-N tail-drop takes the older tar while this
					// launch is still working through the list it took a moment ago.
					if err := os.Remove(older); err != nil {
						t.Fatal(err)
					}
					return 125, true // and this candidate is itself unusable
				}
				return 0, true
			}
			return 1, true // inspect: no image in the runtime
		},
	})

	if res.OK {
		t.Fatalf("both candidates were unusable, so the launch must fail\n%s", out.String())
	}
	if len(ghosts) != 0 {
		t.Errorf("handed %v to `podman load -i` after it had been reclaimed; a launch "+
			"must not report someone else's eviction as a load failure of its own", ghosts)
	}
	if len(loaded) != 1 || loaded[0] != "newer.tar" {
		t.Errorf("loader saw %v, want only [newer.tar]", loaded)
	}
	s := out.String()
	if !strings.Contains(s, "Skipping cached image older.tar") {
		t.Errorf("the evicted candidate was skipped silently: %q", s)
	}
	if strings.Contains(s, "Loading image from cache: older.tar") {
		t.Errorf("announced loading a tar that had been reclaimed: %q", s)
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
