package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withBuildDir points GLOBAL_STORAGE at a temp HOME so BuildDir()/GlobalCache()
// resolve under it, returning the build dir.
func withBuildDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	bd := filepath.Join(home, ".local", "share", "yolo-jail", "build")
	if err := os.MkdirAll(bd, 0o755); err != nil {
		t.Fatal(err)
	}
	return bd
}

func TestAutoLoadImageFreshLoad(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	f := newFakeRuntime()
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		Getpid:  func() int { return 4242 },
		BuildStorePath: func(repoRoot string, extra []any, outLink string) (string, []string) {
			return "/nix/store/abc-image", nil
		},
		Run: f.run,
		// C3: on podman the load is a PIPE, so there is no cache file to simulate.
		// Materialize is left nil on purpose — see c2Opts.
		StreamLoad: f.streamLoad,
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = false; out=%q", out.String())
	}
	if f.loads != 1 {
		t.Errorf("expected exactly one load command, got %d", f.loads)
	}
	if !strings.Contains(out.String(), "first run") {
		t.Errorf("expected first-run message, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Done: loaded image") {
		t.Errorf("expected done message, got %q", out.String())
	}
	// A build that SUCCEEDED must say nothing about build failures. The
	// loud-failure report is worthless if it also fires on the happy path.
	if strings.Contains(out.String(), BuildFailedMarker) {
		t.Errorf("successful build reported a build failure: %q", out.String())
	}
}

// TestAutoLoadImageAlreadyLoaded: the image for THIS store path is present in
// the runtime, so nothing is materialized and nothing is said.
//
// The runtime store answers for the CONTENT REF only — deliberately. The
// previous fixture returned rc 0 to every inspect, so it passed identically
// whether production asked about the content ref, about :latest, or about a
// string nobody had ever loaded: the callee was pinned and the question was not.
func TestAutoLoadImageAlreadyLoaded(t *testing.T) {
	bd := withBuildDir(t)
	storePath := "/nix/store/xyz-image"
	// The sentinel still names the path — and is now IRRELEVANT to the decision.
	// It stays in the fixture so that a regression which re-promotes it to
	// authority is not accidentally satisfied by an empty file.
	sentinel := filepath.Join(bd, "last-load-podman")
	if err := os.WriteFile(sentinel, []byte(storePath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	materialized, streamed := false, false
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return storePath, nil
		},
		Run: newFakeRuntime(JailImageRef("podman", storePath)).run,
		Materialize: func(string, string) int64 {
			materialized = true
			return 1
		},
		StreamLoad: func(string, string, []string) (int64, bool) {
			streamed = true
			return 1, true
		},
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = false; out=%q", out.String())
	}
	if materialized {
		t.Error("should not materialize when already loaded + present")
	}
	if streamed {
		t.Error("should not stream when already loaded + present")
	}
	if strings.Contains(out.String(), "Image load needed") {
		t.Errorf("unexpected load-needed message: %q", out.String())
	}
	if strings.Contains(out.String(), BuildFailedMarker) {
		t.Errorf("successful build reported a build failure: %q", out.String())
	}
}

