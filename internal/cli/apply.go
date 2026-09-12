package cli

// apply.go is `yolo apply` — "make this environment match its description," split from
// "run something in it" (env-manager plan Phase 3, design §3.1). Today `yolo` means
// launch, and provisioning is a side effect of launching; `apply` names the make-it-so
// half so there is an answer to "set up my environment but don't run anything," and an
// answer at all at the host notch, where there is nothing to enter.
//
// Scope of THIS phase: the verb, its flags, and the notch routing. The heavy lifting
// differs by notch:
//   - jail: provision (build image, stage packs, render config) then exit. That is the
//     existing run pipeline minus the exec, and wiring a no-exec mode through it is
//     deferred (noted below) rather than stubbed — so at jail `apply` currently reports
//     what a launch WOULD provision and directs to `yolo` / `yolo apply --at host`.
//   - host: render the applicable config into the real home. That is Phase 4
//     (`yolo host apply`), gated on the host-render work; here it is recognized and routed
//     with an honest "not yet" rather than silently doing nothing.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func runApply(args []string) int {
	return applyMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout), os.Stdin)
}

// applyMain parses the flags and routes to a notch. stdin is the reader the host notch's
// destructive-change confirmation reads; a nil stdin (a test, or any non-interactive run)
// means "not confirmed", matching packMain's fail-closed contract — a scripted
// `yolo host apply --assert` must not destroy a user's MCP server because nobody was there to
// answer.
func applyMain(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
	// Read off the same argv before anything else, and refused here if the value is not one
	// yolo emits (outputformat.go). What the format is FOR is the host notch's dry run
	// ([OQ-RO4]); the refusal for every other route is below, where the notch is known.
	format, ok := parseOutputFormat("apply", args, errw)
	if !ok {
		return 2
	}
	var at string
	var dryRun, sealed, assert bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		// Already consumed by the format parse above; this parser refuses an unrecognized
		// argument, so a `--format json` it did not skip would be rejected as one.
		if n := outputFormatTokens(args, i); n > 0 {
			i += n - 1
			continue
		}
		switch {
		case isHelpToken(a):
			io.WriteString(out, applyUsage+"\n")
			return 0
		case a == "--at":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo apply: --at needs a value (jail|guest|host)\n")
				return 2
			}
			i++
			at = args[i]
		case hasPrefix(a, "--at="):
			at = a[len("--at="):]
		case a == "--dry-run":
			dryRun = true
		case a == "--assert":
			assert = true // write (the assert posture); default is observe/dry-run
		case a == "--sealed":
			sealed = true
		default:
			fmt.Fprintf(errw, "yolo apply: unexpected argument %q\n\n%s\n", a, applyUsage)
			return 2
		}
	}

	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "yolo apply: %v\n", err)
		return 1
	}
	// --at overrides the configured notch for this invocation (the §4.1 escape valve);
	// otherwise use the configured confinement.
	notch := config.ResolveConfinement(cfg)
	if at != "" {
		n := config.Confinement(at)
		ok := false
		for _, k := range config.KnownConfinements {
			if n == k {
				ok = true
			}
		}
		if !ok {
			fmt.Fprintf(errw, "yolo apply: --at %q is not a confinement level (jail|guest|host)\n", at)
			return 2
		}
		notch = n
	}

	pr := richtext.Printer{W: out, Color: color}
	// THE FLAG BELONGS TO ONE ROUTE, and every other one refuses it rather than answering a
	// request for data with prose — which is the failure the flag exists to prevent, and the
	// shape `yolo broker` already uses for its acting verbs. `--sealed` is a refusal verb,
	// the guest notch is unbuilt, and the jail notch's apply is a stub pointing at launch:
	// none of the three has a document to emit, and each would otherwise print a human
	// report to something that asked for JSON.
	if outfmt.IsJSON(format) && (sealed || notch != config.ConfinementHost) {
		fmt.Fprintln(errw, "yolo apply: --format json is the HOST notch's dry run "+
			"(`yolo apply --at host`, or `yolo host apply`). No other notch has a document "+
			"to emit: guest is unbuilt, and at jail this verb points at launch.")
		return 2
	}
	if sealed {
		return applySealed(out, errw, color)
	}

	switch notch {
	case config.ConfinementHost:
		return applyHostFormatted(out, errw, color, assert && !dryRun, stdin, format)
	case config.ConfinementGuest:
		pr.Printf("[yellow]apply at the guest notch is not built yet (env-manager plan " +
			"Phase 7 — the LSM-confined backend).[/yellow]")
		return 1
	default: // jail
		_ = dryRun
		pr.Printf("[bold]apply[/bold] at confinement [cyan]jail[/cyan].")
		pr.Printf("[dim]At the jail notch, provisioning happens as part of launch. Run " +
			"`yolo -- <cmd>` to provision and enter, or `yolo -- true` to provision and exit. " +
			"A dedicated provision-without-launch path is a follow-up (env-manager plan Phase 3 " +
			"leaves the no-exec jail provision to a later increment).[/dim]")
		pr.Printf("")
		return describeMain(nil, out, errw, color)
	}
}

// applyHost renders the configured packs' config surfaces into the invoking user's REAL
// home (env-manager plan Phase 4). Default posture is OBSERVE (dry-run): it prints what
// would change and writes nothing; --assert (write=true) actually renders. Pure RMW, no
// computed layer, user-scoped, no --revert — the resolved OQ-1..4 model. Non-config
// kinds are refused by name via the host FieldSet, and `program` resolves to the host's
// real dep state (present/missing + the remedy for the detected manager) without running
// an install — that stays confirm-gated behind env-manager plan Phase 4.3.
func applyHost(out, errw io.Writer, color bool, write bool, stdin io.Reader) int {
	return applyHostSurveyed(out, errw, color, write, stdin, nil)
}

// applyHostFormatted is applyHost plus the output-format family (report-tiers.md §4.8,
// self-documenting-cli.md item 7). Both spellings of the verb route through it, because
// `yolo host apply` and `yolo apply --at host` are one operation and only differ in how
// they are typed (OQ-7) — a flag that worked at one of them would be a flag an agent has to
// guess about.
//
// THE POSTURE DECIDES, NOT THE VERB ([OQ-RO4]). The dry run's whole output is a state
// report, so it emits the document; --assert ACTS, so it refuses the flag rather than
// growing a second output mode — exit 2, and stdout stays empty because nothing has been
// written to it yet. The refusal is HERE, above the render, for the reason the format parse
// is above the probes: a refusal after the work is a refusal that cost something.
//
// The human report is DISCARDED, never reshaped (outfmt.Sink): the text form is the contract
// existing readers have, and the same single pass fills the survey the document is built
// from. stdin is nil in the JSON branch by construction — the observe posture prompts for
// nothing, and promptYesNo reads nil as NO, so a document can never be the thing that
// answered a question.
func applyHostFormatted(out, errw io.Writer, color bool, write bool, stdin io.Reader,
	format string) int {
	if !outfmt.IsJSON(format) {
		return applyHost(out, errw, color, write, stdin)
	}
	if jsonRefusedForPosture(format, write) {
		return refuseJSONForActingApply(errw)
	}
	survey := &hostApplySurvey{}
	rc := applyHostSurveyed(outfmt.Sink(out, format), errw, false, false, nil, survey)
	return emitHostApplyDoc(out, errw, format, survey, rc)
}

