package cli

// applyhostskills.go is the `apply --host` call site for the `skills` kind, which yolo now
// COMPOSES WHOLESALE at every notch (maintainer ruling 2026-08-04, roadmap.md §6a-2).
//
// It is the second host kind whose call site is PACK-SET-WIDE rather than per-pack, and for the
// same reason applyhostbriefings.go is: a destination's content is the union of every contributing
// pack's skills, so a per-pack pass would have to either leave an earlier pack's stale entry behind
// or refuse to overwrite it — the latter being §6a-5, where the local pack lost a flat-tier
// collision to a shared pack because the ownership record forbade any pack overwriting another's
// recorded name whatever the order.
//
// The order below is load-bearing, and it is the briefing kind's order because the lifecycle is
// the same one:
//
//  1. ADOPT — ask which entries at these destinations yolo cannot prove it composed, and CONFIRM.
//     The first apply that takes over a hand-written ~/.claude/skills/mine is a one-way door, so it
//     rides the same warn-and-confirm gate confirmHostLosses established, with the same
//     fail-closed-on-nil-stdin contract.
//  2. MIGRATE — MOVE those entries into the local pack's skills/, so each one still reaches every
//     agent. Archive is the fallback, never the first answer.
//  3. RENDER — compose and write, retiring this destination's own stale entries as it goes.
//  4. RETIRE — archive the composed output at a destination no active pack contributes to any
//     more, so dropping the last contributing pack does not leave an orphan.
//
// Steps 1 and 2 must not run in observe: a dry run writes nothing, so there is nothing to confirm,
// and the migration is reported as `would move` instead.
//
// The two host-notch decisions that predate composition and survive it:
//
//   - Built-ins are NOT written to a real home. yolo's own skills (jail-startup,
//     diagnosing-the-jail, configuring-the-jail) are about being inside a jail; on the host they
//     are noise at best and misleading at worst. The jail still stages them as its layer 1.
//   - The user's OWN skills tree is not a source. In a jail, PrepareSkills layers the host's
//     ~/.<agent>/skills in last so a local skill outranks a pack's. At the host that tree IS the
//     destination — and since §6a-2 it is not a layer at all: it MOVES into the local pack, which
//     is composed last and therefore holds exactly the precedence that layer used to.

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostSkillsManifestPath is where the pre-composition PER-ENTRY provenance record lives. Under the
// state dir (not the config dir) because it is yolo's own bookkeeping about what it did, not
// something a user edits or commits.
//
// Still read, and still written by the `files` kind which shares it. The skills composition reads
// it for one purpose only: a path this record names is yolo's own output from BEFORE composition,
// so the first apply after an upgrade must not offer to migrate it into the user's local pack as
// though they had written it.
func hostSkillsManifestPath() string {
	return filepath.Join(paths.GlobalStorage(), "host-skills-manifest.json")
}

// hostComposedSkillsManifestPath is where the COMPOSITION's ownership record lives — its OWN file,
// beside the per-entry one and the briefing one.
//
// Separate, not shared, and the reason is a defect the shared version produced immediately in the
// sibling kind (§6a-6 defect 1): every composed path's owner is a pseudo-owner
// (hostskills.ComposedOwner) because the content belongs to the pack SET, while droppedPackOrphans
// reads every owner in the per-entry record as a PACK NAME and archives the paths of any owner
// absent from `packs`. So every composed skill would be retired as a dropped pack's output on the
// very next apply. Three questions, three key spaces, three files.
func hostComposedSkillsManifestPath(home string) string {
	return filepath.Join(paths.GlobalStorageUnder(home), "host-composed-skills.json")
}

