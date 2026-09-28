package prune

import (
	"strings"
)

// THERE IS NO ProtectedImageTags ANY MORE, and this is where it was.
//
// It read the load sentinel as image TAGS, and it was what `PruneOldImages`
// vetoed with: the
// content tag of every recently-USED store path, plus the legacy `latest` tag.
// It shipped with C2, which armed a pass that had never fired, and `4064f720`
// gave it the tri-state that kept it from failing open.
//
// OQ-LS3 (docs/reference/image-retention.md#why-its-this-way, ruled
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
//
// Its sibling ProtectedImagePaths — the same ledger as store paths, read by the
// store GC's sentinel-based rooting check — went with that check on 2026-09-28
// (OQ-LS4, docs/reference/image-retention.md): once the GC-root reaper holds a
// running image's root by liveness, refusing the GC for a closure no container
// runs protected a cache, not a jail. The sentinel's one remaining reader is the
// load diagnosis in internal/image.

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