// TestRevertedConfigReusesItsOwnImageInsteadOfReloading is the INVERSION of the
// regression for https://github.com/mschulkind-oss/yolo-jail/issues/35, kept
// rather than deleted because it is where the argument for C2 is measurable.
//
// The original scenario: nix builds are content-addressed, so reverting a config
// change (removing then re-adding a package) can reproduce a store path that is
// still sitting in the last-10-loads sentinel history, even though a *different*,
// newer path has since become the runtime's actual :latest image. Under one tag
// for every image the only safe answer was to RELOAD path A — checking mere
// membership in the history would wrongly conclude "already loaded" and leave
// :latest silently stale. The old test asserted that third load, and it was
// correct for the code it described.
//
// Content addressing dissolves the scenario. Path A's image is still in the
// runtime under `yolo-jail:<sha16(A)>`, untouched by B's load, so reverting is
// free: the correct answer is now 2 loads, not 3. What made the old answer
// necessary — not knowing what a tag points at — is no longer representable.
//
// This test fails if someone reintroduces a tag-and-sentinel decision, in either
// of its forms: the equality form reloads A (3 loads), and the LRU-membership
// form skips B's own recognition and runs a stale image.
func TestRevertedConfigReusesItsOwnImageInsteadOfReloading(t *testing.T) {
	bd := withBuildDir(t)
	sentinel := filepath.Join(bd, "last-load-podman")

	pathA := "/nix/store/path-A-image"
	pathB := "/nix/store/path-B-image"
	f := newFakeRuntime()

	makeOpts := func(storePath string) AutoLoadOptions {
		return AutoLoadOptions{
			Runtime: "podman",
			Out:     &bytes.Buffer{},
			BuildStorePath: func(string, []any, string) (string, []string) {
				return storePath, nil
			},
			Run:        f.run,
			StreamLoad: f.streamLoad,
		}
	}

	// Step 1: path A builds and loads under its own content ref.
	refA := AutoLoadImage(makeOpts(pathA))
	if !refA.OK {
		t.Fatal("step 1: AutoLoadImage returned false")
	}
	if f.loads != 1 {
		t.Fatalf("step 1: expected 1 load, got %d", f.loads)
	}

	// Step 2: config changes, path B builds and loads under ITS content ref. This
	// is what used to move :latest away from A.
	refB := AutoLoadImage(makeOpts(pathB))
	if !refB.OK {
		t.Fatal("step 2: AutoLoadImage returned false")
	}
	if f.loads != 2 {
		t.Fatalf("step 2: expected 2 loads total, got %d", f.loads)
	}

	// The premise of the original bug still holds: A is in the sentinel history.
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), pathA) {
		t.Fatalf("expected sentinel history to still contain path A: %q", string(data))
	}
	// And the fact that dissolves it: A's image is still in the runtime, under a
	// name B could not have taken.
	if f.present[refA.Ref] == "" || refA.Ref == refB.Ref {
		t.Fatalf("A's image no longer has a name of its own: A=%q B=%q present=%v",
			refA.Ref, refB.Ref, f.present)
	}

	// Step 3: config reverts, nix reproduces path A. Nothing to load.
	again := AutoLoadImage(makeOpts(pathA))
	if !again.OK {
		t.Fatal("step 3: AutoLoadImage returned false")
	}
	if f.loads != 2 {
		t.Fatalf("step 3: reverting reloaded path A (%d loads total); its image was "+
			"never displaced, so the reload the pre-C2 code needed is now waste", f.loads)
	}
	if again.Ref != refA.Ref {
		t.Fatalf("step 3 ran %q, want A's own ref %q", again.Ref, refA.Ref)
	}

	last, ok := CurrentLoadedPath(sentinel)
	if !ok || last != pathA {
		t.Fatalf("expected sentinel's most-recent entry to be path A, got %q (ok=%v)", last, ok)
	}
}

// The storage-lifecycle §1 root is registered for the store path we run against
// on the fresh-load path and the already-loaded self-heal path, but NOT on the
// degraded "Using existing"/cached-tar fallbacks (currentPath is unknown there,
// so there is no store path to root).
func TestAutoLoadImageRegistersRoot(t *testing.T) {
	// (a) fresh load → root registered with the built store path.
	withBuildDir(t)
	var rooted []string
	fresh := newFakeRuntime()
	optsFresh := AutoLoadOptions{
		Runtime:        "podman",
		Out:            &bytes.Buffer{},
		BuildStorePath: func(string, []any, string) (string, []string) { return "/nix/store/abc-image", nil },
		Run:            fresh.run, // nothing loaded → triggers a load
		StreamLoad:     fresh.streamLoad,
		RegisterRoot:   func(p string) { rooted = append(rooted, p) },
	}
	if !AutoLoadImage(optsFresh).OK {
		t.Fatal("fresh load = false")
	}
	if len(rooted) != 1 || rooted[0] != "/nix/store/abc-image" {
		t.Errorf("fresh load rooted %v, want [/nix/store/abc-image]", rooted)
	}

	// (b) build fails but the operator opted into a stale launch, existing image
	// present → NO root (store path unknown). The escape hatch is what keeps this
	// branch reachable at all now that a failed build is otherwise fatal.
	withBuildDir(t)
	rooted = nil
	optsExisting := AutoLoadOptions{
		Runtime:        "podman",
		Out:            &bytes.Buffer{},
		BuildStorePath: func(string, []any, string) (string, []string) { return "", []string{"boom"} },
		Run:            func(argv []string) (int, bool) { return 0, true }, // inspect present
		RegisterRoot:   func(p string) { rooted = append(rooted, p) },
		LookupEnv:      allowStaleEnv,
	}
	if !AutoLoadImage(optsExisting).OK {
		t.Fatal("using-existing = false")
	}
	if len(rooted) != 0 {
		t.Errorf("degraded using-existing rooted %v, want none (no known store path)", rooted)
	}
}

// allowStaleEnv / denyStaleEnv are the LookupEnv seam's two answers, so a test's
// intent about the escape hatch is visible at the call site.
func allowStaleEnv(key string) (string, bool) {
	if key == StaleImageEnv {
		return "1", true
	}
	return "", false
}

func denyStaleEnv(string) (string, bool) { return "", false }

