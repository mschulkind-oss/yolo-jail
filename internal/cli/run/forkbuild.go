package run

// forkbuild.go is the launch's half of the fork route (docs/design/forked-programs-as-packs.md):
// which forks this launch's packs carry, what each is pinned to, and the build trigger.
//
// THE LAUNCH PINS AN UNPINNED FORK, AND NEVER MOVES A PIN (FP-D18, which supersedes FP-D7 under the
// maintainer's OQ-PF1: "yolo pack install and update still exist, but neither is required"). A fork
// the fork lock does not pin for its declared source is resolved once, here, and recorded; a
// standing pin is left where it is however far its branch has moved, because moving one at launch
// is the rebuild on a timer §9 forbids. Moving a pin is `yolo pack update`'s alone.

import (
	goruntime "runtime" // stdlib; this package's `runtime` is yolo's own (run.go)

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// forkDeliveriesFor is THE TRIGGER (OQ-FP4: eager, at the notch's readiness act): for every fork
// this launch carries, the store key its jail materializes, or why there is none — building a
// pinned fork the machine holds no build of, through the injected build act (Options.BuildForks).
//
// The trigger is the SELECTED PACK SET, which is statically knowable (the fork pins read above the
// dispatch), exactly as auto-capture's is; core cannot know what the jail will run. A hit builds
// nothing, so §9's "never rebuild on a timer or on every launch" survives.
//
// Nothing here can fail the launch: every "no" is a reason the program's launcher prints in place
// of the program (§9: a broken fork is one missing tool, not a broken jail).
//
// THE REASONS, in the order they are asked:
//
//   - NEVER IN A CAPTURE OR BUILD JAIL — the switch that suppresses the store mount (CapturesDir
//     returning "") is the one that suppresses this, as autoCaptureInstallerPrograms reads it, so a
//     build cannot recursively trigger a build. Those jails get no decision at all.
//   - BELOW APPLE CONTAINER'S READ-ONLY FLOOR, capturesArgs mounts no store, so a build would file
//     an entry no jail on this runtime could read: nothing is built, and the jail is told why.
//   - A fork with no usable pin, or none for this platform, is its reason.
func (o *Options) forkDeliveriesFor(rt string) map[string]entrypoint.ForkDelivery {
	if len(o.forkPinned) == 0 || o.CapturesDir() == "" {
		return nil
	}
	out := map[string]entrypoint.ForkDelivery{}
	platform := containerJailPlatform()
	var build []packload.ForkPin
	for _, p := range o.forkPinned {
		if p.Commit == "" {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: p.Reason}
			continue
		}
		if why := (packdecl.Install{Platforms: p.Fork.Platforms}).UnpublishedReason("linux", goruntime.GOARCH); why != "" {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "fork " + p.Fork.Pack + " builds for none of this jail's platform: " + why}
			continue
		}
		build = append(build, p)
	}
	if len(build) == 0 {
		return out
	}
	if reason := o.roBindsUnsupported(rt); reason != "" { // parity: Warned — the capture store is not mounted below Apple Container's read-only floor, so no fork is built and each fork's launcher prints this reason
		for _, p := range build {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "this runtime mounts no capture store to deliver a " +
				"source-built program from — " + reason}
		}
		return out
	}
	if o.BuildForks == nil {
		for _, p := range build {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "this launch builds no fork"}
		}
		return out
	}
	for bin, d := range o.BuildForks(build, platform) {
		out[bin] = d
	}
	for _, p := range build {
		if _, ok := out[p.Fork.Bin]; !ok {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "the fork build returned no answer for " + p.Fork.Bin}
		}
	}
	return out
}

