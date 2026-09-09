package image

import (
	"io"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// prefixroot.go roots the INSTALL PREFIX — the directory a jail's own yolo
// binaries are bind-mounted from since C8, pid1 included.
//
// WHY IT IS NOT AN IMAGE ROOT, and why it lives in its own directory
// (disk-levers-and-backfill.md OQ-BF4). Two reasons, and the second is the one
// that bites:
//
//  1. build/roots/ is enumerated by prune.PruneOrphanImageRoots, which since
//     OQ-LS1 reaps on AGE alone. That is right for an image closure — losing one
//     costs a rebuild — and exactly wrong here: the longer a jail runs, the more
//     certainly its own binaries would be reaped out from under it.
//  2. A prefix root is held by LIVENESS, not by recency or age. The question is
//     "is a container executing from this directory right now", and the runtime
//     answers it directly.
//
// The one test that decides which policy any root gets: CAN LOSING IT COST ONLY
// A REBUILD? Image closure — yes, so age. A running jail's prefix — no, it is a
// process losing the file behind its own pid1, so liveness.
//
// Sibling precedent: build/package-roots (storepackages.go's
// storeProfileRootLink) is a second directory chosen for the same reason — to
// escape PruneOrphanImageRoots' reach. paths.PackageRootsDir's doc states that
// rule; this is its third instance.

// PrefixRootsDir is where per-prefix durable nix GC roots live:
// BUILD_DIR/prefix-roots/<sha16>. Deliberately NOT build/roots.
func PrefixRootsDir() string {
	return filepath.Join(paths.BuildDir(), "prefix-roots")
}

// PrefixRootLink is the durable GC-root symlink for one prefix store path,
// whether or not it exists yet. Keyed by ImageStoreKey so a reaper can
// correlate a root with the path it pins with no reverse lookup — the same
// property build/roots has, against a different directory.
func PrefixRootLink(prefixStorePath string) string {
	return filepath.Join(PrefixRootsDir(), ImageStoreKey(prefixStorePath))
}

// RegisterPrefixRoot pins the install prefix's closure for as long as this jail
// might run.
//
// It is RegisterImageRoot's mechanism against a different directory, and it is
// deliberately a separate function rather than a parameter: the two roots have
// opposite retention policies, and one call site accidentally passing the wrong
// directory is the whole failure mode. Same host-only rule — from inside a jail
// /nix/var/nix/gcroots is not mounted and the host daemon prunes a root pointing
// into the jail's /home tree as stale — so callers gate on !inJail exactly as
// the image root's do.
//
// Best-effort, same as the image root: an unrooted-but-running jail is the state
// this fixes, not a regression to hard-fail on.
func RegisterPrefixRoot(prefixStorePath string, out io.Writer) (string, error) {
	if out == nil {
		out = io.Discard
	}
	return registerGCRoot(PrefixRootsDir(), PrefixRootLink(prefixStorePath), prefixStorePath, out,
		"could not register a GC root for the jail's own binaries "+
			"(a nix-collect-garbage could delete the prefix this jail runs from)")
}
