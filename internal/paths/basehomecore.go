package paths

import "path/filepath"

// BaseHomeCoreDirs returns the home-relative directories core provisioned in the LEGACY
// base home, <state>/home, back when podman bound that one directory at /home/agent in every
// jail: HomeSkeletonCoreDirs plus `.pi/agent`. storage.EnsureGlobalStorage created them on
// every launch until the per-jail skeleton replaced the shared base
// (docs/design/base-home-legacy-state.md#2-the-design-a-per-jail-skeleton), so a legacy base
// holds them, empty.
//
// Its consumer is the base-home legacy-state walk (internal/basehome, nonPackDirs), which
// EXCLUDES them, so its sweep of every other top-level directory never proposes archiving
// core's own. That is why `.pi/agent` stays here after the skeleton dropped it: without it
// the walk reports an old base's empty `.pi/agent` as legacy state (the MEASURED
// case in internal/basehome/classify.go's ProvisionedDir).
//
// ONE list plus one named addition, not two copies: a second, hand-maintained copy is what
// OQ-BH4 names as the outcome to avoid — the sweep and the provisioner disagreeing about
// which dirs are core's is a sweep that proposes archiving `.ssh`.
//
// It is deliberately NOT the exclusion set itself: the walk derives the top-level
// segment of each entry, because `.config/git` is here for what it provisions while
// `.config` is what a top-level walk sees.
//
// Core-owned CACHE dirs a tool creates on its own are not here (nothing provisions
// them); the sweep's exclusion set adds those from prune's shadowed-home registry.
func BaseHomeCoreDirs() []string {
	return append(HomeSkeletonCoreDirs(), filepath.Join(".pi", "agent"))
}
