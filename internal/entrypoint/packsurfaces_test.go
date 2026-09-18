package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// withCtxRoot points the /ctx root at a temp dir and returns the per-pack subdir a host
// file for that pack would be mounted into, creating it.
//
// The layout MATTERS and is not a fixture detail: packload.CtxPath decides it, the host
// CLI emits that mount destination, and the entrypoint reads the host layer from it. A
// test that invented its own layout would pass while the two real sides disagreed.
func withCtxRoot(t *testing.T, root, pack string) string {
	t.Helper()
	// THROUGH THE PRODUCTION SEAM, not a package var: YOLO_CTX_ROOT is what Apple Container
	// actually sets and what ctxRootDir reads, so a fixture that set the var instead would
	// stop exercising the one path a real backend takes.
	t.Setenv("YOLO_CTX_ROOT", root)
	dir := filepath.Join(root, "host-"+pack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
