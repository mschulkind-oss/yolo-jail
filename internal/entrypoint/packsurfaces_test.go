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
	orig := ctxRoot
	ctxRoot = root
	t.Cleanup(func() { ctxRoot = orig })
	dir := filepath.Join(root, "host-"+pack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
