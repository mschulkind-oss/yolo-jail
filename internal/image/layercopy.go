package image

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// C9 — DELIVER THE IMAGE AS A NEGOTIATED COPY, NOT AS A STREAM.
// docs/design/layer-aware-image-delivery.md
//
// This file replaces streamload.go, and the replacement is a different SHAPE of
// problem rather than a faster version of the same one. What was here:
// `.#ociImage`'s out-link was an executable whose stdout was a 3.47 GB
// docker-archive, joined by a pipe to `podman load`. A docker-archive is a
// sequential tar with no protocol for asking the destination which blobs it
// already holds, so every launch that saw a new store path re-shipped the whole
// image — 81 s of a 96 s load on the maintainer's host, and the customisation
// layer that a `flake.nix` edit is guaranteed to move was 0.78% of it (§2.1,
// §2.2). `podman load` also spooled all 3.55 GB to /var/tmp before parsing, so
// "no tar" was true of yolo's disk and not of podman's.
//
// What is here now: `.#ociImage` is a nix2container image.json NAMING its layer
// digests, and `skopeo copy nix:<image.json> <dest>` asks the destination for
// each blob before sending it. One process, no pipe, no archive on either side.
//
// MEASURED in this jail, 2026-09-09, `podman --root` on a virgin overlay store:
//
//	cold, legacy stream | podman load    39.5 s   (3.47 GB, 99 layers)
//	cold, this copy                      24.0 s   (3.45 GB, 91 layers)
//	a flake.nix-only edit, destination holding the previous image:
//	  legacy, IDENTICAL image (floor)    12.8 s   — it re-spools regardless
//	  this copy, 1 layer of 26.2 MiB      1.4 s
//
// THE FOUR PIPE HAZARDS streamload.go documented ARE GONE, and it is worth
// saying why rather than letting the absence look like an oversight. They were
// all consequences of two processes joined by a pipe: a stream that exits
// nonzero after a plausible prefix, a loader that stops reading (EPIPE), the
// close-order deadlock, and a byte total that lies. There is one process now and
// nothing writes into it, so there is one exit status to read. What replaces
// them is `tailWriter` on skopeo's stderr — kept intact from streamload.go,
// because a copy that fails must still say what skopeo said.

// copyTailLines is how many stderr lines the copier retains for a failure
// report. skopeo's stderr is a handful of lines (its per-blob progress goes to
// STDOUT, measured 2026-09-09), so the interesting part is all of it.
const copyTailLines = 12

// CopyReport is what a completed copy says about the bytes, and it exists
// because that ratio IS the claim this whole change makes (§3.10: "the launch
// prints bytes copied *and* bytes skipped, because that ratio is the whole
// claim"). A duration alone cannot distinguish "the negotiation worked" from
// "this machine is fast today".
//
// Copied is a CEILING, not an exact count: it is the size of every layer whose
// digest was not already present in the destination when we looked. An orphan
// blob from an interrupted earlier copy (skopeo commits the image record last,
// so orphans are the normal interrupted state) is invisible to the probe and
// therefore counted as copied. Erring toward over-reporting the cost is the safe
// direction for a number whose job is to keep a performance claim honest.
type CopyReport struct {
	// Layers is how many layers the image has.
	Layers int
	// Total is the sum of every layer's size.
	Total int64
	// CopiedLayers / Copied cover the layers the destination did not have.
	CopiedLayers int
	Copied       int64
}

// Skipped is the bytes the destination already held — Total minus Copied.
func (r CopyReport) Skipped() int64 { return r.Total - r.Copied }

// SkippedLayers is the layer count the destination already held.
func (r CopyReport) SkippedLayers() int { return r.Layers - r.CopiedLayers }

// String renders the one line a launch prints after a copy.
func (r CopyReport) String() string {
	return fmt.Sprintf("%d layer(s), %s copied; %d layer(s), %s already present",
		r.CopiedLayers, FormatImageSize(r.Copied),
		r.SkippedLayers(), FormatImageSize(r.Skipped()))
}

