package image

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ImageExtrasAttr is C5's store-delivered bulk — the `fullPackages` set plus the
// chromium graphics stack the /lib farm used to link — realized as ONE buildEnv a
// jail is handed by store path (internal/entrypoint/storepackages.go links it into
// /run/yolo/packages). A launch that builds ImageAttrLean realizes THIS beside it,
// so a preflight that proved the lean image alone would prove half a launch.
//
// It is named here rather than in nixflags.go's block only because the preflight is
// what first needed a name for it: internal/cli/run/storepackages.go still spells
// the literal ".#yoloImageExtras" in realBuildImageExtras and should read this
// constant instead — two spellings of one flake attr is exactly the drift that
// block exists to prevent.
const ImageExtrasAttr = ".#yoloImageExtras"

// extraPackagesEnv is the variable flake.nix reads with `builtins.getEnv` to bake a
// workspace's `packages:` into the image. Spelled once here because this file both
// SETS it (a baked preflight) and REMOVES it (a lean one), and those two must not be
// able to disagree about the name.
const extraPackagesEnv = "YOLO_EXTRA_PACKAGES"

// OCIBuildRequest is what one `yolo check` preflight asks nix to prove.
//
// IT EXISTS BECAUSE THE ARTIFACT SET IS NOT FIXED. BuildOCIImage took only
// (repoRoot, extraPackages) and built ImageAttrDefault unconditionally, so on a host
// whose next launch realizes ImageAttrLean + ImageExtrasAttr the Image section went
// green on an image that host would never build, and said nothing at all about the
// two attrs it would — structurally unfailable for the hosts it matters most to
// (docs/plans/setup-support-gaps.md §7 F6). The check slice decides which artifacts
// a launch here implies; this type is how it says so.
type OCIBuildRequest struct {
	// RepoRoot is the flake source the build runs in.
	RepoRoot string
	// Attr is the IMAGE attr, and the one whose store path is returned. "" =>
	// ImageAttrDefault, so a caller that does not know about the lean variant keeps
	// proving the image it always proved.
	Attr string
	// AlsoBuild are further attrs proven in the SAME nix invocation, beside the
	// copier (which BuildOCIImage adds itself — every launch realizes it). Their
	// store paths are not named back to the caller; being buildable is the whole
	// claim. Never first: the returned path is the FIRST attr's out-link.
	AlsoBuild []string
	// ExtraPackages, when non-empty, is JSON-encoded into YOLO_EXTRA_PACKAGES the
	// way the run path does.
	ExtraPackages []any
	// NoExtraPackagesEnv REMOVES YOLO_EXTRA_PACKAGES from the build environment
	// rather than passing on whatever the calling shell had. The lean image's
	// rootTree still reads that variable (flake.nix: `extraPackages` is in the join
	// for every non-minimal variant), so an ambient one would bake packages into the
	// very image C5 built lean — and the preflight would then prove an image with an
	// input its launch does not intend.
	//
	// EXPLICIT, not inferred from an empty ExtraPackages, because a BAKED launch
	// inherits the ambient variable too (stockimage.go's stockInputs calls that out
	// as a pre-existing quirk of the build path). Suppressing it there would make
	// check prove a different image than the launch builds, which is the defect
	// above with its sign flipped.
	NoExtraPackagesEnv bool
}

