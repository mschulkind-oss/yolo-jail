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

// jailPackHostAccess is the mayAccessHost every pack is loaded with IN THE JAIL, and it is
// deliberately the permissive answer — named rather than written as a bare `true` at the call
// site, because a reader who finds a literal `true` where a security gate's input goes is
// right to be alarmed and deserves the reason in one place.
//
// IT IS THE ONLY VALUE THIS SIDE CAN COMPUTE. Origin decides host access, and origin is a fact
// about the user's config — which the jail deliberately cannot read. From in here an embedded
// pack, a local `file://` pack and an approved fetched pack are three identical directories.
//
// AND IT IS NOW CORRECT FOR EVERY INPUT IT CAN RECEIVE, which is what changed with OQ-TP6
// (docs/design/trust-paths.md §3.1). A refused contribution refuses the LAUNCH, so a pack with
// an unapproved claim never reaches a jail at all: every tree under YOLO_PACK_ROOT is one whose
// every claim the host either granted by origin or found already approved in the lockfile.
// The permissive default used to be a security defect (the host printed "refused
// installer" and the jail wrote the curl-to-bash launcher anyway); it is now accidentally, and
// then deliberately, right.
//
// WHY IT IS NOT DERIVED, since the tempting fix is to pass `false` for anything outside
// `_official/`: that would be wrong, not stricter. The tree under YOLO_PACK_ROOT holds local
// packs and APPROVED fetched packs beside the embedded ones, and refusing their host files,
// mounts and installers in-jail would break exactly the packs the user did approve, while
// protecting nothing — there is nothing left in that tree to protect against. The host is the
// only side with the inputs, and the ruling put the whole decision there on purpose.
const jailPackHostAccess = true

