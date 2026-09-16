package check

import (
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// buildImageReal runs the real nix build check() needs and returns
// (storePath, stderrTail). The out-link + streaming
// spinner are elided (check only consumes the result); the store path is the
// resolved out-link.
//
// IT PROVES THE ARTIFACTS THIS HOST'S NEXT LAUNCH WILL REALIZE, which is not
// always the default image. It used to call image.BuildOCIImage for
// `image.ImageAttrDefault` unconditionally, with the config's `packages:` in
// YOLO_EXTRA_PACKAGES — so on a host that opts into store-delivered packages
// (C4/C5), where a launch builds `.#ociImageLean` with NO extras and realizes
// `.#yoloImageExtras` beside it, the Image section proved an image that host would
// never build and said nothing about the two attrs it would. Structurally green on
// the wrong artifact, and unfailable for exactly the hosts that opted in
// (docs/plans/setup-support-gaps.md §7 F6).
func buildImageReal(repoRoot string, extraPackages []any) (string, []string) {
	return image.BuildOCIImage(preflightBuildRequest(os.Getenv, pathPresent, paths.IsMacOS,
		repoRoot, extraPackages))
}

// pathPresent is the real filesystem probe for the store-delivery facts below.
// Options.PathExists cannot serve: buildImageReal is the seam's default
// IMPLEMENTATION (fillDefaults assigns the bare function), so it has no Options.
func pathPresent(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// The two host paths a launch bind-mounts to give the jail the nix store — the
// daemon socket and the store itself. Store delivery resolves store paths from
// INSIDE the jail, so without both there is nothing for the jail to resolve and the
// launch bakes instead.
//
// ⚠ THESE ARE A SECOND READING OF FACTS THE LAUNCH OWNS. The authority is
// internal/cli/run: `StorePackagesOptInEnv` + `storePackagesEligible`
// (storepackages.go) and `shouldMountHostNix` (hostprobes.go, which spells these two
// paths). Only the env var is exported and `check` does not import the run pipeline
// (internal/cli/check/packs_test.go records that constraint where the same problem
// moved a message into internal/config), so the preflight re-reads them here. The
// real fix is ONE exported predicate both sides call, which is a change to the run
// slice; until that lands this comment is the record of the drift, and the test below
// is what makes a divergence visible rather than silent.
const (
	hostNixDaemonSocket = "/nix/var/nix/daemon-socket"
	hostNixStore        = "/nix/store"
	// storeDeliveryOptInEnv mirrors run.StorePackagesOptInEnv.
	storeDeliveryOptInEnv = "YOLO_STORE_PACKAGES"
)

// preflightBuildRequest decides WHICH artifacts a launch on this host implies, and
// so which ones the preflight has to prove. Two shapes, one per delivery mechanism —
// exactly one is live in any jail, by ruling (OQ-1, "opt-in fast path, baked path
// retained, per LAUNCH and never per package").
func preflightBuildRequest(getenv func(string) string, exists func(string) bool,
	isMacOS bool, repoRoot string, extraPackages []any) image.OCIBuildRequest {
	if !storeDeliveryLaunch(getenv, exists, isMacOS) {
		return image.OCIBuildRequest{
			RepoRoot:      repoRoot,
			Attr:          image.ImageAttrDefault,
			ExtraPackages: extraPackages,
		}
	}
	// The lean pair. NoExtraPackagesEnv rather than "no packages passed": the
	// workspace's `packages:` are realized as a store profile by the launch, and an
	// ambient YOLO_EXTRA_PACKAGES in this shell would otherwise bake them into the
	// image C5 built lean (image.OCIBuildRequest says why that matters).
	return image.OCIBuildRequest{
		RepoRoot:           repoRoot,
		Attr:               image.ImageAttrLean,
		AlsoBuild:          []string{image.ImageExtrasAttr},
		NoExtraPackagesEnv: true,
	}
}

// storeDeliveryLaunch answers "will a launch on this host deliver packages from the
// mounted nix store?" — the question that decides the attrs above.
//
// THE RUNTIME TERM IS IMPLIED, not dropped. Eligibility needs podman, and the two
// runtimes that are not podman cannot reach this code with a different answer:
// Apple Container is macOS-only (paths.SupportedRuntimes) and macos-user builds no
// Linux image at all, so sectionImageBuild returns before the build seam is called.
// Off macOS, "the store is mounted" is shouldMountHostNix's whole Linux branch.
func storeDeliveryLaunch(getenv func(string) string, exists func(string) bool, isMacOS bool) bool {
	// One spelling of "the operator said yes" across every launcher dial
	// (run.envTruthy): two dials that disagree about what counts as true turn "I set
	// the variable and nothing happened" into a legitimate bug report.
	switch strings.ToLower(strings.TrimSpace(getenv(storeDeliveryOptInEnv))) {
	case "1", "true", "yes":
	default:
		return false
	}
	if isMacOS {
		// A macOS podman runs in a VM that shares no /nix/store with the host, and
		// the jail's packages are Linux builds — so the launch bakes, whatever the
		// dial says, and the preflight must prove the baked image.
		return false
	}
	return exists(hostNixDaemonSocket) && exists(hostNixStore)
}

// itoa avoids strconv import churn across the check package.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
