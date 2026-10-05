package entrypoint

// hostrender.go is the host-target render entry (env-manager plan Phase 4): render a
// pack's config surfaces into the invoking user's REAL home, at the `host` confinement
// notch, reusing the same boot writers via an Env pointed at that home. It is the
// implementation `yolo host apply` calls.
//
// The resolved decisions this encodes (env-manager plan OQ-1..4, host-render-target.md
// §6.3, §6.6):
//   - THE MECHANISM IS THE DECLARED CONTRACT'S, not this file's. `host_management` selects
//     it (config-ownership-and-promotion.md §4.1) and render's census states it per contract;
//     the loop below ASKS — ModeSet.Mechanism, per surface — rather than calling one writer
//     unconditionally as it once did. Under `own` it is `stateful` — whole-file composition
//     with the capture store §6.2 specifies — except for a surface its pack declares `rmw`,
//     which is PURE RMW: yolo regenerates only the keys it declares (managed + dynamic tables)
//     and leaves every key the agent wrote, with no whole-file compose and so no capture
//     overlay (OQ-4). Under `none` nothing composes at all. The retired `assert` (OQ-CO14) ran
//     rmw for every surface; the hardcoded call was unsafe precisely because it would have
//     gone on doing that after the census said otherwise.
//     ⚠ "So no --revert" USED TO FOLLOW HERE, and it does not: that inference was the
//     resolved OQ-1, REVERSED on 2026-09-11 (docs/design/config-ownership-and-promotion.md
//     §10 step 3). A revert needs to know which keys are yolo's, not a capture overlay, and
//     the per-key record below is exactly that. hostrevert.go is the verb, and it consumes
//     that record rather than any sidecar this mode lacks.
//   - PROVENANCE IS STILL RECORDED. "No sidecars" covers the two CAPTURE sidecars
//     (last_render, overlay), which pure RMW genuinely has no use for. It does not cover
//     the per-key winning-layer record: a host render knows which layer won each key, and
//     for a while it wrote that down nowhere, so `yolo config diff` at the host inferred
//     the winner from declarations and could state the opposite of what happened (an
//     overlay key with no competing `managed` value reported as "managed won"). The record
//     goes under the rendered home's STATE dir, not a workspace and not the user's config
//     dir — see render.Target.ProvenanceDir. Assert only: recording a winner in observe
//     posture would document a write that never happened.
//   - THE JAIL'S DERIVES, OVER HOST INPUTS (OQ-HC1, 2026-09-28: "host parity with the same
//     handling"). Every derived surface's computed layer is rendered here from the same
//     derive.lua a jail runs, over the wire tables the caller composed at user scope
//     (HostInputs, hostinputs.go): the provider table, the user's own mcp_servers and
//     lsp_servers, and the profile selection. It was empty here until then, on the stated
//     reason that the live tables embed jail-absolute paths; only the MCP presets do, and they
//     are never an input at this notch. The output lands per key (hostcomputed.go, HC-D10) and a
//     surface whose output still names a jail-only path is refused, never written (HC-D14). A
//     ${workspace}-derived value has no referent off-container (OQ-2/§6.6), and such a branch is
//     pruned, not bound.
//   - Config kinds only. The FieldSet census: only config surfaces are target-
//     independent; mount/reads-host/state/files are refused by name upstream (the caller
//     reports them). This entry renders the surfaces; the confinement gate is the
//     caller's.
//   - User-scoped. What is rendered is a function of the pack + user config, never of a
//     workspace — so Workspace is empty and a ${workspace} surface is skipped.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// HostRenderResult reports, per surface, what a host render did (or would do, in
// observe/dry-run mode) — enough for `yolo host apply` to print an honest summary.
type HostRenderResult struct {
	Surface string // "agent/name"
	Path    string // resolved real-home path
	Action  string // "rendered" | "would render" | "refused: <reason>"
	// BrokenLink is set when this destination was refused because a symlink on its path leads
	// into a directory that does not exist (hostbrokenlink.go). A BLOCKER the user has to act
	// on, unlike the policy refusals `Action` also carries, so the report itemizes it and the
	// verdict names it; nil for every other result.
	BrokenLink *BrokenLink
	// Overwrites lists the dotted managed keys whose EXISTING value in the real file
	// differs from what this render writes — the reviewer's "always warn on overwrite"
	// for the host notch (§4.2 / env-manager plan Phase 9). Empty when the render only
	// adds keys or re-asserts identical values. Populated in both observe and assert, so
	// the dry-run preview shows the collision BEFORE anything is written (finding D2).
	// A key coming from a config-overlay is labelled with the contributing pack, since
	// the remedy there is a different pack than the surface's owner.
	Overwrites []string
	// Kept lists the dotted config-overlay keys whose declared value differs from the file and
	// which the write nevertheless LEFT AS THE FILE HAS THEM, each labelled with the declaring
	// pack — "hooks.Notification (config-overlay from matt)". Only `host_management: own`
	// produces one: its fold ranks a captured edit above every config-overlay, so the user's
	// value wins and the overlay's never lands. Not a loss (nothing of the user's changes); a
	// declaration of theirs that is not in effect, which the report names with the way to take
	// the pack's value instead. Mutually exclusive with Overwrites per key.
	Kept []string
	// Overlays names the packs contributing config-overlay keys to this surface, in fold
	// order (later wins). It is the host-side half of ruling R3: an override folds in
	// below the owner's managed layer, so it is invisible in the resulting file, and
	// provenance nobody can read does not make it legible. Empty for the common case.
	Overlays []string
	// Lists names the packs contributing config-list ENTRIES to this surface, in fold order
	// (docs/reference/pack-system.md#config-list-visibility) — Overlays' twin for the
	// additive kind: an assembled array reads in the file exactly like one the owner
	// declared, so which packs appended to it is said here. Empty for the common case.
	Lists []string
	// Outranked names the overlay keys this render ACCEPTS and then BEATS: a key a
	// config-overlay declares that the owner's own managed layer — or its guarded autonomy
	// posture, which folds into that layer at this notch — asserts too, so the overlay's
	// value never reaches the file. Each entry carries the contributing pack(s) and the
	// layer that owns the key.
	//
	// It is Overlays' missing half, and the gap was worse than silence (finding F4): the
	// overlay was accepted, LISTED as contributing, and then lost — while the ⚠ overwrite
	// line fired for the same key, so the output read as though the overlay had WON. Naming
	// the loss and its cause is what lets a reader tell "my declaration lost to a deliberate
	// policy" from "my declaration was ignored by a bug". Empty whenever no overlay contests
	// a managed key, which is the common case.
	Outranked []string
	// Pruned lists the dotted ${workspace}-keyed paths this host render DROPPED from the
	// surface's layers, in sorted order. Non-empty means the surface carried per-jail
	// content that has no host referent; the surface itself may still have rendered (see
	// PruneWorkspaceKeyed for why the two are now independent). Reported by name, never
	// silently — a pruned key is a declaration the user made that yolo chose not to honor.
	Pruned []string
	// EntryLosses reports the NAMED-ENTRY casualties of this render: an entry in a table
	// like `mcpServers` that the render mangles or destroys, rather than a key whose value
	// merely changes. Split from Overwrites because the two need different treatment and the
	// difference is the difference between reversible and not:
	//
	//	Overwrites  a scalar goes from the user's value to the pack's. The key survives, the
	//	            ⚠ line names it, and re-declaring it puts it back. Warn (§4.2).
	//	EntryLosses an atomic record — an MCP server — comes out broken or gone. Nothing in
	//	            the resulting file says what it used to be. Confirm.
	//
	// This is what the one-way-door gate reads, and why it is not "any overwrite": a
	// confirmation that fires on every scalar flip trains people to hit `y` blind, which
	// would cost more than it protects.
	//
	// It is computed by the mechanism that writes (hostMechanismTableLosses, HC-D5): under
	// `own` it names only what the `stateful` write drops or replaces, which is not what the
	// `assert` write's wholesale regeneration would drop.
	EntryLosses []string
	// FirstApply is true when this home has NO provenance record for this surface, i.e.
	// yolo has never asserted it here — or when an entry would be lost from a TABLE the
	// record does not already attribute to a layer yolo asserts (HC-D8): the first apply
	// that makes that table yolo's. It is the other half of the one-way-door signal:
	// wholesale regeneration is correct policy ONCE THE USER HAS OPTED IN, so an
	// EntryLoss in a home yolo has managed before is policy, and the same loss on the
	// first-ever apply is data loss.
	FirstApply bool
	// Formatting names the NON-VALUE losses a codec-canonical re-emit costs the user, one
	// line each ("comments are dropped", …). Distinct from every field above because nothing
	// they CONFIGURED changes: the values all round-trip and the file stays valid. What is
	// lost is the prose and layout around them.
	//
	// Reported rather than fixed because comment preservation is BACKLOG E4 — tracked,
	// deliberately-unbuilt work — and a user whose config.toml is half explanatory comments
	// deserves to know they will not survive, in observe, before the write. Empty for every
	// JSON surface (JSON has no comments) and for an uncommented TOML or YAML one.
	//
	// PER MECHANISM, and the two answers are not the same shape. An rmw write (a surface its
	// pack declares `rmw`; every surface under the retired `assert`) REATTACHES comments (TOML)
	// or keeps their nodes (YAML), so this names the few it could not place. A `stateful` write
	// under `own` has no reattachment at all — the file is composed through the shared codec,
	// which has no comment channel — so every comment goes and this says so once, for the
	// whole file.
	//
	// ⚠ UNDER `own` IT IS THE ONLY DISCLOSURE THIS LOSS HAS, which is why the branch is
	// load-bearing rather than a nicety. §11's criterion has been KEYS AND VALUES since
	// OQ-CO12 and a comment is neither, so comment destruction is CONFORMANT and no criterion
	// test fails for it; `WouldChange` reports a byte moved in the same word it uses for a
	// re-sorted key; and `Archived` is written on the WRITE, never in the dry run that is
	// supposed to precede the one-way door. Computing the rmw answer for an `own` render —
	// which is what this did until 2026-09-12 — therefore reported approximately nothing on
	// the one render that drops every comment in the file.
	Formatting []string
	// WouldChange is THE CHANGE PREDICATE (docs/reference/host-apply-staleness.md §3.4, coined
	// there): would an `--assert` alter this destination's CONTENT, as it stands on disk right
	// now? False means "in sync" — the render is a no-op and nothing needs the user's
	// attention.
	//
	// It exists because no other field on this struct answers that question. `Action` reads
	// "would render" for every surface not skipped or refused, and `Overwrites` is empty both
	// when the render re-asserts an identical value AND when it only adds keys — so a
	// byte-for-byte correct surface and one needing a whole new key were indistinguishable.
	// It is computed by COMPARING CONTENT, never by inspecting which of the fields above are
	// populated (§3.4); hostSurfaceWouldChange is the config-surface computation and states how.
	//
	// TWO CARVE-OUTS, which are the difference between a predicate and a prompt that never
	// stops. Both are stated here because a future reader will otherwise "fix" them:
	//
	//	Formatting  a loss that is purely about the file's PROSE AND LAYOUT — a dropped TOML
	//	            comment, a re-indented JSON file, a re-sorted TOML table — does NOT count.
	//	            Nothing the user configured changes, so a launch gate reading this field
	//	            would prompt forever on a config they are perfectly happy with.
	//	Pruned      a ${workspace}-keyed key with no host referent is dropped from the layers
	//	            on EVERY render, by design (PruneWorkspaceKeyed). It is a declaration yolo
	//	            never honors at this notch, so it is never a pending change.
	//
	// Populated in both postures, from the file as it stands BEFORE any write, so the observe
	// preview and the assert run cannot disagree about it. A skipped or refused surface is not
	// a written destination and is always false: a render that will not happen cannot be a
	// change that is pending (§4.4's cannot-determine class).
	WouldChange bool
	// Archived is where this render copied the PRE-EXISTING file before ADOPTING it — the
	// one-time archive OQ-CO7 rules, written once per surface per home
	// (§6.3.3, entrypoint/adoptionarchive.go). Empty for every render that adopted nothing:
	// an rmw render (rmw asserts keys and adopts no file), a surface with no file yet, a
	// steady-state owned render, and a second adoption of a surface already archived.
	//
	// ASSERT ONLY, and that is not an omission the way a missing Overwrites would be. The
	// other fields on this struct describe what an --assert WOULD do; this one describes a
	// copy that was MADE, and a dry run makes none — reporting a path that does not exist
	// would be the one lie a net cannot afford. What observe reports instead is the render
	// it would run, through the fields above.
	Archived string
	// Repaired is the one-shot repairs this render makes to values YOLO ITSELF SHIPPED that
	// the target program cannot load (agentcfg/rejectedvalues.go), one whole sentence per
	// repair, ready to print. Empty for every surface holding no such value, which is every
	// surface but the transitional one an entry exists for.
	//
	// It is here rather than on the Env's stderr because the host Env carries no Stderr by
	// design, so the jail's boot notice has no counterpart at this notch — and a one-shot
	// mutation of a file in the user's real home that nobody announced is the defect, not the
	// mutation. Populated in BOTH postures from the file as it stands before any write, so
	// `yolo host apply` previews the repair (the dry-run half of the requirement) and
	// `--assert` reports the same one it performed; the sentence carries the tense
	// (agentcfg.Repair.Describe), so the preview cannot claim a mutation that has not happened.
	//
	// NOT an input to the confirmation gate, deliberately. The gate is about what a render
	// COSTS the user — an overwritten value, a damaged entry — and this is yolo retiring a
	// value of its own that never worked; there is no preference to protect, so the boundary
	// is disclosure (OQ-TP9), which is this field.
	Repaired []string
	// Content is the exact file text an --assert writes to Path: THE WRITE'S OWN BYTES, from
	// the pure half of the mechanism the census named (composeRMWSurface or
	// composeStatefulSurface, the calls the writers make before they write). It is what
	// `yolo config render --at host` prints, so that preview and `yolo host apply --assert`
	// agree byte for byte (docs/plans/notch-convergence.md item 23, row D5). Before this the
	// preview composed the JAIL's surfaces — the autonomous posture — and showed keys host
	// apply never writes.
	//
	// OBSERVE ONLY, and empty for a skipped or refused surface: a render that will not happen
	// has no content, and on --assert the file at Path is the answer.
	Content string
	// InputSkips names each input this surface's derive was not handed at the host, one
	// sentence each: an MCP server whose requires_env this agent's composed environment does
	// not hold (HC-D6). Empty for the common case.
	InputSkips []string
	// Provenance is the per-key record the write keeps (key → winning layer), computed the
	// way the write computes it, for `yolo config render --at host --explain`. Observe only,
	// like Content; nil where the notch's census keeps no record for the mechanism.
	Provenance map[string]string
	// capture is the stateful render the observe posture composed for Content — the write's own
	// compose, stopped before the write — kept for its capture half (Capture). Observe only, and
	// nil for a skipped or refused surface and for every surface rmw renders.
	capture *statefulRender
}