// TestAutoLoadImageBuildFailureIsFatalEvenWithAnExistingImage is THE regression
// for this defect. A build ran, it failed, and an image happens to be sitting in
// the runtime — the old code printed "Using existing yolo-jail:latest image."
// and returned true, handing the developer a working-looking jail running code
// that was not theirs, with nix's error discarded. That silent fallback is what
// made a nix failure surface, two layers away, as a lib-farm assertion.
func TestAutoLoadImageBuildFailureIsFatalEvenWithAnExistingImage(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"error: builder for '/nix/store/aaa.drv' failed"}
		},
		Run:       func(argv []string) (int, bool) { return 0, true }, // inspect: image IS present
		LookupEnv: denyStaleEnv,
	}
	if AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = true after a FAILED build; a run must not look "+
			"successful while silently stale\n%s", out.String())
	}
	s := out.String()
	if !strings.Contains(s, BuildFailedMarker) {
		t.Errorf("failed build was not announced: %q", s)
	}
	if !strings.Contains(s, "error: builder for '/nix/store/aaa.drv' failed") {
		t.Errorf("nix's own output never reached the human: %q", s)
	}
	if strings.Contains(s, "Using existing") {
		t.Errorf("still falling back to the existing image: %q", s)
	}
}

// TestAutoLoadImageBuildFailureEscapeHatchIsLoud: with the escape hatch set the
// launch proceeds on the existing image (offline, out of disk, bisecting a
// host-side-only change) — but it must STILL report the failure and state the
// staleness. Opting in buys continuation, never silence.
func TestAutoLoadImageBuildFailureEscapeHatchIsLoud(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"error: connection to cache.nixos.org timed out"}
		},
		Run:       func(argv []string) (int, bool) { return 0, true }, // inspect present
		LookupEnv: allowStaleEnv,
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = false; the escape hatch must allow the launch\n%s", out.String())
	}
	s := out.String()
	if !strings.Contains(s, BuildFailedMarker) {
		t.Errorf("escape hatch went SILENT — the exact defect: %q", s)
	}
	if !strings.Contains(s, "error: connection to cache.nixos.org timed out") {
		t.Errorf("nix's own output never reached the human: %q", s)
	}
	if !strings.Contains(s, "STALE") {
		t.Errorf("continuing without stating the staleness: %q", s)
	}
	if !strings.Contains(s, "Using existing") {
		t.Errorf("expected the fallback to still happen: %q", s)
	}
}

// TestAutoLoadImageBuildFailureFatalWithNoImageAtAll: nothing to fall back to
// either. The report is still emitted (nix's words are the point), and the
// return is false as before.
func TestAutoLoadImageBuildFailureEscapeHatchWithNothingCached(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"error: out of disk space"}
		},
		Run:       func(argv []string) (int, bool) { return 1, true }, // inspect: absent
		LookupEnv: allowStaleEnv,
	}
	if AutoLoadImage(opts).OK {
		t.Fatal("AutoLoadImage = true with no image anywhere")
	}
	s := out.String()
	if !strings.Contains(s, BuildFailedMarker) {
		t.Errorf("failed build was not announced: %q", s)
	}
	if !strings.Contains(s, "Cannot start jail") {
		t.Errorf("expected a cannot-start line, got %q", s)
	}
}

// TestAutoLoadOffloadInvokedOnMacOS: when the plain build fails on macOS, the
// container-builder offload (J3) is tried; its success feeds the normal load
// path. On Linux the offload must NOT be consulted.
func TestAutoLoadOffloadInvokedOnMacOS(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	offloadCalled := false
	offloaded := newFakeRuntime()
	opts := AutoLoadOptions{
		Runtime: "podman",
		IsMacOS: true,
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"needs linux"} // plain build fails
		},
		BuildOffload: func(string, []any, string) (string, []string) {
			offloadCalled = true
			return "/nix/store/offloaded", nil // offload succeeds
		},
		Run:        offloaded.run, // nothing loaded → the offload's image loads
		StreamLoad: offloaded.streamLoad,
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = false; want true (offload built the image)\n%s", out.String())
	}
	if !offloadCalled {
		t.Error("BuildOffload was not invoked on a macOS build failure")
	}
}

func TestAutoLoadOffloadSkippedOnLinux(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	offloadCalled := false
	opts := AutoLoadOptions{
		Runtime: "podman",
		IsMacOS: false, // Linux
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"boom"}
		},
		BuildOffload: func(string, []any, string) (string, []string) {
			offloadCalled = true
			return "/nix/store/x", nil
		},
		Run:             func(argv []string) (int, bool) { return 1, true },
		DiagnoseFailure: func([]string) (string, string) { return "t", "r" },
		LookupEnv:       denyStaleEnv,
	}
	if AutoLoadImage(opts).OK {
		t.Fatal("AutoLoadImage = true; want false on Linux (no offload)")
	}
	if offloadCalled {
		t.Error("BuildOffload must NOT be invoked on Linux")
	}
}

