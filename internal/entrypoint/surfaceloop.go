package entrypoint

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// surfaceloop.go is THE RENDER LOOP (docs/plans/notch-convergence.md item 24, row D3): the one
// walk over a pack's declared surfaces that every notch writing them runs — the jail boot
// (ConfigurePackSurfaces), `yolo check`'s single-pack probe (configureOnePack) and the host apply
// (RenderHostPack) — and the one dispatch that writes a surface through a mechanism.
//
// Before it there were four loops that agreed only by inspection. The jail boot and the check
// probe each folded the posture, walked the surfaces and dispatched on the DECLARED mode; the
// host apply folded the posture a second way, walked the surfaces and dispatched on the
// census's mechanism (render.ModeSet.Mechanism); `yolo config render --at host` composed the
// jail's surfaces a fourth way (item 23 routed it through the host apply's render). What they had
// to agree about — which surfaces a pack declares at this target's posture, which contributions
// reach each one, which mechanism writes it, and whether a config-list is refused on it — is now
// decided HERE, once, and the notch is an input: the Env's render target supplies the posture bit
// and the census, and each notch supplies its own layers (the jail: a derive over the live tables
// and the staged host bytes; the host: the declared tables and no host layer) and its own
// disposition of a failure (the jail boot collects it through genStep, the check probe stops at
// it, the host apply reports it as a result row).
//
// WHAT IS NOT HERE, and why. The jail's `yolo config render` preview is a reader, not a writer:
// it composes a surface without its computed layer and capture overlay (its documented scope,
// renderSurface in internal/cli/config.go) and it previews core surfaces (mise/config) no pack
// declares, so it has no mechanism to ask and no pack walk to share. Recorded as NC-D29 in the
// plan. `host_files` entries have their own four modes (hostfiles.go), which are the user's
// vocabulary rather than the engine's, and reach the same two writers directly.

// surfacePlan is one declared surface as the one loop decided it for one target.
type surfacePlan struct {
	pack *packload.Pack
	// surface is the declaration with the target's posture folded in (SurfacesForReport),
	// not yet prepared for the target: ${workspace} is still a placeholder, because the jail
	// binds it and the host prunes it, and the host has to NAME what it pruned.
	surface manifest.Surface
	// contribs are the config-overlay layers and config-list entries other packs contribute to
	// this surface, resolved cross-pack by the caller's packoverlay.Collect.
	contribs *surfaceContribs
	// unrendered is a surface declared `unrendered`: honored at every notch by writing nothing.
	unrendered bool
	// mechanism is the engine mechanism the TARGET's census runs this surface through, and
	// decided is false when the census declines the declared mode (modes.Excludes says why).
	// Both are zero for an unrendered surface.
	mechanism string
	decided   bool
	// listedIn is the surface whose list the declaration's `whenListed` reads, resolved from
	// the same pack (packload refuses a name it does not declare earlier), or nil when the
	// surface renders unconditionally.
	listedIn *manifest.Surface
}

// planPackSurfaces is the loop's head: p's surfaces at the Env's render target, each with its
// contributions and the mechanism the target's census runs it through. problems are malformed
// declarations (fatal at every notch, in each notch's own way). A posture patch naming a surface
// p does not declare is not among them and is not dropped: it is a posture overlay, which the
// caller's packoverlay.Collect placed in overlays (or reported as an orphan), so it reaches the
// owner's plan through contribsFor like any config-overlay (OQ-3, NS-D22).
func planPackSurfaces(e *Env, p *packload.Pack, overlays *packoverlay.OverlaySet) ([]surfacePlan,
	[]string) {
	surfaces, problems, _ := p.SurfacesForReport(e.renderTarget().Profile().AgentAutonomy)
	plans := make([]surfacePlan, 0, len(surfaces))
	byKey := make(map[manifest.SurfaceKey]int, len(surfaces))
	for i, s := range surfaces {
		byKey[s.Key()] = i
		pl := planSurface(e, p, s, contribsFor(overlays, s.Agent, s.Name))
		if c := s.WhenListed; c != nil {
			if j, ok := byKey[c.Key()]; ok && j < i {
				named := surfaces[j]
				pl.listedIn = &named
			}
		}
		plans = append(plans, pl)
	}
	return plans, problems
}

