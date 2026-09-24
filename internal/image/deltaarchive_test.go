package image

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hexDigest is a real-shaped sha256 digest for a fixture layer name. The delta
// archive refuses to turn anything else into a path (blobDigestRe), so the
// fixtures below must use the shape a runtime actually reports.
func hexDigest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// hexManifest writes a nix2container image.json whose layers are the named ones,
// in stack order, each 1,000,000 bytes, with real-shaped digests.
func hexManifest(t *testing.T, file string, layers ...string) string {
	t.Helper()
	var parts []string
	for _, l := range layers {
		d := hexDigest(l)
		parts = append(parts, `{"digest":"`+d+`","size":1000000,"diff_ids":"`+d+`"}`)
	}
	p := filepath.Join(t.TempDir(), file+".json")
	body := `{"version":1,"arch":"amd64","layers":[` + strings.Join(parts, ",") + `]}`
	must(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func digestSet(names ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, n := range names {
		out[hexDigest(n)] = struct{}{}
	}
	return out
}

func digests(names ...string) []string {
	var out []string
	for _, n := range names {
		out = append(out, hexDigest(n))
	}
	sort.Strings(out)
	return out
}

// recordPathFor is where the Apple Container arm records a delivery of storePath.
func recordPathFor(storePath string) string {
	return filepath.Join(paths.ImageDeliveryDir(), keyFor(storePath)+deliveryRecordSuffix)
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestTheMacPodmanArchiveLeavesOutWhatTheStoreHolds is OQ-LR1 through the
// production call site: podman on macOS reports it holds the base layers, and the
// archive the loader is handed carries only the one it lacks — while the image
// still lands under its content ref.
//
// Replace the `seed` argument at the deliverViaArchive call site with nil and the
// archive carries every layer; drop the placeholder seeding and the same.
func TestTheMacPodmanArchiveLeavesOutWhatTheStoreHolds(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "mac-delta", "base1", "base2", "top")
	f := newFakeRuntime()
	f.blobs[hexDigest("base1")] = true
	f.blobs[hexDigest("base2")] = true
	var out bytes.Buffer
	o := macPodmanOpts(storePath, f, &out)
	// A digest of some OTHER jail image is in the probe's answer too: the store
	// holds it, this image does not have it, and it must neither be counted nor
	// leak into the archive.
	o.PresentDigests = func() map[string]struct{} { return digestSet("base1", "base2", "unrelated") }

	res := AutoLoadImage(o)
	if !res.OK {
		t.Fatalf("delta delivery failed: %s", out.String())
	}
	if len(f.archiveBlobs) != 1 {
		t.Fatalf("loads = %d, want exactly one (no retry): %s", len(f.archiveBlobs), out.String())
	}
	if got, want := sorted(f.archiveBlobs[0]), digests("top"); !reflect.DeepEqual(got, want) {
		t.Errorf("archive carried %v, want only the layer the store lacks %v", got, want)
	}
	if f.present[res.Ref] == "" {
		t.Errorf("no image under %q after the delta load", res.Ref)
	}
	s := out.String()
	if !strings.Contains(s, "1 layer(s), 1 MB sent; 2 layer(s), 2 MB reused from podman's store") {
		t.Errorf("the report does not say what was sent vs reused: %q", s)
	}
	if strings.Contains(s, "retrying once") {
		t.Errorf("a clean delta load announced a retry: %q", s)
	}
}

// TestAnEmptyPresentSetIsTheFullArchive: nothing present means every layer is in
// the archive, through the same argv and the same loader — the property that lets
// the retry be "the same thing again" rather than a second mechanism.
func TestAnEmptyPresentSetIsTheFullArchive(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "mac-full", "base1", "base2", "top")
	f := newFakeRuntime()
	var out bytes.Buffer
	res := AutoLoadImage(macPodmanOpts(storePath, f, &out))
	if !res.OK {
		t.Fatalf("full delivery failed: %s", out.String())
	}
	if len(f.archiveBlobs) != 1 {
		t.Fatalf("loads = %d, want 1", len(f.archiveBlobs))
	}
	if got, want := sorted(f.archiveBlobs[0]), digests("base1", "base2", "top"); !reflect.DeepEqual(got, want) {
		t.Errorf("full archive carried %v, want every layer %v", got, want)
	}
	if !strings.Contains(out.String(), "3 layer(s), 3 MB sent; 0 layer(s), 0 MB reused") {
		t.Errorf("report: %q", out.String())
	}
}

// TestAnOverClaimedPresentSetRetriesOnceWithTheFullArchive is the fail-closed
// rule: the store does NOT hold what the probe said (a layer pruned between the
// probe and the load), the loader refuses the delta and writes no image, and the
// launch recovers by delivering the full archive exactly once more — saying so in
// one line, and reporting what the archive that LANDED carried.
func TestAnOverClaimedPresentSetRetriesOnceWithTheFullArchive(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "mac-overclaim", "base1", "base2", "top")
	f := newFakeRuntime() // holds nothing
	var out bytes.Buffer
	o := macPodmanOpts(storePath, f, &out)
	// "unrelated" is another image's layer: the probe reports it, this image does
	// not have it, and the retry line must count only what THIS archive left out.
	o.PresentDigests = func() map[string]struct{} { return digestSet("base1", "base2", "unrelated") }

	res := AutoLoadImage(o)
	if !res.OK {
		t.Fatalf("the retry did not recover the over-claim: %s", out.String())
	}
	if f.loads != 2 || len(f.archiveBlobs) != 2 {
		t.Fatalf("loads = %d, want 2 (the delta, then the full archive)", f.loads)
	}
	if got, want := sorted(f.archiveBlobs[1]), digests("base1", "base2", "top"); !reflect.DeepEqual(got, want) {
		t.Errorf("the retry's archive carried %v, want every layer %v", got, want)
	}
	s := out.String()
	if n := strings.Count(s, "retrying once with the full archive"); n != 1 {
		t.Errorf("retry announced %d time(s), want exactly one line: %q", n, s)
	}
	if !strings.Contains(s, "2 layer(s) left out") {
		t.Errorf("the retry line does not say what the delta left out: %q", s)
	}
	if !strings.Contains(s, "3 layer(s), 3 MB sent; 0 layer(s)") {
		t.Errorf("the report describes the planned delta, not the full archive that landed: %q", s)
	}
}

