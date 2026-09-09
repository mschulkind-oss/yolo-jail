package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file replaces streamload_test.go. Every behavior case there was about a
// PIPE — which end broke, whether the close order deadlocked, whether a
// truncated stream could be recorded as a size — and C9 has no pipe. What the
// cases are about now is the same three questions one layer over: does the copy
// get the right argv, does a failure say what skopeo said, and is there anything
// it can fall back to (there must not be).

// cacheImagesDir is where a tar would land if anything still wrote one — derived
// from the build dir withBuildDir() returns, which is its sibling.
func cacheImagesDir(buildDir string) string {
	return filepath.Join(filepath.Dir(buildDir), "cache", "images")
}

// writeScript drops an executable at path with a /bin/sh body — a stand-in for
// the store-path skopeo the copier attr realizes, so the REAL copyImage can be
// driven without building a patched skopeo.
func writeScript(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTheDestinationsCarryTheirTransportAndTheirName pins the two destination
// spellings, because they are the whole of "the image is named on the way in"
// and each is a string skopeo parses rather than a struct anyone type-checks.
func TestTheDestinationsCarryTheirTransportAndTheirName(t *testing.T) {
	const storePath = "/nix/store/dest-image"
	podRef := JailImageRef("podman", storePath)
	if got, want := ContainersStorageDest(podRef), "containers-storage:"+podRef; got != want {
		t.Errorf("podman dest = %q, want %q", got, want)
	}
	// The podman destination must name the CONTENT ref, not the legacy alias:
	// nothing retags afterwards, so whatever is in this string is the only name
	// the image will ever have from this launch.
	if strings.Contains(ContainersStorageDest(podRef), ":latest") {
		t.Errorf("podman dest names the legacy alias: %q", ContainersStorageDest(podRef))
	}
	acRef := JailImageRef("container", storePath)
	if got, want := OCIArchiveDest("/tmp/x.oci", acRef), "oci-archive:/tmp/x.oci:"+acRef; got != want {
		t.Errorf("apple container dest = %q, want %q", got, want)
	}
	// The FILE comes first and skopeo splits at the FIRST colon — its archive
	// transports cannot express a path containing one, while the reference very
	// much does (`yolo-jail:<key>`). Reverse the order and the reference's own
	// colon makes the filename unparseable.
	if !strings.HasPrefix(OCIArchiveDest("/tmp/x.oci", acRef), "oci-archive:/tmp/x.oci:") {
		t.Errorf("the OCI destination does not lead with its file: %q",
			OCIArchiveDest("/tmp/x.oci", acRef))
	}
	if strings.Count(acRef, ":") != 1 {
		t.Fatalf("this test's premise is that the ref contains a colon: %q", acRef)
	}
}

// TestTheCopierArgvIsWhatSkopeoNeeds drives the REAL copyImage against a
// stand-in skopeo that records its own argv, so what is pinned is the command
// production runs and not a builder function nothing calls.
//
// Three properties, each of which has broken a `nix:` copy in this jail:
// `--insecure-policy` (without it skopeo demands /etc/containers/policy.json and
// fails on a host that has never run a registry client), the `nix:` prefix on
// the SOURCE (a bare path is read as a directory), and the destination arriving
// verbatim as one argument.
func TestTheCopierArgvIsWhatSkopeoNeeds(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	copier := writeScript(t, filepath.Join(dir, "skopeo"),
		"printf '%s\\n' \"$@\" > "+argsFile)

	var out bytes.Buffer
	if !copyImage(copier, "/nix/store/abc-image.json",
		"containers-storage:localhost/yolo-jail:deadbeef", &out) {
		t.Fatalf("a clean copy reported failure: %q", out.String())
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the copier was never run: %v", err)
	}
	want := "--insecure-policy\ncopy\nnix:/nix/store/abc-image.json\n" +
		"containers-storage:localhost/yolo-jail:deadbeef\n"
	if string(got) != want {
		t.Errorf("copier argv =\n%q\nwant\n%q", string(got), want)
	}
	if out.String() != "" {
		t.Errorf("a successful copy said something: %q", out.String())
	}
}

// TestACopyFailurePrintsSkopeosOwnWordsAndSaysNoImageWasWritten is §3.8's
// requirement on the error text, and it is a requirement rather than a courtesy:
// skopeo commits the image record LAST, so a failed copy leaves orphan blobs and
// no image, and a reader who cannot tell that is left guessing whether a partial
// image is now runnable.
func TestACopyFailurePrintsSkopeosOwnWordsAndSaysNoImageWasWritten(t *testing.T) {
	dir := t.TempDir()
	copier := writeScript(t, filepath.Join(dir, "skopeo"),
		"echo 'time=... level=fatal msg=\"initializing destination: no space left\"' >&2; exit 1")

	var out bytes.Buffer
	if copyImage(copier, "/nix/store/abc-image.json", "containers-storage:x:y", &out) {
		t.Fatal("a copier that exited 1 was reported as success")
	}
	s := out.String()
	if !strings.Contains(s, "no space left") {
		t.Errorf("skopeo's own diagnosis was swallowed: %q", s)
	}
	if !strings.Contains(s, "NO image was written") {
		t.Errorf("the report does not say whether a partial image is runnable: %q", s)
	}
	if !strings.Contains(s, "exit 1") {
		t.Errorf("the exit status is missing: %q", s)
	}
}

// TestACopyIsRetriedExactlyOnce pins the bound in §3.6. One recovery from a
// transient loss — a neighbour holding the c/storage lock, a blob write
// interrupted — and never a loop that re-copies gigabytes forever.
//
// Both polarities, because only the pair pins the number: a copier that fails
// once then succeeds must produce a green launch, and a copier that always fails
// must be run TWICE and no more.
func TestACopyIsRetriedExactlyOnce(t *testing.T) {
	t.Run("a transient failure is recovered", func(t *testing.T) {
		dir := t.TempDir()
		counter := filepath.Join(dir, "n")
		copier := writeScript(t, filepath.Join(dir, "skopeo"),
			"printf x >> "+counter+"; test -s "+counter+" && "+
				"[ $(wc -c < "+counter+") -gt 1 ]")
		var out bytes.Buffer
		if !copyImageWithRetry(copier, "/nix/store/a.json", "containers-storage:x:y", &out) {
			t.Fatalf("the one retry did not happen: %q", out.String())
		}
		if n := fileSize(t, counter); n != 2 {
			t.Errorf("copier ran %d time(s), want 2 (one attempt + one retry)", n)
		}
		if !strings.Contains(out.String(), "Retrying the image copy once") {
			t.Errorf("the retry was silent: %q", out.String())
		}
	})

	t.Run("a persistent failure is not looped", func(t *testing.T) {
		dir := t.TempDir()
		counter := filepath.Join(dir, "n")
		copier := writeScript(t, filepath.Join(dir, "skopeo"),
			"printf x >> "+counter+"; exit 1")
		var out bytes.Buffer
		if copyImageWithRetry(copier, "/nix/store/a.json", "containers-storage:x:y", &out) {
			t.Fatal("a copier that always fails was reported as success")
		}
		if n := fileSize(t, counter); n != 2 {
			t.Errorf("copier ran %d time(s), want exactly 2 — an unbounded retry turns "+
				"a full disk into a hang that re-copies a multi-GB image forever", n)
		}
	})
}

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// TestPodmanHappyPathCopiesAndNeverWritesATar is C9's disk claim stated as a
// test, and it PINS THE CALL SITE: reinstate a materialize-then-load and the
// cache-dir assertion fires; send the copy to an oci-archive and the destination
// assertion fires.
//
// The disk half is asserted on DISK rather than on the code path taken — "no
// file appears in cache/images" is the sentence OQ-DF1 ruled ("stream, keep zero
// tars"), and a test that only checked which function ran could be satisfied by
// a copy that also wrote a tar on the side.
func TestPodmanHappyPathCopiesAndNeverWritesATar(t *testing.T) {
	bd := withBuildDir(t)
	storePath := storeManifest(t, "copy-me-image")
	f := newFakeRuntime()
	var out bytes.Buffer

	res := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if !res.OK {
		t.Fatalf("copied launch failed: %s", out.String())
	}
	if res.Ref != JailImageRef("podman", storePath) {
		t.Errorf("ref = %q, want the content ref %q", res.Ref, JailImageRef("podman", storePath))
	}

	// The copy went to containers-storage, on THIS store path's manifest — no
	// file transport anywhere in it.
	if len(f.copiedDests) != 1 {
		t.Fatalf("copies = %v, want exactly one", f.copiedDests)
	}
	if want := ContainersStorageDest(res.Ref); f.copiedDests[0] != want {
		t.Errorf("copied to %q, want %q", f.copiedDests[0], want)
	}
	if f.copiedManifests[0] != storePath {
		t.Errorf("copy read %q, want the built manifest %q", f.copiedManifests[0], storePath)
	}

	// And nothing landed on disk. Both spellings: the specific name this store
	// path's tar would have had, and the directory as a whole (which catches a
	// file written under any other name — including the Apple Container path's
	// transient archive, which must never be reached on podman).
	tarName := keyFor(storePath) + ".tar"
	if _, err := os.Stat(filepath.Join(cacheImagesDir(bd), tarName)); err == nil {
		t.Errorf("%s was written; the copy path must retain no archive", tarName)
	}
	if entries, err := os.ReadDir(cacheImagesDir(bd)); err == nil && len(entries) != 0 {
		t.Errorf("cache/images is not empty after a copied load: %v", entries)
	}
}

// TestPodmanOnMacOSTakesAnArchiveBecauseTheVMOwnsTheStore is the arm the DESIGN
// LEFT UNSERVED, and the reason it is a test rather than a comment.
//
// §3.4 said podman/macOS stays "unchanged (stream into `podman load`)" — but
// OQ-LI5 deleted the stream, so "unchanged" named a mechanism that no longer
// exists. The fact underneath is real and measured elsewhere (C8, 2026-09-07,
// `prefixUnreachableFromVM`): that backend's containers-storage lives INSIDE the
// Podman Machine VM, which shares the user's home and `/private` and NOT
// `/nix`. A local `skopeo copy … containers-storage:…` would therefore write a
// store the VM never reads — an image that exists on the Mac and cannot be run,
// on every macOS podman launch.
//
// Delete the `|| o.IsMacOS` from the delivery branch and this fails on the
// destination. It is asserted here rather than only in the run slice because no
// CI runner exercises macOS podman end to end, so this is the only place the
// decision is observable at all.
func TestPodmanOnMacOSTakesAnArchiveBecauseTheVMOwnsTheStore(t *testing.T) {
	withBuildDir(t)
	storePath := storeManifest(t, "mac-podman-image")
	f := newFakeRuntime()
	var out bytes.Buffer

	res := AutoLoadImage(macPodmanOpts(storePath, f, &out))
	if !res.OK {
		t.Fatalf("macOS podman launch failed: %s", out.String())
	}
	if len(f.copiedDests) != 1 {
		t.Fatalf("copies = %v, want exactly one", f.copiedDests)
	}
	// NOT containers-storage — that is the whole point.
	if strings.HasPrefix(f.copiedDests[0], "containers-storage:") {
		t.Fatalf("macOS podman copied into the LOCAL containers-storage (%q), which "+
			"the Podman Machine VM never reads — the image would exist and be "+
			"unrunnable", f.copiedDests[0])
	}
	// podman's loader reads a docker-archive, not an OCI layout.
	if !strings.HasPrefix(f.copiedDests[0], "docker-archive:") {
		t.Errorf("copy destination = %q, want a docker-archive `podman load -i` can "+
			"read", f.copiedDests[0])
	}
	if len(f.ociFiles) != 1 {
		t.Fatalf("no archive was written: %v", f.copiedDests)
	}
	if want := DockerArchiveDest(f.ociFiles[0], res.Ref); f.copiedDests[0] != want {
		t.Errorf("copy destination = %q, want %q", f.copiedDests[0], want)
	}
	// The image is still named by CONTENT, and podman's spelling of it.
	if res.Ref != JailImageRef("podman", storePath) {
		t.Errorf("ref = %q, want %q", res.Ref, JailImageRef("podman", storePath))
	}
	// `podman load -i <file>` ran, and the file was there when it did.
	loaded := false
	for _, c := range f.cmds() {
		if c == "podman load -i "+f.ociFiles[0] {
			loaded = true
		}
	}
	if !loaded {
		t.Errorf("no `podman load -i %s` among %v", f.ociFiles[0], f.cmds())
	}
	// And the archive is gone: leaving a `*.tar`-shaped file behind would give the
	// degraded fallback a candidate it loads and then mis-names :latest.
	if _, err := os.Stat(f.ociFiles[0]); err == nil {
		t.Errorf("%q survived the launch", f.ociFiles[0])
	}
	if strings.HasSuffix(f.ociFiles[0], ".tar") {
		t.Errorf("%q ends in .tar, which newestTars matches", f.ociFiles[0])
	}
	// The legacy alias still moves on podman, on both platforms, because the
	// degraded fallback has nothing else to ask about.
	tagged := false
	for _, c := range f.cmds() {
		if strings.HasPrefix(c, "podman tag "+res.Ref+" ") {
			tagged = true
		}
	}
	if !tagged {
		t.Errorf(":latest was not pointed at the new image: %v", f.cmds())
	}
}

// TestLinuxPodmanCopiesStraightIntoTheStore is the OTHER polarity of the same
// decision, and the pair is what pins it: without this, making the archive path
// unconditional would satisfy every other test here while throwing away the
// layer reuse this whole change exists for.
func TestLinuxPodmanCopiesStraightIntoTheStore(t *testing.T) {
	withBuildDir(t)
	storePath := storeManifest(t, "linux-podman-image")
	f := newFakeRuntime()
	var out bytes.Buffer
	opts := c2Opts("podman", storePath, f, &out)
	opts.IsMacOS = false
	opts.LoadArchive = func(string) bool {
		t.Error("podman on Linux went through an archive; it can be copied into " +
			"directly, and only that destination negotiates per layer")
		return false
	}
	res := AutoLoadImage(opts)
	if !res.OK {
		t.Fatalf("linux podman launch failed: %s", out.String())
	}
	if want := ContainersStorageDest(res.Ref); len(f.copiedDests) != 1 || f.copiedDests[0] != want {
		t.Errorf("copied to %v, want [%s]", f.copiedDests, want)
	}
	if len(f.ociFiles) != 0 {
		t.Errorf("an archive was written on the negotiating path: %v", f.ociFiles)
	}
}

// TestACopyFailureRefusesTheLaunchWithNoSecondMechanism is OQ-LI5 as a test, and
// it is the one that must never be "fixed" by adding a fallback.
//
// The rule it encodes: an escape hatch is for a config the USER broke, not for
// yolo's own mechanism being broken. So a failed copy abandons the launch — it
// does not stream (there is nothing left to stream with), does not fall back to
// a cached tar (that branch belongs to a launch with no store path), and does not
// hand back a ref for an image nobody wrote.
func TestACopyFailureRefusesTheLaunchWithNoSecondMechanism(t *testing.T) {
	bd := withBuildDir(t)
	// A cached tar IS present, which is the whole point: the failing copy must
	// not quietly fall through to it and report success on a stale image.
	cacheImages := cacheImagesDir(bd)
	if err := os.MkdirAll(cacheImages, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheImages, "stale.tar"), []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}

	storePath := storeManifest(t, "uncopyable-image")
	f := newFakeRuntime()
	f.copyFails = true
	var out bytes.Buffer

	res := AutoLoadImage(c2Opts("podman", storePath, f, &out))
	if res.OK {
		t.Fatalf("a failed copy produced a runnable launch: ref=%q\n%s", res.Ref, out.String())
	}
	if res.Ref != "" {
		t.Errorf("ref = %q; a failed delivery has no image to name", res.Ref)
	}
	if f.loads != 0 {
		t.Errorf("the failed copy fell back to a loader: %v", f.cmds())
	}
	if strings.Contains(out.String(), "Done: loaded image") {
		t.Errorf("a failed copy announced success: %q", out.String())
	}
	// The error must not advertise a knob that does not exist. Naming a legacy
	// stream, or an env var to re-enable one, is worse than admitting the launch
	// is over.
	for _, ghost := range []string{"YOLO_LEGACY_IMAGE_STREAM", "streamLayeredImage", "podman load"} {
		if strings.Contains(out.String(), ghost) {
			t.Errorf("the failure report names %q, which does not exist: %q", ghost, out.String())
		}
	}
}

