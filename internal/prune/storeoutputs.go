package prune

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// storeoutputs.go reclaims YOLO'S OWN superseded /nix/store outputs
// (disk-levers-and-backfill.md OQ-BF3, L3). Measured there at ≥ 28.8 GB with
// ≈ 0.43 GB/day of accrual and NO collector at all: `nix store gc` is reachable
// only through `yolo prune --nix-gc --apply` (host-only, default off) and the
// daemon's min-free of 0.
//
// WHAT MAKES THIS ADMISSIBLE UNDER P5 ("never carelessly GC the host store").
// It is not a GC. It is a NAMED, SELF-SCOPED deletion of paths yolo itself
// realized, and the three properties that keep it that way are load-bearing:
//
//   - BY NAME. Only outputs whose store-path name is one yolo's own derivations
//     produce. The set never widens to a path yolo did not realize, which is the
//     line cache-relocation.md's threat model draws.
//   - HOST-ONLY. In-jail /nix/store is a read-only bind of the host's and the
//     gcroots dir is unmounted, so a jail cannot tell rooted from unrooted.
//   - NEVER --ignore-liveness. nix's own refusal to delete a live path is the
//     SECOND veto, behind yolo's rooting. A flag that turns it off would make
//     this exactly the careless GC P5 forbids.
//
// ORDERING AGAINST OQ-BF4 IS THE WHOLE SAFETY ARGUMENT. Until every running
// jail's prefix has a durable root, "unrooted" does not mean "unused" — it means
// "we have not been recording", and deleting on that basis takes pid1's binary
// out from under a live jail. BF4 ships first; this reads the roots BF4 writes.

// hostNixStoreDir is the store this pass scans. A constant rather than a
// parameter at the call site because the ONE thing that must never happen is
// pointing it somewhere else: the name-scoping argument above only holds for
// paths yolo realized in the host store.
const hostNixStoreDir = "/nix/store"

// yoloStoreOutputSuffixes are the store-path name endings yolo's own derivations
// produce. Spelled as a list rather than a pattern so adding one is a deliberate
// act with a reviewer, which is what "never widens" has to mean in practice.
//
// The two here are the measured accrual: install prefixes (85.6 MB each, 220
// unrooted) and the Go build outputs behind them (40.9 MB each, ALL 245
// unrooted — a Go build output is garbage the moment its prefix exists, because
// keep-outputs is false).
var yoloStoreOutputSuffixes = []string{
	"-yolo-jail-install-prefix",
	"-yolo-jail-go-0-dev",
}