// TestTheArchiveRetryIsBoundedAndOnlyForADelta pins both halves of the bound: a
// delta that keeps failing is tried twice in all (delta + full) and the launch
// fails; a full archive that fails is tried ONCE — it is today's archive failing,
// and there is nothing smaller to fall back to.
func TestTheArchiveRetryIsBoundedAndOnlyForADelta(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present map[string]struct{}
		want    int
	}{
		{"a failing delta is retried once, then fails", digestSet("base1"), 2},
		{"a failing full archive is not retried", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withBuildDir(t)
			storePath := hexManifest(t, "mac-fail", "base1", "top")
			f := newFakeRuntime()
			var out bytes.Buffer
			o := macPodmanOpts(storePath, f, &out)
			present := tc.present
			o.PresentDigests = func() map[string]struct{} { return present }
			loads := 0
			o.LoadArchive = func(string) bool { loads++; return false }
			if res := AutoLoadImage(o); res.OK {
				t.Fatal("a loader that always fails produced a green launch")
			}
			if loads != tc.want {
				t.Errorf("loader ran %d time(s), want %d", loads, tc.want)
			}
			if got := strings.Contains(out.String(), "retrying once"); got != (tc.want == 2) {
				t.Errorf("retry line present = %v, want %v: %q", got, tc.want == 2, out.String())
			}
		})
	}
}

