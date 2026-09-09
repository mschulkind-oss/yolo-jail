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
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

var (
	allSurfacesOnce sync.Once
	allSurfacesMan  *manifest.Manifest
)

// surfaceManifest is core's surfaces merged with every embedded pack's.
//
// Cached: the commands here call it several times per invocation and each merge walks
// every pack's declarations. A pack surface REPLACES a core one with the same
// (agent, name) — manifest.Merge's rule — so a pack can override, which is the same
// "later wins" ordering packs already use everywhere else.
//
// Through packload.Embedded rather than a MaterializeEmbedded of its own: this file used
// to copy the whole embedded tree into a second `yolo-cli-packs-` temp dir it never
// removed, so `yolo config ls` left TWO directories behind instead of one. The packs are
// registered by internal/packreg's init (internal/config imports it, and this package
// cannot avoid internal/config), and configdiff_test.go's copilot/claude lookups fail
// outright if that registration ever stops reaching here.
func surfaceManifest() *manifest.Manifest {
	allSurfacesOnce.Do(func() {
		var extra []manifest.Surface
		// Empty when materialization failed, which keeps the fallback below: core's own
		// surfaces rather than a refusal.
		for _, p := range packload.Embedded() {
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

var (
	computedOnce sync.Once
	computedSet  map[manifest.SurfaceKey]bool
)

// surfaceHasComputedLayer reports whether the boot render hands this surface a per-boot
// dynamic (`computed`) layer — the MCP/LSP/provider tables and the mise pins, as opposed
// to the static layers a manifest carries.
//
// DERIVED, and that is the point of it. This was a hand-maintained
// `map[string]bool{"claude/settings": true, …}` sitting beside `config ls` (with a `host`
// twin next to it), restating what the render knows structurally — the payoff
// docs/design/host-render-target.md §3.4 promised and step 3 shipped without. It had
// drifted, and by three surfaces: `config ls` reported no computed layer for pi/settings,
// pi/models or claude/config (pi/models composes from nothing else, so it listed with an
// EMPTY layer stack), and `config render --explain` omitted its "this surface also has a
// computed layer, not shown" note for the two of those it renders. A map keyed on identity
// cannot answer for a surface nobody remembered to add, which is why the answer now comes
// from the packs.
//
// Two sources, because there are two producers and no third:
//
//   - a PACK surface's dynamic layer comes from its pack's `yolo.derive(agent, surface,
//     fn)` registration, read by packload.DerivedSurfaces;
//   - a CORE surface's comes from core's own Go (mise/config, via
//     entrypoint.ConfigureMisePrism), declared at agentcfg.CoreComputedSurfaces.
//
// Cached like surfaceManifest, for the same reason and beside it: `config ls` asks per
// surface, and each answer costs one run of a pack's registration script.
func surfaceHasComputedLayer(s manifest.Surface) bool {
	computedOnce.Do(func() {
		computedSet = map[manifest.SurfaceKey]bool{}
		for _, k := range agentcfg.CoreComputedSurfaces() {
			computedSet[k] = true
		}
		for _, p := range packload.Embedded() {
			derived, err := packload.DerivedSurfaces(p)
			if err != nil {
				// A pack whose derive.lua will not even register is a yolo bug for an
				// embedded pack, and the boot path fails loudly on the same input (A12).
				// These are read-mostly reporting commands: show what the rest of the
				// corpus says rather than refusing to list anything — the same
				// disposition surfaceManifest takes for a broken embedded pack.
				continue
			}
			for _, k := range derived {
				computedSet[k] = true
			}
		}
	})
	return computedSet[s.Key()]
}
