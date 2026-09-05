package run

// autocapture.go is the pipeline half of AUTO-CAPTURE: the decision, taken on the host
// after packs resolve and before any container starts, that this machine has never
// recorded what one of the selected packs' vendor installers leaves behind.
//
// # Why a launch does this at all
//
// docs/design/program-delivery.md OQ-PD18, ruled 2026-09-04: *"(d), DEFAULT ON."* Until
// this slice `yolo capture` was the store's only writer, no launch path called it, and it
// had never been run — so every machine's store was empty, `_try_materialize` had never
// once hit, and slices 1-4 and 6 of install-capture.md were shipped and unreachable. The
// trigger is what makes the store fill itself.
//
// # Container backends only, and that is structural rather than a guard
//
// The call site is BELOW the macos-user arm's return in Run, so this file cannot run for
// that backend. Two independent reasons, either of which alone would be enough:
//
//   - NOTHING ON macos-user CAN MATERIALIZE A CAPTURE. entrypoint.CapturesDirEnv is
//     emitted by capturesArgs (the podman/Apple-Container argv) and by nothing else, so
//     a native launcher there bakes an empty CAPTURES_DIR and `_try_materialize` returns 1
//     on its first line. An auto-capture would pay a full installer download to file an
//     entry no launcher on that backend could ever read.
//   - THE REWRITE IS NOT BUILT. A macos-user capture stages on neutral ground
//     (/Users/Shared/yolo-captures/<bin>/home), so its Manifest.Home is never the
//     /Users/_yolojail a materialize would target, and slice 6's relocation contract
//     REFUSES a destination that is not Manifest.Home until hand-off H2 lands
//     (docs/plans/install-capture.md slice 6, H2).
//
// `yolo capture <bin>` stays available on that backend and is the right way to exercise
// it — an explicit act, by a human who knows both facts.

import (
	goruntime "runtime" // stdlib; this package's `runtime` is yolo's own (run.go)

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// autoCaptureInstallerPrograms fires the trigger for one launch.
//
// It is deliberately a thin method over an injected seam: `captureHost` — the whole
// capture act — lives in internal/cli, which imports THIS package, so the pipeline can
// name the moment but not perform it. Same injection shape and same reason as
// MacosUserRun and CaptureOnTerminate.
//
// NEVER IN THE CAPTURE JAIL ITSELF, and the switch that suppresses it is the SAME ONE
// that suppresses the store mount. `yolo capture` sets Options.CapturesDir to a func
// returning "" so the throwaway jail has no store to resolve against (see the field's
// doc, and install-capture.md slice 4(f)); reading that same seam here means one
// suppression covers both halves, so a capture cannot recursively trigger a capture and
// the two cannot drift apart. Relying instead on "runCaptureJail happens not to inject
// AutoCapture" would be true today and one line from being false.
func (o *Options) autoCaptureInstallerPrograms(packs []*packload.Pack) {
	if o.AutoCapture == nil || o.CapturesDir() == "" {
		return
	}
	bins := installerBins(packs)
	if len(bins) == 0 {
		return
	}
	o.AutoCapture(bins, containerJailPlatform())
}

// installerBins is every program the SELECTED packs install with `via: "installer"`,
// deduplicated, in declaration order.
//
// THROUGH HonoredInstalls, never the manifest, for the reason resolveCaptureTarget gives:
// a fetched pack's installerUrl is refused by the origin gate precisely so a git ref
// cannot make yolo execute a shell script, and a trigger that read InstallContributions
// directly would run exactly what that gate exists to refuse — automatically, on every
// launch, which is strictly worse than the manual command the gate was written against.
// Refusals are not reported here: `yolo capture` already names them when a human asks
// about a specific bin, and a launch that printed them would blame the user for a config
// that is behaving as designed.
//
// The predicate is a non-empty InstallerURL rather than Kind == "native", because that is
// the very field HonoredInstalls gates on — "granted, and it has an installer URL" cannot
// mean anything else. (Three names for one mechanism: manifest `via:"installer"` →
// packdecl.Install.Kind == "native" → receipt `kind:"installer"`.)
func installerBins(packs []*packload.Pack) []string {
	var bins []string
	seen := map[string]bool{}
	for _, p := range packs {
		granted, _ := p.HonoredInstalls()
		for _, in := range granted {
			if in.Bin == "" || in.InstallerURL == "" || seen[in.Bin] {
				continue
			}
			seen[in.Bin] = true
			bins = append(bins, in.Bin)
		}
	}
	return bins
}

// containerJailPlatform is capture.Platform() AS THE JAIL WILL ANSWER IT: linux, on this
// machine's architecture.
//
// THE PLATFORM IS THE JAIL'S, NOT THE HOST'S, and getting it wrong is the failure that
// looks like success. A Mac on the podman backend runs a linux/arm64 jail, so its
// captures are recorded `linux/arm64` by the driver inside (capture.Manifest.Platform is
// set in-jail for exactly this reason — see capture.Platform's own comment). A host-side
// runtime.GOOS here would ask for `darwin/arm64`, miss every entry the machine holds, and
// re-capture on every launch: a store that never hits while looking full.
//
// The ARCH is DERIVED from GOARCH rather than probed, on containerbuilder.BuilderSystem's
// precedent: the jail is a Linux container on THIS machine, so its architecture is a fact
// about the local one, known without asking anything.
func containerJailPlatform() string { return "linux/" + goruntime.GOARCH }