func TestAutoLoadImageBuildFailsNoImage(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"nix: dependency failed"}
		},
		Run: func(argv []string) (int, bool) { return 1, true }, // inspect: not present
		DiagnoseFailure: func(tail []string) (string, string) {
			return "needs a Linux builder", "do the thing"
		},
		LookupEnv: denyStaleEnv,
	}
	if AutoLoadImage(opts).OK {
		t.Fatal("AutoLoadImage = true; want false (no image, can't build)")
	}
	s := out.String()
	if !strings.Contains(s, "Cannot start jail: needs a Linux builder.") {
		t.Errorf("missing diagnosis title: %q", s)
	}
	if !strings.Contains(s, "do the thing") {
		t.Errorf("missing remedy: %q", s)
	}
	if !strings.Contains(s, BuildFailedMarker) {
		t.Errorf("missing the failed-build marker: %q", s)
	}
	if !strings.Contains(s, "nix: dependency failed") {
		t.Errorf("nix's own output never reached the human: %q", s)
	}
}

// TestAutoLoadSkipBuildUsesExisting is the D2 (graceful launch degradation)
// regression: when repo-root resolution fails, the run slice sets SkipBuild so
// AutoLoadImage never shells out to `nix build` in an empty cwd (which would
// error or, worse, build against the process's own cwd). With an image already
// present in the runtime it must proceed on it — never invoking BuildStorePath.
func TestAutoLoadSkipBuildUsesExisting(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	buildCalled := false
	opts := AutoLoadOptions{
		Runtime:   "podman",
		SkipBuild: true,
		Out:       &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			buildCalled = true
			return "/nix/store/should-not-happen", nil
		},
		Run: func(argv []string) (int, bool) { return 0, true }, // inspect present
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = false; want true (existing image on degraded launch)\n%s", out.String())
	}
	if buildCalled {
		t.Error("BuildStorePath must NOT run when SkipBuild is set")
	}
	if !strings.Contains(out.String(), "Using existing") {
		t.Errorf("expected using-existing message, got %q", out.String())
	}
	// SkipBuild STAYS QUIET. A build that was never attempted has not failed, and
	// a warning here would train the reader to ignore the one that matters.
	if strings.Contains(out.String(), BuildFailedMarker) {
		t.Errorf("SkipBuild reported a build failure though no build ran: %q", out.String())
	}
}

// TestAutoLoadSkipBuildLoadsCachedTar: degraded launch with no runtime image
// but a cached tar present must load the cache — again without building.
func TestAutoLoadSkipBuildLoadsCachedTar(t *testing.T) {
	bd := withBuildDir(t)
	// Drop a cached tar into GlobalCache()/images (sibling of the build dir).
	cacheImages := filepath.Join(filepath.Dir(bd), "cache", "images")
	if err := os.MkdirAll(cacheImages, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheImages, "jail.tar"), []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	buildCalled, loaded := false, false
	opts := AutoLoadOptions{
		Runtime:   "podman",
		SkipBuild: true,
		Out:       &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			buildCalled = true
			return "", nil
		},
		Run: func(argv []string) (int, bool) {
			if len(argv) >= 2 && argv[1] == "load" {
				loaded = true
				return 0, true
			}
			return 1, true // inspect: not present
		},
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("AutoLoadImage = false; want true (cached tar on degraded launch)\n%s", out.String())
	}
	if buildCalled {
		t.Error("BuildStorePath must NOT run when SkipBuild is set")
	}
	if !loaded {
		t.Error("expected a load command for the cached tar")
	}
}

// TestAutoLoadSkipBuildNoImageFails: degraded launch with neither a runtime
// image nor a cached tar can't conjure one — must fail with the degraded
// diagnosis (not a nix-build diagnosis, since no build was attempted).
func TestAutoLoadSkipBuildNoImageFails(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	buildCalled := false
	opts := AutoLoadOptions{
		Runtime:   "podman",
		SkipBuild: true,
		Out:       &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			buildCalled = true
			return "", nil
		},
		Run: func(argv []string) (int, bool) { return 1, true }, // inspect: not present
	}
	if AutoLoadImage(opts).OK {
		t.Fatal("AutoLoadImage = true; want false (degraded, no image available)")
	}
	if buildCalled {
		t.Error("BuildStorePath must NOT run when SkipBuild is set")
	}
	if !strings.Contains(out.String(), "Cannot start jail") {
		t.Errorf("expected a cannot-start diagnosis, got %q", out.String())
	}
	// Still quiet about build failures: none was attempted, so the degraded
	// diagnosis is the whole truth here.
	if strings.Contains(out.String(), BuildFailedMarker) {
		t.Errorf("SkipBuild reported a build failure though no build ran: %q", out.String())
	}
}