// TestACopierThatIgnoresPlaceholdersStillDeliversACorrectArchive is the benign
// failure mode the placeholder choice rests on: if the copier stops treating a
// zero-byte file as a present blob and writes the real one over it, that blob is
// KEPT (it is correct content), the archive is simply full-size, and the launch
// says so.
func TestACopierThatIgnoresPlaceholdersStillDeliversACorrectArchive(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "mac-ignore", "base1", "top")
	f := newFakeRuntime()
	f.blobs[hexDigest("base1")] = true
	var out bytes.Buffer
	o := macPodmanOpts(storePath, f, &out)
	o.PresentDigests = func() map[string]struct{} { return digestSet("base1") }
	o.LayerCopy = func(imageJSON, dest string, prefix []string) (CopyReport, bool) {
		// Overwrite every blob, placeholders included — the regression.
		dir, _, _ := strings.Cut(strings.TrimPrefix(dest, "oci:"), ":")
		for _, d := range []string{"base1", "top"} {
			p := filepath.Join(dir, "blobs", "sha256", strings.TrimPrefix(hexDigest(d), "sha256:"))
			must(t, os.WriteFile(p, []byte("real "+d), 0o644))
		}
		return f.layerCopy(imageJSON, dest, prefix)
	}
	res := AutoLoadImage(o)
	if !res.OK {
		t.Fatalf("launch failed: %s", out.String())
	}
	if got, want := sorted(f.archiveBlobs[0]), digests("base1", "top"); !reflect.DeepEqual(got, want) {
		t.Errorf("archive carried %v, want both blobs the copier wrote %v", got, want)
	}
	if !strings.Contains(out.String(), "the image copier wrote 1 layer(s) the destination already holds") {
		t.Errorf("the ignored placeholder was not reported: %q", out.String())
	}
}

// TestTheLayoutCopyAsksForUncompressedLayers drives the REAL copy seam (a
// stand-in skopeo recording its argv) through the macOS podman arm: the layout
// destination must carry --dest-oci-accept-uncompressed-layers, or skopeo gzips
// every blob under a new digest and neither the placeholders nor any present set
// can ever match again.
func TestTheLayoutCopyAsksForUncompressedLayers(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "mac-argv", "base1")
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	// Record argv, then write the minimal layout a real copier would, so the tar
	// and the load that follow have something to read.
	copier := writeScript(t, filepath.Join(dir, "skopeo"),
		"printf '%s\\n' \"$@\" > "+argsFile+"\n"+
			"for a in \"$@\"; do last=$a; done\n"+
			"d=${last#oci:}; d=${d%%:*}\n"+
			"mkdir -p \"$d/blobs/sha256\" && echo '{}' > \"$d/index.json\" && echo '{}' > \"$d/oci-layout\"\n")
	f := newFakeRuntime()
	var out bytes.Buffer
	o := macPodmanOpts(storePath, f, &out)
	o.LayerCopy = nil // the real copyImageLayers → copyArgv
	o.BuildCopier = func(string) (string, []string) { return copier, nil }
	o.LoadArchive = func(string) bool { return true }
	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("launch failed: %s", out.String())
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the copier never ran: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(args) != 5 || args[0] != "--insecure-policy" || args[1] != "copy" ||
		args[2] != ociAcceptUncompressedFlag || args[3] != "nix:"+storePath ||
		!strings.HasPrefix(args[4], "oci:") {
		t.Errorf("copier argv = %q, want [--insecure-policy copy %s nix:<json> oci:<dir>:<ref>]",
			args, ociAcceptUncompressedFlag)
	}
	// And the flag is NOT on a containers-storage copy, which negotiates by itself.
	if a := copyArgv(nil, "skopeo", "/x.json", ContainersStorageDest("r")); strings.Contains(strings.Join(a, " "), ociAcceptUncompressedFlag) {
		t.Errorf("containers-storage argv carries the layout flag: %v", a)
	}
}

// TestAppleContainersPresentSetIsItsOwnDeliveryRecord is OQ-LR3 through the
// production default: no PresentDigests stub, so Apple Container's present set
// comes from recordedPresentDigests, fed by the record deliverViaArchive writes.
//
//  1. A is delivered in full and recorded.
//  2. B shares A's base, and its archive carries only B's own layer.
//  3. A's image is removed from the runtime; the next delivery must neither
//     count A's record nor keep it (reaped with the image).
func TestAppleContainersPresentSetIsItsOwnDeliveryRecord(t *testing.T) {
	withBuildDir(t)
	f := newFakeRuntime()
	var out bytes.Buffer
	deliver := func(storePath string) LoadResult {
		t.Helper()
		o := acOpts(storePath, f, &out)
		o.PresentDigests = nil // the real default for Runtime "container"
		res := AutoLoadImage(o)
		if !res.OK {
			t.Fatalf("delivery of %s failed: %s", storePath, out.String())
		}
		return res
	}

	pathA := hexManifest(t, "ac-A", "base", "topA")
	resA := deliver(pathA)
	recA := recordPathFor(pathA)
	data, err := os.ReadFile(recA)
	if err != nil {
		t.Fatalf("no delivery record after a successful load: %v", err)
	}
	var rec deliveryRecord
	must(t, json.Unmarshal(data, &rec))
	if rec.Ref != resA.Ref || !reflect.DeepEqual(sorted(rec.Layers), digests("base", "topA")) {
		t.Errorf("record = %+v, want ref %q and A's layers", rec, resA.Ref)
	}

	pathB := hexManifest(t, "ac-B", "base", "topB")
	deliver(pathB)
	if got, want := sorted(f.archiveBlobs[1]), digests("topB"); !reflect.DeepEqual(got, want) {
		t.Errorf("B's archive carried %v, want only B's own layer %v", got, want)
	}

	// `container image rm` of A and of B: their records describe nothing now.
	delete(f.present, resA.Ref)
	delete(f.present, JailImageRef("container", pathB))
	pathC := hexManifest(t, "ac-C", "base", "topC")
	deliver(pathC)
	if got, want := sorted(f.archiveBlobs[2]), digests("base", "topC"); !reflect.DeepEqual(got, want) {
		t.Errorf("C's archive carried %v, want every layer — the records' images are gone %v", got, want)
	}
	if _, err := os.Stat(recA); err == nil {
		t.Error("A's record survived its image; it must be reaped with the image")
	}
}

