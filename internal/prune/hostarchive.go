package prune

// hostarchive.go reclaims the host-render archive: the generations `yolo apply --host`
// creates when it retires a skill or file it previously delivered.
//
// Why the archive exists at all: retiring a delivered entry is authorized by an ownership
// record that can go stale (the user edited the file, the state dir was pruned, two machines
// share one config). A stale record plus `rm` is data loss in the user's own home, so the
// render MOVES the entry aside and prints where. That makes being wrong cost a `mv` back.
//
// Why the cleanup lives HERE and not in apply: an unbounded archive is a disk leak, but a
// destructive cleanup must not be a side effect of a render. Reclaiming disk is something
// the user asks for — which is exactly what `yolo prune` is — so apply only ever adds, and
// prune is the one verb that removes. `apply --host` never deletes an archive generation, no
// matter how old.

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// PruneHostArchive reclaims host-render archive generations, keeping the newest `keep` and
// removing the rest. Returns (bytesRemoved, generationsRemoved, removedNames); apply=false
// reports without touching disk.
//
// KEEP-NEWEST-N rather than an age cutoff, deliberately. The archive is an undo buffer, and
// what a user needs from it is "the last few applies", not "everything from the last 30
// days" — a week of untouched config leaves nothing to undo, while three applies in an hour
// are exactly when a mistake gets noticed. An age rule would delete the generation you want
// precisely when you were iterating.
//
// Generations sort by NAME, which is the render's own timestamp stamp (20060102-150405), so
// lexical order is chronological order without stat'ing anything. A directory whose name is
// not a stamp is left alone: prune does not delete what it cannot explain.
func PruneHostArchive(archiveRoot string, keep int, apply bool) (bytesRemoved int64, removed int, removedNames []string) {
	if keep < 0 {
		keep = 0
	}
	entries, err := os.ReadDir(archiveRoot)
	if err != nil {
		return 0, 0, nil // no archive is the normal case
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
	// Oldest first; everything but the newest `keep` goes.
	doomed := generations[:len(generations)-keep]
	for _, name := range doomed {
		dir := filepath.Join(archiveRoot, name)
		size := dirSize(dir)
		if apply {
			if err := os.RemoveAll(dir); err != nil {
				continue // report only what actually went
			}
		}
		bytesRemoved += size
		removed++
		removedNames = append(removedNames, name)
	}
	return bytesRemoved, removed, removedNames
}

// looksLikeArchiveStamp reports whether name has the render's generation-stamp shape,
// `YYYYMMDD-HHMMSS`. Parsed rather than pattern-matched so a directory that merely looks
// numeric is not mistaken for a generation — prune must not delete a dir it cannot account
// for.
func looksLikeArchiveStamp(name string) bool {
	_, err := time.Parse("20060102-150405", name)
	return err == nil
}

// dirSize totals the apparent size of a tree, best-effort. An unreadable entry contributes
// zero rather than aborting: the number is for a human-facing "reclaimed N MB" line, so
// being slightly low is better than reporting nothing.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
