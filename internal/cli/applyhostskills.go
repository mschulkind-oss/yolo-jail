package cli

// applyhostskills.go is the `apply --host` call site for skill delivery: it turns a pack's
// `skills` contributions into internal/hostskills requests, prints one line per entry, and
// persists the tier-B provenance record.
//
// The delivery policy lives in internal/hostskills (tiers, ownership, archiving); this file
// is only the wiring plus two decisions that belong to the HOST notch specifically:
//
//   - Built-ins are NOT written to a real home. yolo's own skills (jail-startup,
//     diagnosing-the-jail, configuring-the-jail) are about being inside a jail; on the host
//     they are noise at best and misleading at worst. The jail still stages them. They are
//     reported as skipped rather than silently omitted.
//   - The user's OWN skills tree is not a source. In a jail, PrepareSkills layers the host's
//     ~/.<agent>/skills in last so a local skill outranks a pack's. At the host that tree IS
//     the destination — reading it as a source and writing it back would be a copy onto
//     itself.

import (
	"io"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostSkillsManifestPath is where the tier-B provenance record lives. Under the state dir
// (not the config dir) because it is yolo's own bookkeeping about what it did, not something
// a user edits or commits.
func hostSkillsManifestPath() string {
	return filepath.Join(paths.GlobalStorage(), "host-skills-manifest.json")
}

// hostSkillsArchiveRoot is where retired skills are moved. Under the state dir so
// `yolo prune` can find and reclaim it.
func hostSkillsArchiveRoot() hostskills.ArchiveRoot {
	return hostskills.ArchiveRoot(filepath.Join(paths.GlobalStorage(), "archive", "skills"))
}

// applyHostSkills delivers every `skills` contribution of one pack into the real home and
// prints the outcome. write=false is the observe posture: it computes and prints exactly
// what assert would do, and writes nothing.
//
// stamp names the archive generation. Threaded in from the caller rather than computed here
// so one apply groups all its archived entries together, and so this stays testable without
// a clock.
func applyHostSkills(pr richtext.Printer, errw io.Writer, p *packload.Pack, home, stamp string, write bool) int {
	contributions := skillsContributions(p)
	if len(contributions) == 0 {
		return 0
	}

	manPath := hostSkillsManifestPath()
	man, err := hostskills.LoadManifest(manPath)
	if err != nil {
		// A corrupt record must not silently grant or silently deny. Report it and carry
		// on with an empty one, which fails CLOSED: every existing entry then looks like
		// the user's and is left alone.
		pr.Printf("  [yellow]⚠ skills: %v — treating every existing entry as yours[/yellow]", err)
	}

	// A pack may WRAP an existing agent plugin — a subtree carrying its own plugin manifest.
	// Those are delivered by copying the tree verbatim (the destination tool already loads
	// exactly that shape), so they are pulled out of the ordinary skill set below rather than
	// flattened into loose skills. Origin-gated: a fetched pack's plugin that runs code needs
	// the same approval any other host-power claim needs.
	plugins, pluginRefused := p.HonoredPlugins()
	for _, msg := range pluginRefused {
		pr.Printf("  [yellow]skills     refused[/yellow] — %s", msg)
	}
	pluginDirs := make([]string, 0, len(plugins))
	for _, pl := range plugins {
		pluginDirs = append(pluginDirs, pl.Dir)
	}

	rc := 0
	for _, c := range contributions {
		tier, ok := hostskills.ParseTier(c.Tier)
		if !ok {
			// Reachable only for a manifest that bypassed validation (an older pack, a
			// hand-edited staged tree). Say so rather than silently choosing.
			pr.Printf("  [yellow]skills     unknown tier %q — using flat (the safe tier)[/yellow]", c.Tier)
		}
		// The pack-relative SOURCE this contribution declares (`from`), or the conventional
		// skills/ dir when it declares none. Resolved through packload so the host notch and
		// the jail read one pack.json the same way — three hardcoded "skills" joins are what
		// made `from` a field yolo validated and ignored.
		src, prob := p.SkillsSourceDir(c)
		if prob != "" {
			// A declared source that is not there delivers nothing, so it is reported by name
			// rather than left to be inferred from an empty destination.
			pr.Printf("  [yellow]skills     refused[/yellow] — %s", prob)
			rc = 1
		}
		skillsDir := filepath.Join(home, c.Into)
		for _, pl := range plugins {
			results, derr := hostskills.DeliverPlugin(hostskills.PluginRequest{
				Pack:        p.Name,
				Plugin:      pl,
				SkillsDir:   skillsDir,
				Tier:        tier,
				Manifest:    man,
				ArchiveRoot: hostSkillsArchiveRoot(),
				Stamp:       stamp,
				Observe:     !write,
			})
			if derr != nil {
				pr.Printf("  [red]skills     plugin %s failed[/red] — %v", pl.Name(), derr)
				rc = 1
				continue
			}
			for _, r := range results {
				printSkillResult(pr, r)
			}
		}
		results, derr := hostskills.Deliver(hostskills.Request{
			Pack:        p.Name,
			Description: p.Decl.Description,
			// The pack's own skills dir — the one this contribution's `from` names — is
			// the only source. Built-ins and the user's own tree are both deliberately
			// excluded (see the file comment). Empty when the source could not be
			// resolved, which Deliver reads as "this pack carries no skills" and
			// therefore leaves the destination alone: the refusal above is the report,
			// and a pack whose `from` is missing must not also retire what a previous
			// apply delivered.
			Sources: sourceList(src),
			// A wrapped plugin's subtree is already delivered verbatim above, so it must not
			// ALSO arrive here as a loose skill dir named after the plugin.
			SkipSources: pluginDirs,
			SkillsDir:   skillsDir,
			Tier:        tier,
			Manifest:    man,
			ArchiveRoot: hostSkillsArchiveRoot(),
			Stamp:       stamp,
			Observe:     !write,
		})
		if derr != nil {
			pr.Printf("  [red]skills     failed[/red] — %v", derr)
			rc = 1
			continue
		}
		if len(results) == 0 && len(plugins) == 0 {
			// A pack that CARRIES no skills is normal and common: the six shipped agent
			// packs declare a `skills` contribution to name the destination their agent
			// reads from, and the content comes from the user's own packs merging into it.
			// So this is a quiet note, not the "check your filters" warning it first was —
			// firing that on every apply of a stock config would train the user to ignore
			// warnings.
			pr.Printf("  [dim]skills     %s ships none (its contribution names the "+
				"destination other packs merge into)[/dim]", p.Name)
			continue
		}
		for _, r := range results {
			printSkillResult(pr, r)
		}
	}

	// Persist the record only after a real write. Saving in observe posture would record
	// deliveries that never happened, which is exactly the stale-record case the delivery
	// policy is built to survive — no reason to manufacture one.
	if write {
		if err := man.Save(manPath); err != nil {
			pr.Printf("  [yellow]⚠ skills: could not save the ownership record: %v[/yellow]", err)
			pr.Printf("  [dim]  (the skills were written; the next apply will treat them " +
				"as yours and leave them alone)[/dim]")
		}
	}
	return rc
}

// printSkillResult renders one entry's outcome, colored by whether it is a write, a
// hands-off, or a problem.
func printSkillResult(pr richtext.Printer, r hostskills.Result) {
	switch r.Action {
	case hostskills.ActionWrote:
		pr.Printf("  [cyan]skills[/cyan]     %-24s %s  [dim]%s[/dim]", r.Name, r.Action, r.Detail)
	case hostskills.ActionSkippedUser:
		pr.Printf("  [green]skills[/green]     %-24s %s  [dim]%s[/dim]", r.Name, r.Action, r.Detail)
	case hostskills.ActionArchived:
		pr.Printf("  [yellow]skills[/yellow]     %-24s %s  [dim]%s[/dim]", r.Name, r.Action, r.Detail)
	default:
		pr.Printf("  [yellow]skills[/yellow]     %-24s %s  [dim]%s[/dim]", r.Name, r.Action, r.Detail)
	}
}

// sourceList wraps a resolved source dir as hostskills.Request.Sources, and yields NO
// sources for an unresolved one ("").
//
// A nil Sources is not the same as a source that happens to be empty on disk, and the
// difference is the point: Deliver reads "no skills collected" as "this pack carries none"
// and returns without touching the destination, so a pack whose `from` is missing gets a
// refusal line and its previously-delivered skills left alone — rather than an apply that
// silently ARCHIVES them because the source it was told to read does not exist.
func sourceList(dir string) []string {
	if dir == "" {
		return nil
	}
	return []string{dir}
}

// skillsContributions returns the pack's skills declarations.
func skillsContributions(p *packload.Pack) []packdecl.Contribution {
	var out []packdecl.Contribution
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindSkills {
			out = append(out, c)
		}
	}
	return out
}
