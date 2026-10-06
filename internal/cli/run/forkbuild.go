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
	"io"
	goruntime "runtime" // stdlib; this package's `runtime` is yolo's own (run.go)

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// ForkBuildRequest is what a launch hands its fork build trigger (Options.BuildForks).
type ForkBuildRequest struct {
	// Pins are the forks to deliver: each PLAIN fork with its pinned commit, and each PATCHED fork,
	// which has no pin by design (docs/design/patched-forks.md §6.5) and whose delivery its advance
	// decides.
	Pins []packload.ForkPin
	// Platform is the jail's platform (containerJailPlatform), never the host's.
	Platform string
	// Runtime is the backend the jail runs on, for a line that names what differs there (§9: an
	// Apple Container capture jail cannot start beside a running one).
	Runtime string
	// Workspace is the launch's workspace, whose launch.log a failed build's line names.
	Workspace string
	// Stdout and Stderr are the launch's own writers, teed into that launch.log (launchlog.go): a
	// build's lines and its progress line go to Stderr, and its build jail's output to the log half
	// alone (LaunchLogOnly); nil for the process's streams.
	Stdout, Stderr io.Writer
	// Progress is the rendering of this launch's stream (progressConfig), for each build's progress
	// line: redrawn in place on a terminal, lines anywhere else.
	Progress progress.Config
	// Interrupt is this launch's act interrupt (ActInterrupt, PF-D57), shared with its tree arm's
	// request (TreeBuildRequest.Interrupt): a Ctrl-C that ends one patched fork's wait ends every
	// later patched fork's and extension's too, and their good builds are handed with no check and no
	// build. Never nil from a launch.
	Interrupt *ActInterrupt
	// Hand records, beside this launch's pack tree, what this launch hands its jail for bin. A
	// patched fork's advance calls it under the fork's record lock, so the move that reaps the
	// fork's other builds reads it and never reaps one a launch is about to hand (PF-D20). nil
	// records nothing.
	Hand func(bin string, h HandedFork) error
}

// forkDeliveriesFor is THE TRIGGER (OQ-FP4: eager, at the notch's readiness act): for every fork
// this launch carries, the store key its jail materializes, or why there is none — building a
// pinned fork the machine holds no build of, through the injected build act (Options.BuildForks).
//
// A PATCHED FORK (docs/design/patched-forks.md) has no pin by design, so it is never given the
// no-pin reason: it goes to the build act too, whose ADVANCE checks its upstream, replays its
// series and builds the newest fit, a first advance included (§6.5). The advance's lines follow the
// launch's "Forks this launch" block, so its move line names the build this jail gets (§7). Never in
// a jail: a patched fork is checked, replayed and built on the host (§4.1).
//
// The trigger is the SELECTED PACK SET, which is statically knowable (the fork pins read above the
// dispatch), exactly as auto-capture's is; core cannot know what the jail will run. A hit builds
// nothing, so §9's "never rebuild on a timer or on every launch" survives.
//
// Nothing here fails the launch: every "no" is a reason the program's launcher prints in place of
// the program (§9: a broken fork is one missing tool, not a broken jail). The launch itself then
// refuses, before its image, when a PATCHED fork has no build (missingbuilds.go, patched-forks.md
// PF-D77).
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
	defer o.recordHandedForks(out)
	platform := containerJailPlatform()
	var build []packload.ForkPin
	for _, p := range o.forkPinned {
		if p.Fork.Patched() && config.InJail() {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "fork " + p.Fork.Key() + " is a patched fork, " +
				"whose upstream is checked and whose series is replayed and built on the host — a launch " +
				"from the host delivers it"}
			continue
		}
		if p.Commit == "" && !p.Fork.Patched() {
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
	req := ForkBuildRequest{Pins: build, Platform: platform, Runtime: rt, Workspace: o.Workspace,
		Stdout: o.Stdout, Stderr: o.Stderr, Progress: o.progressConfig(), Interrupt: o.actInterrupt()}
	if o.packTree != "" {
		req.Hand = func(bin string, h HandedFork) error {
			o.handedForks = append(o.handedForks, bin)
			return recordHandedFork(o.packTree, bin, h)
		}
	}
	for bin, d := range o.BuildForks(req) {
		out[bin] = d
	}
	for _, p := range build {
		if _, ok := out[p.Fork.Bin]; !ok {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "the fork build returned no answer for " + p.Fork.Bin}
		}
	}
	return out
}

