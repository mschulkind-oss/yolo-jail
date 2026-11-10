package cli

// describe.go is `yolo describe` and `yolo apply` — the two new verbs of the
// environment-manager framing (env-manager plan Phase 3, design §3.1/§3.2).
//
//	describe   print the resolved environment description (human, --json, --hash)
//	apply      make the environment match its description (jail-first)
//
// describe is the reproducibility claim made checkable: the description is a thing you
// can hold. --json is the canonical computed config (the same bytes `config dump`
// prints and the startup diff validates); --hash is a cache key / CI pin over it. The
// --hash caveat (§3.2): a hash over an UNSEALED environment moves for reasons the user
// cannot enumerate, so it is printed MARKED until sealing (Phase 5) makes it authoritative.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func runDescribe(args []string) int {
	return describeMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

func describeMain(args []string, out, errw io.Writer, color bool) int {
	var asJSON, asHash bool
	var setupName string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, describeUsage+"\n")
			return 0
		case a == "--json":
			asJSON = true
		case a == "--hash":
			asHash = true
		case a == "--setup" || hasPrefix(a, "--setup="):
			fl, _ := readValueFlag(args, i, "--setup")
			if fl.err != nil {
				fmt.Fprintf(errw, "yolo describe: %v (a setup's name; `yolo setup list` names them)\n", fl.err)
				return 2
			}
			setupName, i = fl.value, fl.last
		default:
			fmt.Fprintf(errw, "yolo describe: unexpected argument %q\n\n%s\n", a, describeUsage)
			return 2
		}
	}
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "yolo describe: %v\n", err)
		return 1
	}
	canonical, err := config.SnapshotJSON(cfg)
	if err != nil {
		fmt.Fprintf(errw, "yolo describe: %v\n", err)
		return 1
	}
	// THE SETUP a launch from here would select (config.SelectSetup, the one selection every
	// verb shares): --setup, then YOLO_SETUP, then the user's `setup`, then the OS default. Its
	// warnings (an inherited YOLO_SETUP ignored, a removed YOLO_RUNTIME or key) go to stderr in
	// every output mode, as a launch prints them. A refused --setup is misuse, exit 2, as apply
	// treats it; a refusal the environment or config makes is reported below, and describe goes on.
	choice, serr := selectedSetupFlag(cfg, setupName)
	for _, w := range choice.Warnings {
		fmt.Fprintln(errw, w)
	}
	if serr != nil && setupName != "" {
		fmt.Fprintf(errw, "yolo describe: %v\n", serr)
		return 2
	}

	if asJSON {
		// The canonical computed config — supersedes `config dump` (same bytes).
		// Without a private inline value (secret-env-scoping.md): the hash below still covers it.
		shown, serr := config.SnapshotJSON(config.RedactPrivateEnvValues(cfg))
		if serr != nil {
			fmt.Fprintf(errw, "yolo describe: %v\n", serr)
			return 1
		}
		fmt.Fprintln(out, shown)
		return 0
	}
	if asHash {
		sum := sha256.Sum256([]byte(canonical))
		// MARKED, not bare: until `apply --sealed` (Phase 5) proves the environment was
		// assembled only from declared inputs, this hash can move for reasons the user
		// cannot enumerate — so it is not yet an authoritative "same env" pin (§3.2).
		fmt.Fprintf(out, "sha256:%s  (UNSEALED — not authoritative until `apply --sealed`; see `yolo apply --help`)\n",
			hex.EncodeToString(sum[:]))
		return 0
	}

	// Human summary. Deliberately compact — the machine-readable answer is --json.
	pr := richtext.Printer{W: out, Color: color}
	// The notch and mechanism below are the selected setup's: --setup, YOLO_SETUP and the
	// user's `setup` move describe as they move the launch. A selection the launch would refuse
	// is printed with its refusal, which names the next step, and describe goes on at the jail
	// notch.
	notch := render.KindJail
	// A SETUP THIS OS CANNOT RUN (a Seatbelt or Apple Container setup on Linux, a Landlock one on
	// a Mac) is one the launch refuses at its setup gate (setup.Buildable, here with no binary
	// probe, so describe stays instant). describe still answers for that setup, says it cannot
	// run here and names this OS's setup as the refusal does; nothing here would materialize its
	// package profile, so the packages line never promises a run.
	var unrunnable string
	if serr != nil {
		pr.Printf("[bold]setup[/bold]        [red]none[/red] — a launch from here refuses: %s", richtext.Escape(serr.Error()))
	} else {
		notch = render.KindForSetup(choice.Setup.Kind())
		if berr := setup.Buildable(choice.Setup, platformGOOS(), nil); berr != nil {
			unrunnable = fmt.Sprintf("setup %s cannot run on %s", choice.Setup.Name, platformOSName())
			pr.Printf("[bold]setup[/bold]        [cyan]%s[/cyan] [dim](from %s)[/dim] — [red]cannot run on %s[/red]: %s",
				choice.Setup.Name, richtext.Escape(choice.From()), platformOSName(), richtext.Escape(berr.Error()))
		} else {
			pr.Printf("[bold]setup[/bold]        [cyan]%s[/cyan] [dim](from %s)[/dim]", choice.Setup.Name, richtext.Escape(choice.From()))
		}
	}
	pr.Printf("[bold]environment[/bold]  confinement [cyan]%s[/cyan]", notch)
	// jailcontent.ConfinementProfile, which is ALSO what the agent's briefing header
	// reads (OQ-DP2, docs/design/declaration-parity.md §2.3). This was `confinementProfile`,
	// a private twin of the briefing's own lookup, and the two disagreed: describe honored
	// render.ProfileFor's instruction to source a printed vector from the backend that knows
	// the platform and the briefing did not, so the human and the agent were told different
	// things about one boundary. One function is the fix; it lives in jailcontent because
	// internal/render cannot host it (a render Target carries neither platform nor mechanism).
	prof := jailcontent.ConfinementProfile(notch, resolvedMechanism(cfg), paths.IsMacOS)
	printConfinementVector(pr, prof)
	// Reports what THIS machine would get, so it filters on the running platform —
	// a linux-only package listed on a Mac would be a description of someone else's
	// jail.
	packages := config.EffectivePackages(cfg, runtime.GOOS)
	if unrunnable != "" && len(packages) > 0 && !prof.Has(render.PrimBakedImage) {
		pr.Printf("[bold]packages[/bold]     %d declared [dim](inert here — %s, so no run here "+
			"materializes a nix profile)[/dim]", len(packages), unrunnable)
	} else {
		printPackageProfile(pr, notch, prof, packages,
			darwinpkg.ProfileRootLink(paths.Home()),
			packagesMaterializeAt(notch, resolvedMechanism(cfg)))
	}
	if packs, perr := config.LoadPacks(nil); perr == nil && len(packs) > 0 {
		names := make([]string, 0, len(packs))
		for _, p := range packs {
			names = append(names, p.Name)
		}
		pr.Printf("[bold]packs[/bold]        %s", joinComma(names))
	} else {
		pr.Printf("[bold]packs[/bold]        [dim](none configured)[/dim]")
	}
	sum := sha256.Sum256([]byte(canonical))
	pr.Printf("[bold]description[/bold]  sha256:%s [dim](unsealed — `describe --hash` for the pin, `describe --json` for the full config)[/dim]",
		hex.EncodeToString(sum[:])[:16])
	return 0
}

