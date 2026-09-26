package entrypoint

// Surface derive driving and selection resolution: docs/reference/providers.md

// packsurfaces.go renders every pack-declared surface in one loop, which is what
// replaced the switch on six hardcoded names.
//
// What was here before: `configureAgent(e, agent)` switching on "claude"/"copilot"/
// "opencode"/"pi"/"codex"/"agy" and calling six Go functions. Reading them side by side,
// five were the same three steps in a different order — mkdir the config dir, build a
// computed map, call one of two render helpers — and the sixth (claude) added imperative
// side effects. So the switch was not expressing six behaviors; it was expressing one
// behavior plus per-agent DATA, with the data trapped in Go.
//
// Now the data is in the pack:
//
//	surfaces[].mode       which engine mechanism writes the file
//	surfaces[].computed   which live table feeds it, and how to reshape it
//	surfaces[].path       what to mkdir (its parent)
//
// and this file is the loop. Core no longer knows any agent's name. Adding a seventh
// tool is a pack.json, not a Go change — which is the claim the whole transition rests
// on, so it is worth saying plainly that it is now literally true of this path.
//
// WHAT DID NOT GENERALIZE, stated because a reader will look for it: claude's
// credentials symlink, per-jail history isolation, and plugin install/uninstall are
// imperative side effects, not surface content. They live in packhooks.go behind a named
// capability a pack requests, rather than being reachable by writing an agent's name.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// LoadJailPacks reads the pack trees the launch delivered at YOLO_PACK_ROOT.
//
// DELIVERED, not "mounted": the variable exists because the three backends deliver the
// same tree three ways — a `:ro` bind at /ctx/packs on podman, a per-launch copy under
// the jail's own home on Apple Container (which ignored `:ro` below 1.1.0), and a root-owned copy
// under /var on macos-user. This side reads whichever it was handed and cannot tell them
// apart, which is the point.
//
// Every pack found here was already staged ON THE HOST, and the jail's job is to render
// what it was given. It could not do otherwise: which packs are selected is a fact about
// the user config, which the jail deliberately cannot read, so from in here an embedded
// pack and a `file://` one are two identical directories.
//
// THERE USED TO BE A THIRD ARGUMENT HERE, a `mayAccessHost` the jail had to answer `true`
// to because it had no way to compute the real value — a named constant with a long
// apology attached. OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) deleted the parameter
// along with the gate it fed, so the awkward half-answer has no question left to answer:
// the host honors every pack's declarations, and so does this side.
//
// A pack whose manifest fails to parse in-jail after parsing on the host means the
// mounted tree disagrees with what was staged — corruption, not a user error — so it is
// returned as an error and the boot fails (A12).
func LoadJailPacks(e *Env) ([]*packload.Pack, error) {
	// Read manifests TOLERANTLY in the jail. The host CLI and this entrypoint come from
	// different places — the CLI is freshly built or `go install`ed, the entrypoint is baked
	// into the image at the last `just load` — so a manifest using a field this build does
	// not know is ordinary version skew, not corruption. Refusing it means the jail does not
	// start at all, and when the manifest is one yolo SHIPS there is no way for the user to
	// route around it. See packdecl.DecodeTolerant for the incident that established this.
	packload.TolerateSkew()

	root := e.Getenv("YOLO_PACK_ROOT")
	if root == "" {
		// No packs mounted. Legitimate: an older host launcher, or a jail started with
		// no packs at all. Renders nothing rather than failing.
		//
		// ⚠ macos-user was in that list and is not a reason any more: it sets
		// YOLO_PACK_ROOT from its own staged tree (macosuser/runplan.go). An empty root
		// on that backend means the same thing it means everywhere else — no packs.
		return nil, nil
	}
	var packs []*packload.Pack
	// Two levels: <root>/_official/<name> for the embedded packs, <root>/<slug> for
	// configured ones. Walking both keeps the jail ignorant of which is which — the
	// distinction only ever mattered for the host-side origin gate.
	for _, dir := range []string{filepath.Join(root, "_official"), root} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// ABSENT is normal and reads as empty: a launch with no embedded packs has no
			// _official dir, and there is nothing to render. Any OTHER error is not —
			// a root that is a file, unreadable, or on a mount that did not appear means
			// the host staged packs this process cannot see. That must NOT degrade to
			// "no packs": B-0 was exactly this shape of silence, and a backend that
			// renders zero surfaces while reporting success is the failure mode the A12
			// contract exists to make impossible.
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("pack root %s: %w", dir, err)
		}
		for _, ent := range entries {
			if !ent.IsDir() || ent.Name() == "_official" {
				continue
			}
			p, problems := packload.LoadDir(filepath.Join(dir, ent.Name()), ent.Name())
			if len(problems) > 0 {
				return nil, fmt.Errorf("pack %s: %s", ent.Name(), problems[0])
			}
			// A contribution whose KIND this build does not know was skipped, not
			// fatal (docs/reference/loophole-system.md#strict-and-tolerant-and-why-both):
			// a jail must boot under version skew.
			// Warn each skip by name so the degradation is visible, never silent.
			//
			// warnOnce, not warn: LoadJailPacks is called five times in one boot (pack
			// surfaces, requires, the agent launchers, the bootstrap, the orphan catalog)
			// and re-derives the same notes on every pass, so a single skipped
			// contribution printed five identical lines. The note is a property of the
			// staged manifest, not of the reader that noticed it, and a reader added
			// tomorrow must not make it six.
			for _, note := range p.SkewNotes {
				e.warnOnce(note)
			}
			packs = append(packs, p)
		}
	}
	return packs, nil
}