// hostArchiveRoot is where a host render moves a copy it is about to replace or retire, for
// one BUCKET. Under the state dir so `yolo prune` can find and reclaim it.
//
// ONE BUCKET PER KIND, which is what V3 fixed: every host kind used to share the literal
// `archive/skills`, so a `files` copy — pi's models.json, a theme, a script the pack owns —
// was archived into a directory named for skills. The report prints the absolute path, so the
// copy was never unreachable; it was unfindable by the one route a user actually takes, which
// is looking under the state dir for the thing they just lost. A bucket that lies about its
// contents is worse than no bucket, because the user stops looking.
//
// THE BUCKET NAME IS NOT ALWAYS A KIND, and `retired` is the case that proves it: the
// dropped-pack retire is kind-agnostic BY DESIGN (droppedPackOrphans answers "whose was this
// path?" from an ownership record keyed on the path alone, and its docstring says in as many
// words that it does not need to know which kind wrote it). Forcing a kind onto it would mean
// inventing an answer the pass deliberately does not have. So the parameter is the bucket, and
// the callers that HAVE a kind pass its name.
//
// MIGRATION — nothing moves. `skills` keeps the historical path, so every copy already under
// `archive/skills` (whatever kind put it there) stays exactly where it is and stays reachable.
// The other buckets are new directories, and prune.PruneHostArchive sweeps every bucket under
// `archive/` rather than the one hardcoded name, so neither the legacy generations nor the new
// ones are orphaned. Moving old copies to "tidy" them would be the one thing this archive
// exists to prevent: yolo relocating a file the user was told the location of.
func hostArchiveRoot(bucket string) hostskills.ArchiveRoot {
	return hostskills.ArchiveRoot(filepath.Join(paths.GlobalStorage(), "archive", bucket))
}

// archiveBucketRetired is the dropped-pack retire's bucket. Named for the OPERATION rather
// than a kind because that pass retires skills and files through one path-keyed record; see
// hostArchiveRoot.
const archiveBucketRetired = "retired"

// localPackSkillsPath is where an adopted entry MOVES to: the conventional local pack's own
// skills/ dir.
//
// Derived from the home this apply is rendering into, not from paths.LocalPackDir(), and that
// distinction is load-bearing for the same reason localPackBriefingPath states it:
// paths.LocalPackDir() reads $HOME, so a test (or any caller rendering into a home it was handed)
// would migrate the user's skills into their REAL config dir.
func localPackSkillsPath(home string) string {
	rel, err := filepath.Rel(paths.Home(), paths.LocalPackDir())
	if err != nil || rel == "" || rel == "." {
		return ""
	}
	return filepath.Join(home, rel, "skills")
}