// TestCopierBuildFailureRefusesTheLaunchAndNamesTheAttr covers the other half of
// "no fallback": the copier is a SOURCE BUILD, so it is the one new thing on the
// critical path that can fail on a machine that is offline or out of disk.
//
// A launch that cannot build a copier cannot deliver an image, and it must say
// which attr failed — the alternative is a nix error about skopeo that reads as
// unrelated to the jail that would not start.
func TestCopierBuildFailureRefusesTheLaunchAndNamesTheAttr(t *testing.T) {
	withBuildDir(t)
	f := newFakeRuntime()
	var out bytes.Buffer
	opts := c2Opts("podman", storeManifest(t, "fine-image"), f, &out)
	opts.BuildCopier = func(string) (string, []string) {
		return "", []string{"error: unable to download 'https://cache.nixos.org': Couldn't connect"}
	}
	opts.LayerCopy = func(string, string) (CopyReport, bool) {
		t.Error("the copy ran without a copier")
		return CopyReport{}, false
	}
	res := AutoLoadImage(opts)
	if res.OK {
		t.Fatalf("a launch with no copier reported success: %s", out.String())
	}
	if !strings.Contains(out.String(), ImageCopierAttr) {
		t.Errorf("the failure does not name %s: %q", ImageCopierAttr, out.String())
	}
	if !strings.Contains(out.String(), "Couldn't connect") {
		t.Errorf("nix's own stderr was swallowed: %q", out.String())
	}
}

