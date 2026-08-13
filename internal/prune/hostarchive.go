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

// PruneHostArchiveBuckets sweeps every BUCKET under the host-render archive root, keeping the
// newest `keep` generations in each. Returns the totals plus every removed generation named as
// `<bucket>/<stamp>`; apply=false reports without touching disk.
//
// WHY BUCKETS EXIST (V3). The archive used to be one directory, `archive/skills`, shared by
// every host kind — so a replaced `files` copy was filed under a name that said "skills". The
// render now writes one bucket per kind (internal/cli.hostArchiveRoot), and this is the sweep
// that keeps that from becoming a disk leak: hardcoding one bucket name would have left every
// new bucket to grow forever while prune reported "none".
//
// IT IS ALSO THE MIGRATION. `archive/skills` is still a live bucket — nothing was moved, so
// every copy a previous yolo archived there is exactly where its report said it was — and
// enumerating buckets rather than naming them is what keeps those legacy generations
// reclaimable instead of stranding them.
//
// PER-BUCKET keep, not global: each bucket is its own undo buffer, and one apply that only
// replaced a skill has no business evicting the generation holding the user's edited `files`
// copy. A directory that holds no generation at all contributes nothing and is left alone,
// which is the same "do not delete what you cannot account for" rule the stamp check applies
// one level down.
func PruneHostArchiveBuckets(archiveRoot string, keep int, apply bool) (bytesRemoved int64, removed int, removedNames []string) {
	entries, err := os.ReadDir(archiveRoot)
	if err != nil {
		return 0, 0, nil // no archive is the normal case
	}
	var buckets []string
	for _, e := range entries {
		if e.IsDir() {
			buckets = append(buckets, e.Name())
		}
	}
	sort.Strings(buckets)
	for _, bucket := range buckets {
		b, n, names := PruneHostArchive(filepath.Join(archiveRoot, bucket), keep, apply)
		bytesRemoved += b
		removed += n
		for _, name := range names {
			// Qualified by bucket, because a stamp is a per-APPLY name and one apply can
			// archive into several buckets — an unqualified list would print the same stamp
			// twice with no way to tell which copy went.
			removedNames = append(removedNames, bucket+"/"+name)
		}
	}
	return bytesRemoved, removed, removedNames
}

// PruneHostArchive reclaims one BUCKET's archive generations, keeping the newest `keep` and
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
