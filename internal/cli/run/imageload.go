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
func (o *Options) autoLoadImage(cfg *jsonx.OrderedMap, rt, repoRoot string, sp storePackagesPlan) image.LoadResult {
	// The IMAGE is Linux whatever the host is, so a `platforms` filter here asks about
	// the image's platform and not the machine's.
	extra := config.EffectivePackages(cfg, config.PlatformLinux)
	if sp.Active {
		// C4, AND THIS LINE IS THE WHOLE OF R2. "The baked path is retained" is per
		// LAUNCH, never per package: a package both baked and staged silently runs the
		// BAKED copy, because a boot-written PATH dir cannot outrank the image (§3.1).
		// So an opt-in launch builds the STOCK image — YOLO_EXTRA_PACKAGES unset — and
		// the entrypoint's farm is then the only copy. Exactly one mechanism is live in
		// any jail.
		//
		// It is also where the saving is: with nothing read from `builtins.getEnv`, the
		// image's derivation stops varying with `packages:` and the machine holds ONE
		// image instead of one per distinct list (§1.5, §1.9).
		extra = nil
	}
	// C5: the same launch also builds the LEAN image — no `fullPackages`, no chromium
	// half of the /lib farm — because the store profile it was handed carries them
	// instead. One dial, two candidates, and that is deliberate: C5 reuses C4's mechanism
	// wholesale, so a launch cannot be in one and not the other, and R2's "exactly one
	// mechanism live in any jail" stays a property rather than a combination to reason
	// about. The lean variant keeps the nested-podman config the CI-minimal one drops
	// (image.ImageAttrLean says why).
	attr := image.ImageAttrDefault
	if sp.Active {
		attr = image.ImageAttrLean
	}
	remedy := nixdiag.LinuxBuilderRemedy()
	load := image.AutoLoadImage
	if o.autoLoad != nil {
		load = o.autoLoad
	}
	return load(image.AutoLoadOptions{
		Runtime:  rt,
		RepoRoot: repoRoot,
		// The call site that makes the image load's phases individually visible.
		// nil on a non-timing launch, where every span is a no-op.
		Perf: o.Perf,
		// Never skip the build on the run path: Run() now hard-exits before here
		// when the repo root is unresolved (a missing flake is fatal, not a
		// degraded cached-image launch), so repoRoot is always non-empty here.
		// SkipBuild stays a field on AutoLoadOptions as a dormant seam.
		SkipBuild:     false,
		ExtraPackages: extra,
		Attr:          attr,
		Out:           o.Stdout,
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
		RegisterRoot:     o.rootImageFn(),
		LockHousekeeping: o.lockHousekeepingFn(),
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