// ConfigurePackSurfaces renders every surface every loaded pack declares.
//
// Each surface is rendered by the mechanism its own `mode` names, so this function has
// no per-pack branching at all. Failures are collected through genStep, so one boot
// reports every broken surface rather than one per restart (A12).
func ConfigurePackSurfaces(e *Env, packs []*packload.Pack) {
	tables := liveTables(e)
	// The §4.2 autonomy policy comes from THIS target's confinement profile — the same
	// render.ProfileFor table the host render reads (plan §6c step 1) — rather than from
	// the literal `true` that used to sit here and in p.Surfaces(). It resolves to ON for
	// a jail target, so the render is byte-identical (TestRenderFingerprintStable); what
	// changes is that the boot path and the host path now read ONE statement of the policy
	// instead of each carrying its own constant.
	autonomy := e.renderTarget().Profile().AgentAutonomy
	// The active profile table (YOLO_USE_PROFILES), keyed by CLI name. Resolved ONCE and
	// handed to both consumers below, so the pack env fold and the config-overlay gate
	// answer "which profile is active" from one resolution — the same rule the derives
	// follow (every pack sees every key), which is what makes a profile visible to a pack
	// that installs no CLI (packs/zai) rather than only to one with a bin to gate on.
	profiles := packload.ProfileTable(e.LoadUseProfiles())
	// The resolved table (YOLO_PROFILES) the same launch lowered in: what every profile
	// NAME means, user declarations included. The selection below reads it — the pack
	// manifests cannot answer for a name only the user declares, and re-deriving here
	// would be the second implementation of ResolveProfiles the one-composition rule
	// forbids.
	resolved := e.LoadProfiles()
	// config-overlay contributions are collected BEFORE the per-pack loop and across the
	// whole set, because an overlay in pack B targets a surface pack A owns — the only
	// case the kind exists for. Collecting per-pack would find, for that case, exactly
	// none (docs/reference/pack-system.md §6). Since OQ-PT8 the gated overlay
	// IS the profile's config channel — there is no separate variant fold beside it — so
	// this table is that gate's whole input, and the same instance reaches
	// surfaceSelectionFor below, so the gate and the derive cannot disagree.
	overlays := packoverlay.Collect(packs, autonomy, profiles)
	reportOverlayResolution(e, overlays)
	for _, p := range packs {
		surfaces, problems, notes := p.SurfacesForReport(autonomy)
		for _, prob := range problems {
			// A malformed surface is fatal: rendering the rest and skipping this one
			// yields a jail whose config is quietly incomplete.
			genStep(e, "pack_"+p.Name+"_surfaces", func() error { return fmt.Errorf("%s", prob) })
		}
		// A config patch that named no surface of its own pack merged into nothing — the
		// OQ-Z5 shape, where the author's patch reads, to them, exactly like one that
		// folded. Named, never fatal: the render is complete, the patch is merely inert
		// (the same ruling that makes an ownerless config-overlay a warning).
		//
		// warnOnce, not warn, for the reason LoadJailPacks gives: the note is a property
		// of the staged manifest, not of the pass that noticed it, and more than one
		// entry renders the same pack's surfaces (the darwin boot path here,
		// ConfigurePackByName for `yolo check`), so an unchanged manifest would print the
		// same line twice and read as two problems.
		for _, n := range notes {
			e.warnOnce(n.String())
		}
		// A pack's derive.lua (if any) produces every dynamic layer for its surfaces
		// — the projection Lua (docs/reference/pack-system.md §7). Read once per pack;
		// absent means no surface has a dynamic layer. packload owns the reader —
		// the host notch's env derive reads the same file through it.
		deriveScript := packload.DeriveScript(p)
		for _, s := range surfaces {
			surface := s
			genStep(e, "configure_"+surface.Agent+"_"+surface.Name, func() error {
				return renderDeclaredSurface(e, surface, tables, deriveScript,
					surfaceSelectionFor(packs, resolved, profiles, surface),
					contribsFor(overlays, surface.Agent, surface.Name))
			})
		}
	}
}