// noteMacosUserForks is FP-D3's line: a macos-user launch that carries a fork says it delivers no
// program for it, and why. On this backend the build trigger sits below the arm's return and no
// launch can read the capture store yet (install-capture.md hand-off H4), so a silent absence would
// read as the fork being broken.
func (o *Options) noteMacosUserForks() {
	for _, p := range o.forkPinned {
		o.pr(o.Stderr).print("[yellow]Warning: " + p.Fork.Bin + " is not delivered on macos-user[/yellow] — " +
			"fork " + p.Fork.Pack + " builds it from source in a capture jail, and no macos-user launch " +
			"can read the capture store yet (install-capture.md hand-off H4); run it on a container " +
			"backend (YOLO_RUNTIME=podman).")
	}
}

// forkLockPath is the fork lock beside the user config.
func forkLockPath() string { return packsrc.ForkLockPath(paths.UserConfigPath()) }

// PinLaunchForks is THE LAUNCH'S PIN (FP-D18) for forks, at the host: each fork the fork lock does
// not pin for its declared source is pinned now through the launch's store (launchStore: the
// launch's fetch budget, and no controlling terminal for git), and every fork's pin is returned. A
// standing pin is never moved and costs no git run; a fork that could not be pinned carries why,
// and the next step, as its reason (packload.PinForks). begin brackets the git work, and is not
// called when every fork is already pinned.
//
// One implementation for every act that readies a fork's program: this launch (noteForkPins),
// `yolo host -- <bin>` and `yolo host apply --assert` (the host floor's PinFork), and `yolo capture
// <forked bin>`, so the host and a jail can never pin one fork two ways.
func PinLaunchForks(forks []packload.Fork, begin func() (func(string), func())) []packload.ForkPin {
	return packload.PinForks(forks, forkLockPath(), launchStore(), begin)
}

// forkPins is the pin of every fork packs carry: pinned now where this launch may pin (PinLaunchForks),
// and read from the fork lock where it may not — a dry run, which materializes nothing, and a launch
// inside a jail, which has no pack store and whose fork lock is not the host's (packload.LoadForkPins:
// a lock that cannot be read pins nothing, and every fork then carries the read error as its reason,
// since a broken lock is a missing tool, never a refused launch, §9).
func (o *Options) forkPins(packs []*packload.Pack) []packload.ForkPin {
	forks := packload.Forks(packs)
	if len(forks) == 0 {
		return nil
	}
	if o.DryRun || config.InJail() {
		return packload.LoadForkPins(forks, forkLockPath())
	}
	// A first pin can fetch the fork's repository (bounded at LaunchFetchTimeout), so it gets a
	// progress line; the lock-wait notice goes THROUGH the line, so it cannot tear it.
	return PinLaunchForks(forks, func() (func(string), func()) {
		line := o.progressConfig().Start(o.Stderr, "Pinning forks")
		return line.Println, func() { line.Done("") }
	})
}

// noteForkPins prints, on every launch that carries a fork, the line OQ-FP6 rules on: the REVISION
// each source-built program is pinned to — never only its ref — or why it has none and what pins
// it, after one line for each pin this launch made (FP-D18) and each warning a pin left. Disclosures,
// so none has a quiet switch (OQ-RO3). It returns the pins, for the build trigger that acts on them.
func (o *Options) noteForkPins(packs []*packload.Pack) []packload.ForkPin {
	// NOT IN A CAPTURE OR BUILD JAIL (the one switch, CapturesDir returning ""): no fork is built
	// or delivered from inside one, and the launch that started it has already said this line.
	if o.CapturesDir() == "" {
		return nil
	}
	pins := o.forkPins(packs)
	if len(pins) == 0 {
		return nil
	}
	out := o.pr(o.Stderr)
	for _, p := range pins {
		if p.Pinned {
			out.print(p.PinnedLine())
		}
		if p.Warning != "" {
			out.print("[yellow]Warning: " + p.Warning + "[/yellow]")
		}
	}
	out.print("[dim]Forks this launch:[/dim]")
	for _, p := range pins {
		if p.Commit == "" {
			out.print("[yellow]  " + p.Line() + "[/yellow]")
			continue
		}
		out.print("[dim]  " + p.Line() + "[/dim]")
	}
	return pins
}