// applyHostSurveyed is applyHost with the change-predicate ROLL-UP handed back to the caller
// (hostapplysurvey.go). It is the same operation and the same output; the survey is an extra
// return channel, not a mode.
//
// It exists so §4.3's launch gate can ask "would an --assert change anything in this home?"
// by running THIS pass in observe posture with the output discarded, rather than growing its
// own traversal of the four written kinds — which would be a second model of the apply, free
// to drift out of step with the apply it describes.
// jsonRefusedForPosture answers [OQ-RO4]'s question from ARGV ALONE — is this
// format+posture pair the one an acting apply refuses? — so a caller can ask it before
// running anything, rather than inferring it from an exit code afterwards.
//
// It exists because the refusal has to stop a COMMAND, not a render. Reached only through
// applyHostFormatted it stopped the render and left every later stage running: `yolo host
// apply --assert --shell-init --format json` exited 2 with an empty stdout and STILL
// appended the PATH line to the user's shell rc, silently — the confirmation line goes
// through outfmt.Sink, which JSON mode discards. A refusal that edits a shell rc file is
// the write P3 forbids.
func jsonRefusedForPosture(format string, write bool) bool {
	return outfmt.IsJSON(format) && write
}

// refuseJSONForActingApply prints the refusal and returns its exit code. ONE function, so
// the two callers that must both stop cannot come to say different things about why.
func refuseJSONForActingApply(errw io.Writer) int {
	fmt.Fprintln(errw, "yolo host apply: --format json is the DRY RUN's — this posture "+
		"ACTS, and an acting verb refuses the flag rather than growing a second output "+
		"mode. Drop the assert flag to see what an apply would do, as data.")
	return 2
}