// TestADeliveryRecordIsKeptButNotTrustedWhenTheRuntimeCannotBeAsked is the
// tri-state half: "the image is gone" licenses deleting the record, "I could not
// ask" licenses nothing — the record is neither counted nor removed.
func TestADeliveryRecordIsKeptButNotTrustedWhenTheRuntimeCannotBeAsked(t *testing.T) {
	withBuildDir(t)
	pathA := hexManifest(t, "ac-unknown", "base")
	must(t, writeDeliveryRecord(pathA, "yolo-jail:x", []LayerInfo{{Digest: hexDigest("base")}}))
	o := &AutoLoadOptions{Runtime: "container", Run: func([]string) (int, bool) { return 0, false }}
	if got := o.recordedPresentDigests(); len(got) != 0 {
		t.Errorf("an unaskable runtime's record was trusted: %v", got)
	}
	if _, err := os.Stat(recordPathFor(pathA)); err != nil {
		t.Errorf("the record was removed on an unanswered probe: %v", err)
	}
	o.Run = func([]string) (int, bool) { return 0, true }
	if got := o.recordedPresentDigests(); !reflect.DeepEqual(got, digestSet("base")) {
		t.Errorf("a record whose image is present = %v, want its layers", got)
	}
}

// TestAPlaceholderIsNeverAPathOutsideTheLayout: the present set comes from a
// runtime's output or a file on disk and becomes a path, so only the exact digest
// shape may be seeded.
func TestAPlaceholderIsNeverAPathOutsideTheLayout(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout")
	seeded, err := seedPlaceholders(layout, map[string]struct{}{
		"sha256:../../escape":               {},
		"sha512:" + strings.Repeat("a", 64): {},
		"sha256:" + strings.Repeat("A", 64): {},
		hexDigest("ok"):                     {},
	})
	must(t, err)
	if len(seeded) != 1 || filepath.Dir(seeded[0]) != filepath.Join(layout, "blobs", "sha256") {
		t.Errorf("seeded %v, want exactly the one well-formed digest inside the layout", seeded)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Error("a traversal digest created a file outside the layout")
	}
}

