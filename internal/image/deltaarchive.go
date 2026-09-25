package image

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE DELTA ARCHIVE — docs/research/macos-layer-reusing-image-delivery.md,
// "Option 3", ruled OQ-LR1 (build it) and OQ-LR3 (Apple Container's present set is
// yolo's own delivery record).
//
// The two archive backends (deliverViaArchive says which and why) used to hand
// their loader the WHOLE image every time: 3.45 GB for a `packages:` change that
// moved two layers of 27.8 MB. Both loaders read an OCI archive by extracting it
// and then copying from the resulting layout, and that copy asks the destination
// for each blob BEFORE it opens the file, so a blob the destination already holds
// never has to be in the archive at all — as long as the manifest still names it.
// (A docker-archive cannot do this: its reader refuses a tarball with a missing
// layer file before any copy starts, which is why podman on macOS moved off it.)
//
// # Terms (coined in the research doc, repeated here so this file stands alone)
//
//   - PRESENT SET: the layer digests the destination runtime already holds.
//   - DELTA ARCHIVE: an oci-archive whose manifest names every layer of the image
//     but whose blobs/ holds only the ones outside the present set. Its manifest is
//     byte-identical to the full archive's.
//   - PLACEHOLDER SEEDING: how the existing copier is made to write one. A
//     zero-byte file at blobs/sha256/<hex> for each present digest, created before
//     `skopeo copy nix:… oci:<dir>:<ref>`, makes the OCI layout destination treat
//     that blob as already written (containers/image oci/layout/oci_dest.go,
//     TryReusingBlobWithOptions: a bare os.Stat of the blob path). The
//     placeholders are removed before the layout is tarred.
//
// # Why placeholders and not the "robust variant"
//
// The research names a second way to the same archive: write the FULL layout and
// delete the present blobs before tarring. It relies on nothing in the copier,
// and it costs a full local write of every layer (6.1–7.6 s measured, plus
// 3.45 GB of transient disk) on EVERY delivery — which is most of what the delta
// exists to remove. The placeholder behaviour is pinned instead, by
// TestPlaceholderSeedingIsHonoredByTheRealCopier in integration/, which runs the
// real `.#imageCopier` and fails if it writes a seeded blob. And the failure mode
// if it ever stops honouring them is benign by construction: the copier writes
// the real blob over the placeholder, removePlaceholders keeps any seeded path
// that is no longer empty, and the archive is simply full-size — correct, only
// not smaller. The launch's report prints the archive's real size beside the
// layer figures, so that regression shows as a number rather than hiding.
//
// # One mechanism, parameterised
//
// An EMPTY present set seeds nothing and so produces the full archive, through
// the same argv and the same loader. That is what lets the retry in
// deliverViaArchive be "the same thing again with nothing left out" rather than
// a second transport (image-staging-vs-baking.md#one-mechanism-no-way-back).

// OCILayoutDest is the skopeo destination for the layout a delta archive is made
// from: an OCI image layout DIRECTORY at dir, carrying ref as the image's name
// (the org.opencontainers.image.ref.name annotation both loaders name the image
// from).
//
// THE DIRECTORY COMES FIRST AND THE REF LAST: skopeo splits the reference at the
// FIRST colon, so it cannot express a path containing one, while the ref very
// much does (`yolo-jail:<key>`). Reversing them would make the path unparseable.
func OCILayoutDest(dir, ref string) string {
	return "oci:" + dir + ":" + ref
}

// ociAcceptUncompressedFlag keeps the layers uncompressed in an `oci:` layout.
//
// LOAD-BEARING FOR REUSE, not a size preference. nix2container's layers are
// uncompressed, so each one's blob digest IS its diffID — the value
// `podman image inspect` reports and the value yolo's delivery record stores.
// skopeo's default for an OCI destination is to gzip every layer, which renames
// every blob to a compressed digest nothing on the other side can match: a
// placeholder at the uncompressed digest would then be ignored, and a loaded
// image's layers would never again line up with a present set. Measured in the
// research: the gzip layout reused 0 of 92 blobs, the uncompressed one 90.
const ociAcceptUncompressedFlag = "--dest-oci-accept-uncompressed-layers"