func applyHostSurveyed(out, errw io.Writer, color bool, write bool, stdin io.Reader,
	survey *hostApplySurvey) int {
	pr := richtext.Printer{W: out, Color: color}
	// The roll-up is always collected, even when the caller wants none: the observe posture
	// ends with it, and a nil survey there would mean the dry run could not report the very
	// summary §10 step 1 asks for. See hostapplysurvey.go.
	if survey == nil {
		survey = &hostApplySurvey{}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply: cannot resolve your home: %v\n", err)
		return 1
	}
	// The survey carries the home from here on: the verdict's footer names it and so does
	// the machine document, and a fact resolved once and passed twice is a fact two readers
	// can come to disagree about.
	survey.noteHome(home)
	entries, err := config.LoadPacks(nil)
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		// "Nothing to apply" is not "nothing to clean up" — emptying `packs` is the MOST
		// complete drop there is, and returning here left every pack's delivered output in the
		// home with nothing that would ever ask about it again. So the retire pass still runs,
		// against an empty configured set. Nothing else does: with no pack to render there is
		// no surface, no briefing, and no candidate whose destination to visit, which is why
		// this is a narrow second call rather than a fall-through into the loop below.
		// A HEADER for the retire passes below, not the result — the result is the verdict
		// this branch now ends with (report-tiers.md §4.3). It says what is about to happen
		// rather than repeating the verdict's own sentence two lines ahead of it.
		pr.Printf("[dim]No packs configured — nothing to apply, so this run only retires " +
			"what dropped packs left behind.[/dim]")
		// The BRANCH, recorded: "no packs are configured" and "every configured pack changed
		// nothing" are different results with different next actions, and both reach the
		// survey as an empty changed set. Nothing can derive it downstream, so it is stated
		// here, where it is known.
		survey.noteZeroPacks()
		// The overlay-key half runs here too, and for the same reason: with `packs` empty every
		// key any pack ever contributed is an orphan, so the most complete drop there is must
		// not be the one case that cleans up nothing. No live overlays exist to cross-check
		// against, which a nil OverlaySet expresses exactly (For returns nil).
		empty := map[string]bool{}
		stamp := time.Now().UTC().Format("20060102-150405")
		// The BRIEFING and SKILLS halves too, now that both destinations are whole yolo-owned
		// content rather than something inside the user's. With no pack contributing anywhere,
		// every composed destination is an orphan — generated content with nobody left to
		// regenerate it — which is precisely what the retire passes archive. A nil active set
		// would be refused, so the honest empty map is passed; the pack set is trivially
		// COMPLETE, since an empty config names nothing that could have failed to resolve.
		// nil reload: with no pack configured there is nothing for a migration to compose back,
		// so re-resolving would find the same empty set.
		rc := applyHostBriefings(pr, out, stdin, nil, packload.Embedded(), empty, true,
			home, stamp, write, nil, survey)
		if src := applyHostSkills(pr, out, stdin, nil, packload.Embedded(), empty, empty, true,
			home, stamp, write, nil, survey); src != 0 {
			rc = src
		}
		if prc := pruneDroppedPackOutput(pr, out, stdin, packload.Embedded(), empty,
			home, stamp, write,
			planOverlayKeyRetirement(pr, packload.Embedded(), empty, nil, home)); prc != 0 {
			rc = prc
		}
		// Wrappers too, and for this branch's own stated reason: with no pack configured
		// there is no program left to wrap, so every wrapper on the user's PATH is an
		// orphan pointing at something nothing will reinstall. Leaving them behind is
		// exactly the "delivered output nobody will ever ask about again" this branch
		// exists to prevent — and these are EXECUTABLES, at the front of a PATH.
		if wrc := applyHostWrappers(pr, errw, home, nil, write, survey); wrc != 0 {
			rc = wrc
		}
		// THE TIER-3 GROUPS, HERE TOO. This branch can retire content, and §4.4's rule is
		// that no default view omits a loss — a branch that cannot currently produce one
		// must still be a caller, or the first loss it learns to produce is silent.
		printRemedyGroups(pr, hostApplyRemedyGroups(survey, home, write))
		// THE VERDICT, HERE TOO. This branch returns before the tail below, so an empty
		// `packs` ended with no count, no verdict and no "nothing written" line at all
		// (§5's first hole, verified 2026-09-11) — the one posture in which the reader has
		// least context got the least output.
		printHostApplyVerdict(pr, survey, write)
		return rc
	}

	// One archive generation per apply, so everything this run retires groups under one
	// directory and the user can undo a whole apply rather than hunting per-file.
	stamp := time.Now().UTC().Format("20060102-150405")

	// THE HEADER SAYS IT IN PLAIN WORDS (§4.3 item 1, §4.6). It used to read
	// `host apply  home <path>  posture observe (dry-run)` — `posture` is a word the reader
	// has never met, and `observe (dry-run)` is the two-word spelling §4.6 rules against:
	// `observe` is the posture's name at the CALL SITE and in the design, and exactly one
	// word reaches the user. The home path stays — it is the fact an in-jail reader needs,
	// because this command renders into *this* jail's home when run from inside one.
	posture := fmt.Sprintf("dry run into %s; nothing is written", home)
	if write {
		posture = fmt.Sprintf("applying into %s", home)
	}
	pr.Printf("[bold]host apply[/bold] — %s", posture)

	hostFields := render.HostFields()
	rc := 0
	// active names the packs this apply is asserting; every other pack yolo SHIPS is a
	// prune candidate. Collected as we go and consumed after the loop (see the prune call),
	// because "which briefing destinations are orphaned?" is only answerable once the whole
	// active set is known — a pack dropped from config never appears in `entries` at all.
	active := map[string]bool{}
	// configured is every pack the config NAMES, resolvable or not. The retire pass below
	// keys on this rather than on `active`, because a fetched pack with an unreachable remote
	// resolves to nothing and would otherwise look dropped.
	configured := map[string]bool{}
	// resolvedAll is whether `active` and `configured` agree — i.e. whether the pack set is
	// COMPLETE this run. The briefing retire needs it for the same reason the skills one keys on
	// `configured`: since §6a a briefing destination is a whole yolo-owned file, so archiving it
	// on a bad guess costs the user a trip to the state dir rather than self-healing on the next
	// reachable launch (which is what a delimited block did).
	resolvedAll := true
	var loaded []*packload.Pack
	// Resolve the packs FIRST, before rendering any of them, because config-overlay is
	// cross-pack: an overlay in pack B targets a surface pack A owns, so the per-pack loop
	// below cannot discover it. Two passes over `entries` is the price of the one thing the
	// kind exists to do (docs/reference/pack-system.md §6).
	for _, e := range entries {
		configured[e.Name] = true
		p := packForCheckDeps(e) // same loader: embedded or local; git needs `pack install`
		if p == nil {
			pr.Printf("[dim]%s: not resolvable offline (fetched packs need `yolo pack install`) — skipped[/dim]", e.Name)
			resolvedAll = false
			continue
		}
		active[p.Name] = true
		loaded = append(loaded, p)
	}
	// reloadPacks re-runs exactly the resolution above. The briefing migration CREATES the local
	// pack (it moves the user's prose into ~/.config/yolo-jail/local/AGENTS.md), and the local
	// pack is included by CONVENTION — implicitly, on the strength of that directory existing.
	// So the set resolved before the migration does not contain it, and without a re-resolve the
	// migrated prose would only reach the destinations on the NEXT apply: the same apply that
	// promised "your instructions still reach your agents" would have removed them for one run.
	// Found by asserting idempotency, not by reading the flow.
	reloadPacks := func() []*packload.Pack {
		fresh, ferr := config.LoadPacks(nil)
		if ferr != nil {
			return nil // keep the already-resolved set; the load error was reported above
		}
		var out []*packload.Pack
		for _, e := range fresh {
			if p := packForCheckDeps(e); p != nil {
				out = append(out, p)
			}
		}
		out, _ = packload.ResolveDestinations(out)
		return out
	}
	// ZERO CEREMONY, AT BOTH NOTCHES (finding F1). A pack with no pack.json — the layout
	// `yolo pack --help` and the migration guide promote as THE starting point — declares no
	// destination, so a render that iterates declarations found nothing to do and said nothing
	// about it: `✓ pack ok` at lint, zero files in a real home, no warning. The jail never had
	// that gap, because it infers a destination from the pack SET (every agent pack's `skills`
	// contribution names the dir its agent reads); this is that same inference, so the two
	// notches now agree about where a silent pack's content goes.
	//
	// Applied HERE — after resolution, before anything reads a declaration — because the whole
	// point is that nothing downstream needs a zero-ceremony branch: `loaded` from this line on
	// holds packs that declare their destinations, whether their author wrote them or the set
	// did. `active`/`configured` are keyed on NAME, which the resolution preserves, so the
	// prune passes are unaffected.
	// REFUSE an `agents` selector naming an agent this pack set does not HAVE, BEFORE the
	// resolution — the P3 gate at the second of the two points §4.3 selects (the other is the
	// launch pre-flight; `yolo pack lint` deliberately cannot decide it, having no config).
	// Prose addressed to nobody is worse than prose addressed to everybody: the author
	// believes it landed.
	//
	// BEFORE, not beside the collision refusals below, and the ordering is the point rather
	// than tidiness. The resolution report says of an unmatched audience that "the name is
	// enabled — what is missing is an `agent` on the owning pack" (R1), which is true only
	// once this gate has passed. Run afterwards, it printed that line about a typo and then
	// refused the typo two lines later.
	//
	// Refused rather than reported because the remedy is the ADDRESSING pack's own line. Its
	// sibling case — a good name whose owner declares no destination of that kind — is R1 and
	// stays a report, since that remedy belongs to the owning pack (see
	// packload/agentaudience.go for why both severities are one rule).
	if probs := packload.AgentAudienceProblems(loaded); len(probs) > 0 {
		for _, prob := range probs {
			pr.Printf("  [red]agent      refused[/red] — %s", prob)
		}
		pr.Printf("[bold red]host apply: refused — %d `agents` selector(s) naming an agent "+
			"your packs do not provide. Nothing was written.[/bold red]", len(probs))
		return 1
	}
	loaded, destinations := packload.ResolveDestinations(loaded)
	for _, d := range destinations {
		if drc := reportInferredDestinations(pr, d); drc != 0 {
			rc = drc
		}
	}
	// REFUSE a doubly-declared config surface before writing anything into a real home
	// (docs/reference/pack-system.md Option 1 / R1). This is also R4: the double
	// `rendered` line was one line per DECLARING pack for one file — the collision made
	// visible while nothing called it one. Refusing the apply is what removes the second
	// line, rather than deduping the output and leaving the ambiguity in place.
	//
	// Before the render loop, not inside it: which pack's `mode`/`path` won is a property of
	// the whole set, so a per-pack check would let the first pack write with a definition the
	// second was about to replace.
	if cols := packload.ConfigSurfaceCollisions(loaded); len(cols) > 0 {
		for _, c := range cols {
			pr.Printf("  [red]config     refused[/red] — surface %s claimed by %s: %s",
				c.Target, strings.Join(c.Packs, ", "), c.Reason)
		}
		pr.Printf("[bold red]host apply: refused — %d config surface(s) with more than one "+
			"owner. Nothing was written.[/bold red]", len(cols))
		return 1
	}
	// And REFUSE an agent NAME claimed by two packs, for the same reason and in the same
	// position (briefing-audiences.md OQ-BA6/BA7). It matters most at THIS notch: the render
	// below routes an addressed contribution to "where <name> reads", so with two owners the
	// prose lands wherever the resolution loop saw first — in a real home, with nothing said.
	if cols := packload.AgentNameCollisions(loaded); len(cols) > 0 {
		for _, c := range cols {
			pr.Printf("  [red]agent      refused[/red] — name %s claimed by %s: %s",
				c.Target, strings.Join(c.Packs, ", "), c.Reason)
		}
		pr.Printf("[bold red]host apply: refused — %d agent name(s) with more than one "+
			"owning pack. Nothing was written.[/bold red]", len(cols))
		return 1
	}
	// The §4.2 autonomy policy comes from THIS apply's render target, not from the `false`
	// that used to sit here (C3, plan §6c step 1). It resolves to OFF — the guarded posture
	// — so the owner set matches the surfaces RenderHostPack will actually produce, which is
	// the property this call needs; but it now reads that from the same render.Host target
	// the render itself keys on, so the two cannot disagree about the notch. The literal
	// could only agree by inspection. The profile table is the host notch's own (see
	// overlayGateProfiles): a `profile`-gated overlay renders here only while its name is
	// active at the surface's agent.
	overlays := packoverlay.Collect(loaded, render.Host(home, nil).Profile().AgentAutonomy,
		overlayGateProfiles(render.KindHost))
	for _, prob := range overlays.Problems {
		pr.Printf("  [red]config-overlay refused[/red] — %s", prob)
		rc = 1
	}
	for _, orphan := range overlays.Orphans {
		// R2: inert, and named. Not an error — a pack the user did not select is not a
		// mistake — but never silent either, which is the whole no-silent-skip invariant
		// this command's census test enforces.
		pr.Printf("  [yellow]config-overlay  %s[/yellow] [dim](pack %s)[/dim]",
			orphan.Reason(), orphan.Pack)
	}

	// THE DEPENDENCY PRE-FLIGHT (report-tiers.md §4.9 point 1, §9 step 5). Every configured
	// pack's declared binaries, probed once, BEFORE anything is written — including before
	// the one-way door below, which is the other thing that can stop this run.
	//
	// It used to be one `resolveHostDeps(p)` inside the render loop. That position is fine
	// while the answer is only a line, and wrong the moment it can stop the run: §9 step 6
	// makes a declined install fatal, and a fatal from inside the loop would leave the packs
	// already visited written and the rest not. "We cannot continue" has to also mean
	// "nothing was written", and only a pre-flight delivers both.
	//
	// It is also the ONE COLLECTOR: it records what it learned in the survey as it goes, so
	// the gate below, the verdict's counts and the per-contribution lines are three renderings
	// of one answer rather than three walks that could disagree about which binary is missing.
	deps := probeHostDeps(loaded, hostFields, survey)

	// THE DEPENDENCY GATE (§4.9, §9 step 5), and it comes FIRST — before the one-way door, and
	// before anything is written.
	//
	// Before the loss confirmation because a decline here is fatal: asking the user to approve
	// losing their MCP entries and then refusing the run over a missing binary would spend a
	// `y` on a run that was never going to complete. The gate that can stop the run asks first.
	//
	// --assert only. The dry run reports the same blockers through the tier-3 groups below,
	// names them in its verdict, exits 0 and never prompts (OQ-RO5): its output IS the finding.
	if write {
		if grc := gateHostDeps(pr, out, stdin, deps, survey); grc != 0 {
			return grc
		}
	}

	// THE ONE-WAY DOOR. Before writing anything, ask an observe pass what an --assert would
	// destroy, and if the answer is "a value yolo has never asserted in this home", require a
	// confirmation. Maintainer ruling (2026-08-02): "let's just warn during the first apply
	// that things will be lost and wait for confirm" — warn-and-confirm, not warn-and-refuse.
	// See confirmHostLosses for the three properties that make it not-noise.
	if write && !confirmHostLosses(pr, out, stdin, loaded, home, overlays) {
		pr.Printf("[bold red]host apply: not confirmed — nothing was written.[/bold red]")
		// ONE remedy string, read here and at the two other places this sentence used to be
		// written out (the per-surface loss line and confirmHostLosses' own trailer). The
		// three had drifted — see hostapplyremedy.go.
		pr.Printf("[dim]Re-run and answer `y`, or keep them: %s.[/dim]", mcpEntryRemedy(home))
		return 1
	}

	// THE TIER-1 FACTS, ONCE FOR THE WHOLE RUN (report-tiers.md §4.1, §9 step 3). Which kinds
	// this notch does nothing with, and which autonomy posture it renders, are properties of
	// the NOTCH: the same sentences in every home on every machine. They used to print per
	// CONTRIBUTION — nineteen ~40-word refusal paragraphs naming seven kinds, plus five
	// identical autonomy lines — and the census invariant they serve is satisfied by NAMING
	// (P5: "appearing once is appearing"), not by repetition. See hostapplynotch.go.
	//
	// Before the loop, so "folded into the config surfaces below" is a true word about what
	// comes next, and so a reader meets the notch before they meet this home.
	notch := surveyNotchFacts(loaded, hostFields)
	survey.noteNotch(notch)
	printNotchFacts(pr, notch)

	for _, p := range loaded {
		// Account for EVERY kind the pack declares. Three outcomes, and the invariant is that
		// there is no fourth: named once above as a kind this notch does not apply (whether
		// the FieldSet refuses it or honors it with no renderer behind it), rendered below, or
		// — for the two dep kinds — probed here. A kind that produced no line at all was the
		// G1 bug: `skills`/`briefing` were honored by the FieldSet but rendered by nothing, so
		// they vanished silently, which is strictly worse than a loud refusal.
		packDeps := deps.of(p) // probed in the pre-flight above, consulted here
		for _, c := range p.Decl.Contributions() {
			// The dep kinds are the one class whose answer is a property of THIS HOST rather
			// than of the notch, so they are the one class still reported per contribution.
			// The SAME predicate the pre-flight counted with, so the set that reaches the
			// verdict and the set that reaches the report are one set (isProbedDep).
			if !isProbedDep(hostFields, c) {
				continue
			}
			// program AND requires: resolved dep state, not a static "confirm-gated" line —
			// which kind asked, which bin, present or missing
			// (pack-host-management-plan.md Phase 8). `requires` shares this path because
			// below the jail notch the two kinds ask the host the same question.
			//
			// The SURVEY is not fed here any more: the pre-flight above records every finding,
			// because the gate needs the merged answer before this loop runs at all. What is
			// left here is the line — and the line is DETAIL (§4.5): the verdict counts every
			// probed dependency, a missing one is itemized by its tier-3 group with its
			// remedy, and this per-contribution rendering is the auditor's third copy.
			detail(pr, "%s", packDeps.depLine(c))
		}
		if frc := applyHostFiles(pr, errw, p, home, stamp, write, survey); frc != 0 {
			rc = frc
		}
		results, rerr := entrypoint.RenderHostPack(p, home, !write, overlays)
		if rerr != nil {
			fmt.Fprintf(errw, "yolo host apply: %s: %v\n", p.Name, rerr)
			// A §4.1 BLOCKER, and it has to reach the verdict: this pack's surfaces are
			// absent from every count below, so a verdict built from those counts alone
			// would claim a completed apply out of a traversal that lost a pack.
			survey.noteRenderFailure(p.Name)
			rc = 1
			continue
		}
		for _, r := range results {
			// ONE call, carrying the predicate, the §4.1 tier and every loss the verdict
			// counts: they are facts about the same render, and splitting them at the call
			// site is how one of them comes to be forgotten at the next one.
			survey.noteConfig(r)
			// THE ONE TIER-2 LINE THE DEFAULT VIEW KEEPS (§4.1's tier-2 row, §4.2): a config
			// surface that would change is itemized — there are few of them and they are what
			// the auditor came for — while one that is unchanged, skipped or refused is a
			// settled run fact the verdict counts and `--verbose` lists.
			reportDestination(pr, configResultTier(r), r.WouldChange,
				"  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
			// Which packs contributed config-overlay keys to this surface (ruling R3). An
			// overlay folds BELOW the owner's managed layer, so it leaves no trace in the
			// resulting file — without this line the only answer to "which pack set that
			// key?" is a sidecar the host render does not even write.
			if len(r.Overlays) > 0 {
				detail(pr, "    [magenta]config-overlay keys from: %s[/magenta] [dim](below this "+
					"surface's own managed layer, which still wins a conflict)[/dim]",
					strings.Join(r.Overlays, ", "))
			}
			// The overlay keys the owner OUTRANKED, by name and by cause (finding F4). The line
			// above says a conflict would go the owner's way; this one says one DID, and which
			// key. Without it the loss was worse than silent: the overlay was listed as
			// contributing and the ⚠ below fired for the same key, so the report read as though
			// the overlay had won. Its own line rather than folded into that ⚠ because nothing
			// was overwritten BY THE OVERLAY here — the policy simply held.
			for _, o := range r.Outranked {
				pr.Printf("    [yellow]↳ %s[/yellow]", o)
			}
			// Warn on every managed key that overwrites a DIFFERING existing value — the
			// host-notch "always warn" (§4.2 / Phase 9). Shown in observe too, so the
			// preview is not path-only (finding D2): you see the collision before writing.
			if len(r.Overwrites) > 0 {
				verb := "would overwrite"
				if write {
					verb = "overwrote"
				}
				pr.Printf("    [yellow]⚠ %s your existing value for: %s[/yellow]",
					verb, strings.Join(r.Overwrites, ", "))
			}
			// Named-ENTRY casualties: a server whose record comes out merged or gone. Louder
			// than an overwrite because nothing in the resulting file says what it used to be
			// — and on a first apply this is the line the confirmation prompt is about.
			if len(r.EntryLosses) > 0 {
				verb := "would damage"
				if write {
					verb = "damaged"
				}
				// The REMEDY is not here any more: this line fires once per surface, and the
				// same three servers dropped from three agents produced three copies of one
				// fix (§3.3). The fix is stated once, for every entry that shares it, in the
				// tier-3 group below (hostapplyremedy.go). What stays is the fact that is
				// true of THIS surface — yolo owns this table, so an undeclared entry goes.
				pr.Printf("    [bold yellow]⚠ %s your existing entry: %s[/bold yellow] "+
					"[dim](yolo owns this table)[/dim]",
					verb, strings.Join(r.EntryLosses, ", "))
			}
			// The ${workspace}-keyed keys this render DROPPED, by name — a TIER-2 fact under
			// its surface, so the --verbose view's since §4.5 ("every tier-2 destination
			// itemized: the skipped surfaces and why").
			//
			// NOT the carve-out a refusal gets. This comment used to argue the opposite —
			// "a line for the same reason a refusal does" — while the line below it was moved
			// behind detail(), which is a written argument, in this file, for reverting the
			// code or for generalising detail() onto a real refusal by the analogy. The two
			// are different classes: a pruned key has another representation (the key is
			// still in the pack that declared it, and `yolo config-ref` says why the host
			// notch does not honor it), where a refused skill adoption has none — which is
			// why THAT one is explicitly exempted from detail() (applyhostskills.go, §4.4).
			// The no-silent-drop rule is unchanged: the key is still named, in the view that
			// itemizes a destination's keys at all.
			if len(r.Pruned) > 0 {
				detail(pr, "    [dim]skipped ${workspace}-keyed (no host referent): %s[/dim]",
					strings.Join(r.Pruned, ", "))
			}
			// What the canonical re-emit costs beyond values — a TOML file's comments. Not an
			// overwrite and not an entry loss: nothing the user CONFIGURED changes, so it does
			// not belong in either ⚠ above. It is still a loss they should see before the
			// write, which is why it has a line of its own.
			//
			// E4 shipped the `rmw` half, so this line has narrowed rather than disappeared: a
			// comment now survives whenever the value it sits above does, and what remains is
			// the exceptions — a comment over a key this render CHANGES (dropped rather than
			// left lying about a value that is gone), and one attached to no key at all.
			for _, f := range r.Formatting {
				pr.Printf("    [yellow]⚠ %s[/yellow]", f)
			}
		}
	}

	// Compose the SKILLS and BRIEFING destinations, for the WHOLE pack set at once. After the
	// per-pack loop because each destination's content is the union of every contributing pack's
	// (§6a, §6a-2): rendering inside the loop would either accumulate or let the last pack's write
	// erase the others' — and for `skills` it would additionally have to negotiate a name two packs
	// both claim, which is the negotiation composition deletes (§6a-5).
	candidates := append(loaded, embeddedPacksForPrune()...)
	// Skills FIRST, deliberately. Both migrations create the local pack and both re-resolve after
	// a confirmed one, so either order converges — but a user answering two prompts should be
	// asked about the bigger move first, and moving a directory of skills is bigger than moving
	// one file's prose.
	if src := applyHostSkills(pr, out, stdin, loaded, candidates, active, configured, resolvedAll,
		home, stamp, write, reloadPacks, survey); src != 0 {
		rc = src
	}
	if brc := applyHostBriefings(pr, out, stdin, loaded, candidates, active, resolvedAll,
		home, stamp, write, reloadPacks, survey); brc != 0 {
		rc = brc
	}

	// Retire the SKILLS, FILES, and CONFIG-OVERLAY KEYS a dropped pack left in the home. After
	// the briefing prune, not folded into it: a briefing block's removal restores the file's own
	// bytes and is unconditional (ruling R4), while these are content and assertions IN files
	// the user owns and ride an explicit confirmation (R1 and R3's first sentence). Keeping them
	// separate is what preserves that asymmetry — one prompt, for the destructive half only, at
	// the end of the report it is about.
	//
	// The key half is PLANNED first and committed inside the prune, so both halves appear in one
	// prompt rather than each getting its own [y/N] for the same edit to `packs`. `configured`,
	// not `active`, for the same offline-remote reason the path half uses it.
	keys := planOverlayKeyRetirement(pr, candidates, configured, overlays, home)
	if prc := pruneDroppedPackOutput(
		pr, out, stdin, candidates, configured, home, stamp, write, keys); prc != 0 {
		rc = prc
	}

	// Launch wrappers, last: they are the only stage that writes OUTSIDE the composed
	// surfaces, and generating them after the surfaces means a wrapper never appears for
	// a pack whose own apply just failed. Silent unless opted in (§5.5).
	if wrc := applyHostWrappers(pr, errw, home, loaded, write, survey); wrc != 0 {
		rc = wrc
	}

	// THE TIER-3 GROUPS (report-tiers.md §4.4, §9 step 4): every loss and blocker this run
	// found, grouped by the FIX rather than by the emitter that noticed it, each group stating
	// its remedy once and every group represented in the verdict below. See
	// hostapplyremedy.go.
	//
	// After every stage that can produce one — the render loop, skills, briefings, the retire
	// passes — because a group is a property of the whole run: three surfaces dropping one
	// server is one fix, and nothing can say so until the third surface has been visited.
	printRemedyGroups(pr, hostApplyRemedyGroups(survey, home, write))

	if !write {
		// THE DESTINATION ROLL-UP, and it is the point of the change predicate at this surface:
		// an observe pass over an already-applied home used to end in N identical `would render`
		// lines with nothing saying they were all no-ops (§10 step 1). It counts DESTINATIONS,
		// which is the launch gate's question and not the reader's (report-tiers.md §3.4), so it
		// sits above the verdict as the detail it is — and it IS detail now: a tier-2
		// itemization, moved behind --verbose by §9 step 6, which is what this block's own
		// comment predicted one step ago. 70 of the measured home's 76 lines were fourteen
		// skills counted once per agent directory.
		detail(pr, "[bold]%s[/bold]", survey.Summary())
		for _, c := range survey.Changed {
			detail(pr, "  [yellow]would change[/yellow] [cyan]%-10s %s[/cyan] [dim]%s[/dim]",
				c.Kind, c.Surface, c.Path)
		}
	}
	// THE VERDICT, OUTSIDE THE POSTURE GUARD. §4.3 requires it on every path in every
	// posture, and the block above used to be the whole tail: an --assert therefore ended
	// with no summary at all, having just written into a real home.
	printHostApplyVerdict(pr, survey, write)
	return rc
}