// HostCapture is the CAPTURE HALF of one owned (`stateful`) host render: the two capture
// sidecars the write would persist for the surface, as the observe posture composed them, and the
// one-shot repairs that composition made to the captured edits.
//
// It is `yolo config capture`'s whole input at an owned host. The verb used to run the engine's
// capture itself, at the JAIL's Target with no layers, over the shipped pack's AUTONOMOUS
// declaration; that composition's managed layer owns `permissions.deny`, so its narrowing erased a
// deny rule an earlier apply had captured, and with no computed layer it recorded a key the apply
// narrows out (MEASURED 2026-10-04, both). Reading the capture off the apply's own render, for the
// configured pack at the host posture with the computed layer, the overlays and the selection, is
// what makes "the verb records what the next apply records" a property of the code: there is one
// composition, and the verb persists part of it.
type HostCapture struct {
	// Overlay is the capture overlay sidecar's content, without the newline the writer adds.
	Overlay []byte
	// ListCapture is the per-entry list capture's content, nil for a surface with no config-list
	// path — and then the writer creates no file.
	ListCapture []byte
	// Repairs are the one-shot repairs the composition made to the captured edits, one sentence
	// each, for the caller to print: a mutation of the user's captured state nobody announced is
	// the defect, not the mutation (noteRepairedValues).
	Repairs []string
}

// Capture is the capture half of this result's render (HostCapture). ok is false when there is
// nothing to capture, which is every result but an observed stateful render of a file that exists
// over a trusted baseline: with no baseline the render ADOPTS the file, and adoption is the
// apply's to perform, with its one-time archive (OQ-CO7) — the capture verb has never acted
// without a baseline, because without one it cannot tell an edit from yolo's own output.
func (r HostRenderResult) Capture() (HostCapture, bool) {
	sr := r.capture
	if sr == nil || sr.out == nil || sr.current == nil || sr.out.FirstMigration {
		return HostCapture{}, false
	}
	c := HostCapture{Overlay: sr.out.OverlayJSON, ListCapture: sr.out.ListCaptureJSON}
	for _, rep := range sr.out.Repairs {
		c.Repairs = append(c.Repairs, rep.Describe("removed", "your captured edits"))
	}
	return c, true
}

// hostMechanismPreview is the content and provenance an --assert through mechanism would
// write for s: the writer's own compose, stopped before the write. Every failure answers
// empty, as the change predicate does — the probes the caller ran first refuse what this
// cannot compose, and a refusal has no content. The stateful compose comes back too, for its
// capture half (HostRenderResult.Capture); rmw keeps no capture, and returns nil.
func hostMechanismPreview(e *Env, mechanism string, s manifest.Surface, l surfaceLayers,
	contribs *surfaceContribs) (string, map[string]string, *statefulRender) {
	if mechanism == manifest.ModeStateful {
		r, err := composeStatefulSurface(e, s, nil, l.computed, l.inFull, contribs)
		if err != nil || r.out == nil || r.out.Result == nil {
			return "", nil, nil
		}
		return r.text(), r.out.Result.Provenance, r
	}
	r, err := composeRMWSurface(e, s, l.computed, contribs)
	if err != nil {
		return "", nil, nil
	}
	var prov map[string]string
	if e.renderTarget().Modes().Records(manifest.ModeRMW) {
		prov = r.provenance(e, l.computed, contribs)
	}
	return r.text, prov, nil
}

// hostRenderEnv is the Env every host render drives: render.Host, not render.Jail, over the
// real home, with the host's wire tables (HostInputs.vars) for the jail's own readers of them.
func hostRenderEnv(homeDir string, ownership render.HostOwnership, in *HostInputs) *Env {
	return &Env{Home: homeDir, Vars: in.vars(), hostTarget: true, hostOwnership: ownership}
}

// RenderHostPack renders one pack's config surfaces into homeDir (the real $HOME), each
// through the mechanism the declared `host_management` contract's census names, with the
// computed layer the pack's derives produce over in (nil for the empty composition). When
// observe is true it computes what WOULD change and writes nothing (the `yolo host apply
// --dry-run` / default `observe` posture). It returns one result per surface and does not run
// hooks or touch any non-config kind.
//
// in is the invocation's ONE host-input composition (HC-D11): the caller composes it once and
// hands the same value to every call, so a preview, an --assert and the launch gate derive
// from identical inputs.
//
// homeDir is the real home; the Env's Workspace is left empty so a ${workspace} surface
// is refused rather than bound to some arbitrary dir.
//
// overlays is the CROSS-PACK config-overlay resolution (packoverlay.Collect over every
// pack this apply is asserting). It has to be a parameter rather than derived here:
// this function sees ONE pack, and an overlay in pack B targets a surface pack A owns, so
// a per-pack derivation would find none of the overlays the kind exists to carry. Pass nil
// for a caller that has no other packs in view.
//
// only, when given, names the "agent/name" surfaces to render, and every other plan is dropped
// before anything is read or written — RenderHostSurface's one-surface write, and the
// one-surface observe whose capture half `yolo config capture` persists at an owned host
// (HostRenderResult.Capture), kept on this entry so the host half of the one loop still has
// exactly one head (surfaceloop_test.go).
func RenderHostPack(p *packload.Pack, homeDir string, ownership render.HostOwnership,
	observe bool, overlays *packoverlay.OverlaySet, in *HostInputs, only ...string) ([]HostRenderResult, error) {
	// hostTarget: this Env drives render.Host, not render.Jail. Load-bearing for every
	// Target-keyed path the writers resolve — without it an empty Workspace reads as the
	// container default "/workspace" (WorkspaceDir()), so a host apply would write its
	// provenance into some jail's .yolo/prism tree. See Env.hostTarget.
	//
	// ownership is the user's DECLARED `host_management` contract, and it is a PARAMETER for
	// render.Host's reason: the CLI resolves the declaration from the user config, this
	// package renders what it is told, and a test renders the contract it names rather than
	// the invoking user's. An unresolved contract leaves the census undecided, so every
	// surface is refused and nothing is written.
	// Vars carries the host's wire tables (HostInputs.vars), so the jail's own readers of them
	// (LoadProviders, LoadProfiles, LoadUseProfiles, mcpServersWith, LoadLSPServers) read the
	// host composition exactly as they read a launch's — the "same handling" of OQ-HC1.
	e := hostRenderEnv(homeDir, ownership, in)
	// The §4.2 autonomy policy comes from the TARGET's confinement profile, not from a
	// literal chosen here (plan §6c step 1). At the host notch that resolves to autonomy OFF
	// — the guarded posture, so a pack's jail-bypass permission keys do NOT reach the real
	// home, which is the fix for the host apply bypass leak. Reading it from the profile is
	// what makes that one statement rather than a boolean repeated in four files.
	//
	// The SELECTION is the user-scope `profile` table the caller composed (OQ-HC3), and
	// nothing else: there is no `-p` here, so no one-launch variant is ever selected.
	//
	// THE ONE LOOP's head (surfaceloop.go, planPackSurfaces): the posture fold, each surface's
	// contributions and the census's mechanism, decided the way the jail boot decides them.
	//
	// A posture patch naming a surface p does not declare gets no row of p's: it is a posture
	// overlay, placed by the caller's packoverlay.Collect on the owner's surface — where it
	// renders, and is attributed `config-overlay:<pack>`, in the OWNER's rows below — or reported
	// by the caller as an orphan (apply.go), the way an ownerless config-overlay is (OQ-3).
	plans, problems := planPackSurfaces(e, p, overlays)
	if len(problems) > 0 {
		return nil, fmt.Errorf("pack %s: %s", p.Name, problems[0])
	}
	if len(only) > 0 {
		// One plan rendered on its own needs nothing from its siblings: the `whenListed` gate
		// reads the surface it names from the real home as it stands.
		kept := plans[:0]
		for _, pl := range plans {
			if contains(only, pl.surface.Agent+"/"+pl.surface.Name) {
				kept = append(kept, pl)
			}
		}
		plans = kept
	}
	return renderHostPlans(e, p, plans, observe, in)
}

// RenderHostSurface is RenderHostPack for ONE of p's surfaces, writing: the same plan, the
// same mechanism and the same write, filtered to agent/name before anything renders. ok is
// false when p declares no such surface.
//
// Its caller is `yolo config reset` under `host_management: own`, which has just truncated the
// surface to its declared layers and re-seeded the baseline, and must then land exactly what
// the next `yolo host apply --assert` would — the overlays, the config-lists, the computed layer
// and the profile selection — with the sidecars, the provenance and the selection record the
// owned write keeps (persistStatefulSurface). A reset that stopped at the declared layers left a
// home that was neither the user's file nor yolo's render (MEASURED 2026-10-04: pi's
// settings.json truncated to two keys, its mcp.json emptied). Rendering the one surface rather
// than the whole pack is what keeps a reset of one surface from rewriting its siblings.
func RenderHostSurface(p *packload.Pack, agent, name, homeDir string,
	ownership render.HostOwnership, overlays *packoverlay.OverlaySet,
	in *HostInputs) (HostRenderResult, bool, error) {
	id := agent + "/" + name
	declared := false
	surfaces, _ := p.SurfacesFor(render.ProfileFor(render.KindHost).AgentAutonomy)
	for _, s := range surfaces {
		if s.Agent == agent && s.Name == name {
			declared = true
		}
	}
	if !declared {
		return HostRenderResult{}, false, nil
	}
	results, err := RenderHostPack(p, homeDir, ownership, false, overlays, in, id)
	if err != nil {
		return HostRenderResult{}, true, err
	}
	for _, r := range results {
		if r.Surface == id {
			return r, true, nil
		}
	}
	// The plan rendered nothing to report (an unrendered surface with no config-list): there is
	// no file for it, at any notch.
	return HostRenderResult{Surface: id, Action: "skipped: nothing is rendered for this " +
		"surface"}, true, nil
}

