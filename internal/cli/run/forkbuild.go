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
	"context"
	"fmt"
	"io"
	"strings"

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
	// FreshUpdate opts patched keys into explicit check-after-registration mode.
	FreshUpdate bool
	// FreshBoundary and FreshBoundaryErr are keyed epoch/start boundaries captured before the pool starts.
	FreshBoundary    map[string]packsrc.CheckStart
	FreshBoundaryErr map[string]error
	FreshIdentity    map[string]string
	// Pins are the forks to deliver: each PLAIN fork with its pinned commit, and each PATCHED fork,
	// which has no pin by design (docs/reference/patched-forks.md §6.5) and whose delivery its advance
	// decides.
	Pins []packload.ForkPin
	// CachedGoodOwner is the selected patched-program owner keys, comma-separated, whose recorded
	// Good.Entry is delivered instead of advancing them (selectCachedGood). Empty preserves the
	// strict ordinary launch path.
	CachedGoodOwner string
	// Platform is the jail's platform (jailPlatform: containerJailPlatform, or macosUserJailPlatform
	// for the Seatbelt sandbox), never the host's.
	Platform string
	// Runtime is the backend the jail runs on, for a line that names what differs there (§9: an
	// Apple Container capture jail cannot start beside a running one).
	Runtime string
	// Workspace is the launch's workspace, whose launch.log a failed build's line names.
	Workspace string
	// Stdout and Stderr are the launch's own writers, teed into that launch.log (launchlog.go): a
	// build's lines and the slot's progress line go to Stderr, and its build jail's output to the log half
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
// A PATCHED FORK (docs/reference/patched-forks.md) has no pin by design, so it is never given the
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
	out, req := o.forkBuildPlan(rt)
	if out == nil {
		return nil
	}
	defer o.recordHandedForks(out)
	if req == nil {
		return out
	}
	if o.BuildForks == nil {
		answers, _ := o.BuildSlot(BuildSlotRequest{Forks: req, MaxBuilds: SlotBuildJails(rt)})
		return o.finishForkDeliveries(out, req, answers)
	}
	return o.finishForkDeliveries(out, req, o.BuildForks(*req))
}

// forkBuildPlan is the trigger's first half (forkDeliveriesFor): every fork's reason it has no
// build to wait for, in out, and the request for the forks the build act readies, nil for none. out
// is nil when this launch decides nothing for any fork (no fork, or a capture or build jail).
func (o *Options) forkBuildPlan(rt string) (map[string]entrypoint.ForkDelivery, *ForkBuildRequest) {
	if len(o.forkPinned) == 0 || o.CapturesDir() == "" {
		return nil, nil
	}
	out := map[string]entrypoint.ForkDelivery{}
	platform := jailPlatform(rt)
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
		if why := forkUnpublishedHere(p.Fork, rt); why != "" {
			who := "fork " + p.Fork.Pack
			if p.Fork.IsPackTree() {
				who = "pack " + p.Fork.Pack + "'s " + p.Fork.Bin
			}
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: who + " builds for none of this jail's platform: " + why}
			continue
		}
		build = append(build, p)
	}
	if len(build) == 0 {
		return out, nil
	}
	if reason := o.roBindsUnsupported(rt); reason != "" { // parity: Warned — the capture store is not mounted below Apple Container's read-only floor, so no fork is built and each fork's launcher prints this reason
		for _, p := range build {
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: "this runtime mounts no capture store to deliver a " +
				"source-built program from — " + reason}
		}
		return out, nil
	}
	if o.BuildForks == nil && o.BuildSlot == nil {
		for _, p := range build {
			why := "this launch builds no fork"
			if p.Fork.IsPackTree() {
				why = "this launch builds no program from its pack's tree"
			}
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: why}
		}
		return out, nil
	}
	req := &ForkBuildRequest{FreshUpdate: o.Update && !o.DryRun, Pins: build, CachedGoodOwner: o.cachedGoodForkOwners(), Platform: platform, Runtime: rt, Workspace: o.Workspace,
		Stdout: o.Stdout, Stderr: o.Stderr, Progress: o.progressConfig(), Interrupt: o.actInterrupt()}
	if o.packTree != "" {
		// Called from every advance of the slot's pool at once (XB-D10): the record's writes are
		// serialized in updateHandedFile, and this list under its own lock.
		req.Hand = func(bin string, h HandedFork) error {
			handedForksMu.Lock()
			o.handedForks = append(o.handedForks, bin)
			handedForksMu.Unlock()
			return recordHandedFork(o.packTree, bin, h)
		}
	}
	return out, req
}

// finishForkDeliveries is the trigger's second half: the build act's answers merged into out, and
// a reason for any fork it did not answer.
func (o *Options) finishForkDeliveries(out map[string]entrypoint.ForkDelivery, req *ForkBuildRequest,
	answers map[string]entrypoint.ForkDelivery) map[string]entrypoint.ForkDelivery {
	for bin, d := range answers {
		out[bin] = d
	}
	for _, p := range req.Pins {
		if _, ok := out[p.Fork.Bin]; !ok {
			what := "the fork build"
			if p.Fork.IsPackTree() {
				what = "the build of pack " + p.Fork.Pack + "'s tree"
			}
			out[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: what + " returned no answer for " + p.Fork.Bin}
		}
	}
	return out
}

// forkUnpublishedHere is why f builds for none of the platform a jail on rt runs (jailPlatform), ""
// when it builds for it: a fork no build is tried for, which the launch's refusal of a missing
// patched build passes over (missingbuilds.go), since nothing failed.
func forkUnpublishedHere(f packload.Fork, rt string) string {
	goos, arch, _ := strings.Cut(jailPlatform(rt), "/")
	return (packdecl.Install{Platforms: f.Platforms}).UnpublishedReason(goos, arch)
}

