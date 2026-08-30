package run

// loopholeretire.go is the launch-path half of retirement-on-deselect: it RECORDS which pack
// owns each per-loophole state dir, and DETECTS the moment that pack leaves `packs`
// (docs/design/loophole-packaging.md §4.5, artifacts one and two of three — the `yolo prune`
// sweeper is the third and lives in internal/prune/loopholestate.go).
//
// # Why the launch path, and not `yolo host apply`
//
// §4.5 measured that the obvious precedent does not reach here, three times over:
//
//  1. The `files` kind's host output is retired by cli.pruneDroppedPackOutput, called ONLY
//     from `yolo host apply` — the exact command §3.4 refuses the loophole kind at. That command
//     never sees a loophole contribution, so it can never see one depart.
//  2. `yolo prune` sweeps the host-render ARCHIVE, a different tree from the state root.
//  3. loopholes.StateDirFor is keyed by loophole NAME ONLY, which is exactly the property §8
//     relies on to make a pack-shipped CA possible (name-keyed ⇒ outside the staged tree ⇒
//     survives restaging). So nothing on disk records WHICH PACK OWNED A STATE DIR: §4.5's
//     requirement and §8's benefit are the same property seen from two sides.
//
// The launch is where deselection is actually observed — it is the thing that reads `packs`,
// compares it to what is staged, and prunes (run/packs.go's pruneDroppedPackStaging). So the
// detector belongs beside that comparison, and this runs immediately after it, from
// stageRunPacks.
//
// # SELECTION CONTROLS ACTIVATION, NOT REVOCATION
//
// Stated here because it is the boundary of what this file can promise. Deselecting a pack
// stops the NEXT launch from starting its daemon, and retires the state the daemon left
// behind. It does not stop a daemon that has already run: the spawn is Setsid, teardown kills
// the process GROUP (loopholesruntime.killServiceGroup — the accepted half of §4.5), and a
// process that has executed once can persist by means yolo has no view of. §4.5 explicitly
// REJECTED recording spawned PIDs so a later prune could reap them: that builds a process
// supervisor for a threat the finding itself calls marginal (once arbitrary host execution
// has happened once, persistence is available through ~/.bashrc or cron), and a stale PID
// file is its own class of bug. No packaging design changes this.

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// --- the loophole modules a pack ships ---
//
// This lives here rather than beside the disclosure because retirement is the first thing that
// needs it: the record is keyed by loophole NAME, and the name comes from the contribution's
// `from` basename. §3.1 makes that exact rather than a guess — "the loophole's `name` must
// equal the directory basename … it is what lets the footprint name the loophole without
// decoding its manifest" — so nothing here parses a manifest to learn a name.

// packLoophole is one loophole a pack ships, as seen from the launch path.
type packLoophole struct {
	// Pack is the pack that contributed it.
	Pack string
	// Name is the loophole name. Taken as the basename of the contribution's `from`,
	// which §3.1 makes exact rather than a guess: "the loophole's `name` must equal the
	// directory basename … it is what lets the footprint name the loophole without
	// decoding its manifest."
	Name string
	// Dir is the module directory inside the STAGED tree (p.Root-relative `from`, resolved).
	Dir string
}

// packLoopholes enumerates the loophole modules a loaded pack ships, in declaration order.
//
// Matches the contribution kind BY VALUE (see packLoopholeKindName), so it works before and
// after the kind constant exists. A contribution with an empty `from` is skipped: the pack
// layer refuses that case with a named error (§3.1), and inventing a name here would report
// a loophole called "." at every launch.
func packLoopholes(p *packload.Pack) []packLoophole {
	var out []packLoophole
	for _, c := range p.Decl.Contributions() {
		if string(c.Kind) != packLoopholeKindName || c.From == "" {
			continue
		}
		out = append(out, packLoophole{
			Pack: p.Name,
			Name: filepath.Base(filepath.Clean(c.From)),
			Dir:  filepath.Join(p.Root, filepath.FromSlash(c.From)),
		})
	}
	return out
}