// applyHostSkills runs the four steps for the whole pack set and returns an rc contribution.
//
// The parameters mirror applyHostBriefings exactly, because the two kinds now share a lifecycle:
// `loaded` is the ACTIVE set (post-ResolveDestinations, so a zero-ceremony pack already declares
// its destinations); `candidates` adds every pack yolo ships, because a dropped pack's destination
// must still be visited; `active` names the packs whose destinations are legitimate; `complete`
// says every pack the config NAMES resolved this run.
//
// `reload` re-resolves the pack set, and is called ONCE, after a confirmed migration. The
// migration creates the conventional local pack, which is included by convention rather than by
// config — so the set resolved before it ran cannot contain it, and the render would otherwise
// drop the user's just-migrated skills for exactly one apply (§6a-6 defect 2, found in the sibling
// kind by asserting idempotency). nil means "no reload available", which is correct for the
// no-packs-configured caller and fails safe everywhere else.
func applyHostSkills(pr richtext.Printer, out io.Writer, stdin io.Reader,
	loaded, candidates []*packload.Pack, active, configured map[string]bool, complete bool,
	home, stamp string, write bool, reload func() []*packload.Pack) int {
	composedPath := hostComposedSkillsManifestPath(home)
	composed, err := hostskills.LoadManifest(composedPath)
	if err != nil {
		// A record yolo cannot read proves nothing, and here that fails CLOSED in the useful
		// direction: every existing entry reads as the user's, so nothing is regenerated without a
		// confirmation. Report it — a silent degradation would make the adoption prompt reappear on
		// a home the user already opted in.
		pr.Printf("  [yellow]⚠ skills: %v — treating every existing entry as yours[/yellow]", err)
	}
	legacy, lerr := hostskills.LoadManifest(hostSkillsManifestPath())
	if lerr != nil {
		// Worse than the composed record failing, and worth its own line: without the legacy
		// record, skills a PREVIOUS yolo delivered read as the user's own and the migration would
		// offer to move yolo's output into the user's local pack.
		pr.Printf("  [yellow]⚠ skills: %v — skills a previous apply delivered may be offered "+
			"for migration[/yellow]", lerr)
	}
	req := hostskills.ComposeRequest{
		Composed:        composed,
		Legacy:          legacy,
		ArchiveRoot:     hostArchiveRoot(string(packdecl.KindSkills)),
		Stamp:           stamp,
		LocalPackSkills: localPackSkillsPath(home),
		PackSetComplete: complete,
		// `configured`, not `active`: the boundary this draws is against ruling R1's confirmed
		// retire, which itself keys on the CONFIGURED set so an unreachable fetched pack does not
		// read as dropped. Passing `active` here would hand a merely-unreachable pack's skills to
		// the silent pass, which is the offline-apply data loss both keys exist to prevent.
		Configured: configured,
	}

	rc := 0
	dests := hostskills.ComposeHostSkills(loaded, home)
	reportSkillDestinations(pr, dests)
	// THE S1 REFUSAL, and it comes BEFORE the adoption gate rather than after. Two packs both
	// claiming one skill NAME at an unnamespaced destination is fatal, so nothing this apply
	// would do to the user's home may happen first — least of all the adoption prompt, which is a
	// ONE-WAY DOOR: answering `y` to a migration and then being told the composition is refused
	// would move the user's skills for a render that never ran.
	//
	// Printed here rather than left to RenderHostSkills' error (which still refuses, so the
	// structural guarantee does not depend on this call site) because the message is the feature:
	// it names both packs, both source paths and both remedies, and the render's error is a
	// single string a caller might collapse into one line.
	if reportSkillCollisions(pr, dests) {
		return 1
	}
	adoptions, foreignPlugins := hostskills.Adoptions(dests, req)
	for _, r := range foreignPlugins {
		// A plugin the user authored is left alone at every posture — never adopted, never
		// composed over — so it is reported once here rather than inside the render loop, where a
		// reader would take it for an entry the composition considered and declined.
		printSkillResult(pr, r)
	}
	if len(adoptions) > 0 {
		if !write {
			// OBSERVE reports the adoption and the migration WITHOUT prompting — which is how the
			// user learns what the write would take over before any prompt exists.
			reportSkillAdoptions(pr, adoptions, req.LocalPackSkills)
			mres, _ := hostskills.MigrateHostSkills(adoptions, req, true)
			for _, r := range mres {
				printSkillResult(pr, r)
			}
		} else if !confirmSkillAdoption(pr, out, stdin, adoptions, req.LocalPackSkills) {
			// Declining is a legitimate answer and leaves the destinations alone — including the
			// render, since composing over skills the user just declined to migrate is the data
			// loss the gate exists to prevent. The rc is unchanged for confirmDroppedPackRetire's
			// reason: nothing the user asked for failed, and a permanent non-zero would make every
			// scripted apply after any hand-edit look broken.
			pr.Printf("[bold yellow]not adopted — %d skill(s) in your agent dirs are still "+
				"yours, and no skills destination was composed.[/bold yellow]", len(adoptions))
			pr.Printf("[dim]Re-run and answer `y`, or move them into %s yourself (yolo composes "+
				"them back into every destination from there). Nothing was moved or "+
				"written.[/dim]", req.LocalPackSkills)
			return rc
		} else {
			mres, merr := hostskills.MigrateHostSkills(adoptions, req, false)
			for _, r := range mres {
				printSkillResult(pr, r)
			}
			if merr != nil {
				pr.Printf("  [red]skills     migrate failed[/red] — %v", merr)
				return 1
			}
			warnSkillRenames(pr, mres, req.LocalPackSkills)
			// The migration just created the local pack, so re-resolve before composing (see
			// `reload`). Only on the CONFIRMED path: the observe and decline branches wrote
			// nothing, so there is no new pack to find.
			if reload != nil {
				if fresh := reload(); len(fresh) > 0 {
					loaded = fresh
					candidates = append(fresh, embeddedPacksForPrune()...)
					for _, p := range fresh {
						// BOTH sets: the local pack the migration just created is now active AND
						// configured (by convention rather than by a config line, which is what
						// makes it invisible to a set built before it existed). Leaving it out of
						// `configured` would hand its freshly-migrated skills to R1's dropped-pack
						// prompt on this very run.
						active[p.Name], configured[p.Name] = true, true
					}
					dests = hostskills.ComposeHostSkills(loaded, home)
					// RE-CHECKED, because the pack set just changed: the migration created the
					// local pack, whose freshly-adopted skill can share a name with a shared
					// pack's. Refusing here leaves the migrated content where it is — in the local
					// pack, reachable and named in the message — rather than composed over.
					if reportSkillCollisions(pr, dests) {
						return 1
					}
				}
			}
		}
	}

	sres, serr := hostskills.RenderHostSkills(dests, req, !write)
	for _, r := range sres {
		printSkillResult(pr, r)
		if r.Action == hostskills.ActionRefused {
			rc = 1
		}
	}
	if serr != nil {
		pr.Printf("  [red]skills     failed[/red] — %v", serr)
		rc = 1
	}

	// Retire the composed output at an ORPHANED destination: one yolo composed into that no active
	// pack contributes skills to any more. Unconfirmed, unlike the dropped-pack retire in
	// applyhostprune.go, because every byte being moved is a byte yolo composed — the same
	// asymmetry PruneHostBriefings carries, and the user's own skills are not here to be moved
	// (they are in the local pack, which does not stop existing when a pack is dropped).
	pres, perr := hostskills.PruneHostSkills(candidates, active, home, req, !write)
	for _, r := range pres {
		printSkillResult(pr, r)
	}
	if perr != nil {
		pr.Printf("  [red]skills prune refused[/red] — %v", perr)
		rc = 1
	}

	// Persist the record only after a real write. Saving in observe posture would record
	// compositions that never happened, manufacturing exactly the stale record the adoption gate is
	// built to survive.
	if write {
		if err := composed.Save(composedPath); err != nil {
			pr.Printf("  [yellow]⚠ skills: could not save the ownership record: %v[/yellow]", err)
			pr.Printf("  [dim]  (the skills were written; the next apply will treat them as " +
				"yours and ask before regenerating)[/dim]")
		}
	}
	return rc
}