// platformOSName is the human name of the OS the read-only verbs answer for (platformGOOS).
func platformOSName() string {
	if paths.IsMacOS {
		return "macOS"
	}
	return "Linux"
}

// confinementLabelPad aligns the primitive/autonomy detail column under the value column
// of describe's own `label  value` lines, so a multi-primitive vector reads as one block
// rather than as three unrelated lines.
const confinementLabelPad = "               "

// The print order and the per-primitive prose both come from internal/render
// (render.PrimitiveOrder / render.PrimitiveDoes) rather than living here, because this is no
// longer the only surface that renders the vector for a human: the per-notch briefing header
// describes the same primitives to an AGENT (C2, internal/jailcontent/briefing.go). Two wordings
// for one primitive drift, and a reader who hits the disagreement cannot tell which is
// current — so the table sits in the package that defines Primitive, with both consumers
// reading it.

// printConfinementVector prints the resolved notch's COMPOSED PRIMITIVES plus the one
// policy bit, which is what internal/render/confinement.go's own comment says describe is
// for ("an implementation fact that `describe` can print"; plan §6c step 2). Two lines'
// worth of output answering two questions nothing else answers:
//
//   - "what does this notch actually give me?" — the primitive vector, in prose. A preset
//     that composes NOTHING (host) prints as such, so it reads as the weakest rather than
//     as just another name on the dial.
//   - "will my packs render their jail-bypass keys here?" — AgentAutonomy. The most
//     consequential thing a user can know about their notch, and invisible everywhere else:
//     it decides posture inside a pack's config surfaces, never as a line of its own.
//
// Printing the vector is NOT a step toward letting a user assemble one — only the three
// named presets are selectable (fill-the-matrix-principle.md), and this adds no config surface.
func printConfinementVector(pr richtext.Printer, prof render.Profile) {
	var composed []string
	for _, prim := range render.PrimitiveOrder() {
		if prof.Has(prim) {
			composed = append(composed, render.PrimitiveDoes(prim))
		}
	}
	if len(composed) == 0 {
		pr.Printf("  enforced by  [dim]nothing — no primitive at all; this is your real machine[/dim]")
	} else {
		pr.Printf("  enforced by  %s", composed[0])
		for _, line := range composed[1:] {
			pr.Printf("%s%s", confinementLabelPad, line)
		}
	}
	if prof.AgentAutonomy {
		pr.Printf("  autonomy     [cyan]ON[/cyan] — packs render their autonomous posture " +
			"(permission prompts off)")
	} else {
		pr.Printf("  autonomy     [yellow]OFF[/yellow] — packs render their guarded posture " +
			"(permission prompts stay on)")
	}
}