// confirmHostLosses gates a WRITING host apply on an explicit confirmation when it would
// destroy a value the user has and yolo has never asserted in this home. Returns true to
// proceed (nothing to lose, or the user said yes) and false to abort without writing.
//
// This is the one-way door. Wholesale table regeneration is correct POLICY — the maintainer
// ruled that managing `mcpServers` through yolo means giving up `claude mcp add`, so an
// undeclared server is stale by definition — but that only holds once the user has opted in.
// On the FIRST apply into a home they have not opted in yet: their hand-added server predates
// the pack, and replacing it before they have declared it anywhere is not policy, it is data
// loss. Warn-and-confirm rather than warn-and-refuse (maintainer ruling 2026-08-02), because
// a refusal leaves no path forward short of hand-editing the file yolo is about to manage.
//
// Three properties make it a real gate instead of noise:
//
//   - ONLY WHEN SOMETHING IS ACTUALLY LOST. Gated on FirstApply && EntryLosses — a clean
//     home, or any home yolo has asserted before, prompts not at all. A confirmation that
//     fires on every run trains people to hit `y` without reading, which is worse than no
//     gate. NOT `Overwrites` — and the relation is not containment either way, because the
//     two read DIFFERENT LAYERS (hostrender.go): `Overwrites` walks the MANAGED and OVERLAY
//     layers against the existing file key by key, `EntryLosses` walks the wholesale TABLE
//     layer against it entry by entry, and the managed half has the table keys stripped out
//     of it before it is walked. So swapping the gate would be wrong in both directions at
//     once. It would fire on every scalar flip — exactly the every-run confirmation this
//     property refuses — AND it would miss the one-way door it exists for: an `mcpServers`
//     entry dropped for not being declared is asserted by no layer `Overwrites` reads, so
//     `EntryLosses` is the only field that names it. See the loop below, and
//     HostRenderResult.EntryLosses for why the two are split.
//   - OBSERVE NEVER REACHES HERE (the caller checks `write`). A dry-run writes nothing, so
//     there is nothing to confirm; it just reports the same collisions as ⚠ lines, which is
//     how the user gets the information BEFORE the prompt ever appears.
//   - FAIL-CLOSED on stdin. promptYesNo reads a nil or EOF stdin as NO (pack.go's contract),
//     so a CI or scripted `yolo host apply --assert` aborts rather than silently destroying a
//     server because no human was present.
//
// It runs a full OBSERVE pass first, which is deliberately a second render: observe writes
// nothing and consumes no first-apply signal, so asking it "what would be lost?" is free and
// cannot itself be the thing that closes the door.
func confirmHostLosses(pr richtext.Printer, out io.Writer, stdin io.Reader,
	loaded []*packload.Pack, home string, overlays *packoverlay.OverlaySet) bool {
	type loss struct {
		surface, path string
		keys          []string
	}
	var losses []loss
	for _, p := range loaded {
		results, err := entrypoint.RenderHostPack(p, home, true, overlays)
		if err != nil {
			// A preflight that cannot answer must not be read as "nothing to lose". The real
			// render below will report the same error properly; here, fail closed by treating
			// an unanswerable preflight as no confirmation needed only if it found nothing —
			// which it did, since it errored. Reporting it and continuing keeps this function
			// from becoming a second error path for the same failure.
			continue
		}
		for _, r := range results {
			// EntryLosses, not Overwrites: a scalar whose value changes is named, reversible,
			// and already reported as an ordinary ⚠. Only a mangled or destroyed named ENTRY
			// leaves the user with nothing in the file saying what it used to be.
			if !r.FirstApply || len(r.EntryLosses) == 0 {
				continue
			}
			losses = append(losses, loss{surface: r.Surface, path: r.Path, keys: r.EntryLosses})
		}
	}
	if len(losses) == 0 {
		return true // nothing would be lost — no prompt (see property 1)
	}
	pr.Printf("[bold yellow]⚠ First apply into this home — the following existing values " +
		"will be REPLACED by what your packs declare:[/bold yellow]")
	for _, l := range losses {
		pr.Printf("  [cyan]%s[/cyan] [dim]%s[/dim]", l.surface, l.path)
		for _, k := range l.keys {
			pr.Printf("    [yellow]%s[/yellow]", k)
		}
	}
	pr.Printf("[dim]yolo regenerates the keys it manages wholesale, so anything above that "+
		"is not in your config is dropped. To KEEP them: %s — then re-run.[/dim]",
		mcpEntryRemedy(home))
	return promptYesNo(out, stdin, "  Proceed and replace the values above? [y/N] ")
}

