package cli

// applyhostprune.go retires the `skills` and `files` output of a pack that LEFT the config.
//
// This is the other axis from the per-pack retire in applyhostskills.go/applyhostfiles.go.
// That one asks "what did this pack write last time that it no longer ships?" and is keyed on
// the pack being ITERATED, so it can only see a pack that CHANGED. A pack dropped from config
// is absent from `entries` entirely — nothing ever asks about it, and its skills stayed
// loadable and its files stayed on disk forever, with the ownership record still naming a pack
// that no longer exists (docs/plans/host-pack-drop-cleanup.md).
//
// Two decisions carry the weight here, and they are the maintainer's rulings rather than
// implementation taste:
//
//   - R1: removing a dropped pack's output from a real home is CONFIRMED, once, and only when
//     something would actually be removed. The user's mental model is "I edited a config
//     list"; the consequence is "files left my real home". Those are far enough apart that the
//     action has to be named at the moment it happens.
//   - R2: retirement ARCHIVES (hostskills.Archive), never deletes. The authority to remove
//     comes from a record that can go stale, so being wrong must cost the user one `mv` back.
//
// Briefings deliberately do NOT ride this gate, and the REASON changed with §6a while the
// answer did not. Under the old delimited-block mechanism, removing a block restored the
// file's own bytes (ruling R4) — nothing to confirm, nothing to archive. Now a briefing
// destination is a file yolo composed WHOLESALE, so its retirement does archive (see
// entrypoint.PruneHostBriefings) but still needs no confirmation: every byte being moved is a
// byte yolo wrote. What this gate protects is user content, and there is none there.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// droppedOutput is one delivered path whose owning pack is gone from the config.
type droppedOutput struct {
	// Pack is the pack the ownership evidence names — the archive's attribution, and what
	// the report groups by. It may name a pack that no longer exists anywhere on the
	// machine, which is exactly why the evidence has to be self-contained.
	Pack string
	// Dest is the absolute path in the user's home.
	Dest string
	// Namespaced marks a whole tier-A subtree (found by its plugin marker) rather than a
	// single recorded entry. Worth distinguishing in the report: retiring it removes every
	// skill inside it at once.
	Namespaced bool
}

