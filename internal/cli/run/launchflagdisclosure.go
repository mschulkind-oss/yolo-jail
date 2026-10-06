package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// launchflagdisclosure.go is the disclosure for the one thing a launch does to the user's
// OWN COMMAND LINE: `packload.InjectLaunchFlags` inserts a pack's declared launch flags
// after the binary the user named, and until this file the rewrite happened in silence.
//
// It is the same trust boundary the two pack disclosures sit on, reached from the other
// side. `packhostgrants.go` states it: "the boundary today is DISCLOSURE, not consent."
// What a pack may do is bounded by user-scope config access; what makes that bound
// trustworthy is that every crossing is NAMED at the launch that makes it. An injected
// flag crosses no host boundary — it is why `launch`-shaped claims are `disclosureSkip` in
// packloopholes.go — but it changes what the agent inside the jail is permitted to do
// without asking, which is the other thing a reader checks a launch for. copilot's
// `--yolo` is `--allow-all-tools --allow-all-paths --allow-all-urls` in copilot's own help
// text; a user who typed `copilot` and got that has a right to read the sentence.

// injectLaunchFlagsDisclosed injects the packs' declared launch flags into argv and
// discloses the rewrite.
//
// A WRAPPER, not two adjacent statements, for the reason startLoopholesDisclosed is one:
// it makes the injection reachable through exactly one path that has already disclosed,
// so the invariant cannot be broken by moving a line. TestLaunchFlagInjectionHasOneDisclosedCallSite
// pins that there is no second call site.
//
// WHY THE DISCLOSURE HAPPENS HERE rather than beside notePackHostAccess at the banner, and
// the position is the better one on both counts.
//
// STRUCTURALLY: Run resolves the argv ABOVE the backend dispatch (the B-0 hoist), and all
// three spellings of a launch consume that one result — the container's fresh path, the
// attach into a running jail (whose targetCmd is built from it), and the macos-user native
// sandbox, which returns before the banner block exists. A printer at the banner would be
// absent from two of the three unless triplicated, which is the shape noteUseProfiles
// already has: three call sites and no structural reason they agree.
//
// IN TIME: this lands between `Flake source:` and the nix build, which is the same slot
// probes.go gives that line and for the same stated reason — "naming what the next few
// gigabytes are being built from while there is still time to Ctrl-C". A permission bypass
// the user did not type is exactly what belongs in that window, not thirty seconds later
// beside a banner they have stopped watching. Everything the launcher prints is teed to
// <workspace>/.yolo/launch.log either way.
//
// THE IN-JAIL PATHS ARE DISCLOSED BY THEIR OWN WRITERS, not here, and the reason is the one
// this file's wrapper rests on: the producer of a rewrite is the only thing that knows it
// happened. There are two of them, and both run THIS SAME INJECTOR over the bare argv
// `<bin>`: entrypoint.packAliases writes the result as a `.bashrc` alias and states what it
// wrote (entrypoint.discloseShellAliases), and entrypoint.DeliverLaunchFlags guarantees a
// script in ~/.yolo/bin/launch for every flagged binary — the carrier that reaches a
// NON-INTERACTIVE shell, which neither this file nor the alias can (DP-B44, closed
// 2026-09-13) — and states the reach. Both lines are silent for a year of launches until a
// pack declares a flag, and each is written where its fact becomes true.
//
// It used to be disclosed NOWHERE, on the argument that `type copilot` prints the definition
// and the alias is a legible line in a file — self-disclosure by a mechanism bash already
// owns. That is a way to CHECK the fact, not a way to be told it, and a permission bypass
// nobody was told about is the gap the maintainer closed. What stays true is the half that
// argued against putting it HERE: the host would be describing a file it has not written
// yet, on the attach path does not rewrite at all, and on macos-user writes into a shell rc
// that backend's login zsh never reads. See docs/design/declaration-parity.md §5.6.
func (o *Options) injectLaunchFlagsDisclosed(packs []*packload.Pack, argv []string) []string {
	out, inj := packload.InjectLaunchFlags(packs, jailLaunchAutonomy(), argv)
	o.noteLaunchFlagInjection(inj)
	return out
}