// packLoopholeOwnersPath is where the pack→loophole-state ownership record lives: its OWN
// file under the state dir, beside the host-render records (host-skills-manifest.json and
// kin) rather than merged into one of them.
//
// Its own file for the reason hostComposedSkillsManifestPath is its own file: the key spaces
// answer different questions. That record maps a DESTINATION PATH in the user's home to a
// pack; this maps a LOOPHOLE NAME to a pack. Sharing one file would mean one reader walking
// two kinds of key, and the sibling kind already produced a live defect that way (every
// composed skill retired as a dropped pack's output on the next apply). Different question,
// different file.
//
// MACHINE-WIDE, matching what it describes. `packs` is user-scope-only (config/packs.go
// makes workspace scope inexpressible) and a loophole's state dir is name-keyed under the
// machine's state root, so there is no per-workspace fact here to key on. A workspace-keyed
// record would ask "did this repo deselect the pack", a question the config cannot express.
func packLoopholeOwnersPath() string {
	return filepath.Join(paths.GlobalStorage(), "pack-loophole-owners.json")
}

// loopholeStateRoot is the directory holding per-loophole state dirs — the parent of what
// loopholes.StateDirFor(name) returns.
//
// Derived here rather than imported from internal/loopholes because what this file needs is
// the PARENT, and StateDirFor's contract is a single name's dir; taking its Dir() would make
// this depend on that function's shape (and on calling it with a fake name). Pinned equal to
// the real layout by TestLoopholeStateRootMatchesStateDirFor.
func loopholeStateRoot() string { return filepath.Join(paths.GlobalStorage(), "state") }

// loopholeLogDir is where host-service-<name>.log is written (loopholesruntime.go's logDir).
func loopholeLogDir() string { return filepath.Join(paths.GlobalStorage(), "logs") }

// recordAndRetirePackLoopholes is the whole launch-path pass: retire the state of every
// loophole whose owning pack left the config, then record the ownership of every loophole
// this launch's packs ship.
//
// RETIRE BEFORE RECORD, deliberately. A pack can be dropped and a DIFFERENT pack shipping
// the same loophole name selected in one edit; recording first would rewrite the owner and
// make the departure invisible, handing the new pack the old pack's CA. Retiring first means
// the state a departed pack left is always archived before anything else can claim its name.
//
// NEVER FATAL. Staging is fail-closed (A12) because a missing pack changes what the jail
// contains; this is bookkeeping over the host's state dir, and failing a launch because a
// JSON file could not be written would trade a real jail for a tidy record. Every failure
// prints and continues, and a failed retirement KEEPS its record entry so the next launch
// retries — the same rule applyhostprune follows for the same reason.
func (o *Options) recordAndRetirePackLoopholes(packs []*packload.Pack) {
	path := packLoopholeOwnersPath()
	rec, err := packstage.LoadLoopholeOwners(path)
	if err != nil {
		// A record yolo cannot read proves nothing, so nothing is retired — and it must not
		// be overwritten either, or an unreadable file would silently become an empty one
		// and every pre-existing state dir would be orphaned unattributed forever.
		o.pr(o.Stdout).printf("[yellow]Warning: pack loophole ownership record: %s — "+
			"no loophole state is retired or recorded this launch[/yellow]", err.Error())
		return
	}
	changed := o.retireDepartedLoopholeState(rec)
	if o.recordPackLoopholeOwners(rec, packs) {
		changed = true
	}
	if !changed {
		return
	}
	if err := rec.Save(path); err != nil {
		o.pr(o.Stdout).printf("[yellow]Warning: could not save the pack loophole ownership "+
			"record: %s[/yellow]", err.Error())
	}
}

