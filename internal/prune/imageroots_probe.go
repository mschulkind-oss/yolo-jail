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

// THERE IS NO ProtectedImageTags ANY MORE, and this is where it was.
//
// It read the SAME sentinel ledger as ProtectedImagePaths above, as image TAGS
// instead of store paths, and it was what `PruneOldImages` vetoed with: the
// content tag of every recently-USED store path, plus the legacy `latest` tag.
// It shipped with C2, which armed a pass that had never fired, and `4064f720`
// gave it the tri-state that kept it from failing open.
//
// OQ-LS3 (docs/design/the-load-sentinel-is-not-a-liveness-oracle.md §6.2, ruled
// 2026-09-08) replaced it with prune.CurrentImageTags — one CURRENT-IMAGE
// POINTER per workspace, written by the launch path — for the reason the whole
// doc is about: a bounded most-recently-used list answers "what would I like to
// still have", and image retention is asking "which image does each
// configuration still want". Ten machine-wide entries answer that only by
// accident, and the accident stops holding on the fourth workspace.
//
// Everything that outlived it moved WITH it and lives in currentimages.go: the
// unconditional `latest` tag for the degraded offline launch, the compare-tags-
// not-refs rule, and the tri-state whose known=false declines the pass.
// ProtectedImagePaths stays, because the store-GC rooting confirmation
// (UnrootedProtectedPaths) is a cache question and recency is a fair answer to
// one — that is OQ-LS1's ruling, not an oversight.

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