// THE DELIVERY'S FILES LIVE OUTSIDE THE CACHE. Each attempt gets a fresh 0700
// directory under paths.ImageDeliveryDir() — `image-delivery/<key>-<random>.delivery.tmp/`
// holding `layout/` (what the copier writes) and `image.oci-archive` (what the
// loader reads) — and the delivery records sit beside those directories. None of
// it is under cache/, which every jail mounts read-write: there, a running jail
// could repoint index.json at an image of its own (which the loader would then
// name with this launch's content ref), swap a blob for a symlink to any host
// file while the tar read it, or plant a record. paths.ImageDeliveryDir states
// the rule; TestTheArchiveDeliveryWorksOutsideEveryJailMount pins it.

// DeliveryWorkSuffix ends every per-attempt directory name. Exported for
// prune.PruneImageDelivery, which reclaims a directory of exactly this shape once
// it is past the grace floor — the leftover of a launch killed mid-delivery.
const DeliveryWorkSuffix = ".delivery.tmp"

// The two entries of an attempt's directory.
const (
	deliveryLayoutName  = "layout"
	deliveryArchiveName = "image.oci-archive"
)

// deliveryDir creates (0700) and returns paths.ImageDeliveryDir().
func deliveryDir() (string, error) {
	dir := paths.ImageDeliveryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// newDeliveryWorkDir makes one attempt's private directory: new every time
// (os.MkdirTemp, mode 0700), named by the store key so a leftover says whose it
// was. Being new is what makes a stale layout harmless: a killed launch's
// placeholders are in ITS directory, never in the one the next copy writes.
func newDeliveryWorkDir(storePath string) (string, error) {
	dir, err := deliveryDir()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, keyFor(storePath)+"-*"+DeliveryWorkSuffix)
}

// blobDigestRe is the only digest shape a placeholder may be created for. The
// present set arrives from a runtime's own output (`podman image inspect`) or
// from a record file on disk, and a digest is about to become a PATH, so anything
// that is not exactly sha256 + 64 lowercase hex is dropped rather than joined.
var blobDigestRe = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)

// seedPlaceholders creates the layout's blob directory and a zero-byte file in it
// for every digest in present, returning the paths it created.
//
// O_EXCL, because the directory was just emptied: a file that already exists
// here is not ours to call a placeholder, and treating it as one would let
// removePlaceholders delete it.
func seedPlaceholders(layoutDir string, present map[string]struct{}) ([]string, error) {
	blobs := filepath.Join(layoutDir, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		return nil, err
	}
	var seeded []string
	for d := range present {
		m := blobDigestRe.FindStringSubmatch(d)
		if m == nil {
			continue
		}
		p := filepath.Join(blobs, m[1])
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return seeded, err
		}
		if err := f.Close(); err != nil {
			return seeded, err
		}
		seeded = append(seeded, p)
	}
	sort.Strings(seeded)
	return seeded, nil
}

// removePlaceholders deletes every seeded path that is STILL EMPTY and returns
// how many the copier wrote real bytes into.
//
// A non-empty seeded path means the copier did not honour the placeholder and
// wrote the blob anyway. That blob is correct content under its own digest, so it
// is KEPT: deleting it would ship an archive missing a blob for no reason, and
// keeping it costs only bytes. A digest in the present set that this image does
// not contain is never touched by the copier and is removed here like any other.
func removePlaceholders(seeded []string) (overwritten int, err error) {
	for _, p := range seeded {
		st, statErr := os.Lstat(p)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			return overwritten, statErr
		}
		if st.Size() != 0 {
			overwritten++
			continue
		}
		if rmErr := os.Remove(p); rmErr != nil {
			return overwritten, rmErr
		}
	}
	return overwritten, nil
}

