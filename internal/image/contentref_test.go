package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRuntime models the one thing C2 moved the load decision onto: a container
// runtime's TAG-KEYED image store. `image inspect <ref>` answers for a ref and
// no other, `load` creates the image the archive's RepoTags name, and `tag`
// points one name at whatever image another name already holds.
//
// The old fixtures answered rc 0 to EVERY inspect, which is why several of them
// passed identically whether production asked about a content ref, about :latest
// or about a string nobody had ever loaded. A store keyed by ref is what makes
// "is THIS image present" a question a test can get wrong.
//
// It maps ref → IMAGE IDENTITY rather than ref → bool, because the defect this
// file exists to keep out is not "the ref is missing" but "the ref names the
// WRONG image": a tag sourced from the shared :latest name binds this config's
// ref to whatever a concurrent launch loaded last, permanently. A set of names
// cannot tell those two apart; a map to identities can.
type fakeRuntime struct {
	// present maps a ref to the identity of the image it names ("" = absent).
	present map[string]string
	// loads counts `load` invocations — the number C2 exists to drive to zero on
	// a repeat launch.
	loads int
	// tagFails makes every `tag` fail, for the degraded-naming path.
	tagFails bool
	// argv records every command in order, so ORDERING (load before tag) is
	// assertable and not merely assumed.
	argv [][]string
	// streamedPaths records the store paths handed to the C3 StreamLoad seam,
	// streamedTags the RepoTags name each was told to write into the archive, and
	// streamedArgv the loader argv each was piped into.
	streamedPaths []string
	streamedTags  []string
	streamedArgv  [][]string
	// pendingRepoTag is the name the NEXT `load` will create, set by streamLoad
	// from the repoTag it was handed — the fake's stand-in for the archive's
	// manifest. Empty means the archive carries the flake's baked name.
	pendingRepoTag string
}

// newFakeRuntime seeds the store with refs, each naming an image of its own.
func newFakeRuntime(loaded ...string) *fakeRuntime {
	f := &fakeRuntime{present: map[string]string{}}
	for _, ref := range loaded {
		f.present[ref] = "img-" + ref
	}
	return f
}

// run is the AutoLoadOptions.Run seam.
func (f *fakeRuntime) run(argv []string) (int, bool) {
	f.argv = append(f.argv, append([]string(nil), argv...))
	switch {
	case len(argv) >= 4 && argv[1] == "image" && argv[2] == "inspect":
		if f.present[argv[3]] != "" {
			return 0, true
		}
		return 1, true
	case len(argv) >= 2 && (argv[1] == "load" || (argv[1] == "image" && len(argv) >= 3 && argv[2] == "load")):
		f.loads++
		// The archive decides the name. Since C2 the stream is invoked with
		// `--repo_tag <content ref>`, so that is what lands — verified end-to-end
		// against a real podman on 2026-08-25. A `load -i <tar>` of an archive
		// nobody overrode (the build-failure fallback) still lands on the flake's
		// baked :latest.
		ref := JailImage(argv[0])
		if f.pendingRepoTag != "" {
			ref = qualifyRef(f.pendingRepoTag)
		}
		f.present[ref] = "img-from-" + ref
		return 0, true
	case len(argv) >= 4 && argv[1] == "tag":
		if f.tagFails || f.present[argv[2]] == "" {
			return 1, true
		}
		// A tag is a NAME for an image, so the destination inherits the SOURCE's
		// identity. That is the whole mechanism by which a tag sourced from a
		// shared name can bind a ref to a foreign image.
		f.present[argv[3]] = f.present[argv[2]]
		return 0, true
	}
	return 1, true
}

// streamLoad is the AutoLoadOptions.StreamLoad seam (C3): the podman happy path
// pipes the nix image stream straight into `<runtime> load` and writes NO tar.
// It records what it was handed and then mutates the store exactly as a real
// `podman load` of that archive would, so every C2 assertion about loads, tags
// and refs keeps its meaning.
func (f *fakeRuntime) streamLoad(storePath, repoTag string, loadArgv []string) (int64, bool) {
	f.streamedPaths = append(f.streamedPaths, storePath)
	f.streamedTags = append(f.streamedTags, repoTag)
	f.streamedArgv = append(f.streamedArgv, append([]string(nil), loadArgv...))
	f.pendingRepoTag = repoTag
	rc, ran := f.run(loadArgv)
	f.pendingRepoTag = ""
	return 4096, ran && rc == 0
}

// cmds renders the recorded argv as "verb ..." strings for order assertions.
func (f *fakeRuntime) cmds() []string {
	var out []string
	for _, a := range f.argv {
		out = append(out, strings.Join(a, " "))
	}
	return out
}

