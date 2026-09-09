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
//     (docs/reference/image-staging-vs-baking.md, "The binary cache"). Trusting the
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

// flakeBuildArgv returns the argv for building ONE attr of this repo's flake:
// `nix … build <attr> --impure --out-link <outLink> --print-build-logs`, plus
// extraArgs (the macOS container-builder offload appends `--builders …` here).
//
// Every nix BUILD this repo runs goes through here, so none of them can drift on
// flags — a `yolo check` preflight that says "it builds" while consulting a
// different substituter set than the run does is worse than no preflight, and a
// prefix build that resolved the flake differently from the image build beside it
// would be a launch whose two halves came from two evaluations.
//
// --impure on every attr, including ones that read no YOLO_EXTRA_PACKAGES
// (installPrefix does not): the flake as a whole reads the environment, and
// keeping one spelling is worth more than shaving an eval that is identical
// either way (verified: installPrefix evaluates to the same path pure or impure).
//
// attr is the FULL flake ref (".#ociImage"), never a bare name, so the constants
// below read exactly as they appear on the command line.
func flakeBuildArgv(attr, outLink string, extraArgs []string) []string {
	argv := []string{"nix"}
	argv = append(argv, NixFlakeFlags()...)
	argv = append(argv,
		"build", attr, "--impure",
		"--out-link", outLink,
		"--print-build-logs",
	)
	return append(argv, extraArgs...)
}

// ociBuildArgv is flakeBuildArgv for an IMAGE attr — the run path
// (buildImageStorePathArgs) and the `yolo check` preflight (BuildOCIImage) both
// name it rather than spelling ".#ociImage" twice. An empty attr is
// ImageAttrDefault, so a caller that does not know about the lean variant keeps
// building the image it always built.
func ociBuildArgv(attr, outLink string, extraArgs []string) []string {
	if attr == "" {
		attr = ImageAttrDefault
	}
	return flakeBuildArgv(attr, outLink, extraArgs)
}

// The flake attrs the CLI builds. Named so a rename in flake.nix breaks
// compilation at one place rather than at a runtime "attribute missing".
//
// ImageAttrLean is C5's (docs/reference/image-staging-vs-baking.md, "Store-delivered
// packages"): the same
// image with `fullPackages` and the chromium half of the /lib farm left OUT, for
// a launch that delivers them from the mounted nix store instead. It is a SECOND
// ATTR rather than a third `builtins.getEnv` switch because the whole point of
// C4/C5 is to take variability OUT of the image derivation — a lean image that
// varied with the environment would multiply exactly the way §1.5 measured.
//
// It is deliberately NOT `ociImageMinimal`, which the design's C5 paragraph
// names. That variant also drops `withNestedPodman`, and the /etc/containers
// config files it lays down are what make podman-in-podman work — the loop
// AGENTS.md makes mandatory for verifying any Go change. C5 wants the package
// set smaller, not the container plumbing gone.
const (
	// ImageAttrDefault is the jail image — since C9 a nix2container image.json
	// naming its layer digests, not a script that streams a docker-archive
	// (docs/design/layer-aware-image-delivery.md).
	ImageAttrDefault = ".#ociImage"
	// ImageAttrLean is ImageAttrDefault without the store-deliverable bulk.
	ImageAttrLean = ".#ociImageLean"
	// ImageCopierAttr is the skopeo carrying nix2container's `nix:` SOURCE
	// TRANSPORT — the only program that can read an `image.json` and negotiate
	// per blob with `containers-storage`. Stock skopeo does not have it, so the
	// launch runs THIS store path and never a `PATH` lookup (§3.2's fourth
	// property; layercopy.go's BuildImageCopier is the one caller).
	//
	// EXPORTED, unlike installPrefixAttr, because `yolo check`'s dry-run probe
	// names it too: without it the preflight reports "nothing will build" while
	// the next launch compiles skopeo from source for minutes.
	ImageCopierAttr = ".#imageCopier"
	// installPrefixAttr is the /opt/yolo-jail install prefix the launch
	// BIND-MOUNTS into the jail — bin/<binary> plus the share/yolo-jail flake
	// bundle. It is no longer part of the image (that is what keeps `goSrc` out
	// of the image derivation), so the run path realizes it separately whenever
	// the resolved flake source ships no prebuilt binaries of its own.
	installPrefixAttr = ".#installPrefix"
)