// actInterrupt is this launch's act interrupt (ActInterrupt, PF-D57): one per launch, made by the
// first of its fork-build slot's two acts to ask, and handed to both, so a Ctrl-C in a patched fork's
// advance reaches the patched extensions' advances after it.
func (o *Options) actInterrupt() *ActInterrupt {
	if o.patchedAct == nil {
		o.patchedAct = &ActInterrupt{}
	}
	return o.patchedAct
}

// noteMacosUserForks is FP-D3's line: a macos-user launch that carries a fork says it delivers no
// program for it, and why. On this backend the build trigger sits below the arm's return and no
// launch can read the capture store yet (install-capture.md hand-off H4), so a silent absence would
// read as the fork being broken. A PATCHED fork says what it is too (docs/design/patched-forks.md
// §9): its upstream is checked and its series replayed by the same fresh-launch slot, which this
// backend never reaches either.
func (o *Options) noteMacosUserForks() {
	for _, p := range o.forkPinned {
		o.pr(o.Stderr).print("[yellow]Warning: " + p.Fork.Bin + " is not delivered on macos-user[/yellow] — " +
			richtext.Escape(macosUserForkWhy(p.Fork)) + ".")
	}
}

// macosUserForkWhy is why a macos-user launch delivers no program for fork f, ending in the next step:
// the one clause FP-D3's warning prints and the sandbox's launcher for the program repeats
// (macosUserForkWire), so `<bin>` typed in the sandbox says what the launch said.
func macosUserForkWhy(f packload.Fork) string {
	what := "fork " + f.Pack + " builds it from source in a capture jail"
	if f.Patched() {
		what = "fork " + f.Key() + " is a patched fork, whose upstream a fresh launch on a container backend " +
			"checks and whose patch series it replays and builds in a capture jail — which no macos-user launch " +
			"runs"
	}
	return what + ", and no macos-user launch can read the capture store yet (install-capture.md hand-off " +
		"H4); run it on a container backend: YOLO_RUNTIME=container (Apple Container) or YOLO_RUNTIME=podman"
}

// macosUserForkWire is the fork decisions a macos-user launch hands its sandbox (entrypoint.ForkBuildsEnv):
// each fork's program, with no build and the reason a macos-user launch delivers none
// (macosUserForkWhy), so the sandbox's launcher for the program names this backend and the step that
// runs it, never "the launch that started this jail built no <bin>", which names a container launch's
// fault. "" when the launch carries no fork.
func (o *Options) macosUserForkWire() string {
	if len(o.forkPinned) == 0 {
		return ""
	}
	d := make(map[string]entrypoint.ForkDelivery, len(o.forkPinned))
	for _, p := range o.forkPinned {
		d[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: p.Fork.Bin + " is not delivered on macos-user: " +
			macosUserForkWhy(p.Fork)}
	}
	return entrypoint.ForkBuildsWire(d)
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
// inside a jail, which has no pack store and whose fork lock is not the host's, so an unpinned fork's
// reason there names the host (packload.InJailForkPins). A lock that cannot be read pins nothing, and
// every fork then carries the read error as its reason, in one spelling for every reader, since a
// broken lock is a missing tool, never a refused launch (§9).
func (o *Options) forkPins(packs []*packload.Pack) []packload.ForkPin {
	forks := packload.Forks(packs)
	if len(forks) == 0 {
		return nil
	}
	if config.InJail() {
		return packload.InJailForkPins(packload.LoadForkPins(forks, forkLockPath()))
	}
	if o.DryRun {
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
// rt is the backend: a macos-user launch builds no fork (FP-D3), so a patched fork's line there names
// a container backend's launch as what builds an edited series.
func (o *Options) noteForkPins(packs []*packload.Pack, rt string) []packload.ForkPin {
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
		if p.Fork.Patched() {
			// No pin by design (PF-D16): its line is its series and its good build (patchedforkline.go).
			rebuilds := "a fresh launch"
			if rt == "macos-user" { // parity: Warned — macos-user builds no fork (FP-D3), so the line names a container backend's launch, and noteMacosUserForks says why
				rebuilds = "a fresh launch on a container backend"
			}
			line, warn := patchedForkLine(p, rebuilds)
			line = richtext.Escape(line)
			if warn {
				out.print("[yellow]  " + line + "[/yellow]")
			} else {
				out.print("[dim]  " + line + "[/dim]")
			}
			continue
		}
		if p.Commit == "" {
			out.print("[yellow]  " + p.Line() + "[/yellow]")
			continue
		}
		out.print("[dim]  " + p.Line() + "[/dim]")
	}
	return pins
}
