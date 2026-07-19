package runcmd

import (
	"github.com/mschulkind-oss/yolo-jail/internal/checkdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// autoLoadImage ports run()'s auto_load_image call path: build/load the nix jail
// image, returning false when no runnable image could be made available (the
// caller must exit(1) instead of a doomed launch). The failure diagnosis uses
// the same checkdiag classifier + Linux-builder remedy the check slice uses, so
// the actionable "needs a Linux builder / cached image" text matches.
func (o *Options) autoLoadImage(cfg *jsonx.OrderedMap, rt, repoRoot string) bool {
	extra := config.EffectivePackages(cfg)
	remedy := linuxBuilderRemedy()
	return image.AutoLoadImage(image.AutoLoadOptions{
		Runtime:       rt,
		RepoRoot:      repoRoot,
		ExtraPackages: extra,
		Out:           o.Stdout,
		ProgressTTY:   o.IsTTYStdout(),
		IsMacOS:       o.IsMacOS,
		Getpid:        o.Getpid,
		DiagnoseFailure: func(tail []string) (string, string) {
			return checkdiag.DiagnoseNixBuildFailure(tail, o.IsMacOS, remedy)
		},
	})
}

// linuxBuilderRemedy resolves _linux_builder_remedy: the remedy template with
// the detected nix-daemon launchd label substituted (macOS), else a default.
func linuxBuilderRemedy() string {
	label := "org.nixos.nix-daemon"
	if l, ok := storage.DetectNixDaemonLabel(); ok {
		label = l
	}
	return checkdiag.LinuxBuilderRemedy(label)
}