// printPackageProfile reports the RESOLVED `packages:` tool closure for a notch that has
// no baked image (N2's fourth sub-item). Two facts a user could not previously get from
// anywhere: that their declared packages resolve to a nix profile at all, and WHERE — the
// store path whose bin/ the agent's PATH is prefixed with.
//
// Sourced from the GC-ROOT SYMLINK (darwinpkg.ProfileRootLink), not from nix: describe is
// a read-only report and must stay instant, where realizing the profile is a build. The
// root is the right oracle precisely because it is what the last materialization pointed
// at — reading it answers "which closure would a launch use" without asking nix, and its
// absence is itself the honest answer ("declared, not yet resolved").
//
// Gated on PrimBakedImage being ABSENT, which is the whole point of the mechanism's rename:
// the question "where does my toolset come from" has a nix-profile answer only below the
// jail notch. A jail's packages come from the image, so printing a profile path there would
// name a closure the launch does not use.
//
// materializes says whether anything at this notch BUILDS the profile, and it is what makes
// the absent-root branch honest. Only the macos-user backend materializes one today
// (darwinpkg.Materialize has exactly one caller, that backend's run seam), which is the macOS
// guest notch as well as `runtime: "macos-user"` (env-manager plan EMP-D1). A Linux `guest`
// has no package layer yet (docs/design/provisioner-evidence.md §3.4, "not orthogonal to
// confinement: the provisioning primitive below jail"); `host` never gets one, by design
// (packagesMaterializeAt). Without it this branch
// offered "a launch or `yolo apply` materializes it" at every notch — a remedy that does not
// exist below jail, and one `yolo apply` does not perform at ANY notch. `check` corrected the
// same cell for itself (check/section_packageprofile.go's `materializes`), and both follow
// report-tiers.md P2: a loss with no remedy says so rather than borrowing one it cannot cash.

// packagesMaterializeAt is the materializes argument for notch under mechanism. At the HOST
// it is false whatever the runtime (OQ-NX8, ruled 2026-10-23, docs/design/provisioner-sets.md):
// `yolo host` never applies the workspace's jail-only keys (HP-DIR3,
// docs/reference/host-agent-environment.md#HP-DIR3), so `runtime: "macos-user"` — whose own
// launches do build a profile — must not turn the host's inert line into "a run materializes
// it". Elsewhere it is whether the mechanism provisions one, which only macos-user does.
func packagesMaterializeAt(notch render.Kind, mechanism string) bool {
	if notch == render.KindHost {
		return false
	}
	return jailcontent.MechanismHasNoContainer(mechanism)
}