// reportSkillAdoptions names every entry about to become yolo-owned and what becomes of it. Shared
// by the observe path and the prompt so the preview and the confirmation say the same thing.
func reportSkillAdoptions(pr richtext.Printer, adoptions []hostskills.Adoption, localPack string) {
	pr.Printf("[bold yellow]⚠ yolo COMPOSES these skills directories wholesale, and they "+
		"currently hold %d skill(s) yolo did not write:[/bold yellow]", len(adoptions))
	for _, a := range adoptions {
		pr.Printf("  [cyan]%s[/cyan]", a.Path)
	}
	if localPack == "" {
		pr.Printf("[dim]Each one is ARCHIVED before its directory is regenerated — nothing is " +
			"deleted, but nothing composes it back either (no local pack location could be " +
			"resolved).[/dim]")
		return
	}
	pr.Printf("[dim]Each one MOVES into %s — the conventional local pack, which yolo composes "+
		"back into EVERY skills destination. So your skills keep reaching your agents, and they "+
		"reach ALL of them instead of drifting per agent. Adding a skill to an agent's dir by "+
		"hand will not survive the next apply — add it to the local pack instead.[/dim]", localPack)
}

// warnSkillRenames is the union caveat's LOUD half, printed once at the migration and never again.
//
// The migration is the only moment the user has the context to fix a conflict — they know which
// agent held which version — so a warning on every later apply would train them to ignore it. Two
// skills sharing a name with DIFFERENT bodies is the only migration outcome yolo cannot resolve
// correctly on its own, so it is the only one that gets this.
func warnSkillRenames(pr richtext.Printer, results []hostskills.Result, localPack string) {
	var renamed []hostskills.Result
	for _, r := range results {
		if r.Action == hostskills.ActionRenamed {
			renamed = append(renamed, r)
		}
	}
	if len(renamed) == 0 {
		return
	}
	pr.Printf("[bold yellow]⚠ %d name conflict(s): two of your agents had DIFFERENT skills "+
		"under one name, so BOTH were kept.[/bold yellow]", len(renamed))
	for _, r := range renamed {
		pr.Printf("  [yellow]%s[/yellow] [dim]— %s[/dim]", r.Name, r.Detail)
	}
	pr.Printf("[dim]Both now compose into every destination under their distinct names. yolo "+
		"does not guess which one you meant: merge or delete one in %s if only one should "+
		"survive.[/dim]", localPack)
}