// reportOverlayResolution surfaces what the overlay collection found, per rulings R2
// (an ownerless overlay is inert AND named) and R3 (an override must be legible).
//
// A MALFORMED overlay is fatal, an ORPHANED one is not, and the split is the ruling:
// "a pack the user simply did not select is not an error", whereas an overlay body that
// redeclares the surface it targets is the author asserting something the mechanism will
// never honor — the same class as a malformed surface, which is already A12-fatal.
//
// The applied overlays get a notice too, and deliberately: an override folding in below
// managed is invisible in the output file, so a boot that says nothing leaves "which pack
// set this key" answerable only from a sidecar. The line names the command that shows it.
func reportOverlayResolution(e *Env, overlays *packoverlay.OverlaySet) {
	for _, prob := range overlays.Problems {
		problem := prob
		genStep(e, "pack_config_overlays", func() error { return fmt.Errorf("%s", problem) })
	}
	for _, orphan := range overlays.Orphans {
		e.warn(fmt.Sprintf("%s  %s (pack %s)", orphan.KindName(), orphan.Reason(), orphan.Pack))
	}
	for _, applied := range overlays.Applied() {
		e.warn(fmt.Sprintf("%s: config-overlay keys from %s (yolo config diff %s)",
			applied.Target, strings.Join(applied.Packs, ", "), applied.Agent))
	}
	// The list twin (pack-system.md#config-list-visibility): an assembled array reads, in the
	// file, exactly like one the owner declared, so which packs appended to it is said at the
	// moment it applies.
	for _, applied := range overlays.AppliedLists() {
		e.warn(fmt.Sprintf("%s: config-list entries from %s (yolo config ls %s)",
			applied.Target, strings.Join(applied.Packs, ", "), applied.Agent))
	}
}

// surfaceSelection is the RESOLVED SELECTION a surface derive sees as
// ctx.profile_name and ctx.selected_provider — the same two fields the env path
// (packload.AgentEnv) hands its producer, set here so a surface derive reads one
// resolution rule instead of re-deriving the provider from ctx.use_profiles in Lua
// (docs/reference/providers.md, §9 OQ-CS3).
//
// "Resolved" is the operative word, and it is why the surface loop computes this rather
// than leaving the derive to read the profile table itself: the provider is NOT
// necessarily the profile's name. The provider comes off the resolved table the launcher
// composed (YOLO_PROFILES) — packload.ProviderFor over LoadProfiles, the ONE rule both
// derive paths answer through, user declarations included — so the fallback a derive can
// write for itself, "index use_profiles by my own agent name", answers a different
// question. The Provider field is "" when no profile is active at
// this agent's CLI name, which is the derive's signal to write nothing (OQ-CS2: the
// no-profile case is the agent's own).
type surfaceSelection struct {
	// Profile is the variant name active at this surface's agent's CLI name —
	// ctx.profile_name; "" when none is.
	Profile string
	// Provider is what that variant delivers — ctx.selected_provider; "" when no
	// variant is active.
	Provider string
	// NativeCapabilities is what this surface's agent's BUILT-IN authentication source
	// performs for itself (§6.1 clause 1's pack.json half). It belongs beside the other
	// two because it completes the same answer: Provider names the source when a profile
	// selects one, and this names it when none does. The derive layer picks whichever
	// applies (luahook.sourceCapabilities) — resolving it here would put the rule in the
	// caller the same way the per-agent Lua branches used to.
	NativeCapabilities []string
	// ViaURL is this agent's per-agent route on the service its active profile's `via`
	// names (OQ-WG7 (d)) — ctx.via_url; "" when the profile is not a via profile, or its
	// service is not in the launch (the host notch).
	ViaURL string
}

