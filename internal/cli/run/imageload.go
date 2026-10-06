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
	load := image.AutoLoadImage
	if o.autoLoad != nil {
		load = o.autoLoad
	}
	return load(o.imageLoadOptions(cfg, rt, repoRoot, sp))
}

// imageLoadOptions is the image step's whole request, which the prewarm beside the fork-build slot
// asks with as well (imageprewarm.go), so the two build one derivation.
func (o *Options) imageLoadOptions(cfg *jsonx.OrderedMap, rt, repoRoot string, sp storePackagesPlan) image.AutoLoadOptions {
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
	opts := image.AutoLoadOptions{
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
		// THE LAUNCH STREAM IS STDERR, for every line the image half writes, and none of
		// them may be written to the jail command's stdout. `yolo -- <cmd>` passes the
		// command's output through untouched, and callers compare it exactly — two
		// integration tests assert `r.stdout == want` on an `env | grep` — so a line on
		// stdout turns into corrupted command output rather than a message. Every other
		// launch line ("Flake source:", "Jail binaries:") is already on stderr for this
		// reason.
		//
		// Out was the command's stdout until 2026-10-01, on the argument that its lines
		// are all on cold paths; a cold path is still a launch whose command's output a
		// caller reads, so a launch after a flake change printed "Image load needed" and
		// the copy report into it (TestAColdImageLoadWritesNothingOnTheCommandsStdout).
		// Report stays set beside it: it is the disclosure stream the image half's own
		// contract names, and nothing here should depend on its fallback to Out.
		Out:    o.Stderr,
		Report: o.Stderr,
		// The long steps' live progress (the nix builds, the layer copy, the archive
		// load) is drawn on Report, in place when the launch stream is a terminal.
		Progress: o.progressConfig(),
		IsMacOS:  o.IsMacOS,
		// THE STORE FACTS COME FROM THE LAUNCH'S ONE `podman info` (PR-D5 of
		// docs/design/podman-reboot-readiness.md): the readiness gate's answer, parsed by the
		// same image.ParsePodmanStoreFacts the image package's own read uses. That read ran
		// with no deadline at all, after a probe that had passed, and a failure there took
		// the bare copy a rootless store refuses.
		StoreFacts: o.storeFactsFromGate,
		Getpid:     o.Getpid,
		DiagnoseFailure: func(tail []string) (string, string) {
			return nixdiag.DiagnoseNixBuildFailure(tail, o.IsMacOS, remedy)
		},
		// Storage-lifecycle §1: root the running image's closure so a
		// `nix-collect-garbage` at any moment can't delete live binaries — on the
		// host with nix-store, in a jail as a translated root (rootImageFn).
		RegisterRoot: o.rootImageFn(),
		// The image copier's own root, which nix's --out-link is on the host and is not
		// in a jail (rootCopierFn).
		RootCopier:       o.rootCopierFn(),
		LockHousekeeping: o.lockHousekeepingFn(),
		// The assembler's own predicate for mounting the host store, not a second
		// reading of it: a jail that will resolve its /bin/* through the host
		// store must not run a stock-tag match whose closure the store cannot be
		// shown to hold (internal/image/stockimage.go).
		JailReadsHostStore: o.hostNixMounted(rt),
	}
	if o.imageIdentity != nil {
		// One eval of the identity for the launch, whichever asks first (imageprewarm.go).
		opts.EvalIdentity = o.imageIdentity.eval
	}
	return opts
}

// rootImageFn returns the durable-GC-root registrar for the loaded image, or nil
// (→ AutoLoadImage's no-op) when this launch has no root the host would honor.
//
// The registration goes through gcRooter: nix-store on the host, and in a jail the
// translated root (docs/design/in-jail-nix-roots.md NR-D2). A jail's own
// `nix-store --add-root` is pruned as stale by the host daemon, because it sends the
// jail's spelling of the link, so a jail used to get nil here; it still does when its
// launcher stated no host path map.
//
// Its warnings go to STDERR, the launch stream, for the reason Report does above: stdout is
// the jailed command's. They went to stdout until 2026-10-01
// (TestAnImageRootRefusalGoesToTheLaunchStream).
func (o *Options) rootImageFn() func(string) {
	root := o.gcRooter()
	if root == nil {
		return nil
	}
	return func(storePath string) { _, _ = image.RegisterImageRoot(storePath, root, o.Stderr) }
}

// rootCopierFn registers the image copier's out-link as a translated root in a jail
// (docs/design/in-jail-nix-roots.md NR-D2), or is nil. On the host nix's own
// `--out-link` registration is the copier's root already; in a jail that registration
// carries the jail's spelling of the link, which the host daemon prunes as stale, and a
// jail whose launcher stated no host path map has no root the host would honor at all.
func (o *Options) rootCopierFn() func(link, storePath string) {
	if !o.inJail() {
		return nil
	}
	root := o.gcRooter()
	if root == nil {
		return nil
	}
	return func(link, storePath string) {
		_ = root(link, storePath, o.Stderr, "could not register a GC root for the image copier "+
			"(a nix-collect-garbage could cost the next launch a rebuild of it)")
	}
}