// renderHostPlans is THE HOST HALF OF THE ONE LOOP (surfaceloop.go): every planned surface
// written through the census's mechanism into e's real home, with the probes, the change
// predicate and the report fields each result carries. Its two callers differ only in where the
// plans came from — a pack's declared surfaces (RenderHostPack, through planPackSurfaces) and the
// user's own source-less `host_files` entries (RenderHostUserFiles, through
// planHostFileSurfaces, OQ-NC8) — so both are rendered, refused and reported by one body.
//
// p is the plans' owner: its derive.lua supplies the computed layer, and the posture probes
// read its declaration. The user's entries are owned by hostUserFilesOwner, which declares
// nothing and has no derive.
func renderHostPlans(e *Env, p *packload.Pack, plans []surfacePlan, observe bool,
	in *HostInputs) ([]HostRenderResult, error) {
	homeDir := e.Home
	var out []HostRenderResult
	modes := e.renderTarget().Modes()
	sources := newHostSources(e, in)
	script := packload.DeriveScript(p)
	for _, pl := range plans {
		s, contribs := pl.surface, pl.contribs
		id := s.Agent + "/" + s.Name
		path := expandHomePath(e, s.Path)

		if pl.unrendered {
			// Declared but never written, at any target. A config-list aimed at it is inert —
			// nothing is written, so there is nothing to refuse — and gets its own line, because
			// an author reads a silent no-op exactly like entries that landed.
			if packs := contribs.listPacks(); len(packs) > 0 {
				out = append(out, HostRenderResult{Surface: id, Path: path, Lists: packs,
					Action: "skipped: config-list has no effect — this surface is declared " +
						"`unrendered`, so yolo writes no file to append to"})
			}
			continue
		}
		// A surface its pack declares `notAtHost` is never rendered here, whatever the
		// contract, and the row carries the pack's own reason (manifest.Surface.NotAtHost).
		if s.NotAtHost != "" {
			out = append(out, HostRenderResult{Surface: id, Path: path,
				Action: "skipped: " + s.NotAtHost})
			continue
		}
		// THE `whenListed` GATE, as in a jail: the list is read from the real home's copy of
		// the named surface as this apply left it (under observe, as it stands).
		if reason := pl.listUnmet(e); reason != "" {
			out = append(out, HostRenderResult{Surface: id, Path: path,
				Action: "skipped: " + reason})
			continue
		}
		// PRUNE the ${workspace}-keyed branches rather than refusing the surface (see
		// PruneWorkspaceKeyed). What remains is target-independent and renders; what was
		// dropped is named, either in the surface's own result line or — when nothing
		// survives — in the skip reason.
		s, pruned := PruneWorkspaceKeyed(s)
		surfaceOverlays := contribs.overlayLayers()
		// THE COMPUTED LAYER (OQ-HC1): the pack's own derive, run over this agent's host tables
		// with the host's resolved selection — the jail's call (deriveComputedLayer) with the
		// host's inputs. Run before the skip below, because a surface whose declared content was
		// all ${workspace}-keyed still renders whatever the derive computes for it (claude/config's
		// mcpServers). A failure is this surface's refusal, reported past the census's own
		// answer below (HC-D7).
		agentSrc := sources.forAgent(s.Agent)
		derived, derivedInFull, deriveErr := deriveComputedLayer(e, s, script,
			sources.selectionFor(s), agentSrc.tables)
		// Which keys are yolo's wholesale tables: the key-name probe's (hostTableKeys, so a table
		// a derive omits when it has nothing to put in it is still regenerated empty) and every
		// object this run's derive declared in full.
		hl := buildHostLayer(s, derived, derivedInFull, hostTableKeys(p, s), surfaceOverlays)
		if len(pruned) > 0 && layerIsEmpty(s.Managed) && layerIsEmpty(s.Defaults) &&
			len(surfaceOverlays) == 0 && len(contribs.listContribs()) == 0 &&
			deriveErr == nil && hl.empty() {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "skipped: only ${workspace}-keyed keys, which have no host referent"})
			continue
		}
		// WHICH MECHANISM RENDERS THIS SURFACE — asked of the census (render.ModeSet), not
		// assumed. The answer is the DECLARED CONTRACT's: under `own` it runs `stateful` and
		// `rmw` and coerces only `computed` onto `stateful`, under `none` it composes nothing at
		// all. This entry used to call renderSurfaceRMWSurface unconditionally, which agreed
		// with the retired `assert` census (rmw alone, every surface through it) and would have
		// gone on agreeing after the census said otherwise — the rot render/modes.go exists to
		// end.
		//
		// IN BOTH POSTURES, and ahead of the mechanism-specific probes below, because observe's
		// job is to report what an --assert would do: a mechanism this notch cannot run is a
		// refusal a dry run has to print, not one discovered at the write.
		mechanism := pl.mechanism
		switch {
		case !pl.decided:
			// A notch with no stated policy (render.KindGuest), a host target whose caller
			// never resolved `host_management`, or a declaration this contract refuses — a
			// `computed` surface under `own`, which has no capture overlay and therefore no
			// adoption path. The reason is the CENSUS'S, so the line names the contract that
			// declined rather than a generic refusal in this entry's vocabulary.
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: " + modes.Excludes(s.ResolvedMode())})
			continue
		case mechanism != manifest.ModeRMW && mechanism != manifest.ModeStateful:
			// The census names a mechanism this entry has no arm for. Unreachable at every
			// contract today — the two arms below cover every mode the three host censuses run
			// — and this stays as the fail-closed answer for the next one: the file is left
			// exactly as the agent wrote it rather than written by whichever arm looks closest.
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: this contract's census renders a surface declaring " +
					s.ResolvedMode() + " through " + mechanism + ", which `yolo host apply` " +
					"does not implement"})
			continue
		}
		// OQ-AL1's REFUSAL, keyed on the mechanism the census just resolved rather than on the
		// declaration: a census may render a declaration through another mechanism (the
		// retired `assert` rendered a `stateful` surface through `rmw`), and it is the
		// mechanism's capture that decides whether a list path is kept per entry. A `refused:` row
		// in both postures, so a dry run shows it before an --assert would reach the file.
		if refusal := pl.listRefusal(s); refusal != "" {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Lists: contribs.listPacks(), Action: "refused: " + refusal})
			continue
		}
		// A DERIVE THAT FAILED refuses this surface only, and its file is left alone: the rest of
		// the apply is independent of one pack's broken derive (HC-D7).
		if deriveErr != nil {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: its derive failed at the host (" + deriveErr.Error() +
					") — the file is untouched"})
			continue
		}
		// THE JAIL-PATH CHECK (HC-D14): B3's fail-open objection answered. The inputs a host
		// composes are host-valid by construction, so a jail-only path in the output is a derive
		// spelling one of its own — and it is refused BY NAME, per surface, and never written.
		// The roots are the jail render target's (jailOnlyRoots), not a list kept here.
		if bad := JailPathsIn(derived, homeDir); len(bad) > 0 {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: its computed layer names a path that exists only inside a " +
					"jail (" + strings.Join(bad, "; ") + "), and yolo never writes one into " +
					"your real home — the file is untouched"})
			continue
		}
		// DYNAMIC MANAGED TABLES at the host notch. yolo owns each of these keys wholesale, so
		// they are written by replacement (regenerateManagedTables) rather than deep-merged —
		// and stripped from the managed layer so nothing merges them back. Without this an
		// http MCP entry and a stdio one declaring the same server name merged into a record
		// carrying BOTH transports, which no client can use: a "nothing was lost" merge that
		// silently breaks the server. Each table holds the declared overlay entries, then the
		// derive's, then managed's (buildHostLayer).
		tables := hl.inFull()
		// Which overlay keys this surface's own managed layer BEATS, computed BEFORE the strip
		// because it reads that layer (finding F4). The mechanism is deliberate — §5 precedence
		// plus whichever autonomy posture the surface was folded under — so this names the loss
		// and its cause rather than changing what wins.
		outranked, outrankedKeys := outrankedOverlayKeys(p, s, surfaceOverlays, tables)
		s = stripTableKeys(s, tables)
		// THE LAYERS EACH MECHANISM WRITES (HC-D10). `stateful` takes the whole computed layer in
		// one map, its tables named in inFull, and applies the selection namespace itself.
		// `rmw` takes the tables alone — its computed write clears and rewrites every object it is
		// handed — while the leaves ride the contributions it already takes, with the selection's
		// edge-triggered apply decided here over the file as it stands (OQ-HC3).
		layers := surfaceLayers{computed: hl.statefulComputed(), inFull: tables}
		var selectionNext, leafNext map[string]any
		selectionTouched, leafTouched := false, false
		// The computed leaves as the write lands them, for the overwrite report below: under
		// `stateful` the whole selection namespace (its composition decides which keys move),
		// under `rmw` the selection the edge-triggered apply lifted.
		leaves := hl.overwriteLeaves(hl.selection)
		if mechanism == manifest.ModeRMW {
			lift, clears, next, touched := hostRMWSelection(e, s, path, hl.selection)
			leaves = hl.overwriteLeaves(lift)
			// The computed-leaf record (HC-D25): each leaf the derive asserted on an earlier apply
			// and no longer does, still holding yolo's value, is cleared. Decided over the derive's
			// own leaves, before the selection lift, which the selection record decides.
			leafClears, lnext, ltouched := hostRMWLeafRecord(e, s, path, hl.leaves)
			contribs = contribs.withHostLeaves(mergeSurfaceRoot(hl.leaves, lift), clears, leafClears)
			layers = surfaceLayers{computed: hl.tables}
			selectionNext, selectionTouched = next, touched
			leafNext, leafTouched = lnext, ltouched
		}
		// The OVERLAYS are deliberately NOT stripped. They are applied before the table
		// write, so regenerateManagedTables clears whatever they merged and rewrites the block
		// from the table layer — the merge is transient and the result identical. Leaving them
		// means rmwProvenance still sees which pack contributed the key, which is R3's
		// "an override must stay legible".
		//
		// REFUSAL PROBE, before anything else is computed and in BOTH postures. Each mechanism
		// has conditions under which it must not touch a real file — a codec rmw cannot express,
		// a keyless surface `own` cannot adopt, an existing file yolo cannot parse — and
		// observe's job is to say so BEFORE an --assert reaches the file. Probing here rather
		// than only at the write is what makes `--dry-run` an honest preview of a refusal
		// instead of promising a render that will not happen.
		// THE BROKEN-LINK RULE (hostbrokenlink.go), ahead of every probe that reads the file and
		// in both postures: a destination linked into a directory that no longer exists is
		// refused BY NAME, per surface, so the rest of the pack still renders. It used to reach
		// the writer, whose ENOENT failed the whole pack.
		if b := FindBrokenLink(path); b != nil {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: " + b.Reason(), BrokenLink: b})
			continue
		}
		if refusal := hostMechanismRefusal(mechanism, s, path); refusal != nil {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: " + refusal.Reason()})
			continue
		}
		// A CONFIG-LIST TYPE CONFLICT (pack-system.md#config-list-type-conflict) is a refusal
		// too, and it is only discoverable by running the fold — so the probe runs the
		// writer's own fold over a scratch copy, in both postures, for the reason the probe
		// above runs at all.
		if reason := hostListConflict(e, mechanism, s, path, layers, contribs); reason != "" {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Lists: contribs.listPacks(), Action: "refused: " + reason})
			continue
		}
		// FIRST-APPLY detection, read BEFORE the write: the provenance record is the only
		// per-home mark yolo leaves at this notch, so its absence is what "yolo has never
		// asserted this surface here" means. Computed for both postures, because observe's
		// job is to tell the user what an --assert would do.
		firstApply := !hostProvenanceExists(e, s)
		// ...OR a TABLE this render makes yolo's for the first time here (HC-D8): a catalog the
		// provenance record does not already attribute to a layer yolo asserts, in a home where
		// the surface itself was rendered before. Without it, the first host apply after the
		// computed layer reached the host dropped a hand-added provider with a report and no
		// prompt, because the prompt fires only on a first apply.
		prevRecord := readProvenanceRecord(e, s.Agent, s.Name)
		// Compute which managed keys would OVERWRITE a differing existing value, from the
		// file as it stands now — before any write, so observe reports the same collisions
		// assert would cause.
		overwrites := managedOverwrites(e, s, path)
		// An overlay ASSERTS its keys on the host too, so a key it would clobber is the
		// same always-warn case a managed key is (§4.2). Attributed to the contributing
		// pack, because "which pack is about to overwrite my value" is the question R3
		// exists to keep answerable — minus the keys the owner outranks, which the overlay
		// never gets to write (see overlayOverwrites).
		overwrites = append(overwrites,
			overlayOverwrites(e, s, path, surfaceOverlays, outrankedKeys)...)
		// A COMPUTED LEAF ASSERTS ITS KEY TOO (HC-D10 rule 2), and a value of the user's it
		// replaces is the same always-warn case — named by the input of THEIRS it is computed
		// from (hostLeafAttribution), since that is the one declaration that keeps their value.
		// It was missing until 2026-10-04: a profile's first activation replaced pi's
		// `defaultModel` and the report listed nothing (MEASURED, both contracts). Under the rmw
		// arm every leaf the file holds differently is force-written; under `stateful` the
		// re-measure below keeps only those the composition actually changes.
		attribution := newHostLeafAttribution(e, s, script, sources.selectionFor(s),
			agentSrc.tables, derived)
		if mechanism == manifest.ModeRMW {
			overwrites = withComputedOverwrites(overwrites, computedOverwritePaths(
				existingSurfaceObject(s, path), leaves, s.ManagedMap()), attribution)
		}
		// UNDER `own`, MEASURED AGAINST THE WRITE. The two lists above read each layer's
		// declaration against the file, which is the write under the rmw arm (rmw asserts every
		// managed and overlay key). The `stateful` fold is not: a captured edit outranks every
		// config-overlay, so a declared key can differ from the file and still leave it exactly as
		// it is. Reported as an overwrite, that was the inverse of what happened, on every apply
		// (hostcaptureoutranksoverlay_test.go). So under `own` an overwrite is a key whose value
		// the composed file changes, and an overlay key the user's edit kept is named as KEPT.
		var kept []string
		if mechanism == manifest.ModeStateful {
			overwrites, kept = hostStatefulOverwrites(e, s, path, layers, contribs,
				surfaceOverlays, outrankedKeys, overwrites, leaves, attribution)
		}
		// What the table write costs, per entry, PER MECHANISM (HC-D5): the loss list is
		// computed by the mechanism that writes, like the change predicate below. Kept SEPARATE
		// from Overwrites — see HostRenderResult.EntryLosses for why the distinction is what
		// makes the confirmation gate usable rather than noise.
		losses := hostMechanismTableLosses(e, mechanism, s, tables, path, layers, contribs)
		if !firstApply && lossInNewTable(losses, prevRecord) {
			firstApply = true
		}
		// Non-value losses from the canonical re-emit (a TOML file's comments). Computed in
		// both postures for the same reason the overwrites are: the point is to see it before
		// the write.
		formatting := hostFormattingLosses(e, mechanism, s, path, layers.computed, contribs)
		// THE CHANGE PREDICATE, computed before the write for both postures — see
		// HostRenderResult.WouldChange for what it means, and the two functions below it for
		// how each mechanism answers. Both run the WRITER'S OWN fold over a scratch copy, so
		// neither is a second model of the write.
		wouldChange := hostMechanismWouldChange(e, mechanism, s, path, layers, contribs)
		// The one-shot repairs this render will make to yolo's own unloadable values, read
		// from the file BEFORE the write like every probe above it, and in both postures for
		// the same reason: the point of a dry run is to see it coming.
		repaired := hostRepairedValues(e, s, path, observe)
		if observe {
			// `in sync` rather than `would render` when nothing would change. The unconditional
			// "would render" was the honest report of a render that could not tell the two apart;
			// now that it can, a dry-run over an already-applied home must not read as a list of
			// pending writes (§10 step 1). The two kinds that share this struct — `briefing` and
			// `files` — already said `unchanged` for their own no-op, so this is one vocabulary
			// across all three rather than a new word.
			action := "would render"
			if !wouldChange {
				action = "unchanged"
			}
			content, provenance, composed := hostMechanismPreview(e, mechanism, s, layers, contribs)
			out = append(out, HostRenderResult{Surface: id, Path: path, Action: action,
				Overwrites: overwrites, Kept: kept, Overlays: overlayPackNames(surfaceOverlays),
				Lists:     contribs.listPacks(),
				Outranked: outranked, Pruned: pruned, EntryLosses: losses,
				FirstApply: firstApply, Formatting: formatting, WouldChange: wouldChange,
				Repaired: repaired, Content: content, Provenance: provenance,
				InputSkips: agentSrc.skippedNotes(), capture: composed})
			continue
		}
		// INTO THE REAL HOME, through the mechanism the census named, with the layers decided
		// above: the derive's computed layer over the host's inputs, landed per key (HC-D10). The
		// tables hold the declared entries too, under both arms: under `own` that is what makes
		// `mcpServers` a table yolo regenerates rather than one adoption freezes at whatever the
		// file held.
		//
		// A REFUSAL here is a per-surface result, not a pack-level error. The probe above has
		// already caught every refusal this render can predict; one arriving at the write is a
		// condition that appeared in between (or an unencodable composed value), and the file
		// is untouched either way — so the honest report is this surface's line, with the
		// remaining surfaces still rendered. Returning an error would abort the pack over a
		// file yolo deliberately left alone.
		//
		// THE MECHANISM RESOLVED ABOVE, executed rather than assumed: the switch there has
		// already refused everything neither arm handles, and a third mechanism is added at
		// that switch and in writeSurfaceThrough together.
		//
		// ONE DISPATCH FOR EVERY NOTCH (writeSurfaceThrough, surfaceloop.go). Under `own` a
		// `stateful` surface is whole-file composition into a real home, with the capture store
		// render.Target.SidecarDir resolves for this notch — the SAME writer the boot path runs,
		// which is what makes "the host is a notch like any other" (P5) a fact about the code
		// rather than an aspiration.
		//
		// ADOPTION IS THE FIRST RENDER'S OWN BEHAVIOUR and needs no SEEDING here.
		// ComposeStateful reads "no trusted last_render" as a first migration and seeds the
		// overlay from the file it finds, so the first owned render reproduces every key the
		// file holds that yolo does not declare (§6.3.1). That branch exists because seeding
		// an EMPTY overlay was a shipped data-loss bug; it is why this arm can be reached on
		// a home full of hand-written config without a guard of its own.
		//
		// THE ONE GUARD IT IS OWED, and it is BUILT (OQ-CO7, 2026-09-12): one archive at
		// adoption — the pre-existing file copied once into the archive subsystem, as a
		// `config` bucket beside the ones that already ship (§6.3.3). The copy is made by the
		// shared writer (entrypoint.archiveAdoption, run from persistStatefulSurface), so the
		// jail's own firstMigration gets the same net from the same line rather than from a
		// second implementation here. It covers the deep-merged leaf adoption drops, which
		// `confirmHostLosses` is structurally blind to: that gate reads EntryLosses and fires
		// only on a first apply, so the `assert` -> `own` switch — the exact transition that
		// loses the leaf — is unprompted. The archive is what stands in for the prompt there.
		// A FAILED ARCHIVE REFUSES THE SURFACE: the refusal arrives as an *rmwRefusedError at
		// the write, handled below like any other.
		//
		// NO HOST LAYER, at either mechanism, and that is not the host LAYER going missing: at
		// this notch the surface's own file IS what a `host` layer would have carried, and it
		// arrives through adoption (stateful) or as the read half of the read-modify-write.
		// Passing the same bytes as a layer too would fold them in BELOW the declared layers,
		// so a key the user owns and a pack also declares would flip to the pack's value.
		// Only the stateful writer reads inFull (it is the one that adopts), and archived is
		// set by it alone: rmw asserts individual keys and adopts nothing.
		// THE stateful ARM'S COMPUTED-LEAF RECORD reads the file as it was before the write and the
		// record as it stood (hostStatefulLeafRecord): the rmw arm decides its record before the
		// write, from the file, and the stateful one can only decide it after, from what landed.
		var statefulLeafBefore, statefulLeafRecord map[string]any
		if mechanism == manifest.ModeStateful {
			data, _ := os.ReadFile(path)
			statefulLeafBefore = agentcfg.DecodeSurfaceObject(s.Codec, data)
			statefulLeafRecord = readHostLeafRecord(e, s.Agent, s.Name)
		}
		w, werr := writeSurfaceThrough(e, mechanism, s, layers, contribs)
		archived := w.archived
		if werr != nil {
			if refusal, isRefusal := asRMWRefusal(werr); isRefusal {
				out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
					Action: "refused: " + refusal.Reason()})
				continue
			}
			return out, fmt.Errorf("%s: %w", id, werr)
		}
		// The rmw arm's SELECTION RECORD, after the write it describes and only then: a record
		// naming a value the file never received would have the next apply read the user's
		// value as one yolo wrote. The stateful arm persists its own (persistStatefulSurface).
		if selectionTouched {
			writeSelectionRecord(e, s.Agent, s.Name, selectionNext)
		}
		// The computed-leaf record, by the same rule: after the write, and only then. The stateful
		// arm decides its record here, from what the write landed.
		if mechanism == manifest.ModeStateful {
			data, _ := os.ReadFile(path)
			leafNext, leafTouched = hostStatefulLeafRecord(hl.leaves, statefulLeafBefore,
				agentcfg.DecodeSurfaceObject(s.Codec, data), statefulLeafRecord)
		}
		if leafTouched {
			writeHostLeafRecord(e, s.Agent, s.Name, leafNext)
		}
		// `unchanged` when the write reproduced the file, in this posture as in the dry run: the
		// line and the verdict's counts read the same predicate, so they cannot disagree about
		// whether this surface changed.
		// An adoption that reproduced the bytes says so: it wrote nothing new, and it took the
		// one-way archive the line under it names.
		action := "rendered"
		switch {
		case !wouldChange && archived != "":
			action = "adopted"
		case !wouldChange:
			action = "unchanged"
		}
		out = append(out, HostRenderResult{Surface: id, Path: path, Action: action,
			Overwrites: overwrites, Kept: kept, Overlays: overlayPackNames(surfaceOverlays),
			Lists:     contribs.listPacks(),
			Outranked: outranked, Pruned: pruned, EntryLosses: losses,
			FirstApply: firstApply, Formatting: formatting, WouldChange: wouldChange,
			Archived: archived, Repaired: repaired, InputSkips: agentSrc.skippedNotes()})
	}
	return out, nil
}