// listUnmet is the `whenListed` gate at render time (manifest.Surface.WhenListed): "" when
// the surface renders, otherwise why it does not. It reads the named surface's file as this
// render left it — the loop renders a pack's surfaces in declaration order, and packload
// requires the named one to come first — with that surface's own codec. Every way of not
// finding a matching entry (no file, no list, a file that does not decode) is the same
// answer, unselected: the condition exists to stay silent until the agent loads the thing it
// names.
//
// A condition whose surface did not resolve cannot reach here from a packload-read pack; it
// answers unselected too, rather than rendering a file nothing asked for.
func (pl surfacePlan) listUnmet(e *Env) string {
	c := pl.surface.WhenListed
	if c == nil {
		return ""
	}
	if pl.listedIn != nil {
		data, _ := os.ReadFile(expandHomePath(e, pl.listedIn.Path))
		if _, held := c.Holds(agentcfg.DecodeSurfaceObject(pl.listedIn.Codec, data)); held {
			return ""
		}
	}
	return fmt.Sprintf("%s/%s not rendered: no entry of %s %s matches %q", pl.surface.Agent,
		pl.surface.Name, c.Surface, c.Path, c.Matches)
}

// planSurface is one surface's plan at the Env's render target: `unrendered` honored by
// writing nothing, every other mode run through the mechanism the target's census names for
// it. planPackSurfaces is its one caller, so no second planner decides a mechanism another
// way (TestEveryPackSurfaceWriterRunsTheOneLoop).
func planSurface(e *Env, p *packload.Pack, s manifest.Surface, contribs *surfaceContribs) surfacePlan {
	pl := surfacePlan{pack: p, surface: s, contribs: contribs}
	if s.ResolvedMode() == manifest.ModeUnrendered {
		pl.unrendered = true
	} else {
		pl.mechanism, pl.decided = e.renderTarget().Modes().Mechanism(s.ResolvedMode())
	}
	return pl
}

// listRefusal is OQ-AL1 at every notch: a config-list contribution on a path whose mechanism
// does not capture per entry is refused, naming the surface and its mode, rather than composed
// into a capture that would freeze the list. s is the surface as the caller will render it. ""
// when no list targets the surface or the mechanism keeps one per entry.
func (pl surfacePlan) listRefusal(s manifest.Surface) string {
	if len(pl.contribs.listContribs()) == 0 {
		return ""
	}
	return agentcfg.ListCaptureRefusal(pl.mechanism, s)
}

// surfaceLayers are the per-target layer inputs a surface's write composes: the staged host
// bytes (nil where the notch has no host layer), the computed layer, and the tables the
// computed layer declares in full (read by the stateful writer alone, the one that adopts).
type surfaceLayers struct {
	hostBytes []byte
	computed  map[string]any
	inFull    []string
}

// surfaceWrite is what one write through a mechanism reports back beside its error.
type surfaceWrite struct {
	// archived is where a stateful write copied the pre-existing file before adopting it
	// (OQ-CO7), "" for every write that adopted nothing.
	archived string
	// firstMigration is a stateful write that migrated the surface onto the capture model,
	// the moment the jail boot retires the sidecars an older layout left.
	firstMigration bool
}

// writeSurfaceThrough is THE MECHANISM DISPATCH: it writes s through the named engine
// mechanism, the one the target's census chose (surfacePlan.mechanism). Every notch that
// writes a pack surface reaches the writers through here, so a mechanism added to the census is
// added here once rather than at each notch's own switch.
//
// A refusal comes back as the writer's *rmwRefusedError, and what it means is the notch's
// decision: a warning at the jail boot (the file is the agent's and was left alone), a
// `refused:` row at the host apply.
func writeSurfaceThrough(e *Env, mechanism string, s manifest.Surface, l surfaceLayers,
	contribs *surfaceContribs) (surfaceWrite, error) {
	switch mechanism {
	case manifest.ModeComputed:
		_, err := renderSurfaceStatelessSurface(e, s, l.hostBytes, l.computed, contribs)
		return surfaceWrite{}, err
	case manifest.ModeRMW:
		return surfaceWrite{}, renderSurfaceRMWSurface(e, s, l.computed, contribs)
	case manifest.ModeStateful:
		sr, err := renderSurfaceStatefulDetail(e, s, l.hostBytes, l.computed, l.inFull, contribs)
		var w surfaceWrite
		if sr != nil {
			w.archived = sr.archived
			if sr.out != nil {
				w.firstMigration = sr.out.FirstMigration
			}
		}
		return w, err
	default:
		// Unreachable from a census: every ModeSet runs a subset of the four engine modes and
		// the unrendered one never reaches a write. Fails closed, writing nothing.
		return surfaceWrite{}, fmt.Errorf("surface %s/%s: no writer for mechanism %q",
			s.Agent, s.Name, mechanism)
	}
}

