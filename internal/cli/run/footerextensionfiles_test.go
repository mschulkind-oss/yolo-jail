package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestFooterAdapterFilesLeaveTheAgentsOwnDirsAlone is TestCopilotFooterScriptLeavesCopilotStateAlone
// for the other two agent-footer adapters that land a single file in a home directory an agent
// also uses (docs/design/agent-footer.md §2.1): omp's extension and opencode's TUI plugin.
// preparePackFiles' one-time migration from the pre-ledger mountpoints archives every
// unclaimed zero-byte file in the DIRECTORY that holds a single-file target. omp's
// extension directory holds the user's own extensions and ~/.oh-omp is workspace state, so
// the footer file sits in a subdirectory of its own; opencode's config directory is
// opencode's, so its plugin sits in ~/.config/opencode/yolo/.
//
// Each case seeds an empty file of the user's in the agent's own directory and fails if the
// first launch with the pack moves it out.
//
// ONLY THE BACKENDS THAT PREPARE A TARGET CAN PIN ITS PLACEMENT. On podman the workspace overlay
// holds only the selected packs' declared state dirs, and preparePackFiles considers a target
// only under one of them (packFilesWorkspaceRel). ~/.oh-omp is one; opencode declares none, so
// on podman the launch never prepares opencode's plugin at all and a case there would pass with
// any placement. That case is therefore the assertion that podman still prepares nothing for
// opencode, which is why the user's directory is safe there; Apple Container, which binds the
// overlay over the whole home, is the case that pins opencode's placement.
func TestFooterAdapterFilesLeaveTheAgentsOwnDirsAlone(t *testing.T) {
	cases := []struct {
		pack string
		// userDir is the agent's own directory, home-relative, where the user's file sits.
		userDir string
		// runtimes are the backends whose launch prepares this pack's footer target.
		runtimes []string
	}{
		{"omp", ".oh-omp/agent/extensions", []string{"podman", "container"}},
		{"opencode", ".config/opencode", []string{"container"}},
	}
	t.Run("opencode/podman prepares nothing", func(t *testing.T) {
		packs := []*packload.Pack{officialPack(t, "opencode")}
		writable := packload.WritableDirs(packs)
		for _, target := range packFilesTargets(packs) {
			if rel, ok := packFilesWorkspaceRel(target.Dest, writable, "podman"); ok {
				t.Errorf("podman now prepares opencode's %s in the workspace overlay (at %s): add podman to "+
					"opencode's runtimes above, so its placement is pinned there too", target.Dest, rel)
			}
		}
	})
	for _, c := range cases {
		p := officialPack(t, c.pack)
		for _, rt := range c.runtimes {
			t.Run(c.pack+"/"+rt, func(t *testing.T) {
				wsState := filepath.Join(t.TempDir(), ".yolo", "home")
				// podman binds <wsState>/<dir without its leading dot> over a declared state
				// dir; Apple Container binds wsState over the whole home.
				dir := filepath.Join(wsState, filepath.FromSlash(c.userDir))
				if rt == "podman" {
					dir = filepath.Join(wsState, filepath.FromSlash(c.userDir[1:]))
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				empty := filepath.Join(dir, "users-own-empty.js")
				if err := os.WriteFile(empty, nil, 0o644); err != nil {
					t.Fatal(err)
				}

				archived := preparePackFiles([]*packload.Pack{p}, wsState, rt)
				if len(archived) != 0 {
					t.Errorf("the first launch with the %s pack archived %v: the user's file in %s is not a "+
						"pack-file mountpoint", c.pack, archived, c.userDir)
				}
				if !fileIsEmptyRegular(empty) {
					t.Errorf("the user's empty file %s is gone after preparePackFiles", empty)
				}
			})
		}
	}
}
