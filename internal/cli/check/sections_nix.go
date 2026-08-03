package check

import (
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
)

// nixDryRunWillBuild runs `nix build .#ociImage
// --dry-run` in repoRoot and classifies its stderr via
// nixdiag.ParseDryRunWillBuild. extraPackages is JSON-encoded into
// YOLO_EXTRA_PACKAGES for the child.
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

// hasLinuxBuilder reports whether a usable builder for THIS host's Linux system is
// reachable per `nix config show` + @/etc/nix/machines. The system comes from
// containerbuilder.BuilderSystem() — the same source the builder advertises with — so
// the probe and the thing it probes can't disagree about which arch is wanted.
func (o *Options) hasLinuxBuilder() bool {
	res := o.Exec([]string{"nix", "config", "show"}, "", nil, 10*time.Second)
	cfg := ""
	if res.Ran && !res.Timeout && res.RC == 0 {
		cfg = res.Stdout
	}
	return nixdiag.HasLinuxBuilderFromConfig(cfg, containerbuilder.BuilderSystem(), func(path string) ([]string, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		return strings.Split(string(data), "\n"), true
	})
}

// preflightBuilderNeeds returns a tri-state:
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
	// macOS: a from-source (Linux) build can't run locally. If the user already
	// has their OWN Linux builder configured (nix-darwin `linux-builder` or a
	// machine in /etc/nix/machines — the §8 escape hatch), Nix will use it, so
	// check's own build is viable.
	if o.hasLinuxBuilder() {
		r.ok("A package will be built from source" + named + "; your configured Linux builder will handle it")
		return true
	}
	// No user builder: a real `yolo` run offloads the from-source build to an
	// on-demand container builder on the active runtime (podman/Apple Container).
	// check's own `nix build` has no offload seam (see image.BuildOCIImage), so it
	// can't reproduce that here — report the container-builder reality (runtime
	// must be up) and skip the doomed local build. WARN, not FAIL: `yolo` itself
	// will build fine when the runtime is up.
	r.warn("A package must be built from source"+named+" — `yolo` will offload it to a container builder",
		"`yolo check` can't run that Linux build locally, but a normal `yolo` run "+
			"handles it automatically: it offloads the build to an on-demand "+
			"container builder on your active runtime (podman/Apple Container), "+
			"then tears it down.  Just make sure the runtime is up "+
			"(`podman machine start` or `container system start`) and run `yolo`.")
	return false
}