// noteLaunchFlagInjection prints, to stderr, the argv rewrite this launch performed.
//
// NOT SUPPRESSIBLE, and there is no flag that could make it so: a launch has no quiet mode
// by ruling (OQ-RO3), and `TestTheLaunchHasNoQuietFlag` guards the list you would add one
// to. YOLO_NO_BANNER covers the version line and nothing else.
//
// SILENT WHEN NOTHING WAS REWRITTEN, which is most launches — `yolo -- bash`, `yolo --
// claude` with no declared flags, and a user who already typed every flag yolo would add.
// A disclosure that prints "nothing" on every launch is how a disclosure surface becomes
// wallpaper (OQ-BP-3), and the nil record is what makes the silence exact rather than a
// judgement call at the print site.
//
// BOLD YELLOW, not the [dim] register notePackHostAccess uses, and for notePackHostExec's
// reason: this is not an inventory of what the environment contains, it is a change to the
// command about to run, and this is the last moment at which reading the line can change
// what the user does.
//
// The BEFORE/AFTER pair is the whole design. A single line naming the added flags asks the
// reader to reconstruct their own command line from memory to see the difference; two
// quoted argvs, one above the other, let them see it. Both are rendered through the same
// shell quoting the container arm uses for the real command, so a line is copy-pasteable
// and an argument containing a space cannot masquerade as two.
func (o *Options) noteLaunchFlagInjection(inj *packload.LaunchInjection) {
	if inj == nil {
		return
	}
	out := o.pr(o.Stderr)
	// The words are packload's (LaunchInjection.DisclosureLines), shared with `yolo host --`;
	// only the color is this notch's.
	for i, line := range inj.DisclosureLines() {
		if i == 0 {
			out.print("[bold yellow]" + line + "[/bold yellow]")
			continue
		}
		out.print("[yellow]" + line + "[/yellow]")
	}
}

// jailLaunchAutonomy is the posture bit a launch's argv rewrite reads: the jail notch's
// policy, render.ProfileFor(render.KindJail), never a literal (docs/plans/notch-convergence.md
// item 20). The run pipeline launches two notches and this is both of their answers: the jail
// notch, and on macOS the guest notch, which is the macos-user backend rendering exactly what
// it renders at the jail notch (render.Jail; env-manager plan EMP-D2), its preset containing
// the agent as the jail's does (render.GuestProfileMacOS, autonomy ON). refuseUnbuiltNotch
// turns the host notch and a Linux guest away before this is read.
func jailLaunchAutonomy() bool { return render.ProfileFor(render.KindJail).AgentAutonomy }

// injectLaunchFlagsForAttach is the injection for an attach into a jail whose packs differ from
// the configured ones (OQ-PK2 (c): the jail keeps the packs it booted with, and its own launch
// flags are the ones that apply to a command exec'd in it). It returns the command the attach
// runs, built exactly as runContainer builds targetCmd.
//
// It discloses ONLY WHEN THE ATTACH'S COMMAND DIFFERS from disclosed, the one Run built and
// already disclosed from the configured packs: a second copy of an identical disclosure is noise,
// and a command that changed is the one fact the reader needs. When the jail's packs add nothing
// where the configured ones did, that is said too, since the line above it promised a flag.
//
// The one other caller of the injector, and inside this file for the reason the wrapper is: the
// rewrite is reachable only through a path that has disclosed it
// (TestLaunchFlagInjectionHasOneDisclosedCallSite).
func (o *Options) injectLaunchFlagsForAttach(packs []*packload.Pack, argv []string, disclosed string) string {
	if len(argv) == 0 {
		return disclosed
	}
	out, inj := packload.InjectLaunchFlags(packs, jailLaunchAutonomy(), argv)
	cmd := shquoteJoin(out)
	if cmd == disclosed {
		return cmd
	}
	if inj != nil {
		o.pr(o.Stderr).print("[bold yellow]This jail keeps the packs it booted with, so for this " +
			"attach:[/bold yellow]")
		o.noteLaunchFlagInjection(inj)
	} else {
		o.pr(o.Stderr).print("[yellow]This jail keeps the packs it booted with, and they add no " +
			"launch flag: this attach runs " + cmd + " as you typed it.[/yellow]")
	}
	return cmd
}
