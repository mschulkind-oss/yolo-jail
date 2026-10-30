package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMain resolves the binary's TMPDIR before any test mints a directory. The edit
// resolver binds the workspace through filepath.EvalSymlinks on purpose, so a fixture
// workspace under macOS's /var (a link to /private/var) never equals the path the resolver
// reports, and every target comparison here failed on darwin while passing on Linux.
func TestMain(m *testing.M) {
	testsupport.UsePhysicalTempDir()
	os.Exit(m.Run())
}

// TestTempDirIsPhysicalHere pins the TestMain call above: without it the target comparisons
// in this package pass on Linux and fail on a host whose temp directory is a symlink.
func TestTempDirIsPhysicalHere(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != dir {
		t.Fatalf("t.TempDir() = %q resolves to %q; this package's TestMain must call testsupport.UsePhysicalTempDir", dir, resolved)
	}
}
