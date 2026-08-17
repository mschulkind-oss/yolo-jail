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

// ociBuildArgv returns the argv for building the image: `nix … build .#ociImage
// --impure --out-link <outLink> --print-build-logs`, plus extraArgs (the
// macOS container-builder offload appends `--builders …` here).
//
// The run path (buildImageStorePathArgs) and the `yolo check` preflight
// (BuildOCIImage) share this builder so the two cannot drift on flags — a
// preflight that says "it builds" while consulting a different substituter set
// than the run does is worse than no preflight.
func ociBuildArgv(outLink string, extraArgs []string) []string {
	argv := []string{"nix"}
	argv = append(argv, NixFlakeFlags()...)
	argv = append(argv,
		"build", ".#ociImage", "--impure",
		"--out-link", outLink,
		"--print-build-logs",
	)
	return append(argv, extraArgs...)
}
