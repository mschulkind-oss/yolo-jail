package image

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
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
// # WHAT A MATCH DOES NOT PROVE, AND WHY THAT IS ENOUGH
//
// It does not prove the store holds the image's closure, so a matched launch
// registers no GC root and appends no load-sentinel entry: it has no store path
// to name (LoadResult.StorePath stays empty, exactly as on the degraded
// branches). Both of those are CACHE bookkeeping — a lost root costs a rebuild,
// never a running container, which is the one thing the sentinel may be cited for
// (docs/reference/image-retention.md#the-load-sentinel-and-what-it-may-be-cited-for:
// "recency answering a cache question") — and the workspace's current-image pointer, which
// is retention EVIDENCE, keeps naming the store path the launch that first
// loaded this image recorded, because an unchanged identity means an unchanged
// store path on that host.

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
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return ParseImageIdentity(string(out))
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
	id, ok := o.EvalIdentity(o.RepoRoot)
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

// tagStockImage gives an image this launch just delivered its stock name, so the
// NEXT launch of an unchanged flake can skip the build.
//
// Best effort and non-fatal, exactly like pointLatestAt beside it: the ref this
// launch runs was decided by the load. A failure costs one future build.
//
// Apple Container is skipped for the reason ImageTagCmd states — there is no
// `container image tag` argv this repo can verify — so that backend keeps
// building every launch, as it does today. It is skipped TWICE: the caller
// declines to call this at all for that runtime, in the same
// `o.Runtime != "container"` branch that skips pointLatestAt beside it — that
// branch is pointLatestAt's ONLY guard, it carries none of its own — and the
// guard below repeats the decision so a second caller cannot lose it.
func (o *AutoLoadOptions) tagStockImage(contentRef, identity string) {
	if o.Runtime == "container" {
		return
	}
	ref := StockImageRef(o.Runtime, identity)
	if ref == "" {
		return
	}
	if rc, ran := o.Run(ImageTagCmd(o.Runtime, contentRef, ref)); ran && rc == 0 {
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

// reportWriter is where a launch-stream DISCLOSURE goes: Report when the caller
// supplied one, else the progress writer it already had.
//
// The fallback is what keeps this change small — every caller that never set
// Report behaves exactly as before — but the run path DOES set it, because Out
// there is the jail command's stdout and a disclosure written to it corrupts the
// output of whatever the user asked the jail to run. See AutoLoadOptions.Out.
func (o *AutoLoadOptions) reportWriter(out io.Writer) io.Writer {
	if o.Report != nil {
		return o.Report
	}
	return out
}
