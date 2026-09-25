package run

// shareddirsources.go creates, in the machine store (<state>/home, paths.GlobalHome), the
// bind SOURCE of every machine-scope shared dir the SELECTED packs declare
// (packload.SharedDirs: a pack's `state` contribution with `scope: machine`, such as
// `.claude-shared-credentials`).
//
// Both container backends bind each one from there at /home/agent/<dir> (assembleRunCmd for
// podman, appleContainerBaseMounts for Apple Container), and podman refuses to start the
// whole container, with a bare "statfs …: no such file or directory", when a bind source is
// missing. storage.EnsureGlobalStorage creates the SHIPPED packs' shared dirs
// (packload.EmbeddedSharedDirs), because it runs before any config is loaded; a CONFIGURED
// pack's shared dir was therefore named in the argv with no directory behind it. This is the
// launch-path half, over the loaded selection, so the set created is the set bound.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ensureSharedDirSources MkdirAlls <state>/home/<dir> for every shared dir packs declares.
//
// An existing directory is left exactly as it is: it holds the machine's shared credentials,
// which every jail selecting the pack reads and writes. A dir that is not a path inside the
// store is refused rather than created, although packdecl already refuses one: this runs on
// the host, and a path that leaves the store is a host write.
func ensureSharedDirSources(packs []*packload.Pack) error {
	store := paths.GlobalHome()
	for _, dir := range packload.SharedDirs(packs) {
		clean := filepath.Clean(filepath.FromSlash(dir))
		if dir == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("a selected pack declares the machine-scope shared dir %q, which is not "+
				"a path inside the machine store %s", dir, store)
		}
		src := filepath.Join(store, clean)
		if err := os.MkdirAll(src, 0o755); err != nil {
			return fmt.Errorf("cannot create %s, the bind source of the machine-scope shared dir ~/%s: %w",
				src, dir, err)
		}
	}
	return nil
}
