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
	return applyMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

func applyMain(args []string, out, errw io.Writer, color bool) int {
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
		return applyHost(out, errw, color, assert && !dryRun)
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
func applyHost(out, errw io.Writer, color bool, write bool) int {
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
		pr.Printf("[dim]No packs configured — nothing to apply to the host.[/dim]")
		return 0
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
	// because "which briefing blocks are stale?" is only answerable once the whole active
	// set is known — a pack dropped from config never appears in `entries` at all.
	active := map[string]bool{}
	var loaded []*packload.Pack
	// Resolve the packs FIRST, before rendering any of them, because config-overlay is
	// cross-pack: an overlay in pack B targets a surface pack A owns, so the per-pack loop
	// below cannot discover it. Two passes over `entries` is the price of the one thing the
	// kind exists to do (docs/design/pack-config-collaboration.md §6).
	for _, e := range entries {
		p := packForCheckDeps(e) // same loader: embedded or local; git needs `pack install`
		if p == nil {
			pr.Printf("[dim]%s: not resolvable offline (fetched packs need `yolo pack install`) — skipped[/dim]", e.Name)
			continue
		}
		active[p.Name] = true
		loaded = append(loaded, p)
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
			case c.Kind == packdecl.KindProgram:
				// Resolved dep state, not a static "confirm-gated" line: which bin, present
				// or missing, and the install command for THIS host's package manager
				// (pack-host-management-plan.md Phase 8). Running it is still Phase 4.3's.
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
				// RenderHostBriefing, applyHostFiles), so a summary line here would just be
				// noise.
			default:
				if why, unbuilt := render.HostUnimplemented(c.Kind); unbuilt {
					pr.Printf("  [yellow]%-10s refused[/yellow] — %s", string(c.Kind), why)
				}
			}
		}
		if src := applyHostSkills(pr, errw, p, home, stamp, write); src != 0 {
			rc = src
		}
		if frc := applyHostFiles(pr, errw, p, home, stamp, write); frc != 0 {
			rc = frc
		}
		// The briefing's managed block. Failures here are reported and do not abort the
		// remaining packs: a refusal is usually one malformed file (an unterminated marker),
		// and stopping would leave the user with a partial apply and no report of the rest.
		bres, berr := entrypoint.RenderHostBriefing(p, home, !write)
		if berr != nil {
			pr.Printf("  [red]briefing   refused[/red] — %v", berr)
			rc = 1
		}
		for _, r := range bres {
			pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
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
		}
	}

	// Retire the briefing blocks of packs that are no longer active. Candidates are every
	// pack yolo SHIPS plus the active ones: a pack dropped from config is absent from
	// `entries`, so asking only the active packs where to look would leave its block in the
	// user's file forever, unattributed and unremovable. Guarded by a non-nil active set —
	// PruneHostBriefings refuses a nil one rather than reading it as "drop everything".
	if pres, perr := entrypoint.PruneHostBriefings(
		append(loaded, embeddedPacksForPrune()...), active, home, !write); perr != nil {
		pr.Printf("  [red]briefing prune refused[/red] — %v", perr)
		rc = 1
	} else {
		for _, r := range pres {
			pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
		}
	}

	if !write {
		pr.Printf("[dim]observe only — nothing written. Re-run with --assert to apply.[/dim]")
	}
	return rc
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