// hostMechanismRefusal reports why this surface must not be written into this home through
// the named mechanism, or nil when it may be. It is the dispatch's own probe, one arm per
// mechanism, so a mechanism added at the switch above has to answer here too rather than
// inheriting the other's conditions — which would be exactly wrong in both directions (rmw's
// codec gate says nothing about adoption; `own`'s keyless refusal would wrongly block a
// surface rmw never composes at all).
func hostMechanismRefusal(mechanism string, s manifest.Surface, path string) *rmwRefusedError {
	if mechanism == manifest.ModeStateful {
		return hostStatefulRefusal(s, path)
	}
	return hostRMWRefusal(s, path)
}

// hostStatefulRefusal reports why a `host_management: own` render must not compose this
// surface into a real home, or nil when it may. Two conditions, and both are data loss that
// would otherwise be silent:
//
//   - A KEYLESS SURFACE (raw/lines) — OQ-CO9's ruling, refuse until a real example exists. Such
//     a surface has one "key", the whole file, so adoption cannot take a partial residue: the
//     first owned render replaces the file wholesale from the declared layers, and
//     confirmHostLosses is structurally blind to it (EntryLosses is defined over NAMED ENTRIES
//     in a table, and a keyless surface has none). The class is empty today — no shipped pack
//     declares one — so the cheap answer is the honest one.
//   - AN EXISTING FILE YOLO CANNOT PARSE. ComposeStateful treats an undecodable current file
//     as "skip capture", which in a jail self-heals: the next boot re-renders from layers. At a
//     real home it means adoption takes NOTHING and the render replaces the user's file with
//     the pure render — the copilot-OAuth-wipe shape (B1), one notch over. Refusing leaves the
//     file exactly as it is, which is what the rmw arm already does for the same condition
//     (decodeSurfaceObject's own refusal, reused here so the two notches give one answer).
//
// The carrier type is *rmwRefusedError because it is the surface-refusal carrier this package
// already has — named for the mechanism that first needed one. Its reader is Reason(), which
// says nothing about rmw, and the dispatch above prints it identically for both arms.
func hostStatefulRefusal(s manifest.Surface, path string) *rmwRefusedError {
	if s.Kind() != codec.KindObject {
		return refuseRMW(s, "`host_management: own` composes the whole file, and a "+
			"%s surface has no keys to adopt — the first owned render would replace %s "+
			"outright rather than keeping what it holds (OQ-CO9). Leave this surface to the "+
			"jail, or set `host_management: none` to keep yolo out of this home's files; the "+
			"file is untouched",
			s.Codec, path)
	}
	if _, err := decodeSurfaceObject(s, path); err != nil {
		if refusal, isRefusal := asRMWRefusal(err); isRefusal {
			return refusal
		}
	}
	return nil
}

// hostMechanismWouldChange is the change predicate, per mechanism. See
// HostRenderResult.WouldChange for what the answer is used for.
func hostMechanismWouldChange(e *Env, mechanism string, s manifest.Surface, path string,
	l surfaceLayers, contribs *surfaceContribs) bool {
	if mechanism == manifest.ModeStateful {
		return hostStatefulWouldChange(e, s, l, contribs)
	}
	return hostSurfaceWouldChange(e, s, path, l.computed, contribs)
}

