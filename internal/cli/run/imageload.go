package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// autoLoadImage builds/loads the nix jail
// image, returning false when no runnable image could be made available (the
// caller must exit(1) instead of a doomed launch). The failure diagnosis uses
// the same nixdiag classifier + Linux-builder remedy the check slice uses, so
// the actionable "needs a Linux builder / cached image" text matches.
func (o *Options) autoLoadImage(cfg *jsonx.OrderedMap, rt, repoRoot string) bool {
	extra := config.EffectivePackages(cfg)
	remedy := linuxBuilderRemedy()
	return image.AutoLoadImage(image.AutoLoadOptions{
		Runtime:  rt,
		RepoRoot: repoRoot,
		// D2: a degraded launch (repoRoot=="") has no flake to build from — skip
		// the build and run whatever image is loaded/cached.
		SkipBuild:     repoRoot == "",
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

// linuxBuilderRemedy resolves the remedy template with
// the detected nix-daemon launchd label substituted (macOS), else a default.
func linuxBuilderRemedy() string {
	label := "org.nixos.nix-daemon"
	if l, ok := storage.DetectNixDaemonLabel(); ok {
		label = l
	}
	return nixdiag.LinuxBuilderRemedy(label)
}
