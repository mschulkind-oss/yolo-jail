package cli

// applyhostbriefings.go is the `apply --host` call site for the `briefing` kind, which yolo now
// GENERATES WHOLESALE at every notch (maintainer ruling 2026-08-04, outstanding-work.md §6a).
//
// It is the one host kind whose call site is PACK-SET-WIDE rather than per-pack, and that is a
// consequence of the ruling rather than a style choice: a destination's content is the union of
// every contributing pack's prose, so a per-pack write would have to append (which is what made
// the old first apply duplicate prose — finding F3) or overwrite (which would silently drop
// every pack but the last).
//
// The order below is load-bearing:
//
//  1. ADOPT — ask which destinations hold prose yolo cannot prove it wrote, and CONFIRM. The
//     first apply that takes over a hand-written ~/.claude/CLAUDE.md is a one-way door, so it
//     rides the same warn-and-confirm gate confirmHostLosses established, with the same
//     fail-closed-on-nil-stdin contract.
//  2. MIGRATE — MOVE that prose into the local pack's AGENTS.md, so it still reaches every
//     agent. Archive is the fallback, never the first answer (§6a as amended).
//  3. RENDER — compose and write.
//  4. RETIRE — archive a destination yolo composed that no active pack contributes to any more,
//     so dropping the last contributing pack does not leave an orphan.
//
// Steps 1 and 2 must not run in observe: a dry run writes nothing, so there is nothing to
// confirm, and the migration is reported as `would move` instead.