// imageManifest is the part of nix2container's image.json this package reads.
// Deliberately a narrow struct rather than the upstream type: the only fields
// with a consumer here are the per-layer digest and size, and depending on the
// generator's Go module for a two-field read would make a nix-level dependency
// into a Go-level one — which §4's "the Go side is untouched" is a promise about
// (`vendor/` does not grow, the goSrc fileset does not grow).
type imageManifest struct {
	Layers []struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"layers"`
}

// ReadLayerInventory reads an image.json and returns its layers as
// (digest, size) pairs in stack order, bottom first.
//
// The digest doubles as the DIFFID here, which is what makes the
// already-present probe below possible at all: nix2container writes
// uncompressed layers and sets Digest and DiffIDs to the same value
// (nix/layers.go newLayers), and `podman image inspect` reports diffIDs in
// .RootFS.Layers. Verified 2026-09-09 against a built image: every
// `digest` equals its `diff_ids`, and the values match what podman lists for the
// copied image.
func ReadLayerInventory(imageJSON string) ([]LayerInfo, error) {
	data, err := os.ReadFile(imageJSON)
	if err != nil {
		return nil, err
	}
	var m imageManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := make([]LayerInfo, 0, len(m.Layers))
	for _, l := range m.Layers {
		out = append(out, LayerInfo{Digest: l.Digest, Size: l.Size})
	}
	return out, nil
}

// LayerInfo is one layer's digest and uncompressed size.
type LayerInfo struct {
	Digest string
	Size   int64
}

// ReportFor splits an inventory against the digests a destination already holds.
// An empty/nil present set yields "everything is copied", which is the honest
// answer when the probe could not run — see PresentLayerDigests.
func ReportFor(layers []LayerInfo, present map[string]struct{}) CopyReport {
	r := CopyReport{Layers: len(layers)}
	for _, l := range layers {
		r.Total += l.Size
		if _, have := present[l.Digest]; !have {
			r.CopiedLayers++
			r.Copied += l.Size
		}
	}
	return r
}

// ImageListIDsCmd lists the image IDs of every jail image in a runtime — the
// first half of the already-present probe.
func ImageListIDsCmd(runtime string) []string {
	return []string{runtime, "images", "--quiet", JailImageRepository(runtime)}
}

// ImageLayerDigestsCmd asks a runtime for the diffIDs of the given images, one
// per line.
func ImageLayerDigestsCmd(runtime string, ids []string) []string {
	argv := []string{runtime, "image", "inspect", "--format",
		"{{range .RootFS.Layers}}{{println .}}{{end}}"}
	return append(argv, ids...)
}

// PresentLayerDigests reports the layer digests a runtime's store already holds,
// derived from the jail images it already has. capture runs an argv and returns
// its stdout; ok=false for anything that did not run cleanly.
//
// IT IS A REPORTING INSTRUMENT AND NOTHING ELSE, which is why it is allowed to
// be approximate. The COPY does its own per-blob negotiation with
// containers-storage and neither consults nor needs this; a wrong answer here
// changes a printed number and no behavior. That is the whole reason it may
// enumerate only OUR images (a base layer shared with some unrelated image would
// be reported as copied) and may return empty on any failure.
//
// Two subprocesses, and only on a launch that is about to copy — i.e. one that
// used to spend 81 s in this span.
func PresentLayerDigests(runtime string, capture func(argv []string) (string, bool)) map[string]struct{} {
	present := map[string]struct{}{}
	if capture == nil {
		return present
	}
	idsOut, ok := capture(ImageListIDsCmd(runtime))
	if !ok {
		return present
	}
	var ids []string
	for _, line := range strings.Split(idsOut, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			ids = append(ids, s)
		}
	}
	if len(ids) == 0 {
		return present
	}
	digestsOut, ok := capture(ImageLayerDigestsCmd(runtime, ids))
	if !ok {
		return present
	}
	for _, line := range strings.Split(digestsOut, "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "sha256:") {
			present[s] = struct{}{}
		}
	}
	return present
}

// ImageCopierOutLink is the durable `nix build --out-link` path for one source
// tree's image copier: BUILD_DIR/image-copier-<sha16 of repoRoot>.
//
// SAME SHAPE AND SAME TWO PROHIBITIONS AS JailPrefixOutLink, for the same
// reasons (prefix.go says them at length): keyed by the SOURCE so re-building a
// tree replaces one link instead of accumulating one per build; NOT under
// `build/roots/`, whose reaper deletes any root that is not a loaded image; and
// NOT named `run-result-*`, which prune.SweepDanglingOutLinks scans.
//
// The out-link IS the copier's GC root, and it has to be: the copier is a store
// path this code EXECUTES, so a `nix-collect-garbage` that reclaimed it would
// leave the next launch unable to deliver an image until it rebuilt skopeo from
// source.
//
// nix writes a SECOND link beside it — `<outLink>-man`, for skopeo's man output
// (verified 2026-09-09) — and that one is left alone: it is replaced in place by
// the next build of the same tree exactly as this one is, so it cannot
// accumulate, and it pins a few hundred KB of man pages that are part of the
// copier's own derivation anyway.
func ImageCopierOutLink(repoRoot string) string {
	return filepath.Join(paths.BuildDir(), "image-copier-"+keyFor(repoRoot))
}

// BuildImageCopier realizes `.#imageCopier` in repoRoot and returns the path of
// the skopeo binary inside it, plus the retained nix stderr tail. A "" path
// means the build FAILED (runNixBuild's contract) and the caller must refuse the
// launch — there is no second delivery mechanism to fall back to (§3.5).
//
// It is built LAZILY, only on a launch that is about to copy, because a launch
// whose image is already loaded should build nothing. Cold it is 2m27s against
// our nixpkgs (MEASURED 2026-09-09: the `nix:` transport is a fetchpatch2 over
// nixpkgs' skopeo, so it is in no public cache and a `flake.lock` bump rebuilds
// it); warm it is a store lookup.
//
// NOTHING IS WIRED TO A CACHE MISS (OQ-LI1). A miss means the copier is built,
// and that is the entire consequence — no degraded path, no alternate
// transport, and no requirement that `--accept-flake-config` be honored (the
// flag is on every flake-evaluating invocation via NixFlakeFlags, but the copier
// builds from source with the project substituter absent, which is what makes
// the cache an optimization rather than a dependency).
func BuildImageCopier(repoRoot string, out io.Writer) (string, []string) {
	if out == nil {
		out = io.Discard
	}
	outLink := ImageCopierOutLink(repoRoot)
	if err := os.MkdirAll(filepath.Dir(outLink), 0o755); err != nil {
		return "", []string{"could not create build dir: " + err.Error()}
	}
	storePath, tail := runNixBuild(
		flakeBuildArgv(ImageCopierAttr, outLink, nil),
		repoRoot, os.Environ(), outLink, out)
	if storePath == "" {
		return "", tail
	}
	return ImageCopierBinary(storePath), tail
}

// ImageCopierBinary is the skopeo inside a realized `.#imageCopier` output.
//
// Spelled here rather than at the call site so the one place that knows the
// layout is the one place that names the attr. §3.2's fourth property is that a
// `PATH` lookup is NEVER the answer: the `nix:` transport is a patch, so an
// unpatched skopeo on someone's PATH would fail confusingly, and the launch runs
// the copier the flake built.
func ImageCopierBinary(storePath string) string {
	return filepath.Join(storePath, "bin", "skopeo")
}

// ContainersStorageDest is the skopeo destination for a podman load: the
// content-addressed ref, named ON THE WAY IN.
//
// C2 SURVIVES AND GETS STRONGER. nix2container's image.json carries no repo:tag
// at all (types.Image has no name field), so the destination argv is the ONLY
// name an image can get — there is no baked `:latest` left for a post-load
// `podman tag` to read, and therefore no window in which a concurrent load of a
// different config could bind this ref to someone else's image. StreamRepoTag
// used to buy that property by overriding the archive's RepoTags; the property
// is now structural and the function is gone.
func ContainersStorageDest(contentRef string) string {
	return "containers-storage:" + contentRef
}

// OCIArchiveDest is the skopeo destination for the Apple Container path, and
// DockerArchiveDest the one for podman on macOS: an archive at `file`, carrying
// `ref` as the image's name.
//
// Two formats because two loaders: `container image load -i` wants an OCI
// layout, `podman load -i` a docker-archive. Both are the shape a backend that
// cannot be copied into directly needs — see `deliverViaArchive`
// (internal/image/autoload.go) for which backends those are and why.
//
// THE FILE COMES FIRST AND THE REF LAST, and skopeo splits at the FIRST colon:
// its archive transports cannot express a path containing one, while the
// reference very much does (`yolo-jail:<key>`). Reversing them would make the
// filename unparseable.
func OCIArchiveDest(file, ref string) string {
	return "oci-archive:" + file + ":" + ref
}

// DockerArchiveDest is OCIArchiveDest for `podman load -i`.
func DockerArchiveDest(file, ref string) string {
	return "docker-archive:" + file + ":" + ref
}

// copyImage runs ONE `skopeo copy nix:<imageJSON> <dest>` and reports whether it
// succeeded, printing the actionable reason itself when it did not — because
// only here is skopeo's own stderr in hand, and "Error loading image into
// podman." with no cause is the C1 defect one layer down.
//
// `--insecure-policy` because the SOURCE is a local nix store path rather than a
// registry: there is no signature to verify and no policy file to consult, and
// requiring /etc/containers/policy.json to exist on every host would make
// delivery depend on a file nothing else here needs. nix2container's own
// copy-to-* wrappers pass it for the same reason.
func copyImage(copier, imageJSON, dest string, out io.Writer) bool {
	cmd := exec.Command(copier, "--insecure-policy", "copy", "nix:"+imageJSON, dest)
	tail := &tailWriter{max: copyTailLines}
	cmd.Stderr = tail
	// skopeo's per-blob progress goes to stdout and duplicates the report the
	// caller prints from the layer inventory, so it is discarded (the same choice
	// the streamed load made for `podman load`'s "Loaded image:" line).
	cmd.Stdout = nil
	err := cmd.Run()
	if err == nil {
		return true
	}
	code, known := exitCodeOf(cmd)
	// NO IMAGE WAS WRITTEN, and saying so is a requirement rather than a
	// courtesy (§3.8): skopeo commits the image record LAST, so a failed or
	// interrupted copy leaves orphan blobs and no image under the ref. The reader
	// must never be left guessing whether a partial image is now runnable.
	reportPipeEnd(out, "the image copy failed; NO image was written to the destination",
		code, known, tail.tail())
	return false
}

// copyImageWithRetry is copyImage plus EXACTLY ONE immediate retry, no backoff.
//
// The bound is the point, and it is the same bound (and the same reasoning) the
// Apple Container cache recovery has always used: one recovery from a transient
// loss — a neighbour holding the c/storage lock and timing out, a blob write
// interrupted — never a loop that re-copies gigabytes forever. Layers the first
// attempt already wrote are reused by the retry, so a retry is cheap in exactly
// the case it is for.
//
// A digest mismatch is NOT distinguished here and deliberately is not retried
// away: skopeo verifies on read and c/storage verifies the diffID, and a second
// read of the same store path produces the same bytes, so a mismatch means a
// corrupt nix store. The retry costs one wasted attempt in that case and the
// second failure abandons the launch with skopeo's own words, which name the
// digest — the honest diagnosis, with `nix store verify` as the remedy.
//
// AND THERE IS NO FALLBACK BEYOND IT (§3.5, OQ-LI5). A second failure abandons
// the launch. `streamLayeredImage` is deleted, there is no legacy knob, and a
// copy that cannot complete leaves no image under the content ref — so there is
// nothing to start from and no remedy to name. That is R8, accepted so that R3
// (two delivery mechanisms indefinitely) never becomes a live cost.
func copyImageWithRetry(copier, imageJSON, dest string, out io.Writer) bool {
	if copyImage(copier, imageJSON, dest, out) {
		return true
	}
	fmt.Fprintln(out, "Retrying the image copy once.")
	return copyImage(copier, imageJSON, dest, out)
}