// TestTheTarConsumesTheLayoutAndCarriesItWhole: every file of the layout is in
// the archive under its layout-relative name, and each blob is gone from the
// layout once archived (the full archive must not cost two images of disk).
func TestTheTarConsumesTheLayoutAndCarriesItWhole(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout")
	blobs := filepath.Join(layout, "blobs", "sha256")
	must(t, os.MkdirAll(blobs, 0o755))
	must(t, os.WriteFile(filepath.Join(layout, "oci-layout"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(layout, "index.json"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(blobs, "aa"), []byte("blob-a"), 0o644))
	archive := filepath.Join(dir, "x.oci-archive.tmp")
	size, err := tarLayoutConsuming(layout, archive)
	must(t, err)
	if st, _ := os.Stat(archive); st == nil || st.Size() != size || size == 0 {
		t.Errorf("reported size %d does not match the archive", size)
	}
	if got := mustBlobs(t, archive); !reflect.DeepEqual(got, []string{"sha256:aa"}) {
		t.Errorf("archive blobs = %v", got)
	}
	if _, err := os.Stat(filepath.Join(blobs, "aa")); err == nil {
		t.Error("an archived blob is still in the layout")
	}
}

func mustBlobs(t *testing.T, archive string) []string {
	t.Helper()
	b, err := archiveBlobDigests(archive)
	must(t, err)
	return b
}

// TestTheArchiveDeliveryWorksOutsideEveryJailMount pins where an archive
// delivery's files live, through the production call site: the path the LOADER
// is handed, and the record the Apple Container arm writes.
//
// THE SECURITY PROPERTY FIRST. cache/ is bind-mounted read-write into every jail,
// and a file there that decides what the host loads under a content ref is a
// substitution channel (paths.ImageDeliveryDir). So the archive, its layout and
// the record must all sit under paths.ImageDeliveryDir(), in a directory only the
// user can enter. Then the old property: nothing the delivery writes ends in
// `.tar`, which the degraded fallback's newestTars would load and mis-name
// :latest. And each attempt's directory is its own, so two attempts share no file.
func TestTheArchiveDeliveryWorksOutsideEveryJailMount(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "where", "base", "top")
	f := newFakeRuntime()
	var out bytes.Buffer
	o := acOpts(storePath, f, &out)
	o.PresentDigests = func() map[string]struct{} { return digestSet("base") }
	var archives []string
	o.LoadArchive = func(p string) bool {
		archives = append(archives, p)
		if len(archives) == 1 {
			return false // force the retry, so there are two attempts to compare
		}
		return f.loadArchive(p)
	}
	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("delivery failed: %s", out.String())
	}
	if len(archives) != 2 {
		t.Fatalf("loader ran %d time(s), want 2", len(archives))
	}
	delivery := paths.ImageDeliveryDir()
	cache := paths.GlobalCache()
	if rel, err := filepath.Rel(cache, delivery); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("ImageDeliveryDir %q is inside the jail-mounted cache %q", delivery, cache)
	}
	for _, a := range archives {
		work := filepath.Dir(a)
		if filepath.Dir(work) != delivery {
			t.Errorf("the loader was handed %q, not a file in a directory of %q", a, delivery)
		}
		if strings.HasPrefix(a, cache+string(filepath.Separator)) {
			t.Errorf("the archive %q is under the jail-mounted cache", a)
		}
		if filepath.Ext(a) == ".tar" {
			t.Errorf("%q ends in .tar, which the degraded fallback's newestTars matches", a)
		}
		if !strings.HasSuffix(work, DeliveryWorkSuffix) {
			t.Errorf("attempt directory %q lacks %q, so no sweep would find its leftover", work, DeliveryWorkSuffix)
		}
		if _, err := os.Stat(work); err == nil {
			t.Errorf("attempt directory %q survived the launch", work)
		}
	}
	if filepath.Dir(archives[0]) == filepath.Dir(archives[1]) {
		t.Errorf("the retry reused the first attempt's directory %q", filepath.Dir(archives[0]))
	}
	if len(f.layouts) != 2 || filepath.Dir(f.layouts[0]) != filepath.Dir(archives[0]) {
		t.Errorf("layouts %v are not beside the archives %v", f.layouts, archives)
	}
	if st, err := os.Stat(delivery); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("delivery directory mode = %v (err %v), want 0700", st, err)
	}
	rec := filepath.Join(delivery, keyFor(storePath)+deliveryRecordSuffix)
	if _, err := os.Stat(rec); err != nil {
		t.Errorf("the delivery record is not at %q: %v", rec, err)
	}
	if m, _ := filepath.Glob(filepath.Join(cache, "images", "*")); len(m) != 0 {
		t.Errorf("the delivery wrote into the jail-mounted cache: %v", m)
	}
}

// archiveBlobSizes reads an archive yolo wrote: each blob's digest and size.
func archiveBlobSizes(t *testing.T, path string) map[string]int64 {
	t.Helper()
	fh, err := os.Open(path)
	must(t, err)
	defer fh.Close()
	out := map[string]int64{}
	tr := tar.NewReader(fh)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		must(t, err)
		if h.Typeflag == tar.TypeReg && strings.HasPrefix(h.Name, "blobs/sha256/") {
			out["sha256:"+strings.TrimPrefix(h.Name, "blobs/sha256/")] = h.Size
		}
	}
}

