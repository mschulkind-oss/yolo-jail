package image

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// stockimage.go is the answer to "must this launch build an image at all?".
//
// THE DEFECT IT REMOVES. AutoLoadImage realized `.#ociImage` on EVERY launch and
// asked the runtime nothing first: `BuildStorePath` ran before any probe, gated
// only by SkipBuild, which the run path hardcodes false
// (internal/cli/run/imageload.go). The build was how the content ref was
// computed, so "is the image already here?" could not even be ASKED until after
// the build that question was supposed to avoid.
//
// On Linux that cost seconds. On darwin it is fatal: the image closure contains
// derivations no public cache serves (`nix-ld` is an overrideAttrs of nixpkgs'
// and this project's own cachix is unconfigured), so every darwin launch has to
// build an x86_64-linux derivation, which needs a Linux builder. The macOS
// nightly has no working one, so every launch there failed its image build even
// though the image it was handed was built from that very commit
// (docs/reference/image-staging-vs-baking.md#the-stock-tag-and-the-question-asked-before-the-build,
// the second link of the chain).
//
// WHY THE IDENTITY FIX ALONE DID NOT REACH THIS. OQ-IP1 made `imageIdentity` a
// content hash any host can compute, which fixed the integration harness's skew
// oracle. It did not touch the launcher, because the launcher never compared
// identities — it compared nothing.
//
// # THE STOCK IMAGE
//
// **Stock image** (coined here): the jail image a flake describes ON ITS OWN —
// the default variant `.#ociImage`, built with no `packages:` extras. It is the
// image every launch gets unless a workspace adds packages or the launch opts
// into store-delivered packages (`.#ociImageLean`).
//
// A stock image's ENTIRE input set is flake.nix + flake.lock, which is exactly
// what `imageIdentity` hashes (flake.nix says so where it declares the label:
// "a hash of flake.nix + flake.lock alone"). So for a stock launch, and only for
// a stock launch, the identity is a COMPLETE key: two stock images with the same
// identity are the same image, whichever host built them.
//
// That is why the identity cannot be the general oracle. It is deliberately
// invariant across the full/minimal/lean trio and across every `packages:` list
// (flake.nix, `Labels`), so an image carrying a matching identity may still be a
// LEAN image or one with extra packages baked in. Accepting one of those for a
// stock launch would be the silent-staleness defect buildfailure.go exists to
// prevent, in a new costume.
//
// # THE TAG IS THE VARIANT CLAIM
//
// So the identity is not asked of an arbitrary image. It names a TAG —
// `<repo>:stock-<64 hex>` — and only code that knows it is looking at a stock
// image ever writes that tag:
//
//   - AutoLoadImage, immediately after delivering an image it built from
//     ImageAttrDefault with no extras (beside pointLatestAt);
//   - .github/workflows/nightly-macos.yml, after loading the archive its
//     build-image job produced with `nix build .#ociImage` and no
//     YOLO_EXTRA_PACKAGES, three steps up in the same file.
//
// Neither writer is asserting "trust me, this image is current". The tag says
// only "this is a stock image whose identity is X"; whether X is what the
// launch's source tree wants is decided by the launch, from its own `nix eval`
// of its own checkout. A CI job that loaded an image from a different commit
// would tag it with THAT commit's identity and the launch would not match it.
//
// # WHAT A MATCH DOES NOT PROVE, AND WHAT THE LAUNCH DOES ABOUT IT
//
// A tag match proves the RUNTIME holds the image. It does not prove the nix
// STORE still holds the image's closure, and on podman/Linux that closure is what
// a running jail executes: the host /nix/store is bind-mounted read-only over the
// image's own (internal/cli/run/assemble.go), so every /bin/* resolves through
// it. Until 2026-09-28 a matched launch registered no GC root and recorded no
// store path, on the reasoning that it had no store path to name — which made
// every normally-launched jail's closure invisible to `yolo prune --nix-gc`
// (it could not map `stock-<hex>` to a root, so it refused while any such jail
// ran) and left the root to age out under a jail still using it.
//
// So the load that WRITES the stock tag also records which store path it tagged
// (RecordStockStorePath, BUILD_DIR/stock-images/<hex>), and a matched launch
// reads that record back (stockStorePath). An unchanged identity means an
// unchanged store path on the host that built it, so the record is exactly the
// store path the match is about. With a record whose path is still valid in the
// store, the matched launch does what every built launch does: roots the path,
// appends it to the load sentinel and returns it as LoadResult.StorePath (so the
// workspace's current-image pointer names it too).
//
// Without one — an image tagged by a yolo older than the record, one tagged by
// the macOS nightly from an archive, or a recorded path a store GC has since
// deleted — the launch says so, and what it does next depends on whether the
// jail will read the host store (AutoLoadOptions.JailReadsHostStore): if it
// will, the image is BUILT, because a jail started now would run with its tools
// missing; if it will not, the jail runs on its own copy of the store and loses
// nothing, so it starts and the missing root is disclosed. Never `nix-store
// --add-root` a path that is not valid: `--realise` would try to substitute or
// build it, which is the build this short-circuit exists to skip, disguised.

