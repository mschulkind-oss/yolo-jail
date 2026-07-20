package checkcmd

import (
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// nixDryRunWillBuild ports _nix_dry_run_will_build: run `nix build .#ociImage
// --dry-run` in repoRoot and classify its stderr via
// nixdiag.ParseDryRunWillBuild. extraPackages is JSON-encoded into
// YOLO_EXTRA_PACKAGES for the child (the Python env["YOLO_EXTRA_PACKAGES"]).
func (o *Options) nixDryRunWillBuild(repoRoot string, extraPackages []any) (nixdiag.WillBuild, []string) {
	argv := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"build", ".#ociImage", "--impure", "--dry-run",
	}
	var env []string
	if len(extraPackages) > 0 {
		if pkgJSON, err := jsonx.DumpsCompact(extraPackages); err == nil {
			env = []string{"YOLO_EXTRA_PACKAGES=" + pkgJSON}
		}
	}
	res := o.Exec(argv, repoRoot, env, 120*time.Second)
	if !res.Ran || res.Timeout {
		return nixdiag.WillBuildUnknown, nil
	}
	return nixdiag.ParseDryRunWillBuild(res.RC, res.Stderr, true)
}

// hasLinuxBuilder ports _has_linux_builder: is a usable aarch64-linux builder
// reachable per `nix config show` + @/etc/nix/machines?
func (o *Options) hasLinuxBuilder() bool {
	res := o.Exec([]string{"nix", "config", "show"}, "", nil, 10*time.Second)
	cfg := ""
	if res.Ran && !res.Timeout && res.RC == 0 {
		cfg = res.Stdout
	}
	return nixdiag.HasLinuxBuilderFromConfig(cfg, func(path string) ([]string, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		return strings.Split(string(data), "\n"), true
	})
}

// linuxBuilderRemedy resolves _linux_builder_remedy: the remedy template with
// the daemon label filled in.
func linuxBuilderRemedy() string {
	label, ok := storage.DetectNixDaemonLabel()
	if !ok {
		label = ""
	}
	return nixdiag.LinuxBuilderRemedy(label)
}

// preflightBuilderNeeds ports _preflight_builder_needs. Returns a tri-state:
// true  → the build is viable (fully cached, builder present, or inconclusive);
// false → known-doomed (skip the real build, one clear message already emitted).
func (o *Options) preflightBuilderNeeds(r *reporter, repoRoot string, extraPackages []any) bool {
	willBuild, offending := o.nixDryRunWillBuild(repoRoot, extraPackages)
	switch willBuild {
	case nixdiag.WillBuildUnknown:
		r.dim("Could not check binary-cache coverage (nix dry-run " +
			"unavailable/offline); attempting the build anyway.")
		return true
	case nixdiag.WillBuildNo:
		r.dim("No Linux builder needed: every image path is served from " +
			"the binary cache (nothing is built from source).")
		return true
	}
	// WillBuildYes.
	named := ""
	if len(offending) > 0 {
		top := offending
		if len(top) > 3 {
			top = top[:3]
		}
		named = " (" + strings.Join(top, ", ") + ")"
	}
	if !o.IsMacOS {
		r.dim("A package will be built from source" + named + " " +
			"(native Linux build; not served from the binary cache).")
		return true
	}
	// macOS: reachability gate.
	if o.BuilderSetupDone() {
		started, err := o.EnsureBuilder(func(m string) { r.dim(m) })
		if started {
			r.ok("A package will be built from source" + named + "; the on-demand " +
				"Linux builder is up to handle it")
			return true
		}
		if err == "needs first-boot" {
			r.fail("Linux builder needs a one-time first boot — a package must be built from source"+named,
				"Run this ONCE (it asks for sudo to install the builder's ssh "+
					"key, then boots the VM):\n"+
					"    nix run nixpkgs#darwin.linux-builder\n"+
					"Wait for the `builder@…` login prompt, then Ctrl-C and re-run "+
					"`yolo` — from then on yolo starts/stops the builder for you.")
			return false
		}
		r.fail("Image needs a Linux builder — a package must be built from source"+named,
			"The on-demand builder is set up but wouldn't start ("+err+").  "+
				"Try `yolo builder start`, or see `yolo builder status`.")
		return false
	}
	if o.hasLinuxBuilder() {
		r.ok("A package will be built from source" + named + "; a Linux builder will handle it")
		return true
	}
	r.fail("Image needs a Linux builder — a package must be built from source"+named,
		linuxBuilderRemedy())
	return false
}