// retireDepartedLoopholeState archives the state of every recorded loophole whose owning
// pack is no longer configured, and forgets each one it successfully archived. Returns
// whether the record changed.
//
// REFUSES ON AN UNKNOWN CONFIGURED SET, which is the same guard pruneDroppedPackOutput opens
// with and here it protects a private key: a bug that made the set empty would read as "every
// pack is gone" and archive every loophole's state on the machine. An unreadable or
// problem-reporting `packs` list is therefore "retire nothing", not "retire everything".
func (o *Options) retireDepartedLoopholeState(rec *packstage.LoopholeOwners) bool {
	configured, ok := configuredPackNames()
	if !ok {
		return false
	}
	departed := rec.Departed(configured)
	if len(departed) == 0 {
		return false
	}
	stamp := packstage.ArchiveStamp(o.Now())
	changed := false
	out := o.pr(o.Stdout)
	for _, d := range departed {
		gen, moved, err := packstage.RetireLoopholeState(packstage.RetireRequest{
			Loophole:  d.Loophole,
			Pack:      d.Pack,
			StateRoot: loopholeStateRoot(),
			LogDir:    loopholeLogDir(),
			Stamp:     stamp,
		})
		if err != nil {
			// KEEP the record entry: the authority to archive came from it, and a partial
			// move must stay attributable so the next launch can finish the job.
			out.printf("[yellow]Warning: could not retire loophole %s's state "+
				"(pack %s left `packs`): %s[/yellow]", d.Loophole, d.Pack, err.Error())
			continue
		}
		delete(rec.Owners, d.Loophole)
		changed = true
		if len(moved) == 0 {
			// Nothing was on disk — the loophole never ran, or its state was already
			// reclaimed. Forgetting it silently is right: a line about a directory that
			// never existed is noise, and NO SILENT CAPS is about things the user LOSES.
			continue
		}
		// ARCHIVED, NOT DELETED, and the path is printed because an archive the user cannot
		// find is a deletion from their point of view. This state may be the only copy of a
		// CA their long-lived jails still trust.
		out.printf("[yellow]Warning: pack %s left `packs`, so loophole %s is retired — "+
			"its state was ARCHIVED (not deleted) at %s[/yellow]", d.Pack, d.Loophole, gen)
	}
	return changed
}

// recordPackLoopholeOwners writes this launch's pack→loophole ownership into rec, returning
// whether anything changed.
//
// It records what is STAGED AND LOADED rather than what the config names, because the
// question the record answers is "which pack put this state dir here" and only a loaded pack
// has declarations to read. A configured pack that failed to resolve this launch contributes
// nothing new and — crucially — loses nothing: its existing entry is left alone, and
// retireDepartedLoopholeState keys on the CONFIG, so an offline launch never looks like a
// deselection.
func (o *Options) recordPackLoopholeOwners(rec *packstage.LoopholeOwners, packs []*packload.Pack) bool {
	changed := false
	for _, p := range packs {
		for _, lp := range packLoopholes(p) {
			if rec.Owners[lp.Name] == lp.Pack {
				continue
			}
			// A name already owned by ANOTHER pack is overwritten here rather than refused,
			// and the refusal is not this file's job: loophole-name exclusivity across packs
			// is a fatal pre-flight (§3.1's fourth bespoke check, landing with the kind), so
			// by the time this runs the set is already known collision-free. Overwriting is
			// the correct behaviour for the case that DOES reach here — the same pack
			// renamed, or a name whose previous owner was retired above.
			rec.Owners[lp.Name] = lp.Pack
			changed = true
		}
	}
	return changed
}

// configuredPackNames returns the set of pack names the USER CONFIG NAMES, and ok=false when
// that set cannot be trusted.
//
// The names the config NAMES — not the ones that RESOLVED — and §4.5 inherits the reason from
// pruneDroppedPackOutput: a fetched pack whose remote is unreachable resolves to nothing, so
// to a resolved-set comparison it looks dropped. For a briefing that mistake self-heals; an
// archived CA does not come back until the user goes digging. Same evidence, different cost
// of being wrong, so the same threshold.
//
// Any per-entry problem makes the whole set untrusted (ok=false), matching warnIfNoPacks'
// reading of the same callback: a malformed `packs` list means "the user configured packs and
// yolo cannot tell which", and treating that as "no packs" would archive every loophole's
// state on the machine.
func configuredPackNames() (map[string]bool, bool) {
	problems := 0
	entries, err := config.LoadPacks(func(string) { problems++ })
	if err != nil || problems > 0 {
		return nil, false
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	return names, true
}