// writeTar is the Materialize seam: produce the cache file AutoLoadImage expects.
// Since C3 only the Apple Container path reaches it.
func writeTar(_, cacheFile string) int64 {
	_ = os.WriteFile(cacheFile, []byte("tar"), 0o644)
	return 1024
}

// c2Opts is the podman fixture. Materialize is deliberately left NIL: on podman
// nothing may materialize a tar since C3, so a regression that reinstates it
// falls through to the real seam, execs a store path that does not exist, and
// fails loudly instead of quietly writing a file.
func c2Opts(rt string, storePath string, f *fakeRuntime, out *bytes.Buffer) AutoLoadOptions {
	return AutoLoadOptions{
		Runtime:        rt,
		Out:            out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		StreamLoad:     f.streamLoad,
	}
}

// TestJailImageRefIsContentAddressedPerRuntime pins the SHAPE of the ref, not
// the literal: the maintainer's ruling is that the image tag is not a public
// surface, so what has to hold is that the ref names the store path's content
// and spells itself the way each runtime wants.
func TestJailImageRefIsContentAddressedPerRuntime(t *testing.T) {
	const pathA = "/nix/store/aaaa-image"
	key := ImageStoreKey(pathA)
	if len(key) != 16 {
		t.Fatalf("ImageStoreKey = %q, want 16 hex chars", key)
	}
	if got, want := JailImageRef("podman", pathA), "localhost/yolo-jail:"+key; got != want {
		t.Errorf("podman ref = %q, want %q", got, want)
	}
	// Apple Container's CLI does not carry the localhost/ prefix.
	if got, want := JailImageRef("container", pathA), "yolo-jail:"+key; got != want {
		t.Errorf("container ref = %q, want %q", got, want)
	}
	// The ref reuses the SAME key as the cache tar and the GC root. A second hash
	// function would let the three drift, which is why the doc says to reuse it.
	tar, err := ImageCachePath(pathA)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(tar) != key+".tar" {
		t.Errorf("cache tar %q does not share the ref's key %q", filepath.Base(tar), key)
	}
	// Distinct store paths must be distinct refs, or C2 buys nothing.
	if JailImageRef("podman", pathA) == JailImageRef("podman", "/nix/store/bbbb-image") {
		t.Error("two store paths produced one ref")
	}
	// The legacy ref survives for the branches with no store path — and is NOT
	// what a content-addressed launch runs.
	if JailImageRef("podman", pathA) == JailImage("podman") {
		t.Error("the content ref collided with the legacy :latest ref")
	}
}

// TestSecondLaunchOnUnchangedStorePathLoadsNothing is C2's whole point stated as
// a test: relaunching the same workspace must cost an `image inspect`, not a
// 3.28 GiB load.
//
// It fails if the load decision goes back to consulting the sentinel and the
// legacy tag, because the fake runtime's store answers for the content ref only.
func TestSecondLaunchOnUnchangedStorePathLoadsNothing(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/steady-image"
	f := newFakeRuntime()
	var out bytes.Buffer

	first := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !first.OK {
		t.Fatalf("first launch failed: %s", out.String())
	}
	if f.loads != 1 {
		t.Fatalf("first launch performed %d loads, want 1", f.loads)
	}
	if first.Ref != JailImageRef("podman", storePath) {
		t.Errorf("first launch ref = %q, want the content ref %q",
			first.Ref, JailImageRef("podman", storePath))
	}

	out.Reset()
	second := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !second.OK {
		t.Fatalf("second launch failed: %s", out.String())
	}
	if f.loads != 1 {
		t.Errorf("second launch performed a load (%d total) — the image was already "+
			"present under its content ref, so nothing needed loading", f.loads)
	}
	if strings.Contains(out.String(), "Image load needed") {
		t.Errorf("second launch announced a load: %q", out.String())
	}
	if second.Ref != first.Ref {
		t.Errorf("ref moved between launches: %q then %q", first.Ref, second.Ref)
	}
}