// packageProfileInert is whether printPackageProfile would print the INERT line: packages are
// declared, this notch bakes none, no profile is resolved, and nothing here would materialize one.
// At the HOST it is true whenever packages are declared, a resolved root included (OQ-NX8).
func packageProfileInert(notch render.Kind, prof render.Profile, packages []any, rootLink string, materializes bool) bool {
	if prof.Has(render.PrimBakedImage) || len(packages) == 0 {
		return false
	}
	if notch == render.KindHost {
		return true
	}
	if materializes {
		return false
	}
	_, err := os.Readlink(rootLink)
	return err != nil
}

// hostPackagesInertNote is the host notch's reason, the same rule `yolo check` prints
// (check/section_packageprofile.go's hostPackagesInertLine).
const hostPackagesInertNote = "inert at the `host` notch by design: `yolo host` never applies " +
	"the workspace's jail-only keys (HP-DIR3)"

func printPackageProfile(pr richtext.Printer, notch render.Kind, prof render.Profile, packages []any, rootLink string, materializes bool) {
	if prof.Has(render.PrimBakedImage) || len(packages) == 0 {
		return
	}
	// THE HOST NEVER APPLIES `packages:` (OQ-NX8, HP-DIR3), so a GC root under this home is
	// not its answer even when one resolves: it is some other launch's closure, and naming it
	// with "add it to PATH" would offer the host a package layer the rule says it has none of.
	if notch == render.KindHost {
		pr.Printf("[bold]packages[/bold]     %d declared [dim]("+hostPackagesInertNote+")[/dim]",
			len(packages))
		return
	}
	target, err := os.Readlink(rootLink)
	if err != nil {
		if !materializes {
			// The absent root is the STEADY state here, not a pending one: no provisioner
			// at this notch would ever fill it, so there is no command to name. The entries
			// are inert, which is not the same as wrong — they are what a notch that grew a
			// package layer would use.
			pr.Printf("[bold]packages[/bold]     %d declared [dim](inert at this notch — "+
				"nothing here materializes a nix profile)[/dim]", len(packages))
			return
		}
		// Declared but never materialized (or the root was collected/removed). Not a
		// warning: a run resolves it, and describe does not provision.
		pr.Printf("[bold]packages[/bold]     %d declared [dim](no nix profile resolved yet — "+
			"a run materializes it)[/dim]", len(packages))
		return
	}
	pr.Printf("[bold]packages[/bold]     %d declared, resolved to [cyan]%s[/cyan]", len(packages), target)
	pr.Printf("%s[dim]add %s/bin to PATH to use them outside a launch; "+
		"GC-rooted at %s[/dim]", confinementLabelPad, target, rootLink)
}

// resolvedMechanism resolves the backend a launch would use: that of the setup the launch
// selects (selectedSetupBackend, the selection every verb shares), which is what makes a macOS
// seatbelt setup print the macos-user vector, separate user and Seatbelt, rather than a
// container's. Loose by design: describe is a read-only report, so an unreachable runtime is
// not its problem, and neither is a setup a launch refuses — `yolo check` is where both are
// errors, and the tolerant resolver never exits.
func resolvedMechanism(cfg *jsonx.OrderedMap) string {
	return selectedSetupBackend(cfg)
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

const describeUsage = `yolo describe — print the resolved environment description

The description is the product: what tools, agents, config, and confinement level this
environment resolves to. It is meant to be a thing you can hold and compare.

  yolo describe          human-readable summary (confinement — what enforces it and
                         whether agent autonomy is on — the resolved package
                         profile below the jail notch, packs, description hash)
  yolo describe --json   the full canonical computed config (supersedes 'config dump')
  yolo describe --hash   a sha256 pin over the canonical config, for CI / cache keys
  --setup <name>         describe for that setup instead of the one a launch from here
                         selects (` + "`yolo setup list`" + ` names them)

The hash is printed MARKED as unsealed: until 'yolo apply --sealed' (which refuses any
undeclared input), the environment can differ from its description in ways the hash
cannot see, so it is a cache key, not yet a reproducibility guarantee.

Examples:
  yolo describe                       # what does this environment resolve to?
  yolo describe --json                # the same, for a script or an agent
  yolo describe --hash                # the pin, for a CI cache key`
