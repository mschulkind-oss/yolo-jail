package run

// autocapture.go is the pipeline half of AUTO-CAPTURE: the decision, taken on the host by a
// FRESH launch — below every attach decision, before its container starts — that this machine
// has never recorded what one of the selected packs' vendor installers leaves behind.
//
// # A fresh launch only
//
// OQ-PD18 rules it "on first launch", and an attach is never one: the jail it enters was started
// by a fresh launch that met the miss first. The trigger used to sit above the backend dispatch,
// so every terminal that joined a running jail repeated it: 14 s per attach on Apple Container
// (MEASURED, docs/research/macos-backend-performance.md §8), where a capture jail cannot start
// beside the running one (INFERRED from §7, which MEASURED that a second unsealed jail cannot).
// OQ-PD25 moved it into runContainer's fresh-launch path, beside the fork builds.
//
// # Why a launch does this at all
//
// docs/design/program-delivery.md OQ-PD18, ruled 2026-09-04: *"(d), DEFAULT ON."* Until
// this slice `yolo capture` was the store's only writer, no launch path called it, and it
// had never been run — so every machine's store was empty, `_try_materialize` had never
// once hit, and slices 1-4 and 6 of install-capture.md were shipped and unreachable. The
// trigger is what makes the store fill itself.
//
// # Every backend, each asking for its own platform
//
// The container arm calls it in runContainer's fresh-launch path, and the macos-user arm before
// its backend dispatch (run.go), each with the platform its jail will answer capture.Platform()
// with: linux on this machine's architecture for a container (containerJailPlatform), darwin for
// the Seatbelt sandbox (macosUserJailPlatform). macos-user has no attach, so every launch there
// is a fresh one.
//
// macos-user USED TO BE EXCLUDED, structurally, by placement below its return, because nothing
// there could materialize a capture: entrypoint.CapturesDirEnv was emitted by capturesArgs (the
// podman/Apple-Container argv) and by nothing else, so a native launcher baked an empty
// CAPTURES_DIR and `_try_materialize` returned 1 on its first line, and an auto-capture would
// have paid a full installer download to file an entry no launcher there could read. Hand-off
// H4 (docs/plans/install-capture.md) answered that with a root-owned copy of each selected
// program's entry under the backend's state dir, which the bootstrap names to its launchers
// (macosuser.StagedCapturesRoot); its relocation half, H2, landed 2026-09-26. The capture there
// is the macos-user capture act (internal/cli picks it for a darwin platform), so its entry is
// a darwin one.

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
//
// platform is the JAIL's (containerJailPlatform or macosUserJailPlatform), named by the arm that
// calls it, because only the arm knows which kind of jail is about to run.
func (o *Options) autoCaptureInstallerPrograms(packs []*packload.Pack, platform string) {
	if o.AutoCapture == nil || o.CapturesDir() == "" {
		return
	}
	bins := installerBins(packs)
	if len(bins) == 0 {
		return
	}
	o.AutoCapture(bins, platform)
}

// installerBins is every program the SELECTED packs install with `via: "installer"`,
// deduplicated, in declaration order.
//
// THROUGH HonoredInstalls, never the manifest, so this trigger and `yolo capture`
// (resolveCaptureTarget) read one accessor. It refuses nothing now: a fetched pack's
// installerUrl used to be refused by an origin gate, and OQ-TP9
// (docs/design/trust-paths.md, 2026-09-04) deleted that gate, because `npm install -g`
// from the same fetched tree runs `postinstall` ungated. Its `refused` return is always
// nil, so there is nothing to report here; the launch banner is what discloses a
// `via: installer` program.
//
// The predicate is a non-empty InstallerURL rather than Kind == "native", because that is
// the field naming what would run — "granted, and it has an installer URL" cannot
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

// macosUserJailPlatform is capture.Platform() as the macos-user sandbox will answer it: darwin,
// on the architecture of this yolo, which is the binary the launch stages for the sandbox to
// self-exec (macosuser.StagedYoloPath), so the two cannot answer differently. The host's own
// platform, and on this backend the right one: the sandbox is a process on this Mac, not a
// Linux guest.
func macosUserJailPlatform() string { return "darwin/" + goruntime.GOARCH }