// TestAlternatingStorePathsEachStayLoaded is the cross-workspace thrash fix
// (§1.5): two configs, two store paths, alternating launches. Each keeps its own
// image, so after the initial two loads NOTHING loads again — including the
// revert-to-A step that the pre-C2 code had to reload by construction, because
// it could only ever recognise the single most recent image.
func TestAlternatingStorePathsEachStayLoaded(t *testing.T) {
	withBuildDir(t)
	const pathA = "/nix/store/path-A-image"
	const pathB = "/nix/store/path-B-image"
	f := newFakeRuntime()
	var out bytes.Buffer

	a1 := AutoLoadImage(c2Opts("podman", pathA, f, &out))
	b1 := AutoLoadImage(c2Opts("podman", pathB, f, &out))
	if !a1.OK || !b1.OK {
		t.Fatalf("initial launches failed: %s", out.String())
	}
	if f.loads != 2 {
		t.Fatalf("expected 2 loads for 2 distinct store paths, got %d", f.loads)
	}
	if a1.Ref == b1.Ref {
		t.Fatalf("both configs got the same ref %q", a1.Ref)
	}

	// Each ref is INDEPENDENTLY detectable: the store holds both names at once.
	for _, ref := range []string{a1.Ref, b1.Ref} {
		if f.present[ref] == "" {
			t.Errorf("%q is not loaded; both images must coexist", ref)
		}
	}

	out.Reset()
	a2 := AutoLoadImage(c2Opts("podman", pathA, f, &out))
	b2 := AutoLoadImage(c2Opts("podman", pathB, f, &out))
	if !a2.OK || !b2.OK {
		t.Fatalf("alternating launches failed: %s", out.String())
	}
	if f.loads != 2 {
		t.Errorf("alternating between two workspaces reloaded (%d loads total) — "+
			"this is the §1.5 thrash C2 removes", f.loads)
	}
	if a2.Ref != a1.Ref || b2.Ref != b1.Ref {
		t.Errorf("refs are not stable per store path: A %q→%q, B %q→%q",
			a1.Ref, a2.Ref, b1.Ref, b2.Ref)
	}
}

// TestTheImageIsNamedOnTheWayIn pins WHERE the content ref comes from. The flake
// bakes tag="latest", so an un-overridden stream cannot produce a
// content-addressed name — C2 overrides it, handing the stream the RepoTags the
// archive must carry, and `podman load` then creates the image under exactly
// that. Nothing binds the ref afterwards, which is what removes the race.
func TestTheImageIsNamedOnTheWayIn(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/tagme-image"
	f := newFakeRuntime()
	var out bytes.Buffer

	res := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !res.OK {
		t.Fatalf("load failed: %s", out.String())
	}
	contentRef := JailImageRef("podman", storePath)

	// The stream was told the name, and the name it was told is the one podman
	// qualifies into the ref the caller is handed. These are two values that MUST
	// agree; before C2 named the image going in, they could not even be compared.
	if len(f.streamedTags) != 1 || f.streamedTags[0] != StreamRepoTag(storePath) {
		t.Fatalf("stream got RepoTags %v, want [%s]", f.streamedTags, StreamRepoTag(storePath))
	}
	if qualifyRef(f.streamedTags[0]) != contentRef {
		t.Errorf("the streamed name %q does not qualify to the returned ref %q",
			f.streamedTags[0], contentRef)
	}
	if f.present[contentRef] == "" {
		t.Errorf("the load did not create %q: %v", contentRef, f.present)
	}
	if res.Ref != contentRef {
		t.Errorf("ref = %q, want %q", res.Ref, contentRef)
	}
	if res.StorePath != storePath {
		t.Errorf("StorePath = %q, want %q", res.StorePath, storePath)
	}

	// NOTHING MAY READ :latest AS A TAG SOURCE. That is the reverted direction,
	// and it is the defect: `tag :latest <contentRef>` binds this config's
	// permanent name to whatever another workspace's load left on the shared tag.
	for _, c := range f.cmds() {
		if strings.HasPrefix(c, "podman tag "+JailImage("podman")+" ") {
			t.Errorf("%q sources the content ref from the shared :latest name", c)
		}
	}
	// The legacy tag is still MOVED (downstream), after the load, so the degraded
	// fallback branch — which has no store path and can only ask about :latest —
	// still finds an image.
	wantTag := "podman tag " + contentRef + " " + JailImage("podman")
	loadAt, tagAt := -1, -1
	for i, c := range f.cmds() {
		// "podman load" exactly since C3 (stdin), "podman load -i <tar>" before it.
		if strings.HasPrefix(c, "podman load") && loadAt < 0 {
			loadAt = i
		}
		if c == wantTag {
			tagAt = i
		}
	}
	if tagAt < 0 {
		t.Fatalf("no %q in %v — :latest stopped tracking the newest load", wantTag, f.cmds())
	}
	if loadAt < 0 || tagAt < loadAt {
		t.Errorf("tag ran at %d, load at %d — the source ref does not exist until the "+
			"load has run", tagAt, loadAt)
	}
	if f.present[JailImage("podman")] != f.present[contentRef] {
		t.Error("the legacy :latest ref does not name the image just loaded; the " +
			"fallback branch depends on it")
	}
}