// hostListConflict reports a config-list type conflict this surface's render would refuse
// on (pack-system.md#config-list-type-conflict), or "" when there is none — by running the
// WRITER'S OWN fold over a scratch copy and writing nothing, per mechanism, so the dry run
// and the --assert cannot disagree. Quiet for a surface no list targets: nothing about it
// can conflict.
func hostListConflict(e *Env, mechanism string, s manifest.Surface, path string,
	l surfaceLayers, contribs *surfaceContribs) string {
	if len(contribs.listContribs()) == 0 {
		return ""
	}
	computed := l.computed
	if mechanism == manifest.ModeStateful {
		if _, err := composeStatefulSurface(e, s, nil, computed, l.inFull, contribs); err != nil {
			if refusal, isRefusal := asRMWRefusal(err); isRefusal {
				return refusal.Reason()
			}
			return err.Error()
		}
		return ""
	}
	s = agentcfg.SubstituteWorkspace(s, e.WorkspaceDir())
	_, obj, _, err := readRMWSource(s, path)
	if err != nil {
		return "" // the codec/decode probe above already refuses what this cannot read
	}
	agentcfg.RepairRejected(s, obj)
	if _, err := applyRMWLayers(e, s, obj, computed, contribs); err != nil {
		if refusal, isRefusal := asRMWRefusal(err); isRefusal {
			return refusal.Reason()
		}
		return err.Error()
	}
	return ""
}

// hostStatefulWouldChange is the `own` half of the change predicate: would an --assert alter
// the file? It runs THE RENDER — composeStatefulSurface, the same call the writer makes — and
// compares the bytes it would write against the bytes that are there.
//
// No carve-outs are needed and none are possible, which is the difference from the rmw half.
// That one compares encode(folded) against encode(unfolded) so a purely canonical re-emit
// cancels; here the render IS the file's content by definition — `own` means yolo composes the
// whole thing — so any difference in the bytes is a difference the user would see. The first
// owned render on an adopted home is the case that matters, and it answers "no change"
// precisely when adoption reproduced the file BYTE FOR BYTE.
//
// ⚠ THAT IS STRICTER THAN §11's CRITERION, since OQ-CO12 relaxed the criterion to keys and
// values and left byte layout conformant. The gap is deliberate and must not be closed by
// teaching this predicate to decode: the question here is "would an apply alter the file?",
// and re-sorting a user's JSON keys or dropping their TOML comments ALTERS IT — a predicate
// that answered "no change" to that would suppress the one disclosure the switch still makes
// (§11's warning: `WouldChange` is true on every conformant axis, so the switch is never
// silent). The criterion and this predicate answer different questions and are allowed to
// disagree; what they may never do is disagree about a KEY.
//
// EVERY FAILURE ANSWERS "no change", as the rmw half does: a surface this cannot compose is
// one the render REFUSES (hostStatefulRefusal, which the caller runs first), and a refusal is
// not a pending change.
//
// It writes nothing — not the sidecars, not the selection record — which is what makes it safe
// to run in the observe posture and, for confirmHostLosses, twice.
func hostStatefulWouldChange(e *Env, s manifest.Surface, l surfaceLayers,
	contribs *surfaceContribs) bool {
	r, err := composeStatefulSurface(e, s, nil, l.computed, l.inFull, contribs)
	if err != nil {
		return false
	}
	return r.text() != string(r.current)
}

// hostRMWRefusal reports why this surface cannot be read-modify-written in this home, or nil
// when it can. It is the OBSERVE-side half of the writer's own gate: the same two checks
// renderSurfaceRMWSurface performs (a codec RMW cannot express, and an existing file yolo
// cannot parse), run without writing.
//
// Duplicating the checks rather than "just try the write and catch the refusal" is what makes
// the dry-run truthful. `yolo host apply` (no --assert) must print `refused: …` for a surface an
// --assert would decline, and it cannot learn that from a write it is forbidden to attempt.
// The writer keeps its own copy because it is also reached from the jail boot path, where
// there is no observe pass at all — so neither one can be the single gate.
func hostRMWRefusal(s manifest.Surface, path string) *rmwRefusedError {
	if refusal := rmwCodecRefusal(s); refusal != nil {
		return refusal
	}
	if _, err := decodeSurfaceObject(s, path); err != nil {
		if refusal, isRefusal := asRMWRefusal(err); isRefusal {
			return refusal
		}
	}
	return nil
}

// hostSurfaceWouldChange is the CONFIG-SURFACE half of the change predicate: would an
// --assert alter the content of the file at path? (HostRenderResult.WouldChange is the field
// it fills; read that first for what the answer is used for and which carve-outs apply.)
//
// # It measures the render, not the declarations
//
// It runs the WRITER'S OWN fold — applyRMWLayers, the same function renderSurfaceRMWSurface
// calls, over a scratch decode — and then encodes the result. Nothing here re-derives what a
// render "would" do from the surface's layers, for hostFormattingLosses' reason: a second
// model of the write is a second thing to drift out of step with the write.
//
// # BOTH SIDES GO THROUGH THE SAME ENCODER, and that is the whole carve-out
//
// The comparison is `encode(folded)` against `encode(unfolded)` — NOT against the file's raw
// bytes. Encoding the pre-render object through the identical codec is what makes every
// difference that is purely about layout cancel out instead of registering as a change:
//
//   - TOML KEY ORDER IS NOT PRESERVED (see encodeSurfaceObject) — the emitter is canonical and
//     sorts. Against raw bytes, every hand-written TOML config would differ forever.
//   - JSON INDENTATION IS CANONICAL (dumpJSONIndent2, 2 spaces). Against raw bytes, a
//     4-space or tab-indented ~/.claude/settings.json would differ forever.
//   - A COMMENT LOSS THE FILE CAUSES cancels: when scanTOMLTrivia cannot read the comment
//     positions, reattachTOMLComments drops them on BOTH sides. What does NOT cancel is a
//     comment dropped because the value above it CHANGED (E4's rule ①) — and there the value
//     changed, so the surface would have counted as changed anyway.
//
// That is `Formatting`'s carve-out implemented structurally rather than checked: there is no
// "is this difference only formatting?" test to get wrong, because a formatting-only
// difference cannot reach the comparison. `Pruned`'s carve-out needs nothing at all — the
// caller has already pruned those branches out of s, so they are not in the fold.
//
// # Every failure answers "no change"
//
// A file that cannot be read, decoded, or encoded is a surface the render REFUSES (see
// hostRMWRefusal, which the caller runs first), and a refusal is not a pending change. So
// every error here returns false rather than defaulting to true: a gate that cannot prove its
// condition does not fire (§4.4).
//
// The provenance record is deliberately NOT part of the answer. It is yolo's own bookkeeping
// under the state dir, not one of the destinations this predicate is about, so a first-ever
// apply into a home whose files already hold exactly what the packs declare correctly reports
// nothing to change (§4.1's "zero stored state on the render side").
func hostSurfaceWouldChange(e *Env, s manifest.Surface, path string, computed map[string]any,
	contribs *surfaceContribs) bool {
	s = agentcfg.SubstituteWorkspace(s, e.WorkspaceDir())
	orig, obj, before, err := readRMWSource(s, path)
	if err != nil {
		return false
	}
	// A FILE THE RENDER CREATES IS A CHANGE (HC-D4, docs/design/host-computed-layer.md §7), and
	// the comparison below cannot see it: an absent file decodes to {} on both sides, so a
	// surface whose layers add nothing folds to the baseline, while renderSurfaceRMWSurface
	// writes the file regardless. Measured on host pi: the dry run said `unchanged`, the
	// --assert counted the surface among the destinations already in sync, and the file was
	// created. Absence is the one state where the bytes on disk and the decode disagree about
	// whether there is anything there, so it is asked of the filesystem rather than of the
	// decode. It cannot make a gate prompt forever: the write creates the file, and the next
	// pass compares content again.
	//
	// Stat, not Lstat: the question is the one the read and the write ask, and both follow a
	// link. A dangling one — a dotfiles link whose target is not created yet — reads as absent
	// and is written through, so it is a file this render creates; asked of the link itself it
	// read as present, and the dry run said `unchanged` while the --assert created the target.
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return true
	}
	// The BASELINE, encoded before the fold: `before` is an independent decode of the same
	// bytes (readRMWSource decodes twice precisely so this one shares nothing with `obj`), and
	// passing it as its own before-snapshot means the trivia keeper sees no changed value and
	// keeps every comment it can place.
	baseline, _, err := encodeSurfaceObjectReporting(s, before, orig, before)
	if err != nil {
		return false
	}
	// THE REPAIR IS PART OF THE WRITE, so it is part of the replay: renderSurfaceRMWSurface
	// deletes the rejected value from the object before it folds the layers, and a predicate
	// that skipped this step would answer "unchanged" about a render that removes a key —
	// `yolo host apply` printing `unchanged` for the surface whose repair it is about to
	// announce. In the same order as the writer, for the same reason the fold is borrowed
	// rather than re-derived.
	agentcfg.RepairRejected(s, obj)
	if _, err := applyRMWLayers(e, s, obj, computed, contribs); err != nil {
		return false // a list conflict the render refuses (hostListConflict reports it)
	}
	rendered, _, err := encodeSurfaceObjectReporting(s, obj, orig, before)
	if err != nil {
		return false
	}
	return rendered != baseline
}

// hostFormattingLosses names what a codec-canonical re-emit costs beyond values — today,
// exactly one thing: a TOML file's comments, and only the ones this render cannot keep.
//
// It used to be a blanket "comments are NOT preserved" line, which was the honest report of
// an emitter that rendered values and nothing else. E4's `rmw` half changed the fact it was
// reporting: a comment now survives whenever the value it sits above does (tomltrivia.go),
// so the line has narrowed to the exceptions — a comment above a key this render CHANGES,
// which rule ① drops rather than leave lying, and one attached to nothing.
//
// It runs the SAME layer fold the writer does (applyRMWLayers) against a scratch copy, which
// is what makes an observe preview honest: the drops it names are the drops --assert will
// cause, not a guess from declarations. Nothing is written — the fold is in memory and the
// encoded text is discarded.
//
// JSON surfaces yield nothing (JSON has no comment syntax, so there is nothing to lose), and
// so does a TOML or YAML file whose comments all survive — the line only appears when there is
// a real loss.
func hostFormattingLosses(e *Env, mechanism string, s manifest.Surface, path string,
	computed map[string]any, contribs *surfaceContribs) []string {
	// TOML and YAML are the codecs with comments. YAML joined on 2026-10-04: its rmw arm
	// (yamltrivia.go) keeps comments by the same rule and reports the same exceptions, and its
	// `own` arm drops every one, which is what the stateful branch below has to say.
	if s.Codec != "toml" && s.Codec != "yaml" {
		return nil
	}
	s = agentcfg.SubstituteWorkspace(s, e.WorkspaceDir())
	orig, obj, before, err := readRMWSource(s, path)
	if err != nil || len(orig) == 0 {
		return nil
	}
	// PER MECHANISM, exactly as the change predicate beside this one is, and for the same
	// reason: the rest of this function SIMULATES AN RMW WRITE, and an `own` surface does not
	// get one. `own` composes the whole file through codec.TOML.Encode, which has no comment
	// channel at all — reattachTOMLComments is reached only from the rmw encoder — so EVERY
	// comment goes, plus the generated header arrives. Running the rmw simulation anyway
	// answered with the comments RMW would have dropped, which is approximately none, on the
	// one render that drops all of them.
	//
	// ⚠ THAT SILENCE IS WHAT MAKES THE LOSS LAUNDERABLE, which is why this is not cosmetic.
	// §11's criterion is KEYS AND VALUES since OQ-CO12, and a comment is neither — so
	// comment destruction is CONFORMANT and no test will ever fail for it again. What §11
	// offers in exchange is that the switch "is never silent on any axis", and the only
	// thing left holding that up for comments is this field: `WouldChange` says a byte
	// changed, in the same undifferentiated word it uses for a re-sorted key, and `Archived`
	// names the copy only AFTER the write (a dry run makes none, deliberately). A user whose
	// config.toml is half explanatory prose has to be told in OBSERVE, before the one-way
	// door — which is what Formatting's own docstring already promised and this had stopped
	// delivering.
	if mechanism == manifest.ModeStateful {
		// ⚠ ASK IT OF THE FILE MINUS YOLO'S OWN HEADER, not of the file. The header IS
		// comments, and the owned render writes it — so "does this file hold a comment?" is
		// true of every file an owned render has ever touched, and answering that would
		// report a loss on every apply forever for a file with nothing left to lose. It
		// would also be a lie in the direction that costs the most: configResultTier reads
		// this field to decide a destination loses something of the user's, so a steady-state
		// owned home would sit at tierLoss permanently and the signal would stop meaning
		// anything. The render reproduces the header exactly, so what is AT RISK is the
		// comments beside it.
		user := render.StripGeneratedHeader(orig)
		hasComments := tomlHasComments(user)
		if s.Codec == "yaml" {
			hasComments = yamlHasComments(user)
		}
		if !hasComments {
			return nil
		}
		// The header clause only where this target writes one (render.Target.GeneratedHeader:
		// TOML only), so a YAML file is not told it gains a banner it never gets.
		header := ""
		if e.renderTarget().GeneratedHeader(s) != "" {
			header = " and yolo's generated header is added"
		}
		return []string{"comments in this file are NOT preserved — `own` composes the whole " +
			"file from the decoded values, so every comment is dropped" + header + " (every " +
			"value survives; the comments do not). The file as it stands is archived once, " +
			"before the first owned render"}
	}
	if _, err := applyRMWLayers(e, s, obj, computed, contribs); err != nil {
		return nil // the render itself will refuse and report; one problem, one message
	}
	_, losses, err := encodeSurfaceObjectReporting(s, obj, orig, before)
	if err != nil {
		return nil // the render itself will refuse and report; one problem, one message
	}
	return losses
}

