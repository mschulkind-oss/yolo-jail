package paths

import "path/filepath"

// BaseHomeCoreDirs returns the home-relative directories yolo itself provisions in the
// BASE home, independent of any pack — the hardcoded half of the union that
// docs/design/base-home-legacy-state.md §1 names.
//
// ONE list, two consumers that must agree about it, which is the only reason it is here
// rather than inline where it is created:
//
//   - storage.EnsureGlobalStorage creates them (with the pack-declared state and shared
//     dirs appended), and
//   - the base-home legacy-state walk EXCLUDES them: §5.1's third root bullet sweeps
//     "every other top-level directory" for the retired/unknown-pack case, and §8 puts
//     "the base's non-pack contents" out of scope. A second, hand-maintained copy of
//     this list is what OQ-BH4 names as the outcome to avoid — the sweep and the
//     provisioner disagreeing about which dirs are core's is a sweep that proposes
//     archiving `.ssh`.
//
// It is deliberately NOT the exclusion set itself: the walk derives the top-level
// segment of each entry, because `.config/git` is here for what it provisions while
// `.config` is what a top-level walk sees.
//
// Core-owned CACHE dirs a tool creates on its own are not here (nothing provisions
// them); the sweep's exclusion set adds those from prune's shadowed-home registry.
func BaseHomeCoreDirs() []string {
	return []string{
		filepath.Join(".config", "git"),
		filepath.Join(".pi", "agent"),
		".npm-global",
		".local",
		"go",
		// One dir, two generated-script children (block/ and launch/). The nested path
		// matters: the OCI runtime cannot mkdirat inside the :ro /home/agent bind, so the
		// mountpoint's PARENT has to exist here or the launch fails with an opaque
		// crun/conmon error rather than a useful one.
		".yolo",
		".yolo/bin",
		".config",
		".cache",
		".ssh",
	}
}