// renderPackSet is the JAIL half of the one loop, for both of its entries: the boot
// (ConfigurePackSurfaces, over every staged pack) and `yolo check`'s probe (configureOnePack,
// over one). packs is the set the derive's selection resolves over — the built-in capability
// half answers by which pack installs a surface's agent. collect gathers the cross-pack
// contributions at the target's posture and the launch's active profile set (both handed to
// it, so the gate and the derive read one resolution); it runs after the MCP table loads, the
// order the boot's notices have always come in.
//
// step is the entry's FAILURE DISPOSITION, and the only thing the two entries differ in: the
// boot runs every step through genStep, which records a failure and carries on so one boot
// reports every broken surface (A12); the check probe runs each step and stops at the first
// error. A non-nil return from step ends the walk with that error.
func renderPackSet(e *Env, packs []*packload.Pack,
	collect func(autonomy bool, activeProfiles map[string][]string) *packoverlay.OverlaySet,
	step func(name string, run func() error) error) error {
	// The MCP table is PER AGENT (loadMCPTables): a server whose requires_env names a
	// provider-claimed variable is written only for the agents whose own env file carries it.
	mcp := loadMCPTables(e)
	tables := liveTables(e, mcp.shared)
	// The active profile table (YOLO_USE_PROFILES), keyed by CLI name, and the resolved
	// table (YOLO_PROFILES) the same launch lowered in: what every profile NAME means, user
	// declarations included. The selection below reads both — the pack manifests cannot answer
	// for a name only the user declares, and re-deriving here would be the second
	// implementation of ResolveProfiles the one-composition rule forbids.
	useProfiles := e.LoadUseProfiles()
	profiles := packload.ProfileTable(useProfiles)
	// Each agent's whole ACTIVE SET off the same table (docs/design/active-provider-sets.md
	// §4.3): the primary is profiles' entry, and a set-capable derive reads the rest as
	// ctx.active_set.
	sets := packload.ProfileSets(useProfiles)
	resolved := e.LoadProfiles()
	// The §4.2 autonomy policy comes from THIS target's confinement profile — the same
	// render.ProfileFor table the host render reads (plan §6c step 1) — never a literal. It
	// resolves to ON for a jail target, so the render is byte-identical
	// (TestRenderFingerprintStable). planPackSurfaces folds the posture from the same target.
	overlays := collect(e.renderTarget().Profile().AgentAutonomy, sets)
	for _, p := range packs {
		plans, problems := planPackSurfaces(e, p, overlays)
		for _, prob := range problems {
			// A malformed surface is fatal: rendering the rest and skipping this one yields a
			// jail whose config is quietly incomplete.
			prob := prob
			if err := step("pack_"+p.Name+"_surfaces", func() error { return fmt.Errorf("%s", prob) }); err != nil {
				return err
			}
		}
		// A pack's derive.lua (if any) produces every dynamic layer for its surfaces — the
		// projection Lua (docs/reference/pack-system.md §7). Read once per pack.
		deriveScript := packload.DeriveScript(p)
		for _, pl := range plans {
			pl := pl
			src := jailLayerSource{
				tables:       tablesForAgent(tables, mcp, pl.surface.Agent),
				deriveScript: deriveScript,
				sel:          surfaceSelectionFor(packs, resolved, profiles, sets, pl.surface),
			}
			if err := step("configure_"+pl.surface.Agent+"_"+pl.surface.Name, func() error {
				return renderPlannedSurface(e, pl, src)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