// hostProvenanceExists reports whether yolo has EVER asserted this surface in this home.
//
// The provenance record is the right mark to read, and it is the only one available: a host
// render is pure RMW, so it keeps no last_render baseline and no capture overlay (OQ-4), and
// the surface file itself is the AGENT's file — present or absent, it says nothing about
// whether yolo touched it. writeProvenanceRecord runs on every assert and writes even an
// empty record precisely so "absent" keeps meaning "never rendered here" rather than
// "rendered, nothing attributed".
//
// Observe never writes one, so a dry-run does not consume the first-apply signal — which is
// what lets observe REPORT the one-way door and a later --assert still gate on it.
func hostProvenanceExists(e *Env, s manifest.Surface) bool {
	path := prismProvenancePath(e, s.Agent, s.Name)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// hostTableKeys returns the surface's DYNAMIC MANAGED TABLE keys — the keys yolo owns
// wholesale rather than merges into (`mcpServers` on claude/config).
//
// It reads them from the pack's own derive.lua, run against a SENTINEL live table. That is
// the whole trick, and it is worth being precise about what does and does not cross:
//
//   - The KEY NAMES cross. Which keys a surface's derive produces is a property of the pack's
//     declaration, identical at every target.
//   - The CONTENT comes from the real derive, run over the host's own inputs (OQ-HC1,
//     buildHostLayer), not from this probe. The probe keeps one job: a table a derive omits
//     when its input is empty (codex's and opencode's `omitEmpty`) is still yolo's, so the
//     last server's removal regenerates it empty instead of leaving it as the file held it.
//
// A KEY IS A TABLE ONLY IF THE DERIVE DECLARED IT IN FULL (ctx.in_full, CO13 —
// docs/design/config-ownership-and-promotion.md). The rule used to be "every object-valued
// key the derive produces", and the sentinel probe makes that rule over-claim by
// construction: fed a non-empty live table, claude/settings' derive always produced a
// non-empty `env`, so every rmw apply cleared the user's real env block and rewrote it
// from yolo's declared layers — which declare no env at all. `env` is a table yolo asserts ONE
// leaf of; it was never yolo's to regenerate. The declaration is the same one the jail's
// stateful adoption reads (deriveComputedLayer hands both), so the host and that adoption
// cannot disagree about which keys are tables — which is what this function exists for.
//
// ⚠ The jail's RMW arm is the one reader that does not consult the declaration yet:
// regenerateManagedTables regenerates every object-valued computed key, declared or not. For
// the shipped packs that changes nothing (claude/config's derive returns only the declared
// mcpServers, and copilot/config has no producer), but a pack's rmw surface returning a table
// NOT declared in full would be merged here and regenerated whole in a jail. What rmw should do
// with one is an open ruling (docs/design/config-ownership-and-promotion.md, "Built
// 2026-09-25 — what shipped").
//
// The alternative was a SHAPE heuristic ("an object whose values are all objects is a
// table"), which would have guessed `mcpServers` right and had no principled answer for the
// next key. Asking the pack is a declaration, and it is the same declaration the jail path
// already uses.
//
// A pack with no derive, a surface with no producer, or a derive that declares nothing in
// full has no tables: the result is nil and every key merges, which is the pre-existing
// behavior for every other surface.
func hostTableKeys(p *packload.Pack, s manifest.Surface) []string {
	script := packload.DeriveScript(p)
	if script == "" {
		return nil
	}
	// A SENTINEL entry, not an empty table, and the difference is load-bearing: several
	// derives implement `omitEmpty` by returning {} when there are no servers (codex's and
	// opencode's both do). Probed with empty tables those two surfaces report NO table keys —
	// so codex/config's `mcp_servers` and opencode/config's `mcp` would have been deep-merged
	// while claude's and copilot's were replaced, an inconsistency invisible in every test
	// that has servers configured. The sentinel makes the probe answer the question actually
	// being asked: which keys does this surface's derive produce when it produces anything.
	sentinel := map[string]any{"__yolo_table_probe__": map[string]any{"command": "probe"}}
	probe := map[string]map[string]any{
		manifest.SourceMCPServers:  sentinel,
		manifest.SourceLSPServers:  sentinel,
		manifest.SourceProviders:   sentinel,
		manifest.SourceUseProfiles: sentinel,
	}
	// The selection is deliberately empty here: this probe asks which keys the derive
	// PRODUCES, and a selection-keyed producer answers it the same way either way —
	// the catalog tables come from presence (OQ-CS1 option D), the selection key itself
	// is a scalar. A real selection would make the probe's answer a fact about a launch
	// this function has no launch for.
	derived, inFull, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, probe)
	if err != nil {
		return nil // a broken derive is the jail path's error to report, not this one's
	}
	var keys []string
	for _, k := range inFull {
		v := derived[k]
		// The reserved selection namespace is never a table, whatever a derive returns
		// under it. Its body is a flat map of SCALARS by contract
		// (agentcfg.TakeSelection refuses the rest), and this probe is exactly the reader
		// that would misread one: a declared object-valued key here is claimed as yolo-owned
		// and wholesale-written (regenerateManagedTables), so a table-shaped selection a
		// derive wrapped in ctx.in_full would reach the agent's file as a literal `selection`
		// table the host render also does not apply — the host notch runs no edge-triggered
		// apply at all.
		if k == agentcfg.SelectionKey {
			continue
		}
		// Only OBJECT-valued keys are tables, matching regenerateManagedTables exactly. The
		// decoder already refuses an in-full wrapper around anything else, so this is the
		// same rule restated where the write depends on it.
		if _, isObj := v.(map[string]any); isObj {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// mergeEntries copies v's named entries into dst when v is an object, later callers winning.
// A non-object (or absent) v contributes nothing.
func mergeEntries(dst map[string]any, v any) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return
	}
	for k, val := range m {
		dst[k] = val
	}
}

// stripTableKeys returns s with the table keys removed from its Managed layer, so the
// wholesale table write is the ONLY thing that touches them.
//
// Without this the render would write each table twice: regenerateManagedTables replaces the
// block, then applyRMWLayer(managed) deep-merges the same entries back over it. The second
// pass is where a user's stale sub-key would survive inside an entry yolo just replaced —
// the exact hybrid-record bug wholesale replacement exists to prevent.
func stripTableKeys(s manifest.Surface, tables []string) manifest.Surface {
	managed, isMap := s.Managed.(map[string]any)
	if !isMap || len(tables) == 0 {
		return s
	}
	trimmed := make(map[string]any, len(managed))
	for k, v := range managed {
		if !contains(tables, k) {
			trimmed[k] = v
		}
	}
	s.Managed = trimmed
	return s
}

// tableLosses reports what a WHOLESALE table write costs the user, per entry, reading the file
// as it stands.
//
// This is the HOST's announce-every-drop mechanism, and it deliberately does not reuse the
// jail's. regenerateManagedTables already calls noteDroppedManagedEntries, but that writes to
// e.Stderr and the host Env has none — by design, since `yolo host apply` reports through its
// RESULT (so observe can show the same lines without a render having happened) rather than
// through boot-notice side effects. Wiring Stderr here instead would produce the notice only
// in the writing posture, which is where it is least useful: the point is to see the loss
// BEFORE writing. Two kinds, named separately because they are different mistakes to have
// made:
//
//	replaced  the user has this entry AND config declares it, with a different value. Their
//	          version is gone — and unlike a scalar overwrite there is nothing in the file
//	          afterwards to show what it was.
//	dropped   the user has this entry and config does not declare it at all. This is the
//	          wholesale-regeneration policy the maintainer ruled correct ("if you manage
//	          mcpServers through yolo, you give up `claude mcp add`") — correct, and still
//	          never silent.
//
// This is the hazard leaf-level overwrite detection structurally cannot see. A user's working
// host entry {"type":"http","url":"…?tavilyApiKey=…"} against a pack declaring
// {"command":"npx","args":[…],"env":{…}} has every incoming key an ADD, so collectOverwrites
// reports nothing — yet the entry is replaced (or, before wholesale replacement, merged into a
// two-transport record that no client can use).
func tableLosses(s manifest.Surface, tables []string, path string, layer map[string]any) []string {
	if len(tables) == 0 {
		return nil
	}
	declared := func(key, name string) (any, bool) {
		table, _ := layer[key].(map[string]any)
		v, ok := table[name]
		return v, ok
	}
	return entryLossLines(existingSurfaceObject(s, path), tables, declared)
}

// hostMechanismTableLosses is the loss list per mechanism, the dispatch the change predicate
// has (hostMechanismWouldChange) and for the same reason: each mechanism writes a table its
// own way, so the list has to be computed by the one that writes (HC-D5,
// docs/design/host-computed-layer.md §7).
//
// tableLosses models the `rmw` write, which regenerates a table declared in full wholesale
// (regenerateManagedTables), so every entry config does not declare goes. The `stateful`
// write under `own` does not: its adoption claims an in-full table only when this render put
// an entry in it (agentcfg's dropComputedTables), and its steady state captures an entry the
// user added. So with no MCP server configured, a hand-added codex `mcp_servers` entry or
// opencode `mcp` entry is KEPT by the owned write — measured by the design's scratch test,
// where the report named both as dropped and the confirmation prompt then asked the user to
// approve a loss that never happened.
func hostMechanismTableLosses(e *Env, mechanism string, s manifest.Surface, tables []string,
	path string, l surfaceLayers, contribs *surfaceContribs) []string {
	if mechanism == manifest.ModeStateful {
		return statefulTableLosses(e, s, tables, path, l, contribs)
	}
	return tableLosses(s, tables, path, l.computed)
}

// statefulTableLosses is the `own` half of the loss list. It runs THE RENDER —
// composeStatefulSurface with the writer's own in-full declaration, the call
// hostStatefulWouldChange makes — decodes what it would write, and names each entry of the
// user's file that the result drops or changes. No second model of adoption or capture: the
// composition decides, and this reads its answer.
//
// Every failure answers "no loss", as the change predicate does: a surface this cannot compose
// or decode is one the render refuses, and the refusal is reported on its own line.
func statefulTableLosses(e *Env, s manifest.Surface, tables []string, path string,
	l surfaceLayers, contribs *surfaceContribs) []string {
	if len(tables) == 0 {
		return nil
	}
	r, err := composeStatefulSurface(e, s, nil, l.computed, l.inFull, contribs)
	if err != nil || r.out == nil || r.out.Result == nil {
		return nil
	}
	written, err := decodeSurfaceBytes(s, path, r.out.Result.Encoded)
	if err != nil {
		return nil
	}
	after := func(key, name string) (any, bool) {
		table, present := written.Get(key)
		m, isMap := table.(*jsonx.OrderedMap)
		if !present || !isMap || m == nil {
			return nil, false // the write leaves no such table, so every entry in it goes
		}
		return m.Get(name)
	}
	return entryLossLines(existingSurfaceObject(s, path), tables, after)
}

// entryLossLines names each entry of each table in the user's file that `incoming` does not
// hold (dropped) or holds with a different value (replaced) — the one spelling both mechanisms
// report in, so the survey's entryLossName reads either the same way.
func entryLossLines(existing *jsonx.OrderedMap, tables []string,
	incoming func(key, name string) (any, bool)) []string {
	var out []string
	for _, key := range tables {
		cur, present := existing.Get(key)
		curTable, isMap := cur.(*jsonx.OrderedMap)
		if !present || !isMap {
			continue // no such table in the user's file — every entry is an ADD
		}
		for _, name := range curTable.Keys() {
			prev, _ := curTable.Get(name)
			next, kept := incoming(key, name)
			if !kept {
				out = append(out, fmt.Sprintf("%s.%s (dropped — not in your config)", key, name))
				continue
			}
			if !sameJSON(prev, next) {
				out = append(out, fmt.Sprintf("%s.%s (replaced — your version is not kept)",
					key, name))
			}
		}
	}
	sort.Strings(out)
	return out
}

// overlayPackNames lists the packs contributing overlays to a surface, in fold order —
// the provenance the caller prints so an override is legible in `yolo host apply` output
// (ruling R3) and not only in the jail's sidecar.
func overlayPackNames(overlays []agentcfg.Overlay) []string {
	if len(overlays) == 0 {
		return nil
	}
	out := make([]string, 0, len(overlays))
	for _, ov := range overlays {
		out = append(out, ov.Pack)
	}
	return out
}

// overlayOverwrites returns the dotted keys an overlay would write over a DIFFERING
// existing value, each labelled with the contributing pack. Same shape and same
// best-effort JSON basis as managedOverwrites; the label is what makes the warning
// actionable, since the remedy is dropping a different pack than the surface's owner.
//
// outranked is the set of keys the owner's managed layer BEATS (outrankedOverlayKeys), and
// they are excluded — the correction half of finding F4. The warning names a writer, so
// attributing it to a pack whose value never reaches the file is a false statement about who
// changed the value; worse, it fired on precisely the keys the overlay LOST, which is what
// made an outranked overlay read as though it had won. The managed layer that actually
// overwrote the value is still reported by managedOverwrites, unlabelled and truthfully.
func overlayOverwrites(e *Env, s manifest.Surface, path string, overlays []agentcfg.Overlay,
	outranked map[string]bool) []string {
	if len(overlays) == 0 {
		return nil
	}
	existing := existingSurfaceObject(s, path)
	var out []string
	for _, ov := range overlays {
		layer, isMap := ov.Data.(map[string]any)
		if !isMap || len(layer) == 0 {
			continue
		}
		var keys []string
		collectOverwrites(existing, layer, "", &keys)
		sort.Strings(keys)
		for _, k := range keys {
			if outranked[k] {
				continue
			}
			out = append(out, k+" (config-overlay from "+ov.Pack+")")
		}
	}
	return out
}

// outrankedOverlayKeys reports the overlay keys this surface's own managed layer BEATS —
// accepted, folded in, and then overwritten before the file is written — as report lines
// plus the set of dotted keys they name.
//
// This is finding F4, and the framing matters: the PRECEDENCE is correct and deliberate
// (config-overlay < managed, §5), and at the host notch the guarded autonomy posture folds
// into that managed layer, so `permissions.defaultMode` landing on `default` instead of a
// pack's `acceptEdits` is the jail-bypass-leak fix working. What was wrong was the output.
// The overlay was accepted, LISTED as contributing (ruling R3's line), and then lost — while
// the ⚠ overwrite warning fired for the same key, so the report read as though the overlay
// had won. R3 made overlay contributions visible; this makes the OUTRANKED ones visible,
// which is the difference between "my declaration lost to a stated policy" and "my
// declaration was ignored by a bug".
//
// Three scoping decisions, each closing a way to cry wolf:
//
//   - ONLY A DIFFERING VALUE. An overlay declaring the same value the managed layer asserts
//     is redundant, not ignored: the key lands on exactly what the pack asked for, so
//     calling it IGNORED would send someone hunting a loss that did not happen.
//   - THE CAUSE IS NAMED, not just the layer. A key the autonomy POSTURE asserts is owned by
//     a notch policy, which is a different message (and a different remedy — none) than a key
//     the pack happens to manage.
//   - TABLES ARE COMPARED PER ENTRY. A dynamic managed table is merged by entry NAME
//     (hostTableLayer), never deep-merged, so a contest inside one is `mcpServers.<name>`
//     wholesale. Walking it leaf-wise would report `mcpServers.foo.type` as ignored when the
//     entire record was replaced.
func outrankedOverlayKeys(p *packload.Pack, s manifest.Surface, overlays []agentcfg.Overlay,
	tables []string) ([]string, map[string]bool) {
	managed := s.ManagedMap()
	if len(overlays) == 0 || len(managed) == 0 {
		return nil, nil
	}
	// Keyed by KEY rather than by pack, so two packs contesting one key produce ONE line
	// naming both — which is the question a reader has ("who wanted this, and why didn't it
	// stick?"), not "what did each pack lose".
	byKey := map[string][]string{}
	for _, ov := range overlays {
		layer, isMap := ov.Data.(map[string]any)
		if !isMap {
			continue
		}
		var keys []string
		collectOutranked(layer, managed, "", tables, &keys)
		for _, k := range keys {
			byKey[k] = append(byKey[k], ov.Pack)
		}
	}
	if len(byKey) == 0 {
		return nil, nil
	}
	set := make(map[string]bool, len(byKey))
	out := make([]string, 0, len(byKey))
	for _, k := range sortedStringKeys(byKey) {
		set[k] = true
		out = append(out, fmt.Sprintf("%s IGNORED (declared by %s, owned by %s)",
			k, strings.Join(byKey[k], ", "), outrankingOwner(p, s, k)))
	}
	return out, set
}

// outrankingOwner names WHAT owns a key the managed layer beat: the pack's autonomy posture
// when that posture is what asserts it, else the pack's own managed layer.
//
// The distinction is the actionable half of the report. A key the pack merely manages is a
// pack-vs-pack precedence question the user can resolve (drop a pack, ask the owner to stop
// managing it). A key an autonomy POSTURE owns is a notch policy — at the host notch, the
// guarded posture holding the jail-bypass keys, which is the whole point of the autonomy-leak
// fix — so the honest answer is that there is no remedy and none is wanted.
//
// Which posture is read from the pack's own DECLARATION rather than from the caller's policy
// bit, and the reason is that this describes a render that already happened: SurfacesFor folded
// exactly one posture into `managed`, and whichever one asserts the contested key is the one
// that beat the overlay. So the attribution cannot drift out of step with the fold. A pack
// declaring both postures with the key in only one gets named for that one; a pack whose
// postures yolo cannot decode falls back to "managed layer", which is true either way (the
// posture folds INTO it) and merely less specific.
func outrankingOwner(p *packload.Pack, s manifest.Surface, key string) string {
	for _, posture := range []struct {
		name  string
		patch map[string]any
	}{
		{"guarded", posturePatchManaged(p, s, false)},
		{"autonomous", posturePatchManaged(p, s, true)},
	} {
		if layerAssertsPath(posture.patch, key) {
			return p.Name + "'s " + posture.name + " autonomy posture"
		}
	}
	return p.Name + "'s managed layer"
}

// collectOutranked walks an overlay layer against the owner's managed layer, appending the
// dotted path of every key the managed layer asserts with a DIFFERENT value. A key managed
// does not name is one the overlay wins, so it is not reported.
//
// Shape mismatches (managed wants an object where the overlay has a scalar, or the reverse)
// are reported at the contested key rather than recursed into: applyRMWLayer replaces the
// whole branch in both directions, so the overlay lost all of it, and naming the leaves
// would overstate how much detail survived the decision.
func collectOutranked(overlay, managed map[string]any, prefix string, tables []string, out *[]string) {
	for _, k := range sortedKeys(overlay) {
		mv, owned := managed[k]
		if !owned {
			continue // the managed layer never names this key — the overlay wins it
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if prefix == "" && contains(tables, k) {
			collectOutrankedEntries(key, overlay[k], mv, out)
			continue
		}
		ovMap, ovIsMap := overlay[k].(map[string]any)
		mvMap, mvIsMap := mv.(map[string]any)
		if ovIsMap && mvIsMap {
			collectOutranked(ovMap, mvMap, key, tables, out)
			continue
		}
		if sameJSON(overlay[k], mv) {
			continue // the same value: redundant, not ignored
		}
		*out = append(*out, key)
	}
}

// collectOutrankedEntries compares one dynamic managed TABLE's entries by name, appending
// "<table>.<entry>" for each entry the managed layer replaces with a different record. A
// non-object on either side contributes no entries to hostTableLayer, so nothing is
// outranked there.
func collectOutrankedEntries(key string, overlay, managed any, out *[]string) {
	ovEntries, ovIsMap := overlay.(map[string]any)
	mEntries, mIsMap := managed.(map[string]any)
	if !ovIsMap || !mIsMap {
		return
	}
	for _, name := range sortedKeys(ovEntries) {
		mv, owned := mEntries[name]
		if !owned || sameJSON(ovEntries[name], mv) {
			continue
		}
		*out = append(*out, key+"."+name)
	}
}

// posturePatchManaged returns the managed keys the pack's selected autonomy posture folds
// into this surface, or nil when it declares none.
//
// It re-decodes the posture rather than reading the folded result because the fold is
// LOSSY for this question: SurfacesFor deep-merges the posture into the surface's own managed
// layer, after which the two are indistinguishable — and telling them apart is the whole point
// of the attribution. Decode problems are ignored: SurfacesFor already reports them against
// the pack, and a posture yolo cannot read simply yields the less specific "managed layer"
// wording rather than a second copy of the same complaint.
func posturePatchManaged(p *packload.Pack, s manifest.Surface, autonomy bool) map[string]any {
	posture := p.Decl.PostureFor(autonomy)
	if posture == nil || len(posture.Config) == 0 {
		return nil
	}
	patches, _ := manifest.DecodeSurfaces(posture.Config)
	for _, patch := range patches {
		if patch.Agent == s.Agent && patch.Name == s.Name {
			return patch.ManagedMap()
		}
	}
	return nil
}

// layerAssertsPath reports whether m asserts the dotted key path — including via an
// ANCESTOR that is not an object, since a scalar there replaces the whole subtree below it.
func layerAssertsPath(m map[string]any, dotted string) bool {
	cur := m
	parts := strings.Split(dotted, ".")
	for i, part := range parts {
		v, present := cur[part]
		if !present {
			return false
		}
		if i == len(parts)-1 {
			return true
		}
		next, isMap := v.(map[string]any)
		if !isMap {
			return true // an ancestor asserts this whole branch
		}
		cur = next
	}
	return false
}

// sortedStringKeys returns a []string-valued map's keys in a deterministic order, so the
// outranked report does not shuffle with Go's map iteration.
func sortedStringKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// managedOverwrites returns the dotted managed keys whose value in the EXISTING file at
// path differs from what this surface's managed layer will write — the host-notch
// "warn before you clobber a user value" (§4.2 / env-manager plan Phase 9, the reviewer's
// always-warn). It reads the file as it stands (with the SURFACE's codec, matching the RMW
// writer's own round-trip) and walks the managed map; a key absent from the file is an ADD,
// not an overwrite, so it is not reported. Deterministic (sorted).
//
// It used to read via loadObject, i.e. JSON unconditionally, with a docstring conceding
// "a non-JSON surface simply yields no findings". That was true and it was the reporting
// half of the same bug the writer had: codex/config is TOML, so the one surface whose
// values `yolo host apply` was actually about to overwrite was the one surface that reported
// no overwrites. Reading with the surface's codec makes the warning cover every surface the
// writer touches, which is the only version of "always warn" that means anything.
func managedOverwrites(e *Env, s manifest.Surface, path string) []string {
	s = agentcfg.SubstituteWorkspace(s, e.WorkspaceDir())
	managed, ok := s.Managed.(map[string]any)
	if !ok || len(managed) == 0 {
		return nil
	}
	existing := existingSurfaceObject(s, path)
	var out []string
	collectOverwrites(existing, managed, "", &out)
	sort.Strings(out)
	return out
}

// hostRepairedValues is the REPORT half of the one-shot repair (agentcfg/rejectedvalues.go):
// the sentences describing every value yolo itself shipped that this surface's file still
// holds and that the target program cannot load. See HostRenderResult.Repaired.
//
// It runs the REPAIRER ITSELF — agentcfg.RepairRejected, the same function both writers call —
// over a throwaway decode of the file, so this is not a second model of the repair the way a
// re-derivation from the list would be. The mutation it makes is to that scratch object and
// nothing reads it back.
//
// ONE PROBE FOR BOTH MECHANISMS, and that is a fact about the host notch rather than a
// shortcut: the file in the real home is the only place a rejected value can be frozen here —
// the rmw arm reads it as its `host` layer, and the `own` arm ADOPTS it into the capture
// overlay on the first render — so "the file holds it" is exactly the condition under which
// either arm's repair fires. Whichever arm runs, the key is gone from the written file.
//
// Best-effort like every other probe here (existingSurfaceObject): a file this cannot decode
// is one the render is about to refuse, and a refusal repairs nothing.
func hostRepairedValues(e *Env, s manifest.Surface, path string, observe bool) []string {
	s = agentcfg.SubstituteWorkspace(s, e.WorkspaceDir())
	repairs := agentcfg.RepairRejected(s, existingSurfaceObject(s, path))
	if len(repairs) == 0 {
		return nil
	}
	verb := "removed"
	if observe {
		verb = "would remove"
	}
	out := make([]string, 0, len(repairs))
	for _, r := range repairs {
		out = append(out, r.Describe(verb, "the file"))
	}
	return out
}

// existingSurfaceObject decodes the file at path with the SURFACE's codec, for the three
// REPORTING helpers (managedOverwrites, overlayOverwrites, tableLosses). An absent,
// unreadable, or unparseable file yields an empty object.
//
// Best-effort is right HERE and wrong in the writer, and the asymmetry is deliberate: these
// three answer "what would this render cost you", so an unreadable file means they have
// nothing to report — and the render itself is about to refuse that file anyway (see
// decodeSurfaceObject), which is the line the user acts on. Refusing twice would turn one
// problem into two messages; reporting nothing here loses nothing.
func existingSurfaceObject(s manifest.Surface, path string) *jsonx.OrderedMap {
	obj, err := decodeSurfaceObject(s, path)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	return obj
}

// collectOverwrites walks the managed layer against the existing OrderedMap, appending a
// dotted key path for each leaf whose existing value differs from the managed value. An
// object managed value recurses (so a sibling the user owns under the same parent is not
// reported); a missing existing key is an add, not an overwrite.
func collectOverwrites(existing *jsonx.OrderedMap, managed map[string]any, prefix string, out *[]string) {
	var paths [][]string
	collectOverwritePaths(existing, managed, nil, &paths)
	for _, p := range paths {
		key := strings.Join(p, ".")
		if prefix != "" {
			key = prefix + "." + key
		}
		*out = append(*out, key)
	}
}

// collectOverwritePaths is collectOverwrites keeping each key as its SEGMENTS, so a caller can
// look the same key up in another document — a key may itself contain a dot (pi's
// "archimedes.sessionName"), which a dotted string cannot round-trip.
func collectOverwritePaths(existing *jsonx.OrderedMap, managed map[string]any, prefix []string,
	out *[][]string) {
	for _, k := range sortedKeys(managed) {
		key := append(append([]string(nil), prefix...), k)
		mv := managed[k]
		cur, present := existing.Get(k)
		if !present {
			continue // an ADD, not an overwrite
		}
		if sub, isMap := mv.(map[string]any); isMap {
			if curMap, ok := cur.(*jsonx.OrderedMap); ok {
				collectOverwritePaths(curMap, sub, key, out)
				continue
			}
			// managed wants an object where the user has a scalar/array — a real overwrite.
			*out = append(*out, key)
			continue
		}
		if !sameJSON(cur, mv) {
			*out = append(*out, key)
		}
	}
}

// hostStatefulOverwrites re-measures the overwrite report against the file the `own` write
// PRODUCES (see the call site). It composes the surface exactly as the writer does — the same
// composeStatefulSurface call the change predicate makes, writing nothing — and keeps an
// overwrite only where the composed value differs from the file's; an overlay key whose composed
// value is still the file's is returned as kept. When the render cannot be composed (the refusal
// probes have already reported why) the declaration-based list is returned unchanged.
func hostStatefulOverwrites(e *Env, s manifest.Surface, path string, l surfaceLayers,
	contribs *surfaceContribs, overlays []agentcfg.Overlay, outranked map[string]bool,
	declared []string, leaves map[string]any, attribution *hostLeafAttribution) (overwrites, kept []string) {
	r, err := composeStatefulSurface(e, s, nil, l.computed, l.inFull, contribs)
	if err != nil || r == nil || r.out == nil || r.out.Result == nil {
		return declared, nil
	}
	composed, ok := r.out.Result.Config.(map[string]any)
	if !ok {
		return declared, nil
	}
	existing := existingSurfaceObject(r.surface, path)
	changes := func(p []string) bool {
		cur, _ := valueAtPath(jsonx.Plain(existing), p)
		next, _ := valueAtPath(composed, p)
		return !sameJSON(cur, next)
	}
	if managed, isMap := r.surface.Managed.(map[string]any); isMap {
		var paths [][]string
		collectOverwritePaths(existing, managed, nil, &paths)
		for _, p := range paths {
			if changes(p) {
				overwrites = append(overwrites, strings.Join(p, "."))
			}
		}
		sort.Strings(overwrites)
	}
	// The computed leaves the composition changes (the rmw arm's report, measured against this
	// write): a selection key the user's own later pick holds is composed as theirs, so it is
	// not one (HC-D17's edge).
	var computed [][]string
	for _, p := range computedOverwritePaths(existing, leaves, r.surface.ManagedMap()) {
		if changes(p) {
			computed = append(computed, p)
		}
	}
	overwrites = withComputedOverwrites(overwrites, computed, attribution)
	for _, ov := range overlays {
		layer, isMap := ov.Data.(map[string]any)
		if !isMap || len(layer) == 0 {
			continue
		}
		var paths [][]string
		collectOverwritePaths(existing, layer, nil, &paths)
		sort.Slice(paths, func(i, j int) bool {
			return strings.Join(paths[i], ".") < strings.Join(paths[j], ".")
		})
		for _, p := range paths {
			key := strings.Join(p, ".")
			if outranked[key] || computedOutranks(computed, key) {
				continue
			}
			label := key + " (config-overlay from " + ov.Pack + ")"
			if changes(p) {
				overwrites = append(overwrites, label)
			} else {
				kept = append(kept, label)
			}
		}
	}
	return overwrites, kept
}

// withComputedOverwrites appends one labelled line per computed leaf in paths and drops any
// config-overlay line for the same key: computed outranks a config-overlay (§5), so the leaf,
// not the overlay, is what replaced the value.
func withComputedOverwrites(overwrites []string, paths [][]string,
	attribution *hostLeafAttribution) []string {
	if len(paths) == 0 {
		return overwrites
	}
	var out []string
	for _, o := range overwrites {
		if key, isOverlay := strings.CutSuffix(o, ")"); isOverlay {
			if i := strings.LastIndex(key, " (config-overlay from "); i >= 0 &&
				computedOutranks(paths, key[:i]) {
				continue
			}
		}
		out = append(out, o)
	}
	labels := make([]string, 0, len(paths))
	for _, p := range paths {
		labels = append(labels, attribution.label(p))
	}
	sort.Strings(labels)
	return append(out, labels...)
}

// computedOutranks reports whether key is one of the computed leaves in paths.
func computedOutranks(paths [][]string, key string) bool {
	for _, p := range paths {
		if strings.Join(p, ".") == key {
			return true
		}
	}
	return false
}

// valueAtPath looks a key up by its segments in a plain decoded document.
func valueAtPath(doc any, p []string) (any, bool) {
	cur := doc
	for _, seg := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// sameJSON reports whether two decoded values are equal by their JSON serialization —
// codec-agnostic value equality that tolerates the OrderedMap vs map/[]any shape
// differences between a loaded file and a managed literal. Both are normalized to plain
// Go values first (jsonx.Plain), so encoding/json sorts object keys deterministically on
// both sides and the comparison is order-independent.
func sameJSON(a, b any) bool {
	ab, errA := json.Marshal(jsonx.Plain(a))
	bb, errB := json.Marshal(jsonx.Plain(b))
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

// PruneWorkspaceKeyed returns s with every ${workspace}-KEYED branch removed from its
// Defaults and Managed layers, plus the sorted dotted paths it dropped.
//
// EXPORTED for one caller outside this package: the CLI's `config reset`, which under
// `host_management: own` must truncate a surface to the same pure render this entry writes.
// Two derivations of "what the host render omits" would be two things to drift; there is one,
// and it is this.
//
// This replaced a surface-level boolean (usesWorkspacePlaceholder) that refused the WHOLE
// surface when any part of it mentioned the placeholder. The granularity was the bug: the
// shipped claude pack keys two per-jail trust flags under projects.${workspace}, and those
// two keys made ALL of ~/.claude.json unreachable at the host notch — including
// `mcpServers`, which is where Claude Code keeps user-scope MCP servers and has nothing to
// do with any workspace. A pack contributing an MCP server through a config-overlay on
// claude/config lint-passed and then silently never landed.
//
// The refusal was right in INTENT — a ${workspace} key has no host referent, so writing it
// would assert a path the user's agent never looks at — and wrong in SCOPE. So prune the
// branch, keep the rest, and name what was pruned. If nothing survives, the caller reports
// the surface as skipped with the pruned keys in the reason, never a bare "uses
// ${workspace}" (the same never-silent discipline the G1 fix established).
//
// Three properties worth stating:
//
//   - CONTAINS, not equals. agentcfg.SubstituteWorkspace rewrites only keys that EQUAL the
//     placeholder, so a key like "${workspace}/sub" is substituted nowhere and would reach
//     a real file with the literal "${workspace}" in it. Pruning on Contains means no
//     placeholder text can survive into the user's home, which is strictly safer than
//     mirroring a substitution that does not happen.
//   - EMPTY PARENTS COLLAPSE. Pruning projects.${workspace} out of {"projects":{…}} leaves
//     {"projects":{}}, and applyRMWLayer would faithfully write that empty object into the
//     user's ~/.claude.json — a key yolo asserted for no reason. A parent left empty BY the
//     prune is dropped with it (one that was declared empty is not, since nothing pruned it).
//   - LEAVES ARE REPORTED, not branch roots. "projects.${workspace}.hasTrustDialogAccepted"
//     says which declaration was not honored; "projects" would not.
func PruneWorkspaceKeyed(s manifest.Surface) (manifest.Surface, []string) {
	var pruned []string
	s.Defaults = pruneWorkspaceValue(s.Defaults, "", &pruned)
	s.Managed = pruneWorkspaceValue(s.Managed, "", &pruned)
	sort.Strings(pruned)
	return s, pruned
}

// pruneWorkspaceValue deep-copies v without its ${workspace}-keyed branches, appending each
// dropped LEAF's dotted path to pruned. A non-map value is returned as-is (there are no keys
// to prune); a map that becomes empty because everything under it was pruned returns nil, so
// the caller can drop the parent too.
func pruneWorkspaceValue(v any, prefix string, pruned *[]string) any {
	m, isMap := v.(map[string]any)
	if !isMap {
		return v
	}
	out := make(map[string]any, len(m))
	for _, k := range sortedKeys(m) {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if strings.Contains(k, agentcfg.WorkspacePlaceholder) {
			collectLeafPaths(m[k], path, pruned)
			continue
		}
		sub, isSubMap := m[k].(map[string]any)
		if !isSubMap {
			out[k] = m[k]
			continue
		}
		kept := pruneWorkspaceValue(sub, path, pruned)
		if kept == nil {
			continue // everything under this parent was pruned — drop the parent too
		}
		out[k] = kept
	}
	if len(out) == 0 && len(m) > 0 {
		return nil // fully pruned: signal the caller to drop this branch
	}
	return out
}

// collectLeafPaths appends the dotted path of every LEAF under v (or v's own path when it is
// not an object) — what a pruned branch cost the user, stated per declaration rather than
// per branch root.
func collectLeafPaths(v any, prefix string, out *[]string) {
	m, isMap := v.(map[string]any)
	if !isMap || len(m) == 0 {
		*out = append(*out, prefix)
		return
	}
	for _, k := range sortedKeys(m) {
		collectLeafPaths(m[k], prefix+"."+k, out)
	}
}

// layerIsEmpty reports whether a surface layer carries nothing to write. nil and an empty
// object both mean "no content"; any other value (a keyless surface's whole-file scalar or
// list) is content.
func layerIsEmpty(v any) bool {
	if v == nil {
		return true
	}
	if m, isMap := v.(map[string]any); isMap {
		return len(m) == 0
	}
	return false
}

// There is no ${VAR} reporting here any more, and its absence is deliberate (2026-08-03).
//
// A host render never resolved variables, so this file used to name every ${VAR} that reached
// the user's config LITERAL, on the theory that an unresolved reference in an MCP `url` is a
// silent 401. Two things were wrong with it. The message's first remedy was "put the value in
// the file directly" — advice to inline a live credential into a file a pack may carry. And it
// was surface-wide, blind to WHERE the reference sat, so it flagged the `env` case where a
// literal ${VAR} is the correct and desired content (the launching agent expands it) with the
// same words as the `url` case where it is not.
//
// The warning existed to paper over yolo's own jail-side interpolation, which has since been
// removed for structural reasons (see the long note in mcp.go). With yolo out of the
// resolution business at BOTH notches, the two notches agree: the literal is what gets
// written, and whoever launches the server resolves it. There is nothing asymmetric left to
// warn about.