import (
	"io"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostBriefingManifestPath is where the briefing ownership record lives — its OWN file, beside
// the skills/files one under the state dir.
//
// Separate, not shared, and the reason is a defect the shared version produced immediately: a
// composed destination's owner is a pseudo-owner (entrypoint's hostBriefingOwner) because the
// file belongs to the pack SET, while droppedPackOrphans reads every owner in the skills record
// as a PACK NAME and archives the paths of any owner absent from `packs`. So every briefing yolo
// composed was retired as a dropped pack's output on the very next apply. Two questions, two key
// spaces, two files.
func hostBriefingManifestPath(home string) string {
	return filepath.Join(paths.GlobalStorageUnder(home), "host-briefing-manifest.json")
}

// localPackBriefingPath is where an adopted destination's prose MOVES to: the conventional local
// pack's own AGENTS.md.
//
// Derived from the home this apply is rendering into, not from paths.LocalPackDir(), and that
// distinction is load-bearing: paths.LocalPackDir() reads $HOME, so a test (or any caller
// rendering into a home it was handed) would migrate the user's prose into their REAL config
// dir. GlobalStorageUnder exists for exactly this reason and this follows it.
func localPackBriefingPath(home string) string {
	// The same "beside config.jsonc" convention paths.LocalPackDir encodes, resolved under an
	// explicit home. Spelled through paths' own constants so the two cannot drift.
	rel, err := filepath.Rel(paths.Home(), paths.LocalPackDir())
	if err != nil || rel == "" || rel == "." {
		return ""
	}
	return filepath.Join(home, rel, "AGENTS.md")
}

// applyHostBriefings runs the four steps for the whole pack set and returns an rc contribution.
//
// `loaded` is the ACTIVE set (post-ResolveDestinations, so a zero-ceremony pack already declares
// its destinations); `candidates` adds every pack yolo ships, because a dropped pack's
// destination must still be visited. `active` names the packs whose destinations are legitimate,
// and `complete` says every pack the config NAMES resolved this run — without which the retire
// pass would read an unreachable fetched pack as a dropped one (see HostBriefingRequest).
//
// `reload` re-resolves the pack set, and is called ONCE, after a confirmed migration. The
// migration creates the conventional local pack, which is included by convention rather than by
// config — so the set resolved before it ran cannot contain it, and the render would otherwise
// drop the user's just-migrated prose for exactly one apply. nil means "no reload available",
// which is correct for the no-packs-configured caller and fails safe everywhere else (the
// pre-migration set is still rendered).
func applyHostBriefings(pr richtext.Printer, out io.Writer, stdin io.Reader,
	loaded, candidates []*packload.Pack, active map[string]bool, complete bool,
	home, stamp string, write bool, reload func() []*packload.Pack) int {
	manPath := hostBriefingManifestPath(home)
	man, err := hostskills.LoadManifest(manPath)
	if err != nil {
		// A record yolo cannot read proves nothing, and here that fails CLOSED in the useful
		// direction: every existing destination reads as the user's, so nothing is regenerated
		// without a confirmation. Report it — a silent degradation would make the adoption
		// prompt reappear on a home the user already opted in.
		pr.Printf("  [yellow]⚠ briefing: %v — treating every existing briefing as yours[/yellow]", err)
	}
	req := entrypoint.HostBriefingRequest{
		Manifest: man,
		// The kind's own bucket (V3) — see hostArchiveRoot. A retired briefing used to be
		// archived under `archive/skills`, which is a directory naming a different kind.
		ArchiveRoot:     hostArchiveRoot(string(packdecl.KindBriefing)),
		Stamp:           stamp,
		LocalPackAGENTS: localPackBriefingPath(home),
		PackSetComplete: complete,
	}

	rc := 0
	adoptions := entrypoint.HostBriefingAdoptions(loaded, home, man)
	if len(adoptions) > 0 {
		if !write {
			// OBSERVE reports the adoption and the migration WITHOUT prompting — which is how
			// the user learns what the write would take over before any prompt exists.
			reportBriefingAdoptions(pr, adoptions, req.LocalPackAGENTS)
			mres, _ := entrypoint.MigrateHostBriefings(adoptions, req, true)
			for _, r := range mres {
				pr.Printf("  [yellow]%-20s %s[/yellow]  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
			}
		} else if !confirmBriefingAdoption(pr, out, stdin, adoptions, req.LocalPackAGENTS) {
			// Declining is a legitimate answer and leaves the destinations alone — including
			// the render, since composing over prose the user just declined to migrate is the
			// data loss the gate exists to prevent. The rc is unchanged for the reason
			// confirmDroppedPackRetire's decline path gives: nothing the user asked for
			// failed, and a permanent non-zero would make every scripted apply after any
			// hand-edit look broken.
			pr.Printf("[bold yellow]not adopted — %d briefing destination(s) still hold your "+
				"own prose, and none was regenerated.[/bold yellow]", len(adoptions))
			pr.Printf("[dim]Re-run and answer `y`, or move the prose into %s yourself (yolo "+
				"composes it back into every destination from there). Nothing was moved or "+
				"written.[/dim]", req.LocalPackAGENTS)
			return rc
		} else {
			mres, merr := entrypoint.MigrateHostBriefings(adoptions, req, false)
			for _, r := range mres {
				pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
			}
			if merr != nil {
				pr.Printf("  [red]briefing   migrate failed[/red] — %v", merr)
				return 1
			}
			// The migration just created the local pack, so re-resolve before composing (see
			// `reload`). Only on the CONFIRMED path: the observe and decline branches wrote
			// nothing, so there is no new pack to find.
			if reload != nil {
				if fresh := reload(); len(fresh) > 0 {
					loaded = fresh
					candidates = append(fresh, embeddedPacksForPrune()...)
					for _, p := range fresh {
						active[p.Name] = true
					}
				}
			}
		}
	}

	bres, berr := entrypoint.RenderHostBriefings(loaded, home, req, !write)
	for _, r := range bres {
		pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
	}
	if berr != nil {
		pr.Printf("  [red]briefing   refused[/red] — %v", berr)
		rc = 1
	}

	// Retire an ORPHANED destination: one yolo composed that no active pack contributes to any
	// more. Unconfirmed, unlike the skills/files retire, because every byte being moved is a
	// byte yolo wrote — there is no user content in a wholesale-composed file to ask about.
	pres, perr := entrypoint.PruneHostBriefings(candidates, active, home, req, !write)
	for _, r := range pres {
		pr.Printf("  [yellow]%-20s %s[/yellow]  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
	}
	if perr != nil {
		pr.Printf("  [red]briefing prune refused[/red] — %v", perr)
		rc = 1
	}

	// Persist the record only after a real write, for applyHostSkills' reason: saving in
	// observe would record compositions that never happened, manufacturing exactly the stale
	// record the adoption gate is built to survive.
	if write {
		if err := man.Save(manPath); err != nil {
			pr.Printf("  [yellow]⚠ briefing: could not save the ownership record: %v[/yellow]", err)
			pr.Printf("  [dim]  (the briefings were written; the next apply will treat them as " +
				"yours and ask before regenerating)[/dim]")
		}
	}
	return rc
}

// reportBriefingAdoptions names every destination about to become yolo-owned and what becomes of
// the prose in it. Shared by the observe path and the prompt so the preview and the confirmation
// say the same thing.
func reportBriefingAdoptions(pr richtext.Printer, adoptions []entrypoint.HostBriefingAdoption,
	localPack string) {
	pr.Printf("[bold yellow]⚠ yolo GENERATES these briefing files wholesale, and each one " +
		"currently holds prose yolo did not write:[/bold yellow]")
	for _, a := range adoptions {
		pr.Printf("  [cyan]%s[/cyan] [dim](will be composed from: %s)[/dim]",
			a.Path, joinPacks(a.Packs))
	}
	if localPack == "" {
		pr.Printf("[dim]Your prose is ARCHIVED before each file is regenerated — nothing is " +
			"deleted, but nothing composes it back either (no local pack location could be " +
			"resolved).[/dim]")
		return
	}
	pr.Printf("[dim]Your prose MOVES into %s — the conventional local pack, which yolo "+
		"composes back into EVERY briefing destination. So your instructions keep reaching "+
		"your agents; they just arrive through your packs from now on. Editing a destination "+
		"by hand will not survive the next apply — edit the local pack instead.[/dim]", localPack)
	if len(adoptions) > 1 {
		// THE UNION CAVEAT, warned about rather than resolved. Prose has no name to dedup on,
		// so several agents' briefings landing in one file is left for the user to edit — with
		// a provenance comment per section so they can tell which was which.
		pr.Printf("[yellow]  ⚠ %d destinations merge into that ONE file, each under a "+
			"`<!-- migrated from … -->` comment. If your agents shared rules you will have "+
			"near-duplicate sections — yolo does not guess which to drop.[/yellow]",
			len(adoptions))
	}
}

// confirmBriefingAdoption is the one-way door for wholesale briefing ownership. Returns true to
// proceed.
//
// It shares confirmHostLosses' three properties, and for the same reasons:
//
//   - ONLY WHEN SOMETHING IS ACTUALLY AT STAKE. The caller reaches here only with at least one
//     destination holding prose yolo cannot prove it wrote — an absent file, an identical one,
//     or one yolo composed before never prompts. A confirmation that fires every run is one
//     people learn to answer blind.
//   - OBSERVE NEVER REACHES HERE. A dry run writes nothing, so it reports the same destinations
//     as `would move` lines instead.
//   - FAIL-CLOSED on stdin. promptYesNo reads a nil or EOF stdin as NO, so a scripted
//     `apply --host --assert` aborts rather than silently taking ownership of the user's file.
func confirmBriefingAdoption(pr richtext.Printer, out io.Writer, stdin io.Reader,
	adoptions []entrypoint.HostBriefingAdoption, localPack string) bool {
	reportBriefingAdoptions(pr, adoptions, localPack)
	verb := "Move your prose into the local pack and let yolo generate"
	if localPack == "" {
		verb = "Archive your prose and let yolo generate"
	}
	return promptYesNo(out, stdin, "  "+verb+" these files? [y/N] ")
}

// joinPacks renders a destination's contributing packs for a report line.
func joinPacks(packs []string) string {
	if len(packs) == 0 {
		return "(none)"
	}
	out := packs[0]
	for _, p := range packs[1:] {
		out += ", " + p
	}
	return out
}
