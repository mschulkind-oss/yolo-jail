package prune

// loopholestate.go reclaims the RETIRED per-loophole state archive: the generations the
// launch path creates when a pack that shipped a loophole leaves `packs`
// (docs/design/loophole-packaging.md §4.5, the third of the three missing artifacts).
//
// # Why this is its own sweeper and not the host-archive one
//
// Measured before this file existed: `rg -c loophole internal/prune/*.go` returned ZERO.
// Nothing here knew loopholes existed, and §4.5 measured why the obvious precedent does not
// reach:
//
//   - The `files` kind's host output is retired by cli.pruneDroppedPackOutput, called ONLY
//     from `yolo host apply` — the exact command §3.4 refuses the loophole kind at, so it never
//     sees a loophole contribution.
//   - PruneHostArchiveBuckets sweeps the HOST-RENDER archive (hostarchive.go), which is a
//     different tree from <state>/.retired: the render's archive lives under
//     GlobalStorage()/archive, the loophole state under GlobalStorage()/state.
//
// So the retirement had nowhere to be reclaimed from, and an unbounded archive is a disk
// leak — the same objection hostarchive.go answers for its own tree.
//
// # The same division of labor, deliberately
//
// The LAUNCH archives (it is the only place deselection is observed); PRUNE reclaims. A
// destructive cleanup must not be a side effect of a launch, and reclaiming disk is exactly
// what the user asks for by running `yolo prune`. So a launch only ever ADDS a generation,
// no matter how old the others are.
//
// KEEP-NEWEST-N, and the archive's contents make the case sharper than for skills: a
// generation may hold a CA private key the user's long-lived TLS clients still trust. An age
// cutoff would delete it precisely when the user had gone a month without touching config
// and then wanted it back.

import (
	"os"
	"path/filepath"
	"sort"
)

// RetiredLoopholeStateDir mirrors packstage.RetiredLoopholeStateDir.
//
// Spelled here rather than imported for the reason pathsref.go keeps for the storage roots:
// internal/prune takes its paths as data so every apply test can point them at a temp root,
// and packstage is the writer's side of the same constant. Pinned equal by
// TestRetiredDirNameMatchesTheWriter so the two cannot drift into a sweep that finds nothing.
const RetiredLoopholeStateDir = ".retired"

// PruneRetiredLoopholeState reclaims retired per-loophole state generations under
// <stateRoot>/.retired, keeping the newest `keep` and removing the rest. Returns
// (bytesRemoved, generationsRemoved, removedNames); apply=false reports without touching
// disk.
//
// Generations sort by NAME, which is the writer's timestamp stamp (20060102-150405), so
// lexical order is chronological without stat'ing anything — the same property
// PruneHostArchive relies on. A directory whose name is not a stamp is LEFT ALONE: prune
// does not delete what it cannot explain, and here that rule also protects a user who
// dragged something into the archive by hand.
//
// A missing archive is the normal case (no pack has ever shipped a loophole, or none has
// been deselected) and reports nothing.
func PruneRetiredLoopholeState(stateRoot string, keep int, apply bool) (bytesRemoved int64, removed int, removedNames []string) {
	if keep < 0 {
		keep = 0
	}
	archive := filepath.Join(stateRoot, RetiredLoopholeStateDir)
	entries, err := os.ReadDir(archive)
	if err != nil {
		return 0, 0, nil
	}
	var generations []string
	for _, e := range entries {
		if e.IsDir() && looksLikeArchiveStamp(e.Name()) {
			generations = append(generations, e.Name())
		}
	}
	if len(generations) <= keep {
		return 0, 0, nil
	}
	sort.Strings(generations)
	doomed := generations[:len(generations)-keep]
	for _, name := range doomed {
		dir := filepath.Join(archive, name)
		size := dirSize(dir)
		// Read the loophole names BEFORE the removal, not after: the label is derived from
		// the directory's own contents, and computing it afterwards yields "?" for every
		// generation on the --apply path (which is the only path where the label matters).
		label := name + " (" + joinLoopholeNames(dir) + ")"
		if apply {
			if err := os.RemoveAll(dir); err != nil {
				continue // report only what actually went
			}
		}
		bytesRemoved += size
		removed++
		// Named with the loopholes inside, because a bare stamp says nothing about what is
		// being reclaimed and this archive is the kind whose contents a user cares about.
		removedNames = append(removedNames, label)
	}
	return bytesRemoved, removed, removedNames
}

// joinLoopholeNames lists the loophole directories inside one generation, comma-separated,
// so the report names what went rather than only when it was archived. Best-effort: an
// unreadable generation yields "?" rather than failing the sweep, since the byte count and
// the removal are still correct.
func joinLoopholeNames(gen string) string {
	entries, err := os.ReadDir(gen)
	if err != nil {
		return "?"
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "?"
	}
	sort.Strings(names)
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