// LoadJailPacks reads the pack trees mounted at YOLO_PACK_ROOT.
//
// Every pack found here was already staged and origin-checked ON THE HOST, so this does
// NOT re-derive trust: the host decided which host files a pack may name, and it did so
// with access to the user config (which the jail deliberately cannot read). The jail's
// job is to render what it was given — and since OQ-TP6 that sentence is enforced rather
// than merely asserted: a pack with a refused contribution refuses the launch, so a jail
// that exists is a jail whose packs are wholly approved (see jailPackHostAccess).
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
		// No packs mounted. Legitimate: an older host launcher, macos-user, or a jail
		// started with no packs at all. Renders nothing rather than failing.
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
			p, problems := packload.LoadDir(filepath.Join(dir, ent.Name()), ent.Name(),
				jailPackHostAccess)
			if len(problems) > 0 {
				return nil, fmt.Errorf("pack %s: %s", ent.Name(), problems[0])
			}
			// A contribution whose KIND this build does not know was skipped, not
			// fatal (loophole-packaging §3.3a): a jail must boot under version skew.
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
	// none (docs/design/pack-config-collaboration.md §6). Since OQ-PT8 the gated overlay
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
		// — the projection Lua (docs/design/pack-system.md §7). Read once per pack;
		// absent means no surface has a dynamic layer. packload owns the reader —
		// the host notch's env derive reads the same file through it.
		deriveScript := packload.DeriveScript(p)
		for _, s := range surfaces {
			surface := s
			genStep(e, "configure_"+surface.Agent+"_"+surface.Name, func() error {
				return renderDeclaredSurface(e, surface, tables, deriveScript,
					surfaceSelectionFor(resolved, profiles, surface),
					overlays.For(surface.Agent, surface.Name))
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
		e.warn(fmt.Sprintf("config-overlay  %s (pack %s)", orphan.Reason(), orphan.Pack))
	}
	for _, applied := range overlays.Applied() {
		e.warn(fmt.Sprintf("%s: config-overlay keys from %s (yolo config diff %s)",
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
}

// surfaceSelectionFor resolves one surface's selection: packload.ProviderFor — the ONE
// rule both derive paths answer through — over the resolved profile table the caller
// already read (LoadProfiles) and the active-profile table it lowered. Both entries that
// render surfaces call this (ConfigurePackSurfaces on the boot path, ConfigurePackByName
// for `yolo check`), so there is no second place to spell the resolution differently.
//
// The packs are NOT an input, and their absence from this signature is the fix rather
// than a shortcut: resolving off the loaded packs' manifests answered only the names a
// PACK declared, so a user-declared profile launched cleanly (the OQ-CS6 declaration
// check reads user entries) and still selected nothing — the manifest walk fell back to
// the bare name. The launcher's table is the one source that holds every declared name,
// pack under user; the manifests are only what fed it.
func surfaceSelectionFor(resolved map[string]packload.ResolvedProfile,
	profiles map[string]string, s manifest.Surface) surfaceSelection {
	return surfaceSelection{
		Profile:  profiles[s.Agent],
		Provider: packload.ProviderFor(resolved, profiles[s.Agent]),
	}
}

// deriveComputedLayer runs a surface's derive producer to build its dynamic
// (computed) layer — the map that feeds Inputs.Computed and, for an RMW surface,
// the managed dynamic table. Returns nil when the pack ships no derive or none is
// registered for this surface (the identity: no dynamic layer). A Lua error is
// fatal, matching the old BuildComputed error contract.
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
func deriveComputedLayer(e *Env, surface manifest.Surface, deriveScript string, sel surfaceSelection, tables map[string]map[string]any) (map[string]any, error) {
	if deriveScript == "" {
		return nil, nil
	}
	out, err := (luahook.GopherLuaVM{}).Derive(deriveScript, &luahook.DeriveCtx{
		Agent:            surface.Agent,
		Surface:          surface.Name,
		ProfileName:      sel.Profile,
		SelectedProvider: sel.Provider,
		Profile:          activeProfileOptions(e, sel.Profile),
		Tables:           tables,
		UnknownAPI:       func(name string) { e.warnOnce(unknownDeriveAPINote(surface.Agent, name)) },
	})
	if err != nil {
		return nil, fmt.Errorf("surface %s/%s: derive: %w", surface.Agent, surface.Name, err)
	}
	return out, nil
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
		manifest.SourceMCPServers:  prismMap(e.LoadMCPServers()),
		manifest.SourceLSPServers:  prismMap(LoadLSPServers(e)),
		manifest.SourceProviders:   prismMap(e.LoadProviders()),
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
// overlays are the config-overlay layers other packs contribute to THIS surface,
// resolved cross-pack by the caller. Empty for every surface nobody overlays, which is
// all of them today — Compose folds an empty slice as a no-op, so the boot output of a
// pack set with no overlays is byte-identical (pinned by TestRenderFingerprintStable).
func renderDeclaredSurface(e *Env, surface manifest.Surface, tables map[string]map[string]any, deriveScript string, sel surfaceSelection, overlays []agentcfg.Overlay) error {
	if surface.ResolvedMode() == manifest.ModeUnrendered {
		// Declared so `yolo config ls` can describe the file and so host_files cannot
		// claim its path, but yolo does not write it. Skipping silently is correct here
		// — "unrendered" is the declaration's whole meaning.
		return nil
	}

	// The config dir. Was an os.MkdirAll per agent in the six Go functions; the surface
	// path already says where the file goes, so the directory is derivable rather than
	// declared.
	if err := os.MkdirAll(filepath.Dir(expandHomePath(e, surface.Path)), 0o755); err != nil {
		return err
	}

	// The dynamic (computed) layer: produced by the surface's derive function over the
	// live tables (docs/design/pack-system.md §7). One map serves both the compose
	// path (as Inputs.Computed) and the RMW path (as the managed dynamic table).
	computed, err := deriveComputedLayer(e, surface, deriveScript, sel, tables)
	if err != nil {
		return err
	}

	switch surface.ResolvedMode() {
	case manifest.ModeComputed:
		computed = dropReservedSelection(e, surface, computed)
		_, err := renderSurfaceStatelessSurface(e, surface, hostSurfaceBytes(e, surface),
			computed, overlays)
		return err
	case manifest.ModeRMW:
		computed = dropReservedSelection(e, surface, computed)
		err := renderSurfaceRMWSurface(e, surface, computed, overlays)
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
		out, err := renderSurfaceStatefulSurface(e, surface,
			hostSurfaceBytes(e, surface), computed, overlays)
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
// The path is DERIVED from the surface's own file name under the pack's /ctx dir, which
// is how a surface's host layer stopped needing a Go constant per agent (hostClaudeDir,
// hostPiDir). The pack declares `hostFiles: [{from: ".claude/settings.json"}]`, the host
// mounts it at /ctx/host-<pack>/settings.json if the origin gate allows, and this finds
// it there.
//
// Read FAIL-OPEN, and deliberately: an absent mount means the user has no such host file
// (or this is macos-user, with no /ctx at all), and the render falls back to its lower
// layers. Treating that as an error would make a jail refuse to start because the user
// had never configured the tool on the host.
func hostSurfaceBytes(e *Env, surface manifest.Surface) []byte {
	if surface.HostSource == "" {
		return nil
	}
	data, _ := os.ReadFile(remapCtx(surface.HostSource))
	return data
}

// ctxRoot is where host-file mounts appear in this process's filesystem. It is
// packload.CtxRoot in a real jail; a var so a test can point it at a temp dir, which is
// what hostClaudeDir/hostPiDir used to be for.
//
// YOLO_CTX_ROOT PROMOTES THAT SEAM TO A PRODUCTION ONE, for Apple Container. That
// backend cannot bind a single file (apple/container#1089) and every host-file grant is
// exactly one file, so the CLI copies them into the home and names the directory here
// instead. Absent — every other backend — this is the /ctx mount and remapCtx is a no-op,
// so the common path is unchanged.
//
// It is read ONCE, at init, rather than per call: the value cannot change during a boot,
// and a per-call getenv would make the "no-op in a real jail" fast path a syscall.
var ctxRoot = func() string {
	if r := os.Getenv("YOLO_CTX_ROOT"); r != "" {
		return r
	}
	return packload.CtxRoot
}()

// remapCtx rewrites a /ctx path onto ctxRoot. A no-op in a real jail.
func remapCtx(p string) string {
	if ctxRoot == packload.CtxRoot {
		return p
	}
	return filepath.Join(ctxRoot, strings.TrimPrefix(p, packload.CtxRoot+"/"))
}

// retireOrphanSidecars deletes the pre-prism sidecars a surface declares, on the boot
// that migrates it. Failures are IGNORED: the file is already unread, so a stale copy is
// untidy rather than wrong, and failing the boot over it would be a worse outcome than
// leaving it.
func retireOrphanSidecars(e *Env, surface manifest.Surface) {
	dir := filepath.Dir(expandHomePath(e, surface.Path))
	for _, name := range surface.RetireOnFirstRender {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
