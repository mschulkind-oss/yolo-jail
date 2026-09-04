package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
)

// autoLoadImage builds/loads the nix jail image, returning OK=false when no
// runnable image could be made available (the caller must exit(1) instead of a
// doomed launch). The failure diagnosis uses the same nixdiag classifier +
// Linux-builder remedy the check slice uses, so the actionable "needs a Linux
// builder / cached image" text matches.
//
// The LoadResult's Ref is not optional decoration: since C2 the loaded image is
// named by the hash of the store path it was built from, so the ref this call
// returns is the ONLY name that identifies the image this launch just made
// ready. runNormal threads it into assembleInput.imageRef, which is the single
// source the container argv and the host-service insert point both read.
func (o *Options) autoLoadImage(cfg *jsonx.OrderedMap, rt, repoRoot string) image.LoadResult {
	// The IMAGE is Linux whatever the host is, so a `platforms` filter here asks about
	// the image's platform and not the machine's.
	extra := config.EffectivePackages(cfg, config.PlatformLinux)
	remedy := nixdiag.LinuxBuilderRemedy()
	return image.AutoLoadImage(image.AutoLoadOptions{
		Runtime:  rt,
		RepoRoot: repoRoot,
		// Never skip the build on the run path: Run() now hard-exits before here
		// when the repo root is unresolved (a missing flake is fatal, not a
		// degraded cached-image launch), so repoRoot is always non-empty here.
		// SkipBuild stays a field on AutoLoadOptions as a dormant seam.
		SkipBuild:     false,
		ExtraPackages: extra,
		Out:           o.Stdout,
		ProgressTTY:   o.IsTTYStdout(),
		IsMacOS:       o.IsMacOS,
		Getpid:        o.Getpid,
		DiagnoseFailure: func(tail []string) (string, string) {
			return nixdiag.DiagnoseNixBuildFailure(tail, o.IsMacOS, remedy)
		},
		// Storage-lifecycle §1: root the running image's closure host-side so a
		// `nix-collect-garbage` at any moment can't delete live binaries. In-jail
		// this is futile — the gcroots dir is unmounted and the host daemon prunes
		// a jail-home root as stale (verified) — so register only host-side; the
		// AutoLoadImage seam defaults to a no-op when left nil.
		RegisterRoot: o.rootImageFn(),
	})
}

// rootImageFn returns the durable-GC-root registrar for the loaded image, or nil
// (→ AutoLoadImage's no-op) when we can't usefully root: in-jail the gcroots dir
// is unmounted and any root pointing into the jail's /home is pruned as stale by
// the host daemon, so rooting is a lie there. Only the host `yolo run` path holds
// a durable root that survives a host GC.
func (o *Options) rootImageFn() func(string) {
	if o.inJail() {
		return nil
	}
	return func(storePath string) { _, _ = image.RegisterImageRoot(storePath, o.Stdout) }
}