// surfaceSelectionFor resolves one surface's selection: packload.ProviderFor — the ONE
// rule both derive paths answer through — over the resolved profile table the caller
// already read (LoadProfiles) and the active-profile table it lowered. Both entries that
// render surfaces call this (ConfigurePackSurfaces on the boot path, ConfigurePackByName
// for `yolo check`), so there is no second place to spell the resolution differently.
//
// The packs are an input for ONE of the three fields, and the split is worth stating
// because the old signature had none. The PROFILE resolution deliberately does not read
// them: resolving off the loaded packs' manifests answered only the names a PACK
// declared, so a user-declared profile launched cleanly (the OQ-CS6 declaration check
// reads user entries) and still selected nothing — the manifest walk fell back to the
// bare name. The launcher's table is the one source that holds every declared name, pack
// under user; the manifests are only what fed it. A BUILT-IN source is the opposite case:
// no user config declares one, there is no launcher table for it to be in, and the only
// statement of it is the manifest of the pack that installs the CLI (§6.1 clause 1).
func surfaceSelectionFor(packs []*packload.Pack, resolved map[string]packload.ResolvedProfile,
	profiles map[string]string, s manifest.Surface) surfaceSelection {
	return surfaceSelection{
		Profile:            profiles[s.Agent],
		Provider:           packload.ProviderFor(resolved, profiles[s.Agent]),
		NativeCapabilities: packload.NativeCapabilities(packs, s.Agent),
		ViaURL:             packload.ViaURLFor(resolved[profiles[s.Agent]], s.Agent),
	}
}