// TestAConcurrentLoadCannotStealTheContentRef is the race stated as a test.
//
// Another workspace's image is already on :latest when this launch starts —
// which is the steady state on any machine running more than one config, and the
// state a concurrent load leaves behind mid-launch. The content ref must name
// the image THIS launch streamed and no other, permanently: a mis-binding is not
// self-correcting, because the next launch finds the ref present and skips the
// load entirely.
//
// Source the tag from :latest again and this fails on the identity comparison,
// not on a missing name — which is exactly the failure a set-of-names fixture
// could not see.
func TestAConcurrentLoadCannotStealTheContentRef(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/mine-image"
	f := newFakeRuntime()
	// A foreign image holds the shared name before we start.
	f.present[JailImage("podman")] = "foreign-workspace-image"
	var out bytes.Buffer

	res := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !res.OK {
		t.Fatalf("load failed: %s", out.String())
	}
	if f.present[res.Ref] == "foreign-workspace-image" {
		t.Fatalf("%q was bound to the image another workspace left on %q — and a tag "+
			"is permanent, so every later launch of this config runs it",
			res.Ref, JailImage("podman"))
	}
	if f.present[res.Ref] == "" {
		t.Fatalf("%q names no image at all: %v", res.Ref, f.present)
	}

	// And the relaunch really does skip the load, which is what makes a
	// mis-binding permanent rather than transient — the property that turns this
	// from a cosmetic race into a correctness one.
	out.Reset()
	again := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !again.OK || f.loads != 1 {
		t.Fatalf("relaunch: OK=%v loads=%d, want true/1: %s", again.OK, f.loads, out.String())
	}
	if f.present[again.Ref] != f.present[res.Ref] {
		t.Errorf("the ref moved to a different image between launches")
	}
}

// TestLatestTagFailureStillRunsTheContentRef: pointing :latest at the new image
// is best effort, and its failure must not touch what this launch runs. Before
// the direction was fixed a failed tag meant the content ref did not exist and
// the launch had to DOWNGRADE to the legacy name; now the image is already named
// by the load, so the only casualty is the alias — said out loud, because
// degrading in silence is the defect C1 exists to prevent.
func TestLatestTagFailureStillRunsTheContentRef(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/untaggable-image"
	f := newFakeRuntime()
	f.tagFails = true
	var out bytes.Buffer

	res := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !res.OK {
		t.Fatalf("a failed TAG must not fail the launch — the image is loaded: %s", out.String())
	}
	if res.Ref != JailImageRef("podman", storePath) {
		t.Errorf("ref = %q; the load named the image, so a failed alias cannot change "+
			"it (want %q)", res.Ref, JailImageRef("podman", storePath))
	}
	if !strings.Contains(out.String(), "could not point") {
		t.Errorf("the failed alias was silent: %q", out.String())
	}
}

// TestFallbackBranchUsesTheLegacyRef: with no store path there is nothing to
// hash, so both degraded fallbacks must report the legacy ref — a content ref
// invented from nothing would name an image that does not exist, and the launch
// would fail on a name rather than on the build failure that caused it.
func TestFallbackBranchUsesTheLegacyRef(t *testing.T) {
	t.Run("existing image", func(t *testing.T) {
		withBuildDir(t)
		f := newFakeRuntime(JailImage("podman"))
		var out bytes.Buffer
		res := AutoLoadImage(AutoLoadOptions{
			Runtime:        "podman",
			SkipBuild:      true,
			Out:            &out,
			BuildStorePath: func(string, []any, string) (string, []string) { return "", nil },
			Run:            f.run,
		})
		if !res.OK {
			t.Fatalf("degraded launch failed: %s", out.String())
		}
		if res.Ref != JailImage("podman") {
			t.Errorf("ref = %q, want the legacy %q", res.Ref, JailImage("podman"))
		}
		if res.StorePath != "" {
			t.Errorf("StorePath = %q; the degraded branch knows none", res.StorePath)
		}
	})

	t.Run("cached tar", func(t *testing.T) {
		bd := withBuildDir(t)
		cacheImages := filepath.Join(filepath.Dir(bd), "cache", "images")
		if err := os.MkdirAll(cacheImages, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheImages, "jail.tar"), []byte("tar"), 0o644); err != nil {
			t.Fatal(err)
		}
		f := newFakeRuntime() // nothing loaded → falls through to the tar
		var out bytes.Buffer
		res := AutoLoadImage(AutoLoadOptions{
			Runtime:        "podman",
			SkipBuild:      true,
			Out:            &out,
			BuildStorePath: func(string, []any, string) (string, []string) { return "", nil },
			Run:            f.run,
		})
		if !res.OK {
			t.Fatalf("cached-tar launch failed: %s", out.String())
		}
		if res.Ref != JailImage("podman") {
			t.Errorf("ref = %q, want the legacy %q — the tar's baked RepoTags are what "+
				"the load produced", res.Ref, JailImage("podman"))
		}
	})
}

