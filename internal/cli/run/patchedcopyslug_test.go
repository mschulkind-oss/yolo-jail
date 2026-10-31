package run

import (
	"strings"
	"testing"
)

// TestPatchedCopySlugOfASlashedPackIsOneComponent: a fork key is `<pack>/<bin>`, and a
// readable-config pack's name is a source handle with a "/" of its own (OQ-RC15), so the key holds
// two. The per-launch copy directory, the tree-build log and the cached-good build log are all
// named by this slug, and each must stay one path component, distinct from a key that differs
// only in where its slashes fall.
func TestPatchedCopySlugOfASlashedPackIsOneComponent(t *testing.T) {
	a := PatchedCopySlug("yolo-packs/pi-fork/pi-subagents")
	b := PatchedCopySlug("yolo-packs--pi-fork/pi-subagents")
	if strings.Contains(a, "/") {
		t.Errorf("PatchedCopySlug = %q, holds a path separator", a)
	}
	if a == b {
		t.Errorf("two keys share the slug %q", a)
	}
}