// deriveComputedLayer runs a surface's derive producer to build its dynamic
// (computed) layer — the map that feeds Inputs.Computed and, for an RMW surface,
// the managed dynamic table. Returns nil when the pack ships no derive or none is
// registered for this surface (the identity: no dynamic layer). A Lua error is
// fatal, matching the old BuildComputed error contract.
//
// The second return is the layer's IN-FULL keys (luahook.DeriveOutput.InFull, CO13): the
// top-level tables the derive declared it regenerates in full. Both notches read it from
// here — the boot's stateful render hands it to adoption (agentcfg.Inputs.ComputedInFull),
// and the host's table probe keeps only those keys (hostTableKeys) — so the two cannot
// disagree about which tables are yolo's to regenerate.
//
// sel is the resolved selection the ctx exposes (surfaceSelection); the env path's
// producer reads the same fields, so neither derive path can grow a private answer to
// "which provider is active".
//
// The derive runs TOLERANT of an unknown `yolo.<name>` (DeriveCtx.UnknownAPI), and this
// is the call site that decides that — the same ruling LoadJailPacks applies to the
// manifest vocabulary one line up (packload.TolerateSkew), for the same reason and at the
// same boundary. A derive.lua is staged by the HOST and executed HERE, so the script can
// legitimately be newer than the build reading it; refusing an API this build lacks means
// the jail does not start, and when the script is one yolo SHIPS there is no way for the
// user to route around it. Measured: yolo.env arriving in packs/claude/derive.lua killed
// every jail on a pre-f55f2109 image with a Lua type error at line 51, and took both
// claude surfaces down over a producer the entrypoint never even invokes.
func deriveComputedLayer(e *Env, surface manifest.Surface, deriveScript string, sel surfaceSelection, tables map[string]map[string]any) (map[string]any, []string, error) {
	if deriveScript == "" {
		return nil, nil, nil
	}
	out, err := (luahook.GopherLuaVM{}).DeriveLayer(deriveScript, &luahook.DeriveCtx{
		Agent:              surface.Agent,
		Surface:            surface.Name,
		ProfileName:        sel.Profile,
		SelectedProvider:   sel.Provider,
		Profile:            activeProfileOptions(e, sel.Profile),
		NativeCapabilities: sel.NativeCapabilities,
		ViaURL:             sel.ViaURL,
		Tables:             tables,
		UnknownAPI:         func(name string) { e.warnOnce(unknownDeriveAPINote(surface.Agent, name)) },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("surface %s/%s: derive: %w", surface.Agent, surface.Name, err)
	}
	return out.Layer, out.InFull, nil
}

// unknownDeriveAPINote is the skew line for one `yolo.<name>` this build does not know,
// worded in the vocabulary the manifest skips already use (packdecl.DecodeTolerant) so a
// boot that skips a kind and an API reads as one story rather than two.
//
// It is keyed by AGENT and not by surface because a derive.lua is per-PACK: the unknown
// call is a property of the script, and one script serves every surface its pack declares.
// Prefixing with the surface would print the same finding once per surface — the
// repetition warnOnce exists to prevent.
//
// It names the REMEDY, which the other skew notes do not have to: a skipped kind leaves a
// jail that works minus one contribution, where an unknown API is the shape that used to
// refuse the boot outright, and a user reading it cannot be expected to know that "version
// skew" means "your image predates your yolo".
func unknownDeriveAPINote(agent, api string) string {
	return fmt.Sprintf("pack derive for %s: skipping unknown API yolo.%s — this build does "+
		"not know it, so the call does nothing (version skew; a build that knows the API "+
		"will run it). The jail's image is older than the yolo that staged this pack: "+
		"restart the jail to rebuild it.", agent, api)
}

// activeProfileOptions returns the resolved option map of the named profile, read off
// the YOLO_PROFILES table the launcher composed — never re-derived here, for the same
// reason liveTables reads YOLO_PROVIDERS rather than recomposing it: one resolution per
// launch, on the host, and this side reads the result. It is the SAME table
// surfaceSelectionFor reads the provider out of, so one profile's two ctx halves come
// from one entry and cannot disagree. Always non-nil and empty for no
// profile (or a name the table does not hold), so a derive reads ctx.profile.model with
// no nil guard and "no profile" is the same world as "a profile with no options".
//
// The name arrives from the surface's resolved selection (surfaceSelectionFor), which is
// where the SURFACE path learns which profile is active — the env path resolves it per
// profiled agent instead (packload.AgentEnv), and both read the same table, so the two
// derive paths cannot answer differently about what the active profile carries.
func activeProfileOptions(e *Env, name string) map[string]string {
	if name == "" {
		return map[string]string{}
	}
	if p, ok := e.LoadProfiles()[name]; ok && p.Options != nil {
		return p.Options
	}
	return map[string]string{}
}

// liveTables gathers the live config tables a surface's `computed` declarations may draw
// from, lowered into the engine's plain value model.
//
// CORE owns this list, and that is the division of labor that makes the rest work: an
// MCP server is a yolo config concept, not an agent concept, so core knows how to
// produce the table and a pack only says which one it wants and what shape it needs.
func liveTables(e *Env) map[string]map[string]any {
	return map[string]map[string]any{
		manifest.SourceMCPServers: prismMap(e.LoadMCPServers()),
		manifest.SourceLSPServers: prismMap(LoadLSPServers(e)),
		// The derive's VIEW of the table (packload.ProvidersForDerive): a provider that
		// lists several credential variables (OQ-CN1) points a derive at none of them,
		// so every derive keeps splicing one name into `${…}`.
		manifest.SourceProviders:   prismMap(packload.ProvidersForDerive(e.LoadProviders())),
		manifest.SourceUseProfiles: prismMap(e.LoadUseProfiles()),
	}
}

// dropReservedSelection removes the reserved selection namespace
// (agentcfg.SelectionKey) from a computed layer headed for a surface that cannot
// apply it, naming the drop. Only the stateful render runs the edge-triggered apply
// — `computed` mode overwrites the file wholesale every boot and `rmw` keeps no
// capture baseline, so neither has the state the apply decides from — and the
// alternative to removing the key here is worse than the mistake it reports: an rmw
// surface treats every object-valued computed key as a wholesale-managed table
// (regenerateManagedTables), so a flat `selection` map would be regenerated INTO the
// agent's config as a literal `selection` table.
func dropReservedSelection(e *Env, surface manifest.Surface, computed map[string]any) map[string]any {
	rest, problems := agentcfg.DropSelection(computed)
	for _, problem := range problems {
		e.warn(surface.Agent + "/" + surface.Name + ": " + problem)
	}
	return rest
}

// renderDeclaredSurface writes one declared surface by the mechanism its mode names.
//
// sel is the resolved selection this surface's derive reads (surfaceSelection), computed
// by the caller from the same profile table it folded the variants with.
//
// contribs are the config-overlay layers and config-list entries other packs contribute to
// THIS surface, resolved cross-pack by the caller. nil for every surface nobody contributes
// to — Compose folds nothing as a no-op, so the boot output of a pack set with no
// contributions is byte-identical (pinned by TestRenderFingerprintStable).
func renderDeclaredSurface(e *Env, surface manifest.Surface, tables map[string]map[string]any, deriveScript string, sel surfaceSelection, contribs *surfaceContribs) error {
	if surface.ResolvedMode() == manifest.ModeUnrendered {
		// Declared so `yolo config ls` can describe the file and so host_files cannot
		// claim its path, but yolo does not write it. Skipping silently is correct here
		// — "unrendered" is the declaration's whole meaning. A config-list aimed at it is
		// inert (nothing is written, so there is nothing to refuse), and said so: its
		// author reads a silent no-op exactly like an entry that landed.
		if packs := contribs.listPacks(); len(packs) > 0 {
			e.warnOnce(fmt.Sprintf("config-list  no effect — %s/%s is declared `unrendered`, so "+
				"yolo writes no file to append to (pack %s)", surface.Agent, surface.Name,
				strings.Join(packs, ", ")))
		}
		return nil
	}

	// OQ-AL1's LAUNCH REFUSAL: a list contribution on a path whose mechanism does not capture
	// per entry is refused, naming the surface and its mode — never composed into a capture
	// that would freeze the list. Returned as an ordinary error, so it is A12-fatal to the
	// in-jail boot. (Not `yolo check`: it renders each embedded pack alone, so a user pack's
	// cross-pack contribution never reaches this line there.) Deliberately NOT an
	// rmwRefusedError, which the rmw arm below downgrades to a warning — that downgrade is for
	// a file the AGENT wrote badly, and this is a declaration the pack author can fix.
	if len(contribs.listContribs()) > 0 {
		mechanism, _ := e.renderTarget().Modes().Mechanism(surface.ResolvedMode())
		if refusal := agentcfg.ListCaptureRefusal(mechanism, surface); refusal != "" {
			return fmt.Errorf("%s", refusal)
		}
	}

	// The config dir. Was an os.MkdirAll per agent in the six Go functions; the surface
	// path already says where the file goes, so the directory is derivable rather than
	// declared.
	if err := os.MkdirAll(filepath.Dir(expandHomePath(e, surface.Path)), 0o755); err != nil {
		return err
	}

	// The dynamic (computed) layer: produced by the surface's derive function over the
	// live tables (docs/reference/pack-system.md §7). One map serves both the compose
	// path (as Inputs.Computed) and the RMW path (as the managed dynamic table). inFull
	// is what the derive declared about it (ctx.in_full, CO13), and only the stateful arm
	// reads it: it is the one mechanism that ADOPTS a file, so the one that has to know
	// which tables it may claim wholesale as yolo's own previous output.
	computed, inFull, err := deriveComputedLayer(e, surface, deriveScript, sel, tables)
	if err != nil {
		return err
	}

	// THE HOST LAYER IS READ BY THE TWO MODES THAT COMPOSE ONE, and not before the switch.
	// `rmw` never folds a host layer — it read-modify-writes the agent's own file — so
	// reading it there could only produce a refusal (hostSurfaceBytes fails closed) over
	// bytes the render would discard. A surface that declares `readsHost` AND `rmw` is
	// making an inert declaration; refusing the boot for it would be the over-refusal this
	// whole mechanism is careful not to be.
	switch surface.ResolvedMode() {
	case manifest.ModeComputed:
		computed = dropReservedSelection(e, surface, computed)
		hostBytes, err := hostSurfaceBytes(e, surface)
		if err != nil {
			return err
		}
		_, err = renderSurfaceStatelessSurface(e, surface, hostBytes, computed, contribs)
		return err
	case manifest.ModeRMW:
		computed = dropReservedSelection(e, surface, computed)
		err := renderSurfaceRMWSurface(e, surface, computed, contribs)
		// A REFUSAL IS A WARNING HERE, NOT AN A12 BOOT FAILURE, and the distinction is the
		// difference between the two things that can go wrong with an rmw surface.
		//
		// A12 makes a generator failure fatal because boot must not hand the agent a
		// half-configured home. A refusal is the opposite situation: yolo looked at an
		// AGENT-OWNED file it could not parse and deliberately left it exactly as it was.
		// Nothing is half-configured — one file is untouched, and the file is one the agent
		// wrote. Escalating that to fatal would mean a corrupt ~/.claude.json (which the
		// agent itself can produce by crashing mid-write) stops the jail from STARTING, so
		// the user could not launch the jail to fix the file inside it. That is a worse
		// failure than the one being prevented.
		//
		// It is still never silent: the warning names the surface and the reason. The old
		// behavior — parse-fail, read as {}, rewrite from yolo's layers alone — was the
		// silent one, and it destroyed the file.
		if refusal, isRefusal := asRMWRefusal(err); isRefusal {
			e.warn("warning: " + refusal.Error() + " (this file was NOT modified)")
			return nil
		}
		return err
	default:
		hostBytes, err := hostSurfaceBytes(e, surface)
		if err != nil {
			return err
		}
		out, err := renderSurfaceStatefulSurface(e, surface, hostBytes, computed, inFull, contribs)
		if err != nil {
			return err
		}
		if out != nil && out.FirstMigration {
			retireOrphanSidecars(e, surface)
		}
		return nil
	}
}

// hostSurfaceBytes reads the surface's host source from its /ctx mount, if it has one.
//
// The path is DERIVED from the surface's own path under the pack's /ctx dir
// (packload.SurfaceHostFile → packload.CtxPath), which is how a surface's host layer
// stopped needing a Go constant per agent (hostClaudeDir, hostPiDir). The surface declares
// `"readsHost": true`, the host mounts its real-home twin at
// /ctx/host-<staged dir>/<basename>, and this opens exactly that string.
//
// # It FAILS CLOSED, and what that means here
//
// It was fail-open until 2026-09-12: `data, _ := os.ReadFile(...)`, composing the surface
// without its host layer whenever the read failed for any reason at all. The user's whole
// ~/.claude/settings.json would drop out of the composition and the jail would come up
// looking healthy, with a config file the agent has no way to tell from the human's. That
// shipped as a real bug (a pack whose staged directory name was escaped had its file
// mounted at one /ctx path and read at another) and OQ-CO10
// (docs/design/config-ownership-and-promotion.md) rules the read closed.
//
// FAILING CLOSED IS NOT "AN ABSENT FILE IS AN ERROR". An absent file is the common,
// correct case — most users have no ~/.pi/agent/settings.json — and refusing a launch for
// it would be a worse bug than the one being fixed. What the jail cannot tell apart, from
// inside, is "there was nothing to deliver" and "it did not arrive", so the LAUNCHER says
// which (packload.HostLayerReport, the YOLO_HOST_LOOPBACK pattern) and this is the
// witness. Exactly one of the five dispositions refuses: the launcher delivered this path
// and the jail cannot read it there.
//
// The other four compose without the host layer, each for its own reason: the user has no
// such file (normal); this LAUNCH carried no host layers at all, so there was never
// anything to arrive; there is no report, which means only that the host half is older than
// this variable and never that nothing was delivered; or the bytes ARRIVED and are yolo's
// own render, which is the one that says so in the boot log rather than nothing
// (HostLayerRender, hostlayerlabel.go — a baseline is not a layer).
//
// ⚠ THE SECOND OF THOSE USED TO NAME A BACKEND — "macos-user, whose deficiency the launch
// and the briefing both name" — and both halves of that sentence expired on 2026-09-13.
// DP-L1 gave macos-user a delivery mechanism (a host-side copy into a root-owned tree), so
// it reports `supported` whenever a tree was staged and a delivered file it cannot read
// refuses the launch here like anywhere else; the launch warning was narrowed to the one
// shape that still does not cross (a directory `host_files` source) and the briefing
// paragraph was deleted outright. `unsupported` is now a fact about a launch that staged
// nothing — the install capture is the shipped one — and OQ-R3 still says such a launch
// is not refused for a delivery nobody attempted.
func hostSurfaceBytes(e *Env, surface manifest.Surface) ([]byte, error) {
	// Through Surface.HasHostLayer rather than an inline HostSource test: the host-side
	// `config` verbs decide the same thing about the same surfaces, and this is the call
	// site that makes the predicate the boot path's own rather than a claim the CLI makes
	// about it (docs/design/host-render-target.md §3.4).
	if !surface.HasHostLayer() {
		return nil, nil
	}
	if surface.HostSource == "" {
		// The surface declares a host layer and no loader derived where it lands. Not a
		// user-reachable state — packload fills HostSource for every surface it decodes,
		// and a `readsHost` on a path that has no host twin is refused at decode — so this
		// is a pack read by something that is not packload. Refused rather than skipped,
		// because skipping is the silent composition this function stopped doing.
		return nil, fmt.Errorf("surface %s/%s declares a host layer (readsHost) but no "+
			"/ctx source was derived for it — the pack was loaded by something other than "+
			"packload.Pack.Surfaces, so the host bytes cannot be found",
			surface.Agent, surface.Name)
	}
	// THE FIFTH DISPOSITION IS READ BEFORE THE FILE, and the order is the whole of it: a
	// labelled path is delivered and readable, so consulting the report only on a read
	// failure would compose exactly the bytes the label exists to keep out of the fold
	// ([OQ-CR6], hostlayerlabel.go). What arrives under a managed home is yolo's own render;
	// it is the BASELINE this jail can report divergence against, and never a layer, because
	// a key yolo wrote must not come back as the user's ([P6]).
	disposition := HostLayerDispositionIn(e.Getenv(packload.HostLayerEnvVar), surface.HostSource)
	if disposition == HostLayerRender {
		e.note("host layer: " + surface.Agent + "/" + surface.Name + ": the host's copy of " +
			surface.Path + " is yolo's own render, so it is a baseline and not a layer; " +
			"this surface composes from its packs and its capture alone")
		return nil, nil
	}
	src := remapCtx(surface.HostSource)
	data, err := os.ReadFile(src)
	if err == nil {
		return data, nil
	}
	if disposition != packload.HostLayerDelivered {
		return nil, nil
	}
	return nil, fmt.Errorf("surface %s/%s: the launch delivered the user's own copy of "+
		"this file to %s and it cannot be read there: %w.\nRefusing rather than composing "+
		"%s without the host layer — the result would be a config file that looks correct "+
		"and is missing the user's own settings (OQ-CO10, "+
		"docs/design/config-ownership-and-promotion.md)",
		surface.Agent, surface.Name, src, err, surface.Path)
}

// ctxRoot is where host-file mounts appear in this process's filesystem. It is
// packload.CtxRoot in a real jail; a var so a test can point it at a temp dir, which is
// what hostClaudeDir/hostPiDir used to be for.
//
// YOLO_CTX_ROOT PROMOTES THAT SEAM TO A PRODUCTION ONE, for Apple Container. That
// backend was believed not to bind a single file (apple/container#1089 — false on 1.1.0,
// measured; the copy is retained by choice, see run.acMaterialize) and every host-file grant is
// exactly one file, so the CLI copies them into the home and names the directory here
// instead. Absent — every other backend — this is the /ctx mount and remapCtx is a no-op,
// so the common path is unchanged.
//
// IT IS READ PER CALL, and it was an init-time var until the host CLI needed the same
// answer. `yolo config render` previews the file a jail's boot would write and therefore
// resolves the same staged copy (StagedHostLayer), from a process whose environment is set
// long after any package init — a snapshot taken at init would have made the CLI's reading
// unsettable and the two halves free to resolve different roots, which is the one thing
// this root exists to prevent. The cost the old comment weighed is a getenv per surface on
// a path that already reads a file.
func ctxRootDir() string {
	if r := os.Getenv("YOLO_CTX_ROOT"); r != "" {
		return r
	}
	return packload.CtxRoot
}

// remapCtx rewrites a /ctx path onto the ctx root. A no-op in a real jail.
func remapCtx(p string) string {
	root := ctxRootDir()
	if root == packload.CtxRoot {
		return p
	}
	return filepath.Join(root, strings.TrimPrefix(p, packload.CtxRoot+"/"))
}

// retireOrphanSidecars deletes the pre-prism sidecars a surface declares, on the boot
// that migrates it. Failures are IGNORED: the file is already unread, so a stale copy is
// untidy rather than wrong, and failing the boot over it would be a worse outcome than
// leaving it.
func retireOrphanSidecars(e *Env, surface manifest.Surface) {
	dir := filepath.Dir(expandHomePath(e, surface.Path))
	for _, name := range surface.RetireOnFirstRender {
		// Dropped per the docstring above, restated as a reason rather than a policy:
		// the file is ALREADY UNREAD — the surface that replaced it is what the tool
		// loads — so a failure leaves an inert copy on disk with no effect on the jail's
		// behavior. There is no degradation to disclose, only a byte count.
		_ = os.Remove(filepath.Join(dir, name))
	}
}