// StockImageTagPrefix is the tag namespace for a stock image. The rest of the
// tag is the identity's hex digest, verbatim — no second hash, so the shell in
// .github/workflows/nightly-macos.yml writes the same string with `${ID#sha256:}`
// and there is no two-language hash function to drift.
// TestNightlyWorkflowTagsTheStockImage pins the two spellings together.
const StockImageTagPrefix = "stock-"

// identityAlgoPrefix is the algorithm tag flake.nix writes in front of the
// digest, so a value that is not an identity at all can be REJECTED rather than
// compared. It is duplicated by integration/imageskew_test.go's own parser on
// purpose: that file is a GUARD on this code and must not import the thing it
// guards (it says so in its header).
const identityAlgoPrefix = "sha256:"

// ParseImageIdentity validates an image identity — the algorithm tag plus a
// 64-char lowercase hex digest — and returns it normalized, or ok=false.
//
// Rejecting is the load-bearing half. A pre-2026-09-12 image answers this
// question with a /nix/store path, and `nix eval` on a broken checkout can
// answer with an error message; either would otherwise be compared as a string
// and could, in principle, match.
func ParseImageIdentity(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	hex, ok := strings.CutPrefix(id, identityAlgoPrefix)
	if !ok {
		return "", false
	}
	if len(hex) != 64 || strings.TrimLeft(hex, "0123456789abcdef") != "" {
		return "", false
	}
	return id, true
}

// StockImageRef is the ref a stock image of `identity` answers to, or "" when
// identity is not an identity.
//
// It shares JailImageRepository with the content-addressed ref, so `podman
// images yolo-jail` and every "any jail image" filter in internal/prune see it
// without knowing it exists — a stock tag is a SECOND NAME for an image that
// already has its content ref, never a second image.
func StockImageRef(runtime, identity string) string {
	id, ok := ParseImageIdentity(identity)
	if !ok {
		return ""
	}
	return JailImageRepository(runtime) + ":" + StockImageTagPrefix +
		strings.TrimPrefix(id, identityAlgoPrefix)
}

// stockStorePathsDirName is BUILD_DIR's directory of stock-image records: one
// file per identity, named by the identity's 64-char hex digest (the same string
// the stock tag carries after StockImageTagPrefix), holding the store path the
// load that wrote that tag built the image from.
//
// It is how a `stock-<hex>` ref — the one a matched launch runs, and so the one
// `podman ps` reports — maps to its GC root: the root is keyed by
// ImageStoreKey(storePath), and the tag carries the identity, not the store
// path, so the mapping has to be written down by the one party that knows both.
const stockStorePathsDirName = "stock-images"

// StockStorePathsDir is that directory under an explicit build dir.
func StockStorePathsDir(buildDir string) string {
	return filepath.Join(buildDir, stockStorePathsDirName)
}

// StockTagHex returns the identity digest a stock TAG (the part of a ref after
// the colon) names, or ok=false when tag is not a stock tag.
func StockTagHex(tag string) (string, bool) {
	hex, ok := strings.CutPrefix(tag, StockImageTagPrefix)
	if !ok {
		return "", false
	}
	if _, valid := ParseImageIdentity(identityAlgoPrefix + hex); !valid {
		return "", false
	}
	return hex, true
}

