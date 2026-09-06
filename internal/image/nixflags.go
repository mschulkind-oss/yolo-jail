package image

// NixFlakeFlags returns the flags every nix invocation that EVALUATES this
// repo's flake must carry — the image build (run path), the `yolo check`
// preflight build, and check's `--dry-run` cache probe:
//
//   - --extra-experimental-features "nix-command flakes" so the CLI works
//     regardless of what the host's nix.conf enables;
//   - --accept-flake-config so nix honors THIS flake's own declared binary
//     cache (flake.nix nixConfig: extra-substituters = yolo-jail.cachix.org,
//     plus its trusted public key). Without it nix prints "ignoring untrusted
//     flake configuration setting 'extra-substituters'" on every run and never
//     consults the cache, so a closure that already exists there is rebuilt
//     from source instead. That is worst on macOS, where a from-source Linux
//     build cannot run locally at all: "build failed" then means only that the
//     cache was never asked, and the failure is debugged at the wrong layer
//     (docs/design/image-staging-vs-baking.md §6 item 3). Trusting the
//     project's own flake config from the project's own build step is the
//     happy path; it mutates no system nix.conf, and a trusted user still
//     gates whether the substituter is actually used.
//
// ONLY flake-evaluating invocations want this. `nix store gc`, `nix path-info`
// and `nix copy` are handed a store path rather than a flake ref, so there is
// no flake config to accept and the flag would be noise on a command that
// cannot benefit from it.
//
// internal/darwinpkg keeps its own copy (nixFlags) for the macos-user native
// package path. The duplication is deliberate: that path realizes a different
// flake attr for a notch with no image at all, and the two packages are
// independent leaves — coupling them so the constant lives in one place would
// buy nothing and make internal/image a dependency of the no-image backend.
func NixFlakeFlags() []string {
	return []string{
		"--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
	}
}

// The two image attributes a run path can build.
//
// ImageAttrLean is C5's (docs/design/image-staging-vs-baking.md §4 C5): the same image
// with `fullPackages` and the chromium half of the /lib farm left OUT, for a launch that
// delivers them from the mounted nix store instead. It is a SECOND ATTR rather than a
// third `builtins.getEnv` switch because the whole point of C4/C5 is to take variability
// OUT of the image derivation — a lean image that varied with the environment would
// multiply exactly the way §1.5 measured.
//
// It is deliberately NOT `ociImageMinimal`, which the design's C5 paragraph names. That
// variant also drops `withNestedPodman`, and the /etc/containers config files it lays
// down are what make podman-in-podman work — the loop AGENTS.md makes mandatory for
// verifying any Go change. C5 wants the package set smaller, not the container plumbing
// gone, so the lean variant keeps it.
const (
	ImageAttrDefault = ".#ociImage"
	ImageAttrLean    = ".#ociImageLean"
)

// ociBuildArgv returns the argv for building the image: `nix … build <attr>
// --impure --out-link <outLink> --print-build-logs`, plus extraArgs (the
// macOS container-builder offload appends `--builders …` here). An empty attr
// is ImageAttrDefault.
//
// The run path (buildImageStorePathArgs) and the `yolo check` preflight
// (BuildOCIImage) share this builder so the two cannot drift on flags — a
// preflight that says "it builds" while consulting a different substituter set
// than the run does is worse than no preflight.
func ociBuildArgv(attr, outLink string, extraArgs []string) []string {
	if attr == "" {
		attr = ImageAttrDefault
	}
	argv := []string{"nix"}
	argv = append(argv, NixFlakeFlags()...)
	argv = append(argv,
		"build", attr, "--impure",
		"--out-link", outLink,
		"--print-build-logs",
	)
	return append(argv, extraArgs...)
}