// pruneDroppedPackOutput archives the `skills`/`files` output of every pack that is no longer
// configured, behind one confirmation, and returns an rc contribution.
//
// `configured` is the set of pack names the config NAMES, not the set that RESOLVED this run —
// and the difference is the whole reason this takes its own set instead of reusing the
// briefing prune's `active`. A fetched pack whose remote is unreachable resolves to nothing,
// so it is absent from `active`, so to a prune it looks dropped. For a briefing that mistake
// self-heals: the block is re-rendered from prose that lives IN the pack the moment the remote
// is reachable again. An archived skills tree does not come back until the user goes digging
// in the state dir. Same evidence, different cost of being wrong, so a different threshold.
//
// `keys` is the CONFIG-OVERLAY half (ruling R3's first sentence), planned by the caller and
// carried through this function so it rides ONE prompt with the paths instead of adding a
// second. Both halves are the consequence of the same user action — "I removed a pack from my
// config" — so two [y/N]s for it would be the prompt-fatigue confirmDroppedPackRetire's own
// docstring warns about. See applyhostoverlaykeys.go for why a key is not archived.
func pruneDroppedPackOutput(pr richtext.Printer, out io.Writer, stdin io.Reader,
	candidates []*packload.Pack, configured map[string]bool, home, stamp string, write bool,
	keys overlayKeyRetirement) int {
	// Same guard as PruneHostBriefings, and here it protects the user's actual files: a bug
	// that made the set empty would read as "every pack is gone" and archive every skill yolo
	// ever delivered. Refusing an unknown set is the only reading that cannot do that.
	if configured == nil {
		pr.Printf("  [red]retire     refused[/red] — refusing to retire host output with an " +
			"unknown configured-pack set")
		return 1
	}
	rc := 0
	if keys.Failed {
		rc = 1
	}
	manPath := hostSkillsManifestPath()
	man, err := hostskills.LoadManifest(manPath)
	var present, vanished []droppedOutput
	if err != nil {
		// A record yolo cannot read proves nothing, and the tier-A scan below leans on it too
		// (an unrecorded plugin dir is attributed by its own manifest, which for a WRAPPED
		// plugin names the plugin rather than the pack). So a corrupt record does not merely
		// find fewer orphans — it would find wrong ones. Report and retire no PATH.
		//
		// The scan is skipped rather than the function returning, so an unreadable skills
		// record does not also block the config-overlay half: that half reads the provenance
		// record, a different file with different failure modes, and coupling the two would
		// make one corrupt file freeze cleanup yolo can still do correctly.
		pr.Printf("  [yellow]⚠ retire: %v — nothing from a dropped pack is retired this "+
			"run[/yellow]", err)
	} else {
		present, vanished = droppedPackOrphans(man, candidates, configured, home)
	}
	for _, o := range vanished {
		// The record outlived the file: the user removed it themselves, or another tool did.
		// Not a loss and not a confirmation — just bookkeeping, so it is dim and unprompted.
		pr.Printf("  [dim]%-20s stale record dropped (the path is already gone)  %s[/dim]",
			o.Pack+"/retire", o.Dest)
	}
	if len(present) == 0 && len(keys.Orphans) == 0 {
		if write && len(vanished) > 0 {
			saveHostSkillsManifest(pr, man, manPath, forgetAll(man, vanished))
		}
		return rc
	}

	if !write {
		for _, o := range present {
			pr.Printf("  [yellow]%-20s would archive (pack no longer configured)[/yellow]  "+
				"[dim]%s%s[/dim]", o.Pack+"/retire", o.Dest, namespacedNote(o))
		}
		keys.observeLines(pr)
		pr.Printf("[dim]--assert will ask before retiring the entries above (paths move under "+
			"%s; a config key is removed in place).[/dim]",
			string(hostSkillsArchiveRoot()))
		return rc
	}

	if !confirmDroppedPackRetire(pr, out, stdin, present, keys.Orphans) {
		// Declining is a legitimate answer, not a failure: nothing the user asked for was
		// skipped — the files simply stay, and this run reports it the same way the next one
		// will. So the rc is unchanged, which also keeps the fail-closed nil-stdin path from
		// making every scripted `apply --host --assert` fail permanently after any drop, with
		// no non-interactive way to ever answer.
		//
		// PARTIAL APPLICATION IS NOT AN OPTION either: a decline leaves the paths AND the
		// keys, because the prompt asked about them as one action and answering it `n` about
		// half of it was never on offer.
		pr.Printf("[bold yellow]not retired — %d path(s) and %d config key(s) from dropped "+
			"pack(s) are still in your home.[/bold yellow]", len(present), len(keys.Orphans))
		pr.Printf("[dim]Re-run and answer `y`, put the pack back in `packs`, or remove them " +
			"yourself. Nothing was moved or removed.[/dim]")
		return rc
	}

	var forget []droppedOutput
	for _, o := range present {
		at, aerr := hostskills.Archive(hostSkillsArchiveRoot(), stamp, o.Pack, o.Dest)
		if aerr != nil {
			pr.Printf("  [red]%-20s retire failed[/red] — %v", o.Pack+"/retire", aerr)
			rc = 1
			continue
		}
		pr.Printf("  [yellow]%-20s archived (pack no longer configured)[/yellow]  [dim]%s%s "+
			"→ %s[/dim]", o.Pack+"/retire", o.Dest, namespacedNote(o), at)
		forget = append(forget, o)
	}
	if krc := keys.commit(pr); krc != 0 {
		rc = krc
	}
	// Forget only what actually moved, and only AFTER it moved: a record dropped for a path
	// still sitting in the home would make the next apply read that path as the user's own,
	// which is the one state from which yolo can never clean up after itself again.
	saveHostSkillsManifest(pr, man, manPath, forgetAll(man, append(forget, vanished...)))
	return rc
}

// namespacedNote flags a whole-subtree retirement in the report line.
func namespacedNote(o droppedOutput) string {
	if !o.Namespaced {
		return ""
	}
	return " (the pack's whole namespaced subtree)"
}

// forgetAll drops each entry from the record and reports whether anything changed, so the
// caller can skip a pointless rewrite of the file.
func forgetAll(man *hostskills.Manifest, gone []droppedOutput) bool {
	for _, o := range gone {
		man.Forget(o.Dest)
	}
	return len(gone) > 0
}

// saveHostSkillsManifest persists the record when it changed.
func saveHostSkillsManifest(pr richtext.Printer, man *hostskills.Manifest, path string, changed bool) {
	if !changed {
		return
	}
	if err := man.Save(path); err != nil {
		pr.Printf("  [yellow]⚠ retire: could not save the ownership record: %v[/yellow]", err)
		pr.Printf("  [dim]  (the path(s) were archived; the record still names them, so the " +
			"next apply will report them as already gone)[/dim]")
	}
}