// reportInferredDestinations names what the zero-ceremony inference concluded for one pack, and
// returns an rc contribution for the pack it could conclude nothing at all for.
//
// Both halves are reported, because both change what lands in a real home and neither is
// something the user wrote down:
//
//   - INFERRED. yolo is about to write into a directory the pack never named. That is the
//     documented promise being kept rather than a problem, so it is dim — one line per kind,
//     naming the destinations, so "why is there a skill in ~/.pi/agent/skills?" has its answer
//     in the report that put it there.
//   - ORPHANED. The pack carries content that reached nothing. TWO CAUSES, and they get two
//     phrasings, because the fields to change are different ones. Either no pack in `packs`
//     names a destination for the kind at all — fixed by selecting an agent pack or writing an
//     `into` — or the contribution named an AUDIENCE (its `agents` selector) that no
//     destination's declared `agent` matches, which `into` cannot fix and is refused alongside
//     (packdecl's validateContribution: a contribution names an audience or a destination,
//     never both). The orphan carries which one it is (packload.Orphan.Agents); this function
//     must not guess, and before it carried the reason the reader of the second case was told
//     to declare `into`.
//
// The severity of an orphan turns on whether the pack reaches anything AT ALL, and the split is
// ruling R2's, already applied to an orphaned config-overlay a few lines above: inert is named
// but is not an error, because a pack the user did not select is not a mistake. A pack that
// delivers its skills and happens to carry an AGENTS.md no selected pack has a destination for
// is in exactly that position — the ordinary shape of a `skills` pack, and failing the apply
// over it would make a warning out of correct usage.
//
// A pack that after resolution declares NOTHING is the other case, and it is finding F1 reached
// by the other route: a zero-ceremony content pack selected with no agent pack renders nothing,
// silently, which is the whole defect. `len(Contributions()) == 0` is the honest test for it
// rather than a heuristic — after ResolveDestinations a pack's declaration is everything it will
// ever be asked to do, so an empty one means it will do nothing.
// An ADDRESSED contribution is the third half, added by briefing-audiences.md, and it needed
// its own line rather than a wider one: "declares no destination" is FALSE of it. A pack
// saying `agents: ["claude"]` declared exactly who its prose is for and deliberately not where
// that prose goes (P4), so reporting it as silence describes the opposite of what the author
// did — and leaves them unable to tell a working selector from a typo, since both produce the
// same line. The audience is named, so the report answers "did my selector reach claude?".
func reportInferredDestinations(pr richtext.Printer, d packload.Destinations) int {
	// Destinations an ADDRESSED contribution accounted for. Subtracted from the silent-inference
	// line below so one delivery is not reported twice, in two voices — a pack MAY carry both a
	// bare into-less contribution (broadcast) and an addressed one, in which case the addressed
	// line names its destinations and the broadcast line names the rest.
	addressed := map[string]bool{}
	for _, a := range d.Addressed {
		for _, into := range a.Into {
			addressed[string(a.Kind)+"\x00"+into] = true
		}
		if len(a.Into) == 0 {
			continue // R1 — reported by the orphan branch below, which carries the severity
		}
		// DETAIL (§4.5): "where did this land, and why there" is the auditor's and the
		// diagnoser's question about a destination that resolved correctly. The ORPHAN
		// branches below are not this — they report content that reached NOTHING, which is a
		// tier-3 fact and prints at every verbosity.
		detail(pr, "  [dim]%-10s %s addresses %s — %s reaches %s, and nothing else[/dim]",
			string(a.Kind), d.Pack.Name, strings.Join(a.Agents, ", "),
			addressedSourceLabel(a.From), strings.Join(a.Into, ", "))
	}

	byKind := map[packdecl.Kind][]string{}
	var order []packdecl.Kind
	for _, c := range d.Inferred {
		if addressed[string(c.Kind)+"\x00"+c.Into] {
			continue
		}
		if _, seen := byKind[c.Kind]; !seen {
			order = append(order, c.Kind)
		}
		byKind[c.Kind] = append(byKind[c.Kind], c.Into)
	}
	for _, kind := range order {
		detail(pr, "  [dim]%-10s %s declares no destination — merging into the ones your packs "+
			"name: %s[/dim]", string(kind), d.Pack.Name, strings.Join(byKind[kind], ", "))
	}
	// R1, AND THE PART Orphaned CANNOT SAY. An audience that matched nothing is reported by
	// NAME, before the kind-level orphan line below, because the two have different remedies
	// and only one of them is legible from the kind: the addressing pack's `agents` is FINE
	// (the launch pre-flight would have refused an agent this jail does not have — P3), so
	// what is missing is an `agent` on the OWNING pack's destination of this kind. Telling
	// that author to "declare `into`" is the one thing they must not do (P4), which is why
	// this line comes first and says something else.
	unmatched := map[packdecl.Kind]bool{}
	for _, a := range d.Addressed {
		if len(a.Into) > 0 {
			continue
		}
		unmatched[a.Kind] = true
		// THE AUDIENCE IS UNMATCHED, NOT MISSING, and the kind-level line below is not true of
		// it: destinations for this kind may well exist and be receiving other packs' content.
		// So the remedy is the owner of the NAME — one name, one owning pack (P5) — or the
		// selector itself. Naming a path is not on the list, and saying so is the point: it is
		// what the reader would otherwise try.
		//
		// Severity is deliberately the `no effect` warning at rc 0. Whether an unmatched
		// audience should refuse outright is a live disagreement between P3 and risk R1 —
		// roadmap 💬 20 — and this reporter is not where it gets settled.
		kind := string(a.Kind)
		pr.Printf("  [yellow]%-10s no effect[/yellow] — %s addresses %s (its `agents` "+
			"selector: the launcher commands this %s content is FOR), and no %s destination "+
			"your packs name declares a matching `agent` (the identity an agent pack "+
			"declares beside its own `into`) [dim](select the pack that owns %s, or correct "+
			"`agents` in %s's pack.json — declaring `into` is not the remedy: a "+
			"contribution names an audience or a destination, never both)[/dim]",
			kind, d.Pack.Name, quotedAgents(a.Agents), kind, kind, quotedAgents(a.Agents),
			d.Pack.Name)
	}
	if len(d.Orphaned) == 0 {
		return 0
	}
	inert := len(d.Pack.Decl.Contributions()) == 0
	for _, o := range d.Orphaned {
		kind := string(o.Kind)
		// TWO SUPPRESSIONS, ONE INTENT: do not print the kind-level line about an orphan whose
		// cause is an unmatched AUDIENCE, because that line's remedy ("declare `into`") is the
		// one thing that author must not do. `unmatched` catches it from the Addressed side;
		// `len(o.Agents) > 0` catches it from the Orphan side. Either alone would be enough for
		// the paths built so far — both are here because the two records are produced
		// independently (see Destinations.Addressed) and a future caller may populate one
		// without the other, at which point the missing guard is a wrong remedy in a refusal
		// rather than a compile error.
		if unmatched[o.Kind] || len(o.Agents) > 0 {
			continue
		}
		if !inert {
			pr.Printf("  [yellow]%-10s no effect[/yellow] — %s carries %s content, and no pack "+
				"in `packs` names a %s destination [dim](select the agent pack that owns one, "+
				"or declare `into` in %s's pack.json)[/dim]",
				kind, d.Pack.Name, kind, kind, d.Pack.Name)
			continue
		}
		pr.Printf("  [yellow]%-10s refused[/yellow] — %s ships %s but no pack in `packs` names a "+
			"destination for it, so this pack renders NOTHING. Select the agent pack that owns "+
			"the destination, or declare `into` in %s's pack.json.",
			kind, d.Pack.Name, kind, d.Pack.Name)
	}
	if inert {
		return 1
	}
	return 0
}