// confirmSkillAdoption is the one-way door for wholesale skills ownership. Returns true to proceed.
//
// It shares confirmHostLosses' three properties, and for the same reasons:
//
//   - ONLY WHEN SOMETHING IS ACTUALLY AT STAKE. The caller reaches here only with at least one
//     entry yolo cannot prove it composed — an empty destination, or one holding only yolo's own
//     output, never prompts. A confirmation that fires every run is one people learn to answer
//     blind.
//   - OBSERVE NEVER REACHES HERE. A dry run writes nothing, so it reports the same entries as
//     `would move` lines instead.
//   - FAIL-CLOSED on stdin. promptYesNo reads a nil or EOF stdin as NO, so a scripted
//     `apply --host --assert` aborts rather than silently moving a user's skills.
func confirmSkillAdoption(pr richtext.Printer, out io.Writer, stdin io.Reader,
	adoptions []hostskills.Adoption, localPack string) bool {
	reportSkillAdoptions(pr, adoptions, localPack)
	verb := "Move these skills into the local pack and let yolo compose"
	if localPack == "" {
		verb = "Archive these skills and let yolo compose"
	}
	return promptYesNo(out, stdin, "  "+verb+" these directories? [y/N] ")
}

// printSkillResult renders one entry's outcome, colored by whether it is a write, a hands-off, or
// a problem.
func printSkillResult(pr richtext.Printer, r hostskills.Result) {
	color := "yellow"
	switch r.Action {
	case hostskills.ActionWrote, hostskills.ActionWouldWrite:
		color = "cyan"
	case hostskills.ActionSkippedUser:
		color = "green"
	case hostskills.ActionMoved, hostskills.ActionWouldMove,
		hostskills.ActionUnioned, hostskills.ActionWouldUnion:
		color = "cyan"
	}
	pr.Printf("  ["+color+"]skills[/"+color+"]     %-24s %s  [dim]%s[/dim]",
		r.Name, r.Action, r.Detail)
}

// reportSkillCollisions prints the S1 refusal and reports whether the apply must stop.
//
// ONE LINE PER COLLISION, each carrying the whole remedy (Collision.Message), rather than a
// summary plus a pointer to docs: a fatal error the user cannot act on is worse than the silent
// loss it replaces, and this one WILL fire on a real case — a personal pack and a shipped pack
// both shipping `agent-standards` is a genuine ambiguity yolo has no business resolving.
func reportSkillCollisions(pr richtext.Printer, dests []hostskills.Destination) bool {
	cols := hostskills.Collisions(dests)
	if len(cols) == 0 {
		return false
	}
	pr.Printf("  [red]skills     refused[/red] — %d name collision(s); nothing was composed",
		len(cols))
	for _, c := range cols {
		pr.Printf("  [red]%s[/red]", c.Message())
	}
	return true
}

// reportSkillDestinations names each composed destination and its contributing packs, once per
// apply, before the per-entry lines.
//
// It exists because composition made the per-entry lines ambiguous in one specific way: an entry
// says which skill landed and where, but not which packs the DIRECTORY is now a function of — and
// "why did my hand-added skill disappear from ~/.codex/skills?" is answered by the destination
// being composed, not by any one entry's line.
func reportSkillDestinations(pr richtext.Printer, dests []hostskills.Destination) {
	for _, d := range dests {
		pr.Printf("  [dim]skills     %s composed from: %s[/dim]", d.Dir,
			strings.Join(d.Packs(), ", "))
	}
}
