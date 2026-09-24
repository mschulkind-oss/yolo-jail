package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// On macos-user the embedded-pack cache resolves to $HOME/.local/share/yolo-jail/…, and
// ~/.local is one of the LINKS the darwin home layout lays into the workspace sidecar. A
// pack read before InstallDarwinHomeLayout would MkdirAll ~/.local as a REAL directory on a
// fresh sandbox account, and the layout never removes a real directory (OQ-HT2): every later
// launch would refuse, remedied only by deleting the account home.
//
// So the order is pinned here with the cache base at its PRODUCTION resolution under the
// bootstrap's HOME (the test binary would otherwise use cmd/go's $WORK and never touch it).
// Add a pack read above the layout step and ~/.local stops being a symlink.
func TestDarwinBootstrapLaysLocalBeforeAnyEmbeddedTree(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	ws := filepath.Join(base, "workspace")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Staged BEFORE the base moves: the fixture reads a real embedded manifest, and that
	// read is the test's, not the boot's.
	packRoot := stagePackForBootstrap(t, "claude")
	t.Cleanup(packload.OverrideEmbeddedCacheDir(paths.EmbeddedPacksDirUnder(home)))
	sidecar := filepath.Join(ws, ".yolo", "home")
	e := DarwinEnvFrom(map[string]string{
		"HOME":                  home,
		"JAIL_HOME":             home,
		"YOLO_HOST_DIR":         ws,
		"YOLO_BLOCK_CONFIG":     `[]`,
		"YOLO_MISE_TOOLS":       `{}`,
		"YOLO_PACK_ROOT":        packRoot,
		"YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv:    sidecar,
		"MISE_DATA_DIR":         filepath.Join(home, ".yolo", "mise"),
	}, home)
	e.Stderr = &strings.Builder{}
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	fi, err := os.Lstat(filepath.Join(home, ".local"))
	if err != nil {
		t.Fatalf("~/.local missing after the bootstrap: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.local is a real directory, not the layout's link into %s: something read "+
			"an embedded pack (creating %s) before InstallDarwinHomeLayout, and the layout "+
			"refuses a real directory on every later launch", sidecar, paths.EmbeddedPacksDirUnder(home))
	}
	// Whatever tree the boot did write landed in the workspace tier, through the link.
	if root, _ := packload.EmbeddedLocation(); root != "" {
		resolvedSidecar, _ := filepath.EvalSymlinks(sidecar)
		if !strings.HasPrefix(root, resolvedSidecar+string(filepath.Separator)) {
			t.Errorf("the boot's embedded tree %s is not in the workspace sidecar %s", root, resolvedSidecar)
		}
	}
}