// quotedAgents renders an audience for the orphan report: the names quoted, joined with "or"
// because the selector is an allowlist — any ONE of them matching would have routed the
// content, so "select the pack that owns X or Y" is the remedy as stated.
func quotedAgents(agents []string) string {
	quoted := make([]string, 0, len(agents))
	for _, a := range agents {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// addressedSourceLabel names the file an addressed contribution delivers, for the report. An
// absent `from` is the pack's CONVENTIONAL source, and saying so beats printing `""` — the
// author who omitted the field is the one most likely to be checking which file was read.
func addressedSourceLabel(from string) string {
	if from == "" {
		return "its conventional source"
	}
	return from
}

// embeddedPacksForPrune returns the packs yolo SHIPS, as prune candidates. A pack the user
// removed from config is not in `entries`, so its briefing destination would otherwise never
// be visited — and its block would outlive the pack silently, unattributed.
//
// packload.Embedded() is deliberately not selection-gated (see AGENTS.md), which is exactly
// what makes it the right source here: the point is to visit the destination of a pack that
// is NOT selected.
func embeddedPacksForPrune() []*packload.Pack { return packload.Embedded() }

// applySealed enumerates the input closure (env-manager design §3.3) and refuses if any
// UNDECLARED input shaped the environment. Sealing does not mean "no host reads" — a
// named-but-impure input (the user config, a pack's reads-host) is declared, nix's
// fixed-output derivation. It means no input that NOTHING names. The three refusals today:
//   - yolo-jail.local.jsonc: auto-merged, gitignored, needs no include entry.
//   - an outstanding capture overlay: in-jail edits that outrank every declared layer,
//     yet nothing declares them (they are a staging area to promote, §3.3).
//   - an UNSET `host_management`: the host-ownership contract is unstated
//     (docs/design/config-ownership-and-promotion.md §4.3 item 3).
//
// ⚠ THE THIRD IS A DIFFERENT SENSE OF "UNDECLARED", and the two are four lines apart. The
// first two are the input-closure tier: a VALUE inside an agent's config file that shapes
// the environment while nothing names it, whose remedy is to promote it or discard it. The
// third is the ordinary word UNSET: a yolo config key with no value at all, whose remedy is
// to write it. §4.3's note reserves "undeclared" for the closure sense on purpose, so the
// refusal below says "unset" and the ones above say "declares".
//
// It belongs here rather than in the apply because this is where the user asked the question
// about declaredness. An unset key changes NO apply — §4.3 rules the default carries the
// whole migration, silently — so `--sealed` is the one place it bites, and a `--sealed` that
// passed while the ownership contract was unstated would be answering a narrower question
// than the one it was asked.
func applySealed(out, errw io.Writer, color bool) int {
	pr := richtext.Printer{W: out, Color: color}
	ws := workspaceRoot()

	var refusals []string
	// (1) yolo-jail.local.jsonc present anywhere in the workspace root.
	localPath := filepath.Join(ws, config.WorkspaceLocalConfigName)
	if _, err := os.Stat(localPath); err == nil {
		refusals = append(refusals,
			config.WorkspaceLocalConfigName+" is present and merges into the config, but "+
				"nothing declares it (it is gitignored, machine-local). Fold its keys into "+
				"yolo-jail.jsonc or remove it to seal.")
	}
	// (2) any capture surface carrying outstanding overlay keys.
	for _, s := range surfaceManifest().Surfaces() {
		if n := overlayKeyCount(s.Agent, s.Name); n > 0 {
			refusals = append(refusals, fmt.Sprintf(
				"%s/%s has %d captured in-jail edit(s) outranking the definition — "+
					"promote them into a pack or `yolo config reset %s --surface %s` to discard.",
				s.Agent, s.Name, n, s.Agent, s.Name))
		}
	}

	// (3) the host-ownership contract, unstated.
	if _, declared := config.HostManagementDeclared(); !declared {
		refusals = append(refusals,
			"`host_management` is unset, so who owns the config files in your real home is "+
				"undeclared (it behaves as \"assert\"). Write one of \"none\", \"assert\" or "+
				"\"own\" into "+paths.UserConfigPath()+" to seal; `yolo config-ref` says what "+
				"each one means.")
	}

	if len(refusals) > 0 {
		pr.Printf("[bold red]apply --sealed: refused — %d undeclared input(s):[/bold red]", len(refusals))
		for _, r := range refusals {
			pr.Printf("  [red]✗[/red] %s", r)
		}
		return 1
	}
	pr.Printf("[green]sealed[/green] — the environment is assembled only from declared inputs.")
	pr.Printf("[dim]Its `describe --hash` is now a reproducibility pin, not just a cache key.[/dim]")
	return 0
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

const applyUsage = `yolo apply — make this environment match its description, without running anything

  yolo apply                provision the environment at its configured confinement
  yolo apply --at <level>   … at a different notch (jail|guest|host) for this run
  yolo apply --at host      render your config into your real home
                            (yolo host apply is the same thing, more typeable)
                            (a DRY RUN by default — prints what would change, writes nothing)
  yolo apply --at host --assert  actually write: regenerate only the keys yolo manages (pure
                            rmw), leaving your own keys; non-config kinds refused by name
  yolo apply --sealed       refuse if any UNDECLARED input shaped the environment
                            (yolo-jail.local.jsonc, an outstanding capture overlay)
  yolo apply --dry-run      show what would change, write nothing

Machine-readable output, at the HOST notch's dry run only (` + "`yolo apply --at host --format json`" + `):
the asserting posture acts, and an acting verb refuses the flag rather than growing a
second output mode — so it exits 2 there instead.
` + outputFormatUsage + `

apply splits "make it so" from "run something in it": ` + "`yolo -- <cmd>`" + ` is
"apply, then exec." Every notch has both halves — the host's exec half is
` + "`yolo host -- <cmd>`" + `, which composes the environment a config file cannot carry.
Examples:
  yolo apply                          # provision the jail, launch nothing
  yolo apply --at host                # what would change in your real home?
  yolo apply --at host --assert       # write it
  yolo apply --sealed                 # refuse if an undeclared input shaped this

See ` + "`yolo describe`" + ` for what the current description resolves to, and
` + "`yolo host`" + ` for the host notch's own verbs.`