// droppedPackOrphans finds every delivered path whose owning pack is not configured, split
// into paths that are still THERE (a confirmation, an archive) and records whose path has
// already vanished (bookkeeping only).
//
// Two sources of ownership evidence, in that priority order, because tier A and tier B record
// ownership in different places by design:
//
//   - the MANIFEST, which is authoritative: it holds an absolute dest and its owning pack, so
//     it answers this question without the dropped pack's manifest or source tree — which may
//     be gone from the machine entirely. This covers every tier-B skill and every `files`
//     entry, and it is why the scan does not need to know which KIND wrote a path.
//   - the tier-A PLUGIN MARKER, for a namespaced subtree, which deliverNamespaced records
//     nowhere else: inside its own subtree "is this mine?" is answered by the path, and that
//     stops working the moment the question becomes "whose was it?". Consulted only for a dir
//     the manifest does not already own, because for a WRAPPED plugin the manifest is right
//     and the marker names the PLUGIN rather than the pack that delivered it.
//
// The tier-A half needs a directory to look in, which it gets from the candidate packs' own
// `skills` destinations — the same "visit the destinations, including a departed pack's" trick
// PruneHostBriefings uses. The residual gap: a dropped pack whose `into` no OTHER candidate
// pack names has no discoverable subtree. That is narrow (the kind exists to merge into an
// agent's dir, and the agent pack names it) and the tier-B half is unaffected.
func droppedPackOrphans(man *hostskills.Manifest, candidates []*packload.Pack,
	configured map[string]bool, home string) (present, vanished []droppedOutput) {
	recorded := map[string]bool{}
	for dest, owner := range man.Entries {
		recorded[dest] = true
		if configured[owner] {
			continue
		}
		o := droppedOutput{Pack: owner, Dest: dest}
		if _, err := os.Lstat(dest); err != nil {
			vanished = append(vanished, o)
			continue
		}
		present = append(present, o)
	}
	for _, dir := range hostSkillsDirs(candidates, home) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a destination nothing has delivered to yet
		}
		for _, e := range entries {
			dest := filepath.Join(dir, e.Name())
			if recorded[dest] || !e.IsDir() {
				continue
			}
			owner, ok := hostskills.YoloPluginOwner(dest)
			if !ok || configured[owner] {
				continue
			}
			present = append(present, droppedOutput{Pack: owner, Dest: dest, Namespaced: true})
		}
	}
	byDest := func(s []droppedOutput) {
		sort.Slice(s, func(i, j int) bool { return s[i].Dest < s[j].Dest })
	}
	byDest(present)
	byDest(vanished)
	return present, vanished
}

// hostSkillsDirs is the deduplicated set of home-absolute skills destinations the given packs
// declare — the dirs a tier-A prune has to look in.
func hostSkillsDirs(packs []*packload.Pack, home string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindSkills || c.Into == "" {
				continue
			}
			dir := filepath.Join(home, filepath.FromSlash(c.Into))
			if seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

// confirmDroppedPackRetire is R1's gate, and by ruling R3 also the overlay-KEY gate: name
// every path and every key, say what becomes of each, and require an explicit yes. Returns
// true to retire.
//
// ONE PROMPT COVERING BOTH KINDS is the ruling, not a convenience. R3 says overlay keys ride
// "the same confirm" rather than a separate silent path, and the user action behind both halves
// is a single edit to `packs` — so splitting it into two [y/N]s would ask twice about one
// decision, which is how a confirmation stops being read.
//
// It shares confirmHostLosses' three properties, for the same reasons:
//
//   - ONLY WHEN SOMETHING IS ACTUALLY REMOVED. The caller reaches here only with at least one
//     path still on disk or one key still in a file; a stale record alone never prompts. A
//     confirmation that fires on every run trains people to answer it blind.
//   - OBSERVE NEVER REACHES HERE. A dry run writes nothing, so it reports the same entries as
//     `would archive` / `would remove key` lines instead — which is how the user learns about
//     them BEFORE any prompt exists.
//   - FAIL-CLOSED on stdin. promptYesNo reads a nil or EOF stdin as NO, so a CI or scripted
//     apply leaves the files alone rather than moving a user's skills with nobody watching.
func confirmDroppedPackRetire(pr richtext.Printer, out io.Writer, stdin io.Reader,
	present []droppedOutput, keys []entrypoint.HostOverlayOrphan) bool {
	pr.Printf("[bold yellow]⚠ These packs are no longer in your config, but their output is " +
		"still in your home:[/bold yellow]")
	var packs []string
	byPack := map[string][]string{}
	add := func(pack, line string) {
		if _, seen := byPack[pack]; !seen {
			packs = append(packs, pack)
		}
		byPack[pack] = append(byPack[pack], line)
	}
	for _, o := range present {
		add(o.Pack, o.Dest+namespacedNote(o))
	}
	for _, k := range keys {
		// Grouped under the SAME pack heading as that pack's paths: the user is deciding about
		// a pack, and a separate "keys" section would present one decision as two.
		add(k.Pack, fmt.Sprintf("%s → key %q in %s", k.Surface, k.Key, k.Path))
	}
	sort.Strings(packs)
	for _, pack := range packs {
		pr.Printf("  [cyan]%s[/cyan]", pack)
		for _, line := range byPack[pack] {
			pr.Printf("    [yellow]%s[/yellow]", line)
		}
	}
	pr.Printf("[dim]A skill left here stays loadable by your agent, so \"the pack is gone\" "+
		"never takes effect. Retiring MOVES each path under %s (reclaim it with `yolo prune`) "+
		"— nothing is deleted. A config KEY is removed from the file in place, since it is the "+
		"pack's own assertion and comes back if you put the pack back in `packs`; every other "+
		"key in that file is left alone. Declining leaves everything above exactly as it "+
		"is.[/dim]", string(hostSkillsArchiveRoot()))
	return promptYesNo(out, stdin, fmt.Sprintf(
		"  Retire the %d path(s) and %d config key(s) above? [y/N] ", len(present), len(keys)))
}
