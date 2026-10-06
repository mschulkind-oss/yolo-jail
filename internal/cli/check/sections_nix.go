package check

import (
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// nixDryRunWillBuild runs `nix build .#ociImage
// --dry-run` in repoRoot and classifies its stderr via
// nixdiag.ParseDryRunWillBuild. extraPackages is JSON-encoded into
// YOLO_EXTRA_PACKAGES for the child.
func (o *Options) nixDryRunWillBuild(repoRoot string, extraPackages []any) (nixdiag.WillBuild, []string) {
	argv := reporoot.FlakeArgv(repoRoot, nixDryRunArgv())
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

// nixDryRunArgv returns the argv for the dry-run cache probe. It carries
// image.NixFlakeFlags() — the SAME flags the real build runs with — because the
// probe's whole job is to predict what that build will do: without
// --accept-flake-config the dry run ignores the flake's own substituter and
// reports "will build from source" for a closure the real build (which does
// pass it) would have substituted, and on macOS that mispredicts a working
// build as a doomed one needing a Linux builder.
//
// TWO ATTRS, because a launch realizes two things. Since C9 the image is
// DELIVERED by a `skopeo copy`, and the copier is a source build over nixpkgs'
// skopeo that no public cache serves (image.ImageCopierAttr) — MEASURED 2m27s
// cold, 2026-09-09. Probing only the image reports "nothing will build" while
// the very next launch compiles skopeo for minutes, which is the preflight
// telling the user the opposite of what happens. One `nix build` naming both
// attrs is one subprocess and one verdict, so the classification below needs no
// per-attr merge.
func nixDryRunArgv() []string {
	argv := []string{"nix"}
	argv = append(argv, image.NixFlakeFlags()...)
	return append(argv, "build", ".#ociImage", image.ImageCopierAttr,
		"--impure", "--dry-run")
}

// nixCmdArgv returns `nix <sub…>` for a nix subcommand that does NOT evaluate
// the flake — `nix store info`, `nix config show` — with the experimental
// features those subcommands live behind turned on for this one invocation.
//
// Every `nix <subcommand>` is gated behind the `nix-command` feature, and the
// official installer (unlike Determinate's) leaves it OFF. Without the flag such
// a host answers with "experimental Nix feature 'nix-command' is disabled" and
// rc 1, which the daemon check read as "Nix daemon: connection failed" — a false
// [FAIL] that stopped `yolo check` on a working Mac. `flakes` rides along so the
// spelling matches every other nix invocation in the repo (image.NixFlakeFlags)
// and a nix.conf naming flake settings does not add warnings to the output.
//
// It deliberately omits image.NixFlakeFlags' --accept-flake-config: these
// commands are handed no flake ref, so there is no flake config to accept.
// `nix --version` needs neither and does not come through here.
func nixCmdArgv(sub ...string) []string {
	return append([]string{"nix", "--extra-experimental-features", "nix-command flakes"}, sub...)
}

// hasLinuxBuilder reports whether a usable builder for THIS host's Linux system is
// reachable per `nix config show` + @/etc/nix/machines. The system comes from
// containerbuilder.BuilderSystem() — the same source the builder advertises with — so
// the probe and the thing it probes can't disagree about which arch is wanted.
func (o *Options) hasLinuxBuilder() bool {
	res := o.Exec(nixCmdArgv("config", "show"), "", nil, 10*time.Second)
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
