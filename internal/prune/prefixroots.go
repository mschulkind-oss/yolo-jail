package prune

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// prefixroots.go reaps durable GC roots for the INSTALL PREFIX — the directory
// a jail's own yolo binaries, pid1 included, are bind-mounted from since C8.
//
// IT IS THE OPPOSITE POLICY TO ITS NEIGHBOUR, and that is the whole reason it is
// a separate pass rather than a parameter on PruneOrphanImageRoots. OQ-LS1 made
// image roots reap on AGE with no liveness evidence at all, on the grounds that
// losing one costs a rebuild. A prefix root fails that test: a running jail is
// EXECUTING from it, so losing it is not a rebuild, and age is precisely
// backwards — the longer a jail runs, the more certainly it would be reaped
// (disk-levers-and-backfill.md OQ-BF4).
//
// The one question that decides which policy a root gets: can losing it cost
// only a rebuild?

// PrefixRootGrace is the startup race guard, and it is a GUARD rather than a
// policy — the distinction OQ-LS1 draws, applied honestly in the place it is
// still needed.
//
// The race is real and asymmetric: resolveJailPrefix registers the root BEFORE
// the image build and the container start, so there is a window — an entire nix
// build wide — in which the root exists and no container references it yet.
// Another launch's pass during that window would reap a root that is about to
// become live. An hour is far more than that window and far less than any
// retention horizon, which is exactly what a race guard should be.
const PrefixRootGrace = time.Hour

// prefixBinMountDest is the container-side path the launcher always bind-mounts
// the prefix's bin/ at. Spelled here rather than imported from internal/cli/run
// because prune must not depend on the run pipeline; the two are pinned together
// by TestPrefixBinMountDestMatchesTheLauncher.
const prefixBinMountDest = "/opt/yolo-jail/bin"

// LivePrefixSources returns the host directories that running jails are
// executing their yolo binaries from, and whether that could be determined.
//
// It asks the RUNTIME, which is the only thing that knows. For each live
// container it reads the source of the bind mount at prefixBinDest — the
// container-side path the launcher always mounts the prefix's bin/ at — so the
// answer is "which host directory is this jail's pid1 actually running from"
// rather than anything inferred from a ledger.
//
// known=false when the runtime could not be enumerated, and every caller must
// treat that as "reap nothing". Unlike the image roots, this pass has an
// authority and therefore keeps its tri-state.
func LivePrefixSources(rt string, live runtime.LiveSet, prefixBinDest string, run RunFunc) (map[string]bool, bool) {
	if !live.Known {
		return nil, false
	}
	sources := map[string]bool{}
	for name := range live.Names {
		src, ok := inspectMountSource(rt, name, prefixBinDest, run)
		if !ok {
			// One container that cannot be inspected makes the whole answer
			// unusable: a root this jail needs might be the one we cannot see.
			return nil, false
		}
		if src != "" {
			sources[src] = true
		}
	}
	return sources, true
}

// PrefixStorePathOf maps a mounted prefix bin/ directory back to the store path
// its root is keyed by: <storePath>/opt/yolo-jail/bin -> <storePath>.
//
// Returns "" for a source that is not shaped like a built prefix — a launch
// running from a bundle's own bin/linux-<arch> is mounted from an installed
// bundle rather than a store path, and has no root to correlate.
func PrefixStorePathOf(mountSource string) string {
	suffix := "/" + image.JailPrefixSubdir + "/bin"
	if !strings.HasSuffix(mountSource, suffix) {
		return ""
	}
	return strings.TrimSuffix(mountSource, suffix)
}

// PruneOrphanPrefixRoots reaps prefix GC roots no running jail is executing
// from. Returns the roots removed (or that WOULD be, in dry-run).
//
// liveKnown==false reaps nothing. A root younger than PrefixRootGrace is kept
// regardless, covering the registration-before-container window described above.
func PruneOrphanPrefixRoots(rootsDir string, liveSources map[string]bool, liveKnown bool, apply bool, now time.Time) []string {
	reaped := []string{}
	if !liveKnown {
		return reaped
	}
	// The live SOURCES are directories; the roots are keyed by the store path.
	liveKeys := map[string]bool{}
	for src := range liveSources {
		if sp := PrefixStorePathOf(src); sp != "" {
			liveKeys[image.ImageStoreKey(sp)] = true
		}
	}
	entries, err := os.ReadDir(rootsDir)
	if err != nil {
		return reaped
	}
	for _, e := range entries {
		link := filepath.Join(rootsDir, e.Name())
		st, err := os.Lstat(link)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if liveKeys[e.Name()] {
			continue // a jail is running from this prefix right now
		}
		if now.Sub(st.ModTime()) < PrefixRootGrace {
			continue // registered moments ago; its container may not exist yet
		}
		reaped = append(reaped, link)
		if apply {
			_ = os.Remove(link)
		}
	}
	return reaped
}
