package prune

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ProtectedImagePaths is the union of store paths currently recorded in every
// runtime's load sentinel (BUILD_DIR/last-load-<runtime>, the LRU-10 of loaded
// image paths — image.ReadLoadedPaths). These are the closures a running or
// recently-run jail depends on; PruneOrphanImageRoots keeps any durable GC root
// pinning one of them. Reads across ALL runtimes (podman + container) so a host
// juggling both never unroots the other's image.
func ProtectedImagePaths(buildDir string) map[string]struct{} {
	protected := map[string]struct{}{}
	for _, rt := range paths.AllRuntimes {
		sentinel := filepath.Join(buildDir, "last-load-"+rt)
		for p := range image.ReadLoadedPaths(sentinel) {
			protected[p] = struct{}{}
		}
	}
	return protected
}

// ProtectedImageTags is the SAME liveness ledger as ProtectedImagePaths, read as
// image TAGS instead of as store paths: the content tag of every recently-used
// store path (image.ImageStoreKey — the identical 16 hex chars JailImageRef puts
// after the colon), plus the legacy `latest` tag.
//
// It exists because C2 armed a pass that had never fired. `PruneOldImages`
// filters by REPOSITORY and removes with `rmi -f`, which also destroys any
// container using the image. While one :latest tag named every image the
// repository filter returned a single row, so `keep=2` could not select
// anything; now every load leaves a permanent per-config tag, the list is as
// long as the number of configs this machine runs, and "keep the newest 2"
// would force-remove the image another workspace's live jail is running —
// killing that session mid-flight. This is the gate PruneOldImages vetoes with.
//
// It is a VETO, not a retention rule. `--keep-images` (default 2) is governed by
// minimal-disk-footprint.md OQ-DF3, still open; nothing here retunes it. The
// pass still computes "everything past the newest N" exactly as before and then
// declines to remove the ones that are in use — the same polarity as
// PruneOrphanImageRoots' guard #2, reading the same sentinel, so the two can
// never disagree about which images are live.
//
// The legacy tag is protected for the branch that still runs it: AutoLoadImage's
// degraded fallback (SkipBuild, or a failed build the operator opted past) has
// no store path to hash, so `<repo>:latest` is the only name it can ask about,
// and it records nothing in the sentinel. Before C2 that image was unreachable
// by this pass in practice; keeping it so preserves an offline launch that
// worked yesterday.
//
// Tags are compared rather than full refs because the runtimes spell the
// repository differently (podman's rows carry the localhost/ prefix, Apple
// Container's do not) while the query has already narrowed every row to the jail
// repository. Comparing the half that is stable is what keeps a prefix mismatch
// from silently reading as "not protected".
func ProtectedImageTags(buildDir string) map[string]struct{} {
	tags := map[string]struct{}{tagOf(paths.JailImage): {}}
	for p := range ProtectedImagePaths(buildDir) {
		tags[image.ImageStoreKey(p)] = struct{}{}
	}
	return tags
}

// tagOf returns the part of a `repo:tag` ref after the LAST colon, or "" when
// there is none. Last-colon rather than first because a registry ref may carry a
// port (`host:5000/name:tag`).
func tagOf(ref string) string {
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return ""
	}
	return ref[i+1:]
}
