package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// A user's pack is named by its source, `<source>/<name>` (config-ref's packs entry), so its name
// holds a slash; its host session directory is still one child of the root, named so the capture
// path's prefix match (captureHostSessionHomePath) finds the pack again, and a name that merely
// starts like another pack's never matches it.
func TestAHostSessionDirForASlashedPackNameIsOneChildOfTheRoot(t *testing.T) {
	root := t.TempDir()
	dir, lease, err := makeHostSessionDir(root, "packs/pi")
	if err != nil {
		t.Fatalf("makeHostSessionDir(packs/pi): %v", err)
	}
	defer lease.Close()
	if filepath.Dir(dir) != root {
		t.Errorf("%s is not a direct child of %s", dir, root)
	}
	name := filepath.Base(dir)
	if !strings.HasPrefix(name, hostSessionDirPrefix("packs/pi")) {
		t.Errorf("%s does not start with the pack's prefix %q", name, hostSessionDirPrefix("packs/pi"))
	}
	if strings.HasPrefix(name, hostSessionDirPrefix("packs")) {
		t.Errorf("%s matches the prefix of a pack named packs too", name)
	}
}