// RecordStockStorePath records that the stock image of `identity` was built, on
// this host, from storePath. Replacing is the whole mechanism: one identity names
// one store path per host, and a later load of the same identity writes the same
// value. Written through a temp file and a rename, so a reader never sees half a
// path.
func RecordStockStorePath(buildDir, identity, storePath string) error {
	id, ok := ParseImageIdentity(identity)
	if !ok || !filepath.IsAbs(storePath) {
		return nil // nothing a reader could use; not worth a name
	}
	dir := StockStorePathsDir(buildDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	// CreateTemp makes the file 0600; the record is no secret, and every other
	// file under the build dir is world-readable.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if _, err := tmp.WriteString(storePath + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	dest := filepath.Join(dir, strings.TrimPrefix(id, identityAlgoPrefix))
	if err := os.Rename(tmp.Name(), dest); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// RecordedStockStorePath reads the store path recorded for the identity digest
// hex (StockTagHex's answer), or ok=false when there is no usable record.
func RecordedStockStorePath(buildDir, hex string) (string, bool) {
	if _, valid := ParseImageIdentity(identityAlgoPrefix + hex); !valid {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(StockStorePathsDir(buildDir), hex))
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(data))
	if !filepath.IsAbs(p) {
		return "", false
	}
	return p, true
}

// imageIdentityEvalTimeout bounds the eval. The build it replaces had no
// timeout, but it also could not run before the launcher had anything else to
// do; this one sits in front of EVERY launch, so a nix that wedges must not take
// the launch with it — it degrades to the build, which is what used to happen
// unconditionally.
const imageIdentityEvalTimeout = 2 * time.Minute

// EvalImageIdentity evaluates (never builds) the identity the flake at repoRoot
// would bake into its image. ok=false on any failure, which is always a
// fall-back to the build rather than a refusal: an oracle that cannot answer is
// a harness limitation, never evidence about an image.
//
// `.#imageIdentity` is a bare flake attribute, not `packages.<system>.…`, which
// is how it carries no system — nix falls back to a bare attribute after trying
// the per-system prefixes, so this spelling is the same on every host. Measured
// in this jail 2026-09-13: 0.49 s, and it touches no nixpkgs.
func EvalImageIdentity(repoRoot string) (string, bool) {
	if repoRoot == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), imageIdentityEvalTimeout)
	defer cancel()
	argv := append([]string{}, NixFlakeFlags()...)
	argv = append(argv, "eval", "--impure", "--raw", ".#imageIdentity")
	cmd := exec.CommandContext(ctx, "nix", argv...)
	cmd.Dir = repoRoot
	// stdout only: nix puts "Git tree is dirty" and the untrusted-substituter
	// warnings on stderr, and they would bury the one line we want.
	var out bytes.Buffer
	cmd.Stdout = &out
	// nixchildren.Run rather than Output: tracked, so a launch a signal ends stops this
	// eval too.
	if err := nixchildren.Run(cmd); err != nil {
		return "", false
	}
	return ParseImageIdentity(out.String())
}

// stockInputs reports whether THIS launch is asking for a stock image: the
// default flake attr, and no `packages:` extras from any of the three places one
// can arrive.
//
// The third place is the reason this is a method and not a comparison inline.
// buildImageStorePathArgs builds with `os.Environ()` and only APPENDS
// YOLO_EXTRA_PACKAGES when the config carried some — so an ambient
// YOLO_EXTRA_PACKAGES in the launching shell reaches the flake's
// `builtins.getEnv` and bakes packages into an image whose `imageIdentity` says
// nothing about them. That is a pre-existing quirk of the build path; here it
// would be a correctness hole, because it is exactly a non-stock image that
// looks stock.
func (o *AutoLoadOptions) stockInputs() bool {
	if o.Attr != "" && o.Attr != ImageAttrDefault {
		return false
	}
	if len(o.ExtraPackages) > 0 {
		return false
	}
	v, _ := o.LookupEnv("YOLO_EXTRA_PACKAGES")
	return strings.TrimSpace(v) == ""
}

// stockIdentity is the identity this launch's source tree describes, or "" when
// the launch is not a stock one or the oracle could not answer.
//
// Evaluated ONCE per AutoLoadImage call and threaded to both consumers — the
// pre-build check and the post-load tag — so a launch that builds pays for one
// eval, not two.
func (o *AutoLoadOptions) stockIdentity() string {
	if o.SkipBuild || o.RepoRoot == "" || !o.stockInputs() {
		return ""
	}
	// A flake eval: half a second warm (measured on Linux), but bounded at
	// imageIdentityEvalTimeout because a cold one fetches inputs, so it gets a
	// progress line like every other nix call on this path.
	line := o.startProgress("Evaluating the image identity with nix")
	id, ok := o.EvalIdentity(o.RepoRoot)
	line.Done(verdict(ok))
	if !ok {
		return ""
	}
	return id
}

// stockImageLoaded is the whole of the pre-build question: is the runtime
// already holding the stock image `identity` names?
//
// Returns the ref to run, or "". Every negative answer falls through to the
// build, which is what every launch did unconditionally before this existed — so
// no failure of this check can make a launch worse than it was.
func (o *AutoLoadOptions) stockImageLoaded(identity string) string {
	ref := StockImageRef(o.Runtime, identity)
	if ref == "" {
		return ""
	}
	if rc, ran := o.Run(ImageInspectCmd(o.Runtime, ref)); !ran || rc != 0 {
		return ""
	}
	return ref
}

