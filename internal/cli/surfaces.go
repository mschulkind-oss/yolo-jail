package cli

// surfaces.go resolves the FULL surface set the host-side config commands operate on:
// core's own surfaces plus every surface the loaded packs declare.
//
// It exists because `agentcfg.BuiltinManifest()` no longer answers the question. That
// function used to return eleven surfaces covering six agents; it now returns core's own
// (mise/config), because every agent surface moved into the pack that owns it. So `yolo
// config ls`, `render` and `diff` need the packs — and unlike the old Go list, which
// packs exist depends on the user's config.
//
// EMBEDDED PACKS ONLY, and that is a real limitation worth stating rather than papering
// over: a CONFIGURED pack's surfaces are not listed here, because resolving one means
// reading the pack store and a `yolo config ls` that failed on an unreachable git remote
// would be worse than one that lists a subset. The boot path does see them (it renders
// from the staged tree), so a configured pack's surface is rendered but not listed. When
// that gap bites, the fix is to read the same staged tree the jail mounts, not to make
// this fetch.

import (
	"os"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

var (
	allSurfacesOnce sync.Once
	allSurfacesMan  *manifest.Manifest
)

// surfaceManifest is core's surfaces merged with every embedded pack's.
//
// Cached: the commands here call it several times per invocation and materializing the
// embedded packs copies a tree. A pack surface REPLACES a core one with the same
// (agent, name) — manifest.Merge's rule — so a pack can override, which is the same
// "later wins" ordering packs already use everywhere else.
func surfaceManifest() *manifest.Manifest {
	allSurfacesOnce.Do(func() {
		var extra []manifest.Surface
		dir, err := os.MkdirTemp("", "yolo-cli-packs-")
		if err != nil {
			allSurfacesMan = agentcfg.BuiltinManifest()
			return
		}
		packs, problems := packload.MaterializeEmbedded(officialpacks.FS, dir)
		if len(problems) == 0 {
			for _, p := range packs {
				surfaces, probs := p.Surfaces()
				if len(probs) > 0 {
					// A broken embedded pack is a yolo bug. The config commands are
					// read-mostly reporting tools, so they show what they can rather than
					// refusing; the boot path fails loudly on the same input (A12), which
					// is where a hard stop belongs.
					continue
				}
				extra = append(extra, surfaces...)
			}
		}
		m, merr := agentcfg.ManifestWith(extra...)
		if merr != nil {
			// Fall back to core's own surfaces rather than panicking in a reporting
			// command.
			m = agentcfg.BuiltinManifest()
		}
		allSurfacesMan = m
	})
	return allSurfacesMan
}