// SupersededStoreOutputs lists yolo's own store outputs that no root of ours
// points at, newest-first-safe: a path younger than grace is kept, covering an
// output realized by a launch whose rooting has not happened yet.
//
// rootDirs are the directories whose symlink TARGETS are the rooted set —
// build/roots and build/prefix-roots. Passing them in rather than reading them
// here keeps this function testable against a temp tree and makes the caller
// state which roots it believes in.
func SupersededStoreOutputs(storeDir string, rootDirs []string, inUse map[string]bool, grace time.Duration, now time.Time) []string {
	rooted := map[string]bool{}
	// IN USE BY A RUNNING CONTAINER — the guard that closes the UPGRADE WINDOW,
	// and it is not redundant with the roots.
	//
	// OQ-BF3's ruling is gated on OQ-BF4 having rooted "every RUNNING jail's
	// prefix", and on the first launch after that ships the precondition is FALSE
	// for every jail that was already up: BF4 roots a prefix when a launch
	// registers it, and a jail launched before BF4 existed never did. So on that
	// first pass, 232 unrooted prefixes look reclaimable and one or more of them
	// is what a live jail is executing pid1 out of. `nix store delete` does not
	// save us — a bind mount is not a nix GC root, so nix does not consider the
	// path live.
	//
	// MEASURED 2026-09-09 on this machine: 233 install prefixes in the store,
	// build/prefix-roots absent, two jails running. Reading the gate as "BF4 has
	// landed" rather than "every running jail is actually rooted" was the letter
	// of the ruling and not its substance.
	for p := range inUse {
		rooted[p] = true
	}
	for target := range rootedTargets(rootDirs) {
		rooted[target] = true
	}
	out := []string{}
	for _, suffix := range yoloStoreOutputSuffixes {
		matches, err := filepath.Glob(filepath.Join(storeDir, "*"+suffix))
		if err != nil {
			continue
		}
		for _, p := range matches {
			if rooted[p] {
				continue
			}
			// A path realized moments ago may belong to a launch that has not
			// reached its rooting step. Same shape as PrefixRootGrace and for the
			// same reason — a guard against a race, not a retention policy. Aged by
			// its change time, never its mtime: see storePathAge.
			if st, err := os.Lstat(p); err == nil && storePathAge(st, now) < grace {
				continue
			}
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// storePathAge is how long ago the store path fi describes entered the store, read from
// its inode CHANGE time (st_ctime) and never from its mtime.
//
// NIX CANONICALIZES EVERY STORE PATH'S MTIME to one second past the epoch as it registers
// the path, so an output built a moment ago has the same mtime as one from 2020, and an age
// read from the mtime called every output 56 years old: the grace protected nothing. That
// canonicalization is itself a write to the inode's metadata (it sets the mode and the
// mtime), so the change time it leaves is when the path was registered. MEASURED
// 2026-10-03 on the host store this jail mounts: for all 51 valid yolo outputs, ctime
// equaled `nix path-info`'s registrationTime to the second, while every mtime was 1. On
// darwin the same holds by POSIX (chmod and utimes both update st_ctime), not by a
// measurement on a Mac.
//
// Chosen over registrationTime, which is a store-database read through the daemon and has
// no answer for a path not registered yet, and over the birth time, which syscall.Stat_t
// does not carry on Linux and which predates registration by the whole build. Anything
// that touches the path after registration (a `nix store optimise`, an xattr, a chmod)
// can only move its change time FORWARD, barring a clock set back, and forward makes the
// path look younger, so it is kept longer: the side a race guard should err on.
//
// A FileInfo with no *syscall.Stat_t behind it, which os.Lstat never returns on linux or
// darwin, has no change time to read, and is treated as just realized: an unknown age is
// not permission.
func storePathAge(fi os.FileInfo, now time.Time) time.Duration {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return now.Sub(statChangeTime(st))
}

// StoreDeleteCmd is the argv that removes one store path, and it is spelled here
// so a reader can see what is NOT in it: no `--ignore-liveness`, no `gc`, no
// `--all`. `nix store delete` refuses a path that is still live, and that
// refusal is a feature of this design rather than an obstacle to it. It turns on
// nix-command itself, as RunNixStoreGC does, because the official installer leaves it off.
func StoreDeleteCmd(path string) []string {
	return []string{"nix", "--extra-experimental-features", "nix-command flakes", "store", "delete", path}
}

// rootedTargets is the symlink targets of every root under rootDirs: the store paths yolo
// has rooted. An unreadable directory contributes nothing.
func rootedTargets(rootDirs []string) map[string]bool {
	rooted := map[string]bool{}
	for _, dir := range rootDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if target, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil {
				rooted[target] = true
			}
		}
	}
	return rooted
}

// DeleteSupersededStoreOutputs removes the given paths, returning those it
// actually removed. A path nix refuses (still live) is SKIPPED silently: that is
// the second veto doing its job, not an error to report.
func DeleteSupersededStoreOutputs(paths []string, apply bool, run RunFunc) []string {
	return DeleteSupersededStoreOutputsGuarded(paths, apply, run, nil, nil)
}

// DeleteSupersededStoreOutputsGuarded is DeleteSupersededStoreOutputs with each `nix store
// delete` bracketed by guard (guard.go). Its recheck re-reads rootDirs right before each
// deletion: a launch that rooted one of these paths since the listing (a prefix it just
// built) has made it not superseded. The in-use half needs no read here: a jail launched
// since the listing roots its prefix before its container starts, and nix itself refuses
// a live path.
func DeleteSupersededStoreOutputsGuarded(paths []string, apply bool, run RunFunc, guard Guard, rootDirs []string) []string {
	done := []string{}
	for _, p := range paths {
		if !apply {
			done = append(done, p)
			continue
		}
		var removed bool
		guard.Do(func() bool { return !rootedTargets(rootDirs)[p] }, func() {
			res := run(StoreDeleteCmd(p), storeDeleteTimeout)
			removed = res.Ran && res.RC == 0
		})
		if removed {
			done = append(done, p)
		}
	}
	return done
}

// storeDeleteTimeout bounds one `nix store delete`. Generous because the daemon
// may be busy, bounded because this runs in the launch's housekeeping slot.
const storeDeleteTimeout = 60 * time.Second

// StoreOutputGrace mirrors PrefixRootGrace: an output younger than this belongs
// to a launch that may not have rooted it yet. Measured by storePathAge, never by
// the mtime nix pins to the epoch.
const StoreOutputGrace = time.Hour

// describeStoreOutput trims the store dir for display, so a report names
// `abc123-yolo-jail-install-prefix` rather than a 60-character path.
func describeStoreOutput(storeDir, p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, storeDir), "/")
}