// TestTheCopiedSkippedReportSplitsTheInventory is §3.10's claim as a test: the
// ratio is what the whole change asserts, so it has to be computed from the
// manifest the copy actually read and the digests the destination actually held.
func TestTheCopiedSkippedReportSplitsTheInventory(t *testing.T) {
	layers := []LayerInfo{
		{Digest: "sha256:aaa", Size: 3_000_000_000},
		{Digest: "sha256:bbb", Size: 100_000_000},
		{Digest: "sha256:ccc", Size: 27_000_000},
	}
	// The §3.10 item-1 shape: everything but the top layer is already there.
	r := ReportFor(layers, map[string]struct{}{
		"sha256:aaa": {}, "sha256:bbb": {},
	})
	if r.Layers != 3 || r.CopiedLayers != 1 {
		t.Errorf("layers=%d copied=%d, want 3/1", r.Layers, r.CopiedLayers)
	}
	if r.Copied != 27_000_000 {
		t.Errorf("copied = %d, want only the absent layer's 27000000", r.Copied)
	}
	if r.Skipped() != 3_100_000_000 || r.SkippedLayers() != 2 {
		t.Errorf("skipped = %d/%d layers, want 3100000000/2", r.Skipped(), r.SkippedLayers())
	}
	if s := r.String(); !strings.Contains(s, "1 layer(s)") || !strings.Contains(s, "2 layer(s)") {
		t.Errorf("the report line does not carry both counts: %q", s)
	}

	// AN UNKNOWN DESTINATION REPORTS EVERYTHING AS COPIED, which is the honest
	// answer when the probe could not run — over-reporting the cost is the safe
	// direction for a number whose job is to keep a performance claim honest.
	all := ReportFor(layers, nil)
	if all.CopiedLayers != 3 || all.Copied != all.Total {
		t.Errorf("with no present set: copied=%d/%d layers of total %d; want everything",
			all.Copied, all.CopiedLayers, all.Total)
	}
}

