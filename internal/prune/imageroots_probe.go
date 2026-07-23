package prune

import (
	"path/filepath"

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
