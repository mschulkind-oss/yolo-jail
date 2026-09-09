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
	// runtime is which CLI this fake is standing in for, so `loadArchive` can
	// build the argv the real backend would (`container image load -i` vs
	// `podman load -i` — ImageLoadCmd is the one place that difference lives).
	runtime string
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
	// copiedManifests records the image.json paths handed to the C9 LayerCopy
	// seam and copiedDests the transport-qualified destinations each was told to
	// write. Together they are how a test asserts that the ref the pipeline
	// RETURNS is the ref it asked the copier to create.
	copiedManifests []string
	copiedDests     []string
	// copyFails makes every copy fail, for the no-fallback path.
	copyFails bool
	// ociFiles records the OCI archives the Apple Container path asked for, in
	// order — and whether each still existed when the loader ran, which is what
	// "the copy writes the file the loader takes" means as an assertion.
	ociFiles []string
	// pendingRef is the name the NEXT `load` will create, set by layerCopy from
	// the ref it wrote into the OCI archive — the fake's stand-in for the
	// archive's manifest. Empty means the archive carries the flake's baked name.
	pendingRef string
}

// storeManifest writes a minimal nix2container image.json under a temp dir and
// returns its path.
//
// IT EXISTS BECAUSE THE STORE PATH IS NOW A FILE. Since C9 `nix build .#ociImage`
// resolves to an image.json rather than to a stream script, and the delivery path
// READS it (ReadLayerInventory, for the copied/skipped report §3.10 requires). A
// fixture that hands AutoLoadImage a `/nix/store/…` string with nothing behind it
// is not modelling the input any more — it models a machine whose manifest is
// missing, which is a different test.
func storeManifest(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name+".json")
	body := `{"version":1,"arch":"amd64","created":"0001-01-01T00:00:00Z","layers":[` +
		`{"digest":"sha256:` + name + `-base","size":3000000000,"diff_ids":"sha256:` + name + `-base"},` +
		`{"digest":"sha256:` + name + `-top","size":27000000,"diff_ids":"sha256:` + name + `-top"}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// newFakeRuntime seeds the store with refs, each naming an image of its own.
func newFakeRuntime(loaded ...string) *fakeRuntime {
	f := &fakeRuntime{runtime: "podman", present: map[string]string{}}
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
		// The archive decides the name. Since C9 the copy writes the content ref
		// into the OCI archive it hands this loader, so that is what lands. A
		// `load -i <tar>` of a LEGACY archive nobody overrode (the build-failure
		// fallback) still lands on the flake's baked :latest.
		ref := JailImage(argv[0])
		if f.pendingRef != "" {
			ref = f.pendingRef
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

// layerCopy is the AutoLoadOptions.LayerCopy seam (C9): one `skopeo copy` from
// the nix store to a transport-qualified destination. It records what it was
// handed and then mutates the store exactly as a real copy of that destination
// would, so every C2 assertion about loads, tags and refs keeps its meaning.
//
// The two destinations do DIFFERENT things, and modelling that difference is the
// point of the fake:
//
//   - `containers-storage:<ref>` IS the load. skopeo creates the image under the
//     ref in the argv, so no `load` argv is ever run and `f.loads` stays 0 —
//     which is what makes "the podman path runs no loader" assertable rather
//     than assumed.
//   - `oci-archive:<file>:<ref>` and `docker-archive:<file>:<ref>` write a FILE
//     and create nothing. The image appears only when the loader reads that
//     file, so the fake writes it and arms pendingRef for the
//     `<runtime> load -i` it expects next.
func (f *fakeRuntime) layerCopy(imageJSON, dest string) (CopyReport, bool) {
	f.copiedManifests = append(f.copiedManifests, imageJSON)
	f.copiedDests = append(f.copiedDests, dest)
	if f.copyFails {
		return CopyReport{}, false
	}
	switch {
	case strings.HasPrefix(dest, "containers-storage:"):
		ref := strings.TrimPrefix(dest, "containers-storage:")
		f.present[ref] = "img-from-" + ref
	case strings.HasPrefix(dest, "oci-archive:"), strings.HasPrefix(dest, "docker-archive:"):
		// `<file>:<reference>`, split at the FIRST colon: skopeo's archive
		// transports cannot express a path containing one, and the REFERENCE very
		// much can (`yolo-jail:<key>`) — splitting at the last colon silently
		// moves half the ref into the filename.
		_, rest, _ := strings.Cut(dest, ":")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return CopyReport{}, false
		}
		file, ref := parts[0], parts[1]
		f.ociFiles = append(f.ociFiles, file)
		if err := os.WriteFile(file, []byte("oci-archive"), 0o644); err != nil {
			return CopyReport{}, false
		}
		f.pendingRef = ref
	default:
		return CopyReport{}, false
	}
	return CopyReport{Layers: 3, Total: 3000, CopiedLayers: 1, Copied: 1000}, true
}

// loadArchive is the AutoLoadOptions.LoadArchive seam: read back the archive the
// copy wrote. It ASSERTS THE FILE IS THERE rather than assuming it, because "the
// copy wrote the file the loader takes" is the only property an archive backend
// has instead of negotiation.
func (f *fakeRuntime) loadArchive(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	rc, ran := f.run(ImageLoadCmd(f.runtime, path))
	f.pendingRef = ""
	return ran && rc == 0
}

// cmds renders the recorded argv as "verb ..." strings for order assertions.
func (f *fakeRuntime) cmds() []string {
	var out []string
	for _, a := range f.argv {
		out = append(out, strings.Join(a, " "))
	}
	return out
}

// c2Opts is the podman fixture. The copier build is stubbed to a fixed path so
// no test compiles skopeo; PresentDigests answers "nothing present" so the
// copied/skipped line reports the whole image, which is what a first load is.
func c2Opts(rt string, storePath string, f *fakeRuntime, out *bytes.Buffer) AutoLoadOptions {
	f.runtime = rt
	return AutoLoadOptions{
		Runtime:        rt,
		Out:            out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		LayerCopy:      f.layerCopy,
		BuildCopier:    func(string) (string, []string) { return "/nix/store/fake-skopeo/bin/skopeo", nil },
		PresentDigests: func() map[string]struct{} { return nil },
	}
}

// acOpts is c2Opts for Apple Container: the same seams plus the archive loader.
// The caller must have set a temp HOME (withBuildDir) so archiveTempPath
// resolves under it.
func acOpts(storePath string, f *fakeRuntime, out *bytes.Buffer) AutoLoadOptions {
	o := c2Opts("container", storePath, f, out)
	o.LoadArchive = f.loadArchive
	return o
}

// macPodmanOpts is c2Opts for podman ON macOS — the backend whose
// containers-storage lives inside the Podman Machine VM, so a local copy into it
// writes a store nothing reads. It takes the same archive pair as Apple
// Container, with podman's own loader and format.
func macPodmanOpts(storePath string, f *fakeRuntime, out *bytes.Buffer) AutoLoadOptions {
	o := c2Opts("podman", storePath, f, out)
	o.IsMacOS = true
	o.LoadArchive = f.loadArchive
	return o
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
	// The ref reuses the SAME key as the GC root and the Apple Container path's
	// transient archive. A second hash function would let them drift, which is
	// why gcroot.go says to reuse ImageStoreKey.
	t.Setenv("HOME", t.TempDir())
	if got, want := filepath.Base(ImageRootLink(pathA)), key; got != want {
		t.Errorf("GC root %q does not share the ref's key %q", got, want)
	}
	arch, err := archiveTempPath(pathA, ociArchiveSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(arch), key) {
		t.Errorf("OCI archive %q does not share the ref's key %q", filepath.Base(arch), key)
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
	storePath := storeManifest(t, "steady-image")
	f := newFakeRuntime()
	var out bytes.Buffer

	first := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !first.OK {
		t.Fatalf("first launch failed: %s", out.String())
	}
	if len(f.copiedDests) != 1 {
		t.Fatalf("first launch performed %d copies, want 1", len(f.copiedDests))
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
	if len(f.copiedDests) != 1 {
		t.Errorf("second launch performed a copy (%d total) — the image was already "+
			"present under its content ref, so nothing needed delivering",
			len(f.copiedDests))
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
	pathA := storeManifest(t, "path-A-image")
	pathB := storeManifest(t, "path-B-image")
	f := newFakeRuntime()
	var out bytes.Buffer

	a1 := AutoLoadImage(c2Opts("podman", pathA, f, &out))
	b1 := AutoLoadImage(c2Opts("podman", pathB, f, &out))
	if !a1.OK || !b1.OK {
		t.Fatalf("initial launches failed: %s", out.String())
	}
	if len(f.copiedDests) != 2 {
		t.Fatalf("expected 2 copies for 2 distinct store paths, got %d", len(f.copiedDests))
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
	if len(f.copiedDests) != 2 {
		t.Errorf("alternating between two workspaces re-copied (%d copies total) — "+
			"this is the §1.5 thrash C2 removes", len(f.copiedDests))
	}
	if a2.Ref != a1.Ref || b2.Ref != b1.Ref {
		t.Errorf("refs are not stable per store path: A %q→%q, B %q→%q",
			a1.Ref, a2.Ref, b1.Ref, b2.Ref)
	}
}

// TestTheImageIsNamedOnTheWayIn pins WHERE the content ref comes from — and C9
// makes the answer STRUCTURAL rather than won. nix2container's image.json
// carries no repo:tag at all, so the copy's DESTINATION ARGV is the only name an
// image can get; before C9 the flake baked `tag = "latest"` and C2 had to
// override it with `--repo_tag`. Nothing binds the ref afterwards either way,
// which is what removes the race.
func TestTheImageIsNamedOnTheWayIn(t *testing.T) {
	withBuildDir(t)
	storePath := storeManifest(t, "tagme-image")
	f := newFakeRuntime()
	var out bytes.Buffer

	res := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !res.OK {
		t.Fatalf("load failed: %s", out.String())
	}
	contentRef := JailImageRef("podman", storePath)

	// The copier was told the name, and the name it was told is the ref the
	// caller is handed. These are two values that MUST agree; before the image
	// was named going in, they could not even be compared.
	if len(f.copiedDests) != 1 || f.copiedDests[0] != ContainersStorageDest(contentRef) {
		t.Fatalf("copy destinations = %v, want [%s]",
			f.copiedDests, ContainersStorageDest(contentRef))
	}
	// The MANIFEST it read is the store path the build produced — the copy reads
	// the nix store directly, so this is also what pins "no archive in between".
	if len(f.copiedManifests) != 1 || f.copiedManifests[0] != storePath {
		t.Errorf("copy read %v, want [%s]", f.copiedManifests, storePath)
	}
	if f.present[contentRef] == "" {
		t.Errorf("the copy did not create %q: %v", contentRef, f.present)
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
	// The legacy tag is still MOVED (downstream), after the copy, so the degraded
	// fallback branch — which has no store path and can only ask about :latest —
	// still finds an image.
	wantTag := "podman tag " + contentRef + " " + JailImage("podman")
	tagged := false
	for _, c := range f.cmds() {
		if c == wantTag {
			tagged = true
		}
	}
	if !tagged {
		t.Fatalf("no %q in %v — :latest stopped tracking the newest load", wantTag, f.cmds())
	}
	if f.present[JailImage("podman")] != f.present[contentRef] {
		t.Error("the legacy :latest ref does not name the image just copied; the " +
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
	storePath := storeManifest(t, "mine-image")
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
	if !again.OK || len(f.copiedDests) != 1 {
		t.Fatalf("relaunch: OK=%v copies=%d, want true/1: %s",
			again.OK, len(f.copiedDests), out.String())
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
	storePath := storeManifest(t, "untaggable-image")
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
// LoadArchive seam's production call site was unpinned and a change to it
// could break the backend with the whole unit gate green.
//
// Apple Container cannot be retagged after the fact here — `container image
// load` takes a file and the name comes from inside it — so the ref has to
// arrive WITH the archive. Under C9 that is the COPY's destination: `oci-archive
// :<file>:<ref>`. Delete the `Runtime == "container"` arm and this fails either
// because the copy went to containers-storage or because the ref it carries is
// the podman spelling.
func TestAppleContainerIsNamedGoingIn(t *testing.T) {
	withBuildDir(t)
	storePath := storeManifest(t, "ac-image")
	var out bytes.Buffer
	f := newFakeRuntime()

	res := AutoLoadImage(acOpts(storePath, f, &out))
	if !res.OK {
		t.Fatalf("apple container load failed: %s", out.String())
	}
	wantRef := JailImageRef("container", storePath)
	if res.Ref != wantRef {
		t.Errorf("result ref = %q, want the UNQUALIFIED content ref %q", res.Ref, wantRef)
	}
	if len(f.copiedDests) != 1 {
		t.Fatalf("expected exactly one copy, got %v", f.copiedDests)
	}
	// The DESTINATION carries both halves: the file the loader will read and the
	// name the image gets. Neither is derivable from the other, which is why the
	// assertion is on the whole string.
	if len(f.ociFiles) != 1 {
		t.Fatalf("the copy did not write an OCI archive: dests=%v", f.copiedDests)
	}
	if want := OCIArchiveDest(f.ociFiles[0], wantRef); f.copiedDests[0] != want {
		t.Errorf("copy destination = %q, want %q", f.copiedDests[0], want)
	}
	// AND IT MUST NOT GO TO containers-storage. That destination is podman's; on
	// this backend it would write into a store nothing reads.
	if strings.HasPrefix(f.copiedDests[0], "containers-storage:") {
		t.Errorf("apple container copied into containers-storage: %q", f.copiedDests[0])
	}
	// THE ARCHIVE IS TEMPORARY (§ deliverToAppleContainer): the loader saw it —
	// f.loadArchive stats it and fails otherwise, so a green result already proves
	// that — and nothing may be left behind for `newestTars` to find later.
	if _, err := os.Stat(f.ociFiles[0]); err == nil {
		t.Errorf("%q survived the launch; the OCI archive must be removed, or the "+
			"degraded fallback will load it and then claim :latest", f.ociFiles[0])
	}
	if strings.HasSuffix(f.ociFiles[0], ".tar") {
		t.Errorf("%q ends in .tar, which newestTars matches — a crashed launch would "+
			"leave a candidate the degraded branch mis-names", f.ociFiles[0])
	}
	// The podman-only retag must NOT have run on this backend.
	for _, c := range f.cmds() {
		if strings.Contains(c, " tag ") {
			t.Errorf("apple container path issued %q; it is named by the copy", c)
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
	pathA := storeManifest(t, "live-A-image")
	pathB := storeManifest(t, "live-B-image")
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
	if len(f.copiedDests) != 2 {
		t.Fatalf("the third launch copied something (%d copies); the premise of this "+
			"test is that it does not", len(f.copiedDests))
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