// TestReadLayerInventoryReadsNix2ContainersManifest pins the parse against the
// real shape of an image.json, including the two fields nothing else in this
// repo would notice going missing.
func TestReadLayerInventoryReadsNix2ContainersManifest(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "image.json")
	// Trimmed from a real `nix build .#ociImage` output, 2026-09-09.
	body := `{
	  "version": 1,
	  "image-config": {"Cmd": ["/bin/bash"]},
	  "arch": "amd64",
	  "created": "0001-01-01T00:00:00Z",
	  "layers": [
	    {"digest": "sha256:base", "size": 3143105536, "diff_ids": "sha256:base",
	     "mediatype": "application/vnd.oci.image.layer.v1.tar",
	     "paths": [{"path": "/nix/store/aaa-libarchive"}]},
	    {"digest": "sha256:top", "size": 27508736, "diff_ids": "sha256:top",
	     "mediatype": "application/vnd.oci.image.layer.v1.tar",
	     "paths": [{"path": "/nix/store/bbb-yolo-jail-root",
	                "options": {"rewrite": {"regex": "^/nix/store/bbb-yolo-jail-root", "repl": ""}}}]}
	  ]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLayerInventory(p)
	if err != nil {
		t.Fatalf("ReadLayerInventory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("layers = %d, want 2", len(got))
	}
	// ORDER IS STACK ORDER, bottom first: the top tier is the LAST entry, which
	// is what makes "a flake.nix-only edit moves the last layer" checkable.
	if got[0].Digest != "sha256:base" || got[0].Size != 3143105536 {
		t.Errorf("layer 0 = %+v, want the base tier", got[0])
	}
	if got[1].Digest != "sha256:top" || got[1].Size != 27508736 {
		t.Errorf("layer 1 = %+v, want the top tier", got[1])
	}
	// A manifest that cannot be read is an error, NOT a silent empty inventory —
	// an empty one would report "0 layers, 0 B copied" for a full 3.4 GB copy.
	if _, err := ReadLayerInventory(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("a missing manifest read as an empty inventory")
	}
}

// TestPresentLayerDigestsAsksTheRuntimeForItsOwnLayers pins the two-probe shape
// and, more importantly, that a probe which cannot run yields EMPTY rather than
// a partial answer — a half-read present set would under-report the copy.
func TestPresentLayerDigestsAsksTheRuntimeForItsOwnLayers(t *testing.T) {
	var seen [][]string
	capture := func(argv []string) (string, bool) {
		seen = append(seen, argv)
		switch {
		case argv[1] == "images":
			return "id-one\nid-two\n", true
		case argv[1] == "image":
			return "sha256:aaa\nsha256:bbb\n\nsha256:aaa\n", true
		}
		return "", false
	}
	got := PresentLayerDigests("podman", capture)
	if len(got) != 2 {
		t.Errorf("digests = %v, want the two distinct ones", got)
	}
	if _, ok := got["sha256:bbb"]; !ok {
		t.Errorf("digests = %v, missing sha256:bbb", got)
	}
	// The listing is SCOPED to the jail repository: enumerating every image on
	// the machine would be slower and would credit unrelated images' layers.
	if len(seen) != 2 {
		t.Fatalf("probes = %v, want exactly two", seen)
	}
	if !contains(seen[0], JailImageRepository("podman")) {
		t.Errorf("the image listing is not scoped to %q: %v",
			JailImageRepository("podman"), seen[0])
	}
	if !contains(seen[1], "id-one") || !contains(seen[1], "id-two") {
		t.Errorf("the digest probe did not receive the listed ids: %v", seen[1])
	}

	// A failing first probe must yield nothing and not run the second.
	seen = nil
	empty := PresentLayerDigests("podman", func(argv []string) (string, bool) {
		seen = append(seen, argv)
		return "", false
	})
	if len(empty) != 0 {
		t.Errorf("a failed probe reported %v as present", empty)
	}
	if len(seen) != 1 {
		t.Errorf("a failed listing still ran %d probes", len(seen))
	}
	// And a nil capture is the test-default case: no probe, no claim.
	if len(PresentLayerDigests("podman", nil)) != 0 {
		t.Error("a nil capture claimed digests were present")
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// TestImageCopierOutLinkIsOutOfBothReapersReach is the copier's half of the
// prefix's own invariant, and it matters more here: the out-link IS the copier's
// GC root, and the copier is a store path this code EXECUTES. Put it where
// either reaper can see it and a `yolo prune` leaves the next launch unable to
// deliver an image until it rebuilds skopeo from source.
func TestImageCopierOutLinkIsOutOfBothReapersReach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	link := ImageCopierOutLink("/home/me/code/yolo-jail")
	base := filepath.Base(link)
	// prune.PruneOrphanImageRoots deletes any root under build/roots/ that is not
	// a loaded image — the copier never is one.
	if strings.Contains(link, string(filepath.Separator)+"roots"+string(filepath.Separator)) {
		t.Errorf("%q is under build/roots/, whose reaper deletes non-image roots", link)
	}
	// prune.SweepDanglingOutLinks scans run-result-*.
	if strings.HasPrefix(base, "run-result-") {
		t.Errorf("%q is named run-result-*, which SweepDanglingOutLinks scans", base)
	}
	// Keyed by SOURCE, so rebuilding one tree replaces one link instead of
	// accumulating one per build.
	other := ImageCopierOutLink("/home/me/code/other-checkout")
	if other == link {
		t.Error("two source trees share one copier out-link")
	}
	if ImageCopierOutLink("/home/me/code/yolo-jail") != link {
		t.Error("the copier out-link is not stable for one source tree")
	}
}

// TestImageCopierBinaryNamesSkopeoInsideTheStorePath pins the layout the attr
// produces, because §3.2's fourth property is that a PATH lookup is never the
// answer: an unpatched skopeo rejects the `nix:` transport in a way that reads as
// a broken image rather than as a wrong binary.
func TestImageCopierBinaryNamesSkopeoInsideTheStorePath(t *testing.T) {
	const sp = "/nix/store/hash-skopeo-1.24.0"
	if got, want := ImageCopierBinary(sp), sp+"/bin/skopeo"; got != want {
		t.Errorf("copier binary = %q, want %q", got, want)
	}
	if !filepath.IsAbs(ImageCopierBinary(sp)) {
		t.Errorf("%q is not absolute, so it could resolve through PATH",
			ImageCopierBinary(sp))
	}
}

// TestBuildFailureFallbackStillLoadsAnExistingTar: C9 removed the CREATION of
// tars, not the ability to consume one. The build-failure fallback (newestTars →
// `podman load -i`) is the only thing that lets a jail start when the build
// failed and nothing is loaded, and it must keep working on whatever LEGACY tars
// are already on disk.
//
// ⚠ It is NOT the copy path's fallback: this branch runs only when there is no
// store path at all (SkipBuild, or a failed build the operator opted past), so
// it never rescues a failed copy — see
// TestACopyFailureRefusesTheLaunchWithNoSecondMechanism for that polarity.
func TestBuildFailureFallbackStillLoadsAnExistingTar(t *testing.T) {
	bd := withBuildDir(t)
	cacheImages := cacheImagesDir(bd)
	if err := os.MkdirAll(cacheImages, 0o755); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(cacheImages, "leftover.tar")
	if err := os.WriteFile(tarPath, []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var ran [][]string
	res := AutoLoadImage(AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"error: out of disk space"}
		},
		Run: func(argv []string) (int, bool) {
			ran = append(ran, append([]string(nil), argv...))
			if len(argv) >= 2 && argv[1] == "load" {
				return 0, true
			}
			return 1, true // inspect: nothing in the runtime
		},
		LayerCopy: func(string, string) (CopyReport, bool) {
			t.Error("the fallback tried to COPY; it has no manifest to copy from " +
				"— the build is what failed")
			return CopyReport{}, false
		},
		BuildCopier: func(string) (string, []string) {
			t.Error("the fallback built a copier for a copy it cannot make")
			return "", nil
		},
		LookupEnv: allowStaleEnv,
	})
	if !res.OK {
		t.Fatalf("the offline safety net did not fire: %s", out.String())
	}
	want := "podman load -i " + tarPath
	found := false
	for _, argv := range ran {
		if strings.Join(argv, " ") == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no %q among %v — the cached-tar fallback must still read a FILE", want, ran)
	}
	if !strings.Contains(out.String(), "Done: loaded image from cache") {
		t.Errorf("the cache load was not announced: %q", out.String())
	}
}

// TestTailWriterKeepsTheLastLines: the delivery tool's stderr used to be
// assigned nil and discarded, which is why a failed materialize could only ever
// say "Error streaming image to cache." with no cause. skopeo's fatal line is at
// the END, so the bound matters and the end is the interesting part.
func TestTailWriterKeepsTheLastLines(t *testing.T) {
	w := &tailWriter{max: 3}
	for _, chunk := range []string{"one\ntwo\n", "three\nfo", "ur\nfive\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	// A line split across two Writes must survive as one line.
	if got := strings.Join(w.tail(), "|"); got != "three|four|five" {
		t.Errorf("tail = %q, want %q", got, "three|four|five")
	}

	// An unterminated remainder is still reported — a process killed mid-line has
	// exactly one line worth reading and it has no \n on it.
	w2 := &tailWriter{max: 3}
	_, _ = w2.Write([]byte("error: killed mid-l"))
	_, _ = w2.Write([]byte("ine"))
	if got := strings.Join(w2.tail(), "|"); got != "error: killed mid-line" {
		t.Errorf("tail = %q, want the flushed remainder", got)
	}
}