// tarLayoutConsuming writes the OCI layout at layoutDir as an uncompressed tar at
// archive — the `oci-archive` both loaders read — and returns the archive's size.
//
// CONSUMING: each blob is deleted from the layout the moment it is in the tar, so
// a FULL archive (the empty-present-set case, 3.45 GB) peaks at one image plus
// one blob of transient disk instead of two images. skopeo's own `oci-archive:`
// destination stages a whole layout in $TMPDIR and then tars it, which is the
// 2x this avoids.
//
// Entries are named relative to the layout root (`oci-layout`, `index.json`,
// `blobs/sha256/<hex>`), directories included, in a fixed order, with ownership
// zeroed: the tar's metadata is data here, and nothing downstream reads it.
func tarLayoutConsuming(layoutDir, archive string) (int64, error) {
	var files []string
	var dirs []string
	// What the walk saw, per entry: appendFile checks the file it opens is this one.
	walked := map[string]fs.FileInfo{}
	walkErr := filepath.WalkDir(layoutDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == layoutDir {
			return nil
		}
		rel, err := filepath.Rel(layoutDir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		walked[rel] = info
		switch {
		case d.IsDir():
			dirs = append(dirs, rel)
		case d.Type().IsRegular():
			files = append(files, rel)
		default:
			return fmt.Errorf("unexpected non-regular entry %q in the image layout", rel)
		}
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	// Metadata first (oci-layout, index.json), then blobs, each group sorted: a
	// deterministic order, and one where a reader that streams could find the
	// index before the blobs it names.
	sort.Strings(dirs)
	sort.SliceStable(files, func(i, j int) bool {
		bi, bj := strings.HasPrefix(files[i], "blobs"+string(filepath.Separator)),
			strings.HasPrefix(files[j], "blobs"+string(filepath.Separator))
		if bi != bj {
			return !bi
		}
		return files[i] < files[j]
	})

	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	tw := tar.NewWriter(f)
	fail := func(e error) (int64, error) {
		_ = tw.Close()
		_ = f.Close()
		return 0, e
	}
	for _, rel := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir,
			Name:     filepath.ToSlash(rel) + "/",
			Mode:     0o755,
			ModTime:  walked[rel].ModTime(),
		}); err != nil {
			return fail(err)
		}
	}
	for _, rel := range files {
		p := filepath.Join(layoutDir, rel)
		if err := appendFile(tw, p, filepath.ToSlash(rel), walked[rel]); err != nil {
			return fail(err)
		}
		if strings.HasPrefix(rel, "blobs"+string(filepath.Separator)) {
			if err := os.Remove(p); err != nil {
				return fail(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	st, err := os.Stat(archive)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// appendFile copies one regular file into the tar under name. walked is what
// the directory walk saw at path.
//
// DEFENSE IN DEPTH, on top of the directory being private. The file is opened
// O_NOFOLLOW and the OPENED file is checked — regular, the same file the walk
// listed, with the size and modification time it recorded — so an entry swapped between the walk and the open (for a symlink to a
// host file, or for different content) fails the tar instead of being archived.
func appendFile(tw *tar.Writer, path, name string, walked fs.FileInfo) error {
	src, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	// Identity alone is not enough: a file rewritten in place keeps its inode, and
	// a removed file's inode number can be reused at once by its replacement. So
	// the size and modification time the walk recorded must match too.
	if !st.Mode().IsRegular() || !os.SameFile(st, walked) ||
		st.Size() != walked.Size() || !st.ModTime().Equal(walked.ModTime()) {
		return fmt.Errorf("%s changed between listing the image layout and archiving it", name)
	}
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     0o644,
		Size:     st.Size(),
		ModTime:  st.ModTime(),
	}); err != nil {
		return err
	}
	n, err := io.Copy(tw, src)
	if err != nil {
		return err
	}
	if n != st.Size() {
		return fmt.Errorf("%s changed size while it was archived (%d of %d bytes)", name, n, st.Size())
	}
	return nil
}

// presentInImage narrows a present set to the digests this image actually has.
//
// The probe answers for the whole store (every jail image podman holds, every
// image a delivery record names), and only the intersection is what the archive
// leaves out — and what the report and the retry line should count. A nil
// inventory (the manifest could not be read) keeps the whole set: a placeholder
// for a digest the image lacks is never touched by the copier and is removed
// afterwards, so it costs a file create and nothing else.
func presentInImage(present map[string]struct{}, layers []LayerInfo, haveLayers bool) map[string]struct{} {
	if !haveLayers {
		return present
	}
	out := map[string]struct{}{}
	for _, l := range layers {
		if _, ok := present[l.Digest]; ok {
			out[l.Digest] = struct{}{}
		}
	}
	return out
}

// THE APPLE CONTAINER PRESENT SET — OQ-LR3, decided: yolo's own record of what it
// delivered, trusted only while the runtime still holds the image it names.
//
// Apple Container has no command whose output yolo already parses for layer
// digests, and inventing a parser for `container image inspect`'s JSON would tie
// the delta to a format nobody here has pinned. The record depends on no Apple
// Container output at all: after a successful load, yolo writes ref → the layer
// digests it just delivered, and a record counts only while `container image
// inspect <ref>` exits 0 — the same exit-status question AutoLoadImage already
// asks Apple Container to decide whether a ref is loaded. (The ruling's wording
// was "while `container image list` still shows that ref"; inspect's exit status
// is that fact with nothing to parse.) Apple Container's `image rm` drops the
// blobs an image orphans, so "the ref is still there" is what makes its layers
// still there.
//
// A WRONG ANSWER COSTS ONE RETRY — ON ONE UNMEASURED PREMISE. An over-claim (a
// blob gone although the ref is listed) fails the delta load, and
// deliverViaArchive retries once with nothing left out. That rests on
// `container image load` EXITING NONZERO when a manifest names a blob neither
// the archive nor its content store holds, which the research reads in its
// import code (SOURCED) and no Mac has run; the Mac commands in the research doc
// include the check. How an over-claim could arise at all is narrow: only yolo
// writes these records (they sit outside every jail mount, paths.ImageDeliveryDir),
// each after a successful load of exactly those layers under exactly that
// content ref, and `just load` loads `yolo-jail:latest`, never a content ref.

// deliveryRecordSuffix names a delivery record: image-delivery/<key>.delivered.json,
// the key being the image's own store-path key (the same 16 hex chars its content
// ref is tagged with). ⚠ It must not end in `.tmp`: PruneImageDelivery sweeps
// that suffix.
const deliveryRecordSuffix = ".delivered.json"

// deliveryRecord is one delivered image: the ref it was loaded under and the
// layer digests the archive delivered (or reused) for it.
type deliveryRecord struct {
	Ref    string   `json:"ref"`
	Layers []string `json:"layers"`
}

// writeDeliveryRecord records a successful delivery of storePath under ref.
// Written to a temp name and renamed, so a reader never sees half a record.
func writeDeliveryRecord(storePath, ref string, layers []LayerInfo) error {
	dir, err := deliveryDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, keyFor(storePath)+deliveryRecordSuffix)
	rec := deliveryRecord{Ref: ref, Layers: make([]string, 0, len(layers))}
	for _, l := range layers {
		rec.Layers = append(rec.Layers, l.Digest)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// recordedPresentDigests is Apple Container's present-set probe: the union of the
// layers of every delivery record whose ref the runtime still holds.
//
// REAPED WITH THE IMAGE. A record whose ref the runtime answers "absent" for
// (inspect ran and exited nonzero) describes nothing any more and is deleted
// here, which is the only place that learns the image is gone — Apple Container
// has no yolo image reaper (OQ-BF6) to tell. A record whose inspect could not run
// at all is KEPT and NOT counted: "gone" and "could not ask" are different
// answers, and only the first licenses a delete. A record that does not parse is
// yolo's own corrupt file and is removed.
func (o *AutoLoadOptions) recordedPresentDigests() map[string]struct{} {
	present := map[string]struct{}{}
	matches, err := filepath.Glob(filepath.Join(paths.ImageDeliveryDir(), "*"+deliveryRecordSuffix))
	if err != nil {
		return present
	}
	sort.Strings(matches)
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rec deliveryRecord
		if json.Unmarshal(data, &rec) != nil || rec.Ref == "" {
			_ = os.Remove(p)
			continue
		}
		rc, ran := o.Run(ImageInspectCmd(o.Runtime, rec.Ref))
		if !ran {
			continue
		}
		if rc != 0 {
			_ = os.Remove(p)
			continue
		}
		for _, d := range rec.Layers {
			present[d] = struct{}{}
		}
	}
	return present
}