// TestAKilledDeliverysLeftoverIsNeverTakenForThisOnesBlobs: a launch killed
// mid-copy (SIGKILL, so its deferred cleanup never ran) leaves a layout holding a
// zero-byte placeholder. The next delivery of the same image, with an EMPTY
// present set, must still ship that blob's real bytes. A delivery that reused
// the leftover directory would let the copier take the placeholder for a
// written blob, and the archive would carry a zero-byte layer — a digest
// mismatch on podman with no retry to catch it.
func TestAKilledDeliverysLeftoverIsNeverTakenForThisOnesBlobs(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "leftover", "base", "top")
	topHex := strings.TrimPrefix(hexDigest("top"), "sha256:")
	f := newFakeRuntime()
	var out bytes.Buffer

	// Attempt 1 dies after the placeholder exists. Its directory is recorded,
	// then put back exactly as a SIGKILL would have left it.
	o := macPodmanOpts(storePath, f, &out)
	var killed string
	o.LayerCopy = func(_, dest string, _ []string) (CopyReport, bool) {
		killed, _, _ = strings.Cut(strings.TrimPrefix(dest, "oci:"), ":")
		return CopyReport{}, false
	}
	if AutoLoadImage(o).OK {
		t.Fatal("a failed copy produced a green launch")
	}
	must(t, os.MkdirAll(filepath.Join(killed, "blobs", "sha256"), 0o755))
	must(t, os.WriteFile(filepath.Join(killed, "blobs", "sha256", topHex), nil, 0o644))

	o = macPodmanOpts(storePath, f, &out)
	var sizes map[string]int64
	o.LoadArchive = func(p string) bool {
		sizes = archiveBlobSizes(t, p)
		return f.loadArchive(p)
	}
	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("second delivery failed: %s", out.String())
	}
	if got := sizes[hexDigest("top")]; got == 0 {
		t.Errorf("the archive carried a %d-byte top layer: the killed delivery's placeholder "+
			"was taken for this delivery's blob (blobs: %v)", got, sizes)
	}
}

// TestTheDeliveryRecordIsWrittenOnlyForALandedAppleContainerLoad pins the two
// halves of the record's guard at the production call site. A FAILED delivery
// must not record anything (the next launch would leave out layers nobody
// loaded), and podman on macOS must never write one (its present set is podman's
// own answer; a record would be a second, unread source of truth).
func TestTheDeliveryRecordIsWrittenOnlyForALandedAppleContainerLoad(t *testing.T) {
	t.Run("a failed Apple Container delivery records nothing", func(t *testing.T) {
		withBuildDir(t)
		storePath := hexManifest(t, "ac-fail", "base", "top")
		f := newFakeRuntime()
		var out bytes.Buffer
		o := acOpts(storePath, f, &out)
		o.PresentDigests = func() map[string]struct{} { return digestSet("base") }
		o.LoadArchive = func(string) bool { return false }
		if AutoLoadImage(o).OK {
			t.Fatal("a loader that always fails produced a green launch")
		}
		if _, err := os.Stat(recordPathFor(storePath)); err == nil {
			t.Error("a delivery record was written for a delivery that never loaded")
		}
	})
	t.Run("podman on macOS records nothing", func(t *testing.T) {
		withBuildDir(t)
		storePath := hexManifest(t, "mac-norecord", "base", "top")
		f := newFakeRuntime()
		var out bytes.Buffer
		if res := AutoLoadImage(macPodmanOpts(storePath, f, &out)); !res.OK {
			t.Fatalf("delivery failed: %s", out.String())
		}
		if m, _ := filepath.Glob(filepath.Join(paths.ImageDeliveryDir(), "*"+deliveryRecordSuffix)); len(m) != 0 {
			t.Errorf("podman on macOS wrote delivery records %v; only Apple Container reads them", m)
		}
	})
}