// TestAppleContainerIsNamedGoingIn closes the gap this work's survey measured:
// before it, ZERO tests drove Runtime "container" through AutoLoadImage, so the
// LoadAppleContainer seam's two production call sites were both unpinned and a
// change to either could break the backend with the whole unit gate green.
//
// Apple Container cannot be retagged after the fact here — its converters write
// the name into the OCI archive — so the ref has to arrive WITH the tar. Delete
// the `Runtime == "container"` arm and this fails either because the seam is
// never called or because the ref it receives is the podman spelling.
func TestAppleContainerIsNamedGoingIn(t *testing.T) {
	withBuildDir(t)
	const storePath = "/nix/store/ac-image"
	var gotTar, gotRef string
	var out bytes.Buffer
	f := newFakeRuntime()

	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "container",
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		Materialize:    writeTar,
		LoadAppleContainer: func(tarPath, ref string) bool {
			gotTar, gotRef = tarPath, ref
			return true
		},
	})
	if !res.OK {
		t.Fatalf("apple container load failed: %s", out.String())
	}
	wantRef := JailImageRef("container", storePath)
	if gotRef != wantRef {
		t.Errorf("converter ref = %q, want the UNQUALIFIED content ref %q", gotRef, wantRef)
	}
	if res.Ref != wantRef {
		t.Errorf("result ref = %q, want %q", res.Ref, wantRef)
	}
	// The converters interpolate a real file path (skopeo's docker-archive:
	// source, podman's -i), so the tar must exist by the time the seam is called.
	if gotTar == "" {
		t.Fatal("LoadAppleContainer was never called")
	}
	if _, err := os.Stat(gotTar); err != nil {
		t.Errorf("LoadAppleContainer got %q, which does not exist: %v", gotTar, err)
	}
	// The podman-only retag must NOT have run on this backend.
	for _, c := range f.cmds() {
		if strings.Contains(c, " tag ") {
			t.Errorf("apple container path issued %q; it is named during conversion", c)
		}
	}
}

// TestSentinelIsRecordedOnEveryLaunchNotOnlyOnLoad pins a call site C2 had to
// MOVE, and whose absence is silent.
//
// prune.ProtectedImagePaths reads this sentinel to decide which store closures a
// `nix-collect-garbage` must not reap (guard #2 of PruneOrphanImageRoots' three).
// Before C2 "already loaded" implied the sentinel already named the path, so
// appending only on the load path was equivalent. It is not equivalent now: many
// images stay loaded, a launch can run one whose load was many launches ago, and
// leaving the append inside the load branch lets a LIVE jail's closure age out of
// the ten-entry LRU and lose its protection.
//
// Delete the AddLoadedPath call that sits beside RegisterRoot and this fails.
func TestSentinelIsRecordedOnEveryLaunchNotOnlyOnLoad(t *testing.T) {
	bd := withBuildDir(t)
	sentinel := filepath.Join(bd, "last-load-podman")
	const pathA = "/nix/store/live-A-image"
	const pathB = "/nix/store/live-B-image"
	f := newFakeRuntime()
	var out bytes.Buffer

	if !AutoLoadImage(c2Opts("podman", pathA, f, &out)).OK {
		t.Fatalf("A: %s", out.String())
	}
	if !AutoLoadImage(c2Opts("podman", pathB, f, &out)).OK {
		t.Fatalf("B: %s", out.String())
	}
	// Relaunch A. No load happens (its image is still there), but A is the image
	// this machine is now running, so it must become the most recent entry.
	if !AutoLoadImage(c2Opts("podman", pathA, f, &out)).OK {
		t.Fatalf("A again: %s", out.String())
	}
	if f.loads != 2 {
		t.Fatalf("the third launch loaded something (%d loads); the premise of this "+
			"test is that it does not", f.loads)
	}
	last, ok := CurrentLoadedPath(sentinel)
	if !ok || last != pathA {
		t.Errorf("sentinel's newest entry = %q (ok=%v), want %q — a no-load launch "+
			"still has to record what it is running, or prune stops protecting it",
			last, ok, pathA)
	}
	if _, protected := ReadLoadedPaths(sentinel)[pathB]; !protected {
		t.Error("path B fell out of the protected set; both images are loaded")
	}
}