// BuildOCIImage runs the side-effecting core of the `yolo check` preflight: build
// req's attrs (plus the copier) and return the IMAGE's store path on success, plus
// the retained stderr tail (last 30 lines) for failure diagnosis via
// DiagnoseNixBuildFailure. storePath is "" on any failure (non-zero exit, missing
// nix).
//
// The out-link resolves to the FIRST attr — nix names the others `<outLink>-1`, and
// one per extra OUTPUT (`<outLink>-1-man`), verified 2026-09-09 — so the returned
// path is the image manifest and everything beside it is proven-buildable without
// being named back to the caller. That is exactly what check wants: it reports "the
// image can be built", and since C9 that sentence is only true if the thing that
// delivers it can be built too. ⚠ Every one of those links is a GC ROOT, so they are
// all removed on the way out (see the removal below).
//
// The rich live-status spinner and the --builders offload path stay in the run
// slice; check's callsite consumes only (storePath, stderrTail).
func BuildOCIImage(req OCIBuildRequest) (string, []string) {
	outLink, err := os.CreateTemp("", "yolo-check-*")
	if err != nil {
		return "", []string{"could not create out-link temp: " + err.Error()}
	}
	outPath := outLink.Name()
	_ = outLink.Close()
	_ = os.Remove(outPath) // nix creates the symlink itself
	// Removing the out-link (and thus its GC root) is safe HERE and only here:
	// the `yolo check` preflight builds the image to prove it *can* build, but
	// never loads or runs it — there is no running closure to protect, so an
	// unrooted result is correct. Do NOT copy this pattern into a load path: the
	// run path (autoload.go) MUST retain a durable root for the image it runs
	// against (storage-lifecycle §1; see image.RegisterImageRoot).
	//
	// The GLOB is not decoration: building a second attr makes nix write
	// `<outPath>-1` and `<outPath>-1-man` beside the first link, and each is its
	// own GC root. Removing only `outPath` would leave a preflight pinning a
	// skopeo closure in /tmp forever, per `yolo check`.
	defer func() {
		_ = os.Remove(outPath)
		if extra, err := filepath.Glob(outPath + "-*"); err == nil {
			for _, link := range extra {
				_ = os.Remove(link)
			}
		}
	}()

	argv, buildEnv := ociPreflightBuild(req, outPath, os.Environ())
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = req.RepoRoot
	cmd.Env = buildEnv
	cmd.Stdout = nil
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", []string{"could not pipe nix stderr: " + err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return "", []string{"nix command not found"}
	}

	var tail []string
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		clean := strings.TrimRight(scanner.Text(), " \t\r\n")
		if clean == "" {
			continue
		}
		tail = append(tail, clean)
		if len(tail) > 30 {
			tail = tail[1:]
		}
	}
	_ = cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		return "", tail
	}
	// Resolve the out-link the way str(out_link.resolve()) does.
	if resolved, err := os.Readlink(outPath); err == nil {
		return resolved, tail
	}
	return outPath, tail
}

// ociPreflightBuild is the request→(argv, env) half of the preflight, PURE so the
// attr selection and the YOLO_EXTRA_PACKAGES handling are testable — the build
// itself never can be.
//
// THE COPIER IS ADDED HERE AND NOT BY THE CALLER, because a launch realizes both
// (C9): the image manifest and the `skopeo` that delivers it. The copier is a source
// build no public cache serves, so a preflight that proved only the image would go
// green on a machine whose next launch cannot deliver one. Leaving it to the caller
// would make that a per-call-site promise; there is nothing a caller could sensibly
// decide about it.
func ociPreflightBuild(req OCIBuildRequest, outLink string, environ []string) (argv, env []string) {
	also := append([]string{ImageCopierAttr}, req.AlsoBuild...)
	argv = ociBuildArgv(req.Attr, outLink, also)

	env = environ
	if req.NoExtraPackagesEnv {
		env = withoutEnvVar(environ, extraPackagesEnv)
	}
	if len(req.ExtraPackages) > 0 {
		if pkgJSON, err := jsonx.DumpsCompact(req.ExtraPackages); err == nil {
			// A copy, never an append onto the caller's slice: os.Environ() is
			// fresh, but a test's fixture environment is not, and a build that
			// silently mutated it would be the kind of aliasing bug that shows up
			// two calls later.
			env = append(append([]string(nil), env...), extraPackagesEnv+"="+pkgJSON)
		}
	}
	return argv, env
}

// withoutEnvVar returns environ with every entry for name dropped. Every entry, not
// the first: a duplicated variable is legal in an environ block and the LAST one is
// what a process reads, so removing one occurrence can leave the value intact.
func withoutEnvVar(environ []string, name string) []string {
	prefix := name + "="
	out := make([]string, 0, len(environ))
	for _, e := range environ {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}
