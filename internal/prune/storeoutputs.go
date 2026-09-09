package prune

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
func SupersededStoreOutputs(storeDir string, rootDirs []string, grace time.Duration, now time.Time) []string {
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
			// same reason — a guard against a race, not a retention policy.
			if st, err := os.Lstat(p); err == nil && now.Sub(st.ModTime()) < grace {
				continue
			}
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// StoreDeleteCmd is the argv that removes one store path, and it is spelled here
// so a reader can see what is NOT in it: no `--ignore-liveness`, no `gc`, no
// `--all`. `nix store delete` refuses a path that is still live, and that
// refusal is a feature of this design rather than an obstacle to it.
func StoreDeleteCmd(path string) []string {
	return []string{"nix", "store", "delete", path}
}

// DeleteSupersededStoreOutputs removes the given paths, returning those it
// actually removed. A path nix refuses (still live) is SKIPPED silently: that is
// the second veto doing its job, not an error to report.
func DeleteSupersededStoreOutputs(paths []string, apply bool, run RunFunc) []string {
	done := []string{}
	for _, p := range paths {
		if !apply {
			done = append(done, p)
			continue
		}
		res := run(StoreDeleteCmd(p), storeDeleteTimeout)
		if res.Ran && res.RC == 0 {
			done = append(done, p)
		}
	}
	return done
}

// storeDeleteTimeout bounds one `nix store delete`. Generous because the daemon
// may be busy, bounded because this runs in the launch's housekeeping slot.
const storeDeleteTimeout = 60 * time.Second

// StoreOutputGrace mirrors PrefixRootGrace: an output younger than this belongs
// to a launch that may not have rooted it yet.
const StoreOutputGrace = time.Hour

// describeStoreOutput trims the store dir for display, so a report names
// `abc123-yolo-jail-install-prefix` rather than a 60-character path.
func describeStoreOutput(storeDir, p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, storeDir), "/")
}