// jailPlatform is capture.Platform() as a jail on rt will answer it: the Seatbelt sandbox's darwin
// on macos-user (PF-D97, FP-D25: a fork gets a darwin build there), a container jail's linux on
// every other runtime.
func jailPlatform(rt string) string {
	if rt == "macos-user" { // parity: HonoredBy — the macos-user sandbox runs darwin programs, so its forks are built for darwin
		return macosUserJailPlatform()
	}
	return containerJailPlatform()
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

// noteMacosUserForks is FP-D3's line for each fork a macos-user launch hands its sandbox no build of,
// once the slot has decided (docs/reference/patched-forks.md PF-D97: a fork gets a darwin build on
// this backend): the program is not delivered, why, and the ways out here. The same words become the
// fork's reason on the wire (o.forkDelivered), so `<bin>` typed in the sandbox says what the launch
// said, never "the launch that started this jail built no <bin>". A patched fork the missing-build
// refusal already named (missingbuilds.go; it starts without it only under
// YOLO_ALLOW_MISSING_PROGRAMS) is not said twice. A fork with a key is said by the slot's own lines.
func (o *Options) noteMacosUserForks(rt string) {
	said := map[string]bool{}
	if !o.DryRun {
		for _, m := range o.missingBuilds(rt) {
			if m.owner == "" {
				said[m.key] = true
			}
		}
	}
	for _, p := range o.forkPinned {
		d, ok := o.forkDelivered[p.Fork.Bin]
		if !ok || d.Key != "" || p.Fork.IsPackTree() {
			// A PACK-TREE program is built on this backend as on every other (the darwin act), so its
			// reason is the build's own — its log, `yolo pack build <pack>` and the bypass, which the
			// slot already printed — and never this line's "run it on a container backend".
			continue
		}
		why := d.Reason
		if !o.DryRun {
			why = macosUserForkWhy(d.Reason)
		}
		d.Reason = p.Fork.Bin + " is not delivered on macos-user: " + why
		o.forkDelivered[p.Fork.Bin] = d
		if !said[p.Fork.Key()] {
			o.pr(o.Stderr).print("[yellow]Warning: " + p.Fork.Bin + " is not delivered on macos-user[/yellow] — " +
				richtext.Escape(why) + ".")
		}
	}
}

// macosUserForkWhy is why a macos-user launch delivers no program for a fork, the slot's reason,
// ending in the next steps on this backend: a container backend, which builds it for linux, or
// starting without it.
func macosUserForkWhy(reason string) string {
	return strings.TrimSuffix(reason, ".") + "; run it on a container setup with `YOLO_SETUP=apple-container-self yolo` or " +
		"`YOLO_SETUP=podman-self yolo`, or start without it with `YOLO_ALLOW_MISSING_PROGRAMS=1 yolo`"
}

// dryRunForkDeliveries is a macos-user DRY RUN's fork decisions, which build nothing: a fork the
// slot would hand its act is said to be built by a launch without --dry-run, and any other keeps
// the reason the slot would give it (no pin, or no darwin build for the fork's platforms), with the
// ways out (macosUserForkWhy). noteMacosUserForks says each.
func (o *Options) dryRunForkDeliveries() {
	if len(o.forkPinned) == 0 || o.CapturesDir() == "" {
		return
	}
	rt := "macos-user"
	o.forkDelivered = make(map[string]entrypoint.ForkDelivery, len(o.forkPinned))
	for _, p := range o.forkPinned {
		var why string
		switch {
		case p.Commit == "" && !p.Fork.Patched():
			why = macosUserForkWhy(p.Reason)
		case forkUnpublishedHere(p.Fork, rt) != "":
			why = macosUserForkWhy("fork " + p.Fork.Pack + " builds for none of this jail's platform: " +
				forkUnpublishedHere(p.Fork, rt))
		default:
			why = "a dry run builds no fork; a launch without --dry-run builds fork " + p.Fork.Key() + " for macOS"
		}
		o.forkDelivered[p.Fork.Bin] = entrypoint.ForkDelivery{Reason: why}
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
	return pinLaunchForks(context.Background(), forks, begin)
}

func mutableForkRef(fork packload.Fork) bool {
	if fork.Patched() {
		return false
	}
	addr, err := packsrc.Parse(fork.Source)
	return err == nil && addr.Kind == packsrc.KindGit && !packsrc.IsFixedRef(addr.Ref)
}

// freshJailForkPinRefusal names the mutable source dependencies a jail cannot manually re-resolve
// because its fork lock is the host's read-only input. Fixed refs and unselected forks are unaffected.
func freshJailForkPinRefusal(forks []packload.Fork) error {
	for _, fork := range forks {
		if !mutableForkRef(fork) || forkUnpublishedHere(fork, "podman") != "" { // a jail is a container's
			continue
		}
		return fmt.Errorf("refusing --update: mutable source fork %s needs a fresh pin, but this jail cannot write its host fork lock. Run `yolo --update -- <command>` from the host workspace, or run this launch without --update",
			fork.Key())
	}
	return nil
}

func pinLaunchForks(ctx context.Context, forks []packload.Fork,
	begin func() (func(string), func())) []packload.ForkPin {
	store := launchStore()
	store.Ctx = ctx
	return packload.PinForks(forks, forkLockPath(), store, begin)
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
// rt is the backend; every backend's fresh launch builds a fork, macos-user's for darwin (PF-D97), so
// a patched fork's line names a fresh launch on each.
func (o *Options) noteForkPins(packs []*packload.Pack, _ string) []packload.ForkPin {
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
			// Every backend's fresh launch builds it, macos-user's included (PF-D97).
			line, warn := patchedForkLine(p, "a fresh launch")
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
