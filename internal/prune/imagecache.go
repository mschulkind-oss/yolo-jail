package prune

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// imageCacheTmpGraceFloor is how long a *.tmp under cache/images must have sat
// untouched before this sweep will call it a crash leftover.
//
// # The failure it prevents
//
// A *.tmp in that directory is no longer only a crash leftover. Since C9 an
// archive-delivering backend writes its transient image archive THERE, under a
// name this sweep cannot tell apart from a corpse: image.archiveTempPath joins
// cache/images with the store key and one of `.oci-archive.tmp` /
// `.docker-archive.tmp`, and filepath.Ext on either is exactly ".tmp". Without a
// floor, one launch's housekeeping slot deletes the multi-GB archive another
// launch is at that moment copying into or loading back out of, and the second
// launch fails on a file it wrote itself.
//
// ⚠ NOT REPRODUCIBLE ON LINUX, which is why a reader there will not find it by
// launching jails. image.deliverViaArchive is the arm Apple Container and
// podman-on-macOS take; a rootless Linux podman copies straight into
// containers-storage and writes no archive into this directory at all.
//
// # What the hour is sized against
//
// The window to outlast is a whole archive delivery, not just its copy. The copy
// is the measured part — copylock.go records 3.45 GB taking ~4 minutes in a store
// write on 2026-09-14 — but mtime stops advancing the moment skopeo finishes,
// and the archive then sits being READ by the runtime's loader for as long again,
// so the file looks idle for most of the half that matters. Serialisation
// stretches it further: the image-copy lock makes N simultaneous launches wait
// for each other, and the measurement that forced that lock was eleven launches
// at once. An hour is an order of magnitude past the measured copy and still
// prompt enough that a genuinely crashed delivery's bytes come back on the next
// pass rather than never.
//
// It is deliberately the same hour PruneLegacyBuildRoots is given by its caller,
// and for a related reason — a floor is how this package keeps a sweep off a
// concurrent writer — but it is its OWN constant, because that one is sized
// against a mid-upgrade window and this one against an image delivery. Tuning
// either must not silently move the other.
//
// # Two alternatives, so nobody re-opens them
//
//   - A SUFFIX THE REAPER SKIPS (teach it `.oci-archive.tmp` and friends) splits
//     what one floor covers together: young means in flight, old means the
//     delivery died, and the same file is both at different times. It also leaves
//     a crashed delivery's multi-GB archive on disk permanently, which is the
//     opposite of this function's job.
//   - TAKING THE IMAGE-COPY LOCK is refused by internal/image/copylock.go's own
//     reasoning: that lock is held across a multi-minute copy and its waiter must
//     never skip, while every housekeeping caller here is allowed to skip. Folding
//     them would park every reaper on the machine behind a 4-minute copy.
const imageCacheTmpGraceFloor = time.Hour

// imageCacheLstat is os.Lstat behind a package var so the "I could not stat it"
// branch — the one that must decline rather than delete — is reachable from a
// test. A real lstat failure needs a permission the test process may not lack
// (it often runs as root), so without this seam the branch has no coverage and a
// later edit could collapse it into a delete unnoticed. Nothing but a test ever
// reassigns it; copyFlockSyscall in internal/image/copylock.go is the same idiom
// for the same reason.
var imageCacheLstat = os.Lstat

// PruneImageCache keeps the `keep` newest *.tar files under imagesDir and removes
// the rest; it sweeps *.tmp files left behind by a crashed materialization, but
// only once they are older than imageCacheTmpGraceFloor, which is what keeps it
// off an image delivery still in flight (see that constant). Returns
// (bytesRemoved, filesRemoved). Classifies by file extension (.tar / .tmp), sorts
// tars by mtime newest-first and drops the tail beyond keep, then sweeps the tmps
// past the floor. apply=false reports without touching disk.
//
// An entry it cannot lstat is left alone rather than removed, for the same reason
// the rest of this package declines on an answer it could not get: "unreadable"
// and "reclaimable" are different facts, and only one of them is safe to act on.
func PruneImageCache(imagesDir string, keep int, apply bool) (bytesRemoved int64, filesRemoved int) {
	info, err := os.Stat(imagesDir)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	children, err := os.ReadDir(imagesDir)
	if err != nil {
		return 0, 0
	}
	now := time.Now()

	type tarEnt struct {
		path  string
		size  int64
		mtime int64
	}
	var tars []tarEnt
	type tmpEnt struct {
		path string
		size int64
	}
	var tmps []tmpEnt

	for _, c := range children {
		p := filepath.Join(imagesDir, c.Name())
		st, err := imageCacheLstat(p)
		if err != nil {
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			continue
		}
		switch filepath.Ext(c.Name()) {
		case ".tar":
			tars = append(tars, tarEnt{p, st.Size(), st.ModTime().UnixNano()})
		case ".tmp":
			// Age grace floor — skip recent entries (an in-flight image delivery
			// writes this exact shape into this exact directory).
			if now.Sub(st.ModTime()) < imageCacheTmpGraceFloor {
				continue
			}
			tmps = append(tmps, tmpEnt{p, st.Size()})
		}
	}

	// Tars: newest first (by mtime), drop the tail beyond keep.
	sort.SliceStable(tars, func(i, j int) bool { return tars[i].mtime > tars[j].mtime })
	if keep < 0 {
		keep = 0
	}
	if keep < len(tars) {
		for _, t := range tars[keep:] {
			if apply {
				if err := os.Remove(t.path); err != nil {
					continue
				}
			}
			bytesRemoved += t.size
			filesRemoved++
		}
	}

	// Orphan tmp files: sweep the ones past the floor, regardless of keep.
	for _, t := range tmps {
		if apply {
			if err := os.Remove(t.path); err != nil {
				continue
			}
		}
		bytesRemoved += t.size
		filesRemoved++
	}

	return bytesRemoved, filesRemoved
}
