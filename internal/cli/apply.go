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
//     (`apply --host`), gated on the host-render work; here it is recognized and routed
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
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func runApply(args []string) int {
	return applyMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout), os.Stdin)
}

// applyMain parses the flags and routes to a notch. stdin is the reader the host notch's
// destructive-change confirmation reads; a nil stdin (a test, or any non-interactive run)
// means "not confirmed", matching packMain's fail-closed contract — a scripted
// `apply --host --assert` must not destroy a user's MCP server because nobody was there to
// answer.
func applyMain(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
	var at string
	var dryRun, sealed, assert bool
	for i := 0; i < len(args); i++ {
		a := args[i]
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
		case a == "--host":
			at = "host" // shorthand for --at host
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
	if sealed {
		return applySealed(out, errw, color)
	}

	switch notch {
	case config.ConfinementHost:
		return applyHost(out, errw, color, assert && !dryRun, stdin)
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
	pr := richtext.Printer{W: out, Color: color}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "yolo apply --host: cannot resolve your home: %v\n", err)
		return 1
	}
	entries, err := config.LoadPacks(nil)
	if err != nil {
		fmt.Fprintf(errw, "yolo apply --host: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		// "Nothing to apply" is not "nothing to clean up" — emptying `packs` is the MOST
		// complete drop there is, and returning here left every pack's delivered output in the
		// home with nothing that would ever ask about it again. So the retire pass still runs,
		// against an empty configured set. Nothing else does: with no pack to render there is
		// no surface, no briefing, and no candidate whose destination to visit, which is why
		// this is a narrow second call rather than a fall-through into the loop below.
		pr.Printf("[dim]No packs configured — nothing to apply to the host.[/dim]")
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
			home, stamp, write, nil)
		if src := applyHostSkills(pr, out, stdin, nil, packload.Embedded(), empty, empty, true,
			home, stamp, write, nil); src != 0 {
			rc = src
		}
		if prc := pruneDroppedPackOutput(pr, out, stdin, packload.Embedded(), empty,
			home, stamp, write,
			planOverlayKeyRetirement(pr, packload.Embedded(), empty, nil, home)); prc != 0 {
			rc = prc
		}
		return rc
	}

	// One archive generation per apply, so everything this run retires groups under one
	// directory and the user can undo a whole apply rather than hunting per-file.
	stamp := time.Now().UTC().Format("20060102-150405")

	posture := "observe (dry-run)"
	if write {
		posture = "assert (writing)"
	}
	pr.Printf("[bold]apply --host[/bold]  home [cyan]%s[/cyan]  posture [cyan]%s[/cyan]", home, posture)

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
	// kind exists to do (docs/design/pack-config-collaboration.md §6).
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
	loaded, destinations := packload.ResolveDestinations(loaded)
	for _, d := range destinations {
		if drc := reportInferredDestinations(pr, d); drc != 0 {
			rc = drc
		}
	}
	// REFUSE a doubly-declared config surface before writing anything into a real home
	// (docs/design/pack-config-collaboration.md Option 1 / R1). This is also R4: the double
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
		pr.Printf("[bold red]apply --host: refused — %d config surface(s) with more than one "+
			"owner. Nothing was written.[/bold red]", len(cols))
		return 1
	}
	// autonomy=false: host renders the GUARDED posture, so the owner set matches the
	// surfaces the render will actually produce (§4.2).
	overlays := packoverlay.Collect(loaded, false)
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

	// THE ONE-WAY DOOR. Before writing anything, ask an observe pass what an --assert would
	// destroy, and if the answer is "a value yolo has never asserted in this home", require a
	// confirmation. Maintainer ruling (2026-08-02): "let's just warn during the first apply
	// that things will be lost and wait for confirm" — warn-and-confirm, not warn-and-refuse.
	// See confirmHostLosses for the three properties that make it not-noise.
	if write && !confirmHostLosses(pr, out, stdin, loaded, home, overlays) {
		pr.Printf("[bold red]apply --host: not confirmed — nothing was written.[/bold red]")
		pr.Printf("[dim]Re-run and answer `y`, or declare the entries above in your config " +
			"(`mcp_servers`, reaching every agent) so nothing is lost.[/dim]")
		return 1
	}

	for _, p := range loaded {
		// Account for EVERY kind the pack declares, before rendering. Three outcomes, and
		// the invariant is that there is no fourth: refused by the census, honored-but-
		// unbuilt (named as such), or rendered below. A kind that produced no line at all
		// was the G1 bug — `skills`/`briefing` were honored by the FieldSet but rendered by
		// nothing, so they vanished silently, which is strictly worse than a loud refusal.
		deps := resolveHostDeps(p) // one probe per pack, consulted by the program case below
		for _, c := range p.Decl.Contributions() {
			switch {
			case !hostFields.Honors(c.Kind):
				pr.Printf("  [yellow]%-10s refused[/yellow] — %s", string(c.Kind), hostFields.Refuse(c.Kind))
			case isDepKind(c.Kind):
				// program AND requires: resolved dep state, not a static "confirm-gated"
				// line — which bin, present or missing, and the install command
				// (pack-host-management-plan.md Phase 8). Running it is still Phase 4.3's.
				// `requires` shares this path because below the jail notch the two kinds ask
				// the host the same question; the line names which kind asked.
				for _, l := range deps.lines(c) {
					pr.Printf("%s", l)
				}
			case c.Kind == packdecl.KindAutonomy:
				// Rendered, but INVISIBLY: an autonomy posture folds into the managed layer
				// of a surface the same pack owns, so it shows up as that surface's line and
				// never as its own. Say which posture won, because "did my jail-bypass keys
				// reach my real home?" is the single most consequential question this
				// command answers (env-manager Phase 9).
				pr.Printf("  [cyan]autonomy[/cyan]   guarded posture — permission prompts " +
					"stay ON; folded into this pack's own config surfaces below")
			case c.Kind == packdecl.KindSkills, c.Kind == packdecl.KindBriefing,
				c.Kind == packdecl.KindFiles:
				// All three render below with their own per-entry lines (applyHostSkills,
				// applyHostBriefings, applyHostFiles), so a summary line here would just be
				// noise. `briefing` and `skills` render once for the whole pack SET rather than
				// per pack (each destination's content is the union of every contributor's), so
				// their lines come after the loop.
			default:
				if why, unbuilt := render.HostUnimplemented(c.Kind); unbuilt {
					pr.Printf("  [yellow]%-10s refused[/yellow] — %s", string(c.Kind), why)
				}
			}
		}
		if frc := applyHostFiles(pr, errw, p, home, stamp, write); frc != 0 {
			rc = frc
		}
		results, rerr := entrypoint.RenderHostPack(p, home, !write, overlays)
		if rerr != nil {
			fmt.Fprintf(errw, "yolo apply --host: %s: %v\n", p.Name, rerr)
			rc = 1
			continue
		}
		for _, r := range results {
			pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
			// Which packs contributed config-overlay keys to this surface (ruling R3). An
			// overlay folds BELOW the owner's managed layer, so it leaves no trace in the
			// resulting file — without this line the only answer to "which pack set that
			// key?" is a sidecar the host render does not even write.
			if len(r.Overlays) > 0 {
				pr.Printf("    [magenta]config-overlay keys from: %s[/magenta] [dim](below this "+
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
				pr.Printf("    [bold yellow]⚠ %s your existing entry: %s[/bold yellow] [dim]"+
					"(yolo owns this table; declare the entry under `mcp_servers` to keep it)[/dim]",
					verb, strings.Join(r.EntryLosses, ", "))
			}
			// The ${workspace}-keyed keys this render DROPPED, by name. A pruned key is a
			// declaration the pack made that the host notch chose not to honor, so it gets a
			// line for the same reason a refusal does — the surface rendering is not a licence
			// for part of it to vanish quietly.
			if len(r.Pruned) > 0 {
				pr.Printf("    [dim]skipped ${workspace}-keyed (no host referent): %s[/dim]",
					strings.Join(r.Pruned, ", "))
			}
			// What the canonical re-emit costs beyond values — a TOML file's comments. Not an
			// overwrite and not an entry loss: nothing the user CONFIGURED changes, so it does
			// not belong in either ⚠ above. It is still a loss they should see before the
			// write, which is why it has a line of its own (comment preservation is BACKLOG
			// E4, tracked and deliberately unbuilt).
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
		home, stamp, write, reloadPacks); src != 0 {
		rc = src
	}
	if brc := applyHostBriefings(pr, out, stdin, loaded, candidates, active, resolvedAll,
		home, stamp, write, reloadPacks); brc != 0 {
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

	if !write {
		pr.Printf("[dim]observe only — nothing written. Re-run with --assert to apply.[/dim]")
	}
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
//   - ONLY WHEN SOMETHING IS ACTUALLY LOST. Gated on FirstApply && Overwrites — a clean home,
//     or any home yolo has asserted before, prompts not at all. A confirmation that fires on
//     every run trains people to hit `y` without reading, which is worse than no gate.
//   - OBSERVE NEVER REACHES HERE (the caller checks `write`). A dry-run writes nothing, so
//     there is nothing to confirm; it just reports the same collisions as ⚠ lines, which is
//     how the user gets the information BEFORE the prompt ever appears.
//   - FAIL-CLOSED on stdin. promptYesNo reads a nil or EOF stdin as NO (pack.go's contract),
//     so a CI or scripted `apply --host --assert` aborts rather than silently destroying a
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
	pr.Printf("[dim]yolo regenerates the keys it manages wholesale, so anything above that " +
		"is not in your config is dropped. To KEEP an entry, declare it (an MCP server goes " +
		"under `mcp_servers`, reaching every agent) and re-run.[/dim]")
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
//   - ORPHANED. The pack carries content for a kind that NO pack in `packs` names a destination
//     for, so that content reaches nothing.
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
func reportInferredDestinations(pr richtext.Printer, d packload.Destinations) int {
	byKind := map[packdecl.Kind][]string{}
	var order []packdecl.Kind
	for _, c := range d.Inferred {
		if _, seen := byKind[c.Kind]; !seen {
			order = append(order, c.Kind)
		}
		byKind[c.Kind] = append(byKind[c.Kind], c.Into)
	}
	for _, kind := range order {
		pr.Printf("  [dim]%-10s %s declares no destination — merging into the ones your packs "+
			"name: %s[/dim]", string(kind), d.Pack.Name, strings.Join(byKind[kind], ", "))
	}
	if len(d.Orphaned) == 0 {
		return 0
	}
	inert := len(d.Pack.Decl.Contributions()) == 0
	for _, kind := range d.Orphaned {
		if !inert {
			pr.Printf("  [yellow]%-10s no effect[/yellow] — %s carries %s content, and no pack "+
				"in `packs` names a %s destination [dim](select the agent pack that owns one, "+
				"or declare `into` in %s's pack.json)[/dim]",
				string(kind), d.Pack.Name, string(kind), string(kind), d.Pack.Name)
			continue
		}
		pr.Printf("  [yellow]%-10s refused[/yellow] — %s ships %s but no pack in `packs` names a "+
			"destination for it, so this pack renders NOTHING. Select the agent pack that owns "+
			"the destination, or declare `into` in %s's pack.json.",
			string(kind), d.Pack.Name, string(kind), d.Pack.Name)
	}
	if inert {
		return 1
	}
	return 0
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
// fixed-output derivation. It means no input that NOTHING names. The two undeclared
// inputs today are:
//   - yolo-jail.local.jsonc: auto-merged, gitignored, needs no include entry.
//   - an outstanding capture overlay: in-jail edits that outrank every declared layer,
//     yet nothing declares them (they are a staging area to promote, §3.3).
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
  yolo apply --host         shorthand for --at host: render your config into your real home
                            (default OBSERVE/dry-run — prints what would change, writes nothing)
  yolo apply --host --assert  actually write: regenerate only the keys yolo manages (pure
                            rmw), leaving your own keys; non-config kinds refused by name
  yolo apply --sealed       refuse if any UNDECLARED input shaped the environment
                            (yolo-jail.local.jsonc, an outstanding capture overlay)
  yolo apply --dry-run      show what would change, write nothing

apply splits "make it so" from "run something in it": ` + "`yolo -- <cmd>`" + ` is
"apply, then exec." The host notch has no exec half — there apply IS the whole feature
(rendering your agent config into your real home). See ` + "`yolo describe`" + ` for what
the current description resolves to.`