// TestACorruptDeliveryRecordIsRemovedWhenRead: a record that does not parse is
// yolo's own broken file (a torn write from before the rename, a hand edit), and
// the probe that finds it through the production default removes it rather than
// stepping over it on every launch.
func TestACorruptDeliveryRecordIsRemovedWhenRead(t *testing.T) {
	withBuildDir(t)
	stale := hexManifest(t, "ac-corrupt", "base")
	must(t, writeDeliveryRecord(stale, "yolo-jail:x", []LayerInfo{{Digest: hexDigest("base")}}))
	must(t, os.WriteFile(recordPathFor(stale), []byte("{not json"), 0o600))

	storePath := hexManifest(t, "ac-next", "base", "top")
	f := newFakeRuntime()
	var out bytes.Buffer
	o := acOpts(storePath, f, &out)
	o.PresentDigests = nil // the real default: recordedPresentDigests
	if res := AutoLoadImage(o); !res.OK {
		t.Fatalf("delivery failed: %s", out.String())
	}
	if _, err := os.Stat(recordPathFor(stale)); err == nil {
		t.Error("an unparseable delivery record survived being read")
	}
}

// TestASeededPathThatAlreadyExistsIsRefused: seedPlaceholders creates each
// placeholder O_EXCL. A file already at that path is not a placeholder, and
// calling it one would let removePlaceholders delete it. (The attempt directory
// is new, so production never meets one; this is the guard should that change.)
func TestASeededPathThatAlreadyExistsIsRefused(t *testing.T) {
	layout := filepath.Join(t.TempDir(), "layout")
	blobs := filepath.Join(layout, "blobs", "sha256")
	must(t, os.MkdirAll(blobs, 0o755))
	existing := filepath.Join(blobs, strings.TrimPrefix(hexDigest("real"), "sha256:"))
	must(t, os.WriteFile(existing, nil, 0o644))
	seeded, err := seedPlaceholders(layout, digestSet("real"))
	if err == nil {
		t.Fatal("seeding over an existing file succeeded")
	}
	for _, p := range seeded {
		if p == existing {
			t.Errorf("the pre-existing file was returned as a placeholder: %v", seeded)
		}
	}
}

// TestOnlyALoaderFailureRetriesTheDelta: the present set reaches nothing before
// the load except which placeholders exist, so a COPIER failure would fail the
// same way with an empty set. It must fail the launch once — no second full
// copy, no retry line.
func TestOnlyALoaderFailureRetriesTheDelta(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "copy-fail", "base", "top")
	f := newFakeRuntime()
	var out bytes.Buffer
	o := macPodmanOpts(storePath, f, &out)
	o.PresentDigests = func() map[string]struct{} { return digestSet("base") }
	copies := 0
	o.LayerCopy = func(string, string, []string) (CopyReport, bool) { copies++; return CopyReport{}, false }
	if AutoLoadImage(o).OK {
		t.Fatal("a failing copier produced a green launch")
	}
	if copies != 1 {
		t.Errorf("the copier ran %d time(s), want 1: a copy failure is not the present set's to fix", copies)
	}
	if strings.Contains(out.String(), "retrying once") {
		t.Errorf("a copier failure announced the delta retry: %q", out.String())
	}
}

// TestTheTarRefusesAnEntrySwappedAfterItWasListed is the defense in depth under
// the private directory: appendFile archives only the file the walk listed. Two
// swaps, one per guard:
//
//   - the entry becomes a SYMLINK to the original file, moved aside (the same
//     inode the walk saw, so only O_NOFOLLOW catches it — the shape that reads
//     any host file when the target is something else);
//   - the entry is REPLACED by a different regular file (only the same-file
//     check catches it).
func TestTheTarRefusesAnEntrySwappedAfterItWasListed(t *testing.T) {
	for _, tc := range []struct {
		name string
		swap func(t *testing.T, p string)
	}{
		{"a symlink to the listed file", func(t *testing.T, p string) {
			aside := p + ".aside"
			must(t, os.Rename(p, aside))
			must(t, os.Symlink(aside, p))
		}},
		{"a different regular file", func(t *testing.T, p string) {
			must(t, os.Remove(p))
			must(t, os.WriteFile(p, []byte("substituted"), 0o644))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "blob")
			must(t, os.WriteFile(p, []byte("original"), 0o644))
			walked, err := os.Lstat(p)
			must(t, err)
			tc.swap(t, p)
			var buf bytes.Buffer
			if err := appendFile(tar.NewWriter(&buf), p, "blobs/sha256/x", walked); err == nil {
				t.Errorf("an entry swapped after the walk was archived (%d bytes written)", buf.Len())
			}
		})
	}
}