// tagStockImage gives the image this launch is about to run its stock name, so
// the NEXT launch of an unchanged flake can skip the build, and records which
// store path that name now stands for (RecordStockStorePath), so the next
// launch can root it and `yolo prune` can map the name to its root.
//
// It runs whether this launch delivered the image or found it already present
// under its content ref: the second case is the self-heal for an image tagged
// before the record existed, which a matched launch sends here rather than
// running it unrooted (stockimage.go, the section on what a match does not
// prove).
//
// Best effort and non-fatal, exactly like pointLatestAt: the ref this launch
// runs was decided by the load. A failure costs one future build. The record is
// written only once the tag is — a record with no tag is never read, and one
// written for a tag that failed would claim a name nothing answers to.
//
// Apple Container is skipped for the reason ImageTagCmd states — there is no
// `container image tag` argv this repo can verify — so that backend keeps
// building every launch, as it does today. It is skipped TWICE: the caller
// declines to call this at all for that runtime, and the guard below repeats the
// decision so a second caller cannot lose it.
func (o *AutoLoadOptions) tagStockImage(contentRef, storePath, identity string) {
	if o.Runtime == "container" {
		return
	}
	ref := StockImageRef(o.Runtime, identity)
	if ref == "" {
		return
	}
	if rc, ran := o.Run(ImageTagCmd(o.Runtime, contentRef, ref)); ran && rc == 0 {
		if err := RecordStockStorePath(paths.BuildDir(), identity, storePath); err != nil && o.Out != nil {
			_, _ = o.Out.Write([]byte("Warning: could not record which store path " + ref +
				" was built from (" + err.Error() + ") — this launch is unaffected; the next " +
				"one cannot root the image without rebuilding it.\n"))
		}
		return
	}
	// Said out loud rather than swallowed: the cost lands on a LATER launch, so
	// a silent failure here is a rebuild nobody can attribute.
	if o.Out != nil {
		_, _ = o.Out.Write([]byte("Warning: could not tag " + contentRef + " as " + ref +
			" — this launch is unaffected; the next one will rebuild the image " +
			"instead of finding it.\n"))
	}
}

// stockStorePath is the store path a matched stock image was built from ON THIS
// HOST, proven still valid in the nix store — or "" and the reason, in the
// user's words, that it could not be.
func (o *AutoLoadOptions) stockStorePath(identity string) (string, string) {
	id, ok := ParseImageIdentity(identity)
	if !ok {
		return "", "its identity is malformed"
	}
	p, ok := RecordedStockStorePath(paths.BuildDir(), strings.TrimPrefix(id, identityAlgoPrefix))
	if !ok {
		return "", "this host has no record of the store path it was built from " +
			"(it was tagged by an older yolo, or loaded here rather than built)"
	}
	if !o.StorePathValid(p) {
		return "", "the store path this host recorded for it, " + p +
			", is no longer valid in the nix store"
	}
	return p, ""
}

// storePathValidTimeout bounds the validity probe, which sits on every matched
// launch. A wedged nix answers "not proven", which costs a build or a
// disclosed missing root — never a launch.
const storePathValidTimeout = 30 * time.Second

// nixStorePathValid is the real StorePathValid: `nix-store --check-validity`,
// which asks the store's database rather than the filesystem, so a path that is
// half-deleted or was never registered reads as absent. It realises nothing.
//
// Tracked while it runs (internal/nixchildren), so a launch a signal ends stops it
// rather than leaving it waiting on a busy daemon with no parent.
func nixStorePathValid(storePath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), storePathValidTimeout)
	defer cancel()
	return nixchildren.Run(exec.CommandContext(ctx, "nix-store", "--check-validity", storePath)) == nil
}

// reportWriter is where a launch-stream DISCLOSURE goes: Report when the caller
// supplied one, else the progress writer it already had.
//
// The fallback is what keeps this change small — every caller that never set
// Report behaves exactly as before — but the run path DOES set it, so a disclosure
// never depends on where that caller sends Out. It sent Out to the jail command's
// stdout until 2026-10-01, where a disclosure corrupted the output of whatever the
// user asked the jail to run. See AutoLoadOptions.Out.
func (o *AutoLoadOptions) reportWriter(out io.Writer) io.Writer {
	if o.Report != nil {
		return o.Report
	}
	return out
}
