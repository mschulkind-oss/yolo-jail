package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file pins P4 — "no reclaimer may delete an artifact an in-flight launch is
// between steps on" (docs/design/minimal-disk-footprint.md §5 P4, sequenced as
// §10 step 2). The racer is `yolo prune --apply`'s keep-N tail-drop, which deletes
// cache/images/*.tar by mtime and knows nothing about which tar a launch is
// mid-flight on.
//
// EVERY TEST HERE MODELS THE EVICTION FROM INSIDE A SEAM, and that is the only
// honest place to put it. The window is between a `fileExists` and a converter
// subprocess that opens the same path, so no test can schedule a deletion "in
// between" from the outside — a goroutine racing a synchronous function would
// pass or fail by timing. Putting the `os.Remove` in the seam that stands in for
// the subprocess reproduces the exact observable production has to survive: the
// path was there when the launch looked, and gone when the converter opened it.
//
// There is NO container and NO real converter here on purpose (and none is
// available on Linux at all): the seams are the whole surface, and the artifact
// being raced is an ordinary file, which is the part that is real in these tests.

// stageTar writes a stand-in cache tar for storePath at the path production will
// compute for it, and returns that path. It is the "reused tar" precondition —
// the case §5 P4 names, where fileExists is TRUE so materialization is skipped
// and the file's only remaining job is to be handed to a converter.
func stageTar(t *testing.T, storePath string) string {
	t.Helper()
	p, err := ImageCachePath(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAppleContainerSurvivesATarEvictedMidLaunch is the race, closed.
//
// A tar for this store path is already on disk, so production skips
// materialization and hands the path straight to the converters. A concurrent
// `yolo prune --apply` evicts it in that window; the converter then fails the way
// skopeo and `podman save` really do against a missing archive. Before the guard
// this returned OK=false and the launch exited 1 on an artifact that was
// regenerable the whole time.
//
// Delete the recovery in loadAppleContainerFromCache — the retry, or the
// `if fileExists(cacheFile) { return false }` discriminator (autoload.go:669) that
// distinguishes a vanished tar from a broken one
// — and this fails on OK.
func TestAppleContainerSurvivesATarEvictedMidLaunch(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/raced-image"
	cacheFile := stageTar(t, storePath)

	var out bytes.Buffer
	materialized := 0
	var converterSaw []bool // whether the tar existed at each converter call
	f := newFakeRuntime()   // nothing loaded → a load is needed

	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "container",
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		Materialize: func(_, dst string) int64 {
			materialized++
			if err := os.WriteFile(dst, []byte("tar"), 0o644); err != nil {
				t.Fatal(err)
			}
			return 1024
		},
		LoadAppleContainer: func(tarPath, _ string) bool {
			if len(converterSaw) == 0 {
				// THE RACE. A concurrent prune lands between the launch's check and
				// this converter's exec.
				if err := os.Remove(tarPath); err != nil {
					t.Fatal(err)
				}
			}
			ok := fileExists(tarPath)
			converterSaw = append(converterSaw, ok)
			return ok // what a converter does with an archive that is not there
		},
	})

	if !res.OK {
		t.Fatalf("a concurrently-evicted tar failed the launch; the tar is regenerable "+
			"from the store path, so losing this race must cost a re-cache and nothing "+
			"more\n%s", out.String())
	}
	if res.Ref != JailImageRef("container", storePath) {
		t.Errorf("ref = %q, want the content ref %q", res.Ref, JailImageRef("container", storePath))
	}
	if len(converterSaw) != 2 || converterSaw[0] || !converterSaw[1] {
		t.Errorf("converter calls saw %v, want [false true] — one call against the "+
			"evicted tar, one against the re-cached one", converterSaw)
	}
	if materialized != 1 {
		t.Errorf("Materialize ran %d times, want exactly 1 (the recovery); the tar was "+
			"present when the launch started", materialized)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("the recovery did not leave the tar in place at %q: %v", cacheFile, err)
	}
	if !strings.Contains(out.String(), "was removed mid-launch") {
		t.Errorf("the launch recovered SILENTLY; a reclaim that cost this launch a "+
			"3.3 GiB re-cache has to be visible: %q", out.String())
	}
}

// TestAppleContainerConverterFailureIsStillFatal is the other polarity, and it is
// what keeps the guard from becoming a blanket retry: a converter that fails
// against a tar that is STILL THERE has failed on its own terms (a corrupt
// archive, no skopeo/podman, a broken `container image load`), and re-caching a
// file that never went missing would hide that behind a duplicated 3.3 GiB write.
//
// Invert the `if fileExists(cacheFile)` discriminator (autoload.go:669) or unbound the
// `pass < 2` loop (autoload.go:648) and this fails on
// the converter-call count.
func TestAppleContainerConverterFailureIsStillFatal(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/broken-archive-image"
	stageTar(t, storePath)

	var out bytes.Buffer
	materialized, converterCalls := 0, 0
	f := newFakeRuntime()

	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "container",
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		Materialize: func(_, dst string) int64 {
			materialized++
			_ = os.WriteFile(dst, []byte("tar"), 0o644)
			return 1024
		},
		// Fails WITHOUT removing the tar: the file is fine, the conversion is not.
		LoadAppleContainer: func(string, string) bool {
			converterCalls++
			return false
		},
	})

	if res.OK {
		t.Fatalf("a failed conversion reported a usable image\n%s", out.String())
	}
	if converterCalls != 1 {
		t.Errorf("converter ran %d times, want 1 — the tar never went missing, so there "+
			"is no race to recover from", converterCalls)
	}
	if materialized != 0 {
		t.Errorf("Materialize ran %d times; the tar was present throughout", materialized)
	}
	if strings.Contains(out.String(), "was removed mid-launch") {
		t.Errorf("a plain conversion failure was reported as an eviction: %q", out.String())
	}
}

// TestAppleContainerTarEvictionRetryIsBounded: the recovery is ONE retry, not a
// loop. A cache directory that cannot keep a file it was just handed (a full
// disk, a tmpfs being unmounted, a reclaimer in a tight loop) must produce a
// failure with a diagnosis, never a launch that spins re-materializing a
// multi-GB tar forever.
//
// Turn the retry into a `for` without the attempt bound and this test hangs
// rather than failing — which is why the assertion is on the call COUNT and the
// package's own test timeout is the backstop.
func TestAppleContainerTarEvictionRetryIsBounded(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/never-keeps-image"
	stageTar(t, storePath)

	var out bytes.Buffer
	materialized, converterCalls := 0, 0
	f := newFakeRuntime()

	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "container",
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		Materialize: func(_, dst string) int64 {
			materialized++
			_ = os.WriteFile(dst, []byte("tar"), 0o644)
			return 1024
		},
		// Every converter call finds the tar gone — the eviction never stops.
		LoadAppleContainer: func(tarPath, _ string) bool {
			converterCalls++
			_ = os.Remove(tarPath)
			return false
		},
	})

	if res.OK {
		t.Fatalf("a tar that never survives long enough to convert reported success\n%s", out.String())
	}
	if converterCalls > 2 {
		t.Errorf("converter ran %d times; the recovery is one retry, not a loop", converterCalls)
	}
	if materialized > 1 {
		t.Errorf("Materialize ran %d times; re-caching a multi-GB image in a loop is "+
			"worse than the failure it is trying to avoid", materialized)
	}
}

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
