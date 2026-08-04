package entrypoint

// hostrender.go is the host-target render entry (env-manager plan Phase 4): render a
// pack's config surfaces into the invoking user's REAL home, at the `host` confinement
// notch, reusing the same boot writers via an Env pointed at that home. It is the
// implementation `yolo apply --host` calls.
//
// The resolved decisions this encodes (env-manager plan OQ-1..4, host-render-target.md
// §6.3, §6.6):
//   - PURE RMW. Every surface is read-modify-written: yolo regenerates only the keys it
//     declares (managed + dynamic tables) and leaves every key the agent wrote. No
//     whole-file compose, so no capture overlay, so no --revert (OQ-4).
//   - PROVENANCE IS STILL RECORDED. "No sidecars" covers the two CAPTURE sidecars
//     (last_render, overlay), which pure RMW genuinely has no use for. It does not cover
//     the per-key winning-layer record: a host render knows which layer won each key, and
//     for a while it wrote that down nowhere, so `yolo config diff` at the host inferred
//     the winner from declarations and could state the opposite of what happened (an
//     overlay key with no competing `managed` value reported as "managed won"). The record
//     goes under the rendered home's STATE dir, not a workspace and not the user's config
//     dir — see render.Target.ProvenanceDir. Assert only: recording a winner in observe
//     posture would document a write that never happened.
//   - NO computed layer. The live MCP/LSP tables embed jail-absolute paths, so a host
//     render passes an empty computed map — a ${workspace}-derived value has no referent
//     off-container (OQ-2/§6.6) and such a surface is refused, not bound.
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
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// HostRenderResult reports, per surface, what a host render did (or would do, in
// observe/dry-run mode) — enough for `apply --host` to print an honest summary.
type HostRenderResult struct {
	Surface string // "agent/name"
	Path    string // resolved real-home path
	Action  string // "rendered" | "would render" | "refused: <reason>"
	// Overwrites lists the dotted managed keys whose EXISTING value in the real file
	// differs from what this render writes — the reviewer's "always warn on overwrite"
	// for the host notch (§4.2 / env-manager plan Phase 9). Empty when the render only
	// adds keys or re-asserts identical values. Populated in both observe and assert, so
	// the dry-run preview shows the collision BEFORE anything is written (finding D2).
	// A key coming from a config-overlay is labelled with the contributing pack, since
	// the remedy there is a different pack than the surface's owner.
	Overwrites []string
	// Overlays names the packs contributing config-overlay keys to this surface, in fold
	// order (later wins). It is the host-side half of ruling R3: an override folds in
	// below the owner's managed layer, so it is invisible in the resulting file, and
	// provenance nobody can read does not make it legible. Empty for the common case.
	Overlays []string
	// Pruned lists the dotted ${workspace}-keyed paths this host render DROPPED from the
	// surface's layers, in sorted order. Non-empty means the surface carried per-jail
	// content that has no host referent; the surface itself may still have rendered (see
	// pruneWorkspaceKeyed for why the two are now independent). Reported by name, never
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
	EntryLosses []string
	// FirstApply is true when this home has NO provenance record for this surface, i.e.
	// yolo has never asserted it here. It is the other half of the one-way-door signal:
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
	// JSON surface (JSON has no comments) and for an uncommented TOML one.
	Formatting []string
}

// RenderHostPack renders one pack's config surfaces into homeDir (the real $HOME), pure
// RMW, no computed layer. When observe is true it computes what WOULD change and writes
// nothing (the `apply --host --dry-run` / default `observe` posture). It returns one
// result per surface and does not run hooks or touch any non-config kind.
//
// homeDir is the real home; the Env's Workspace is left empty so a ${workspace} surface
// is refused rather than bound to some arbitrary dir.
//
// overlays is the CROSS-PACK config-overlay resolution (packoverlay.Collect over every
// pack this apply is asserting). It has to be a parameter rather than derived here:
// this function sees ONE pack, and an overlay in pack B targets a surface pack A owns, so
// a per-pack derivation would find none of the overlays the kind exists to carry. Pass nil
// for a caller that has no other packs in view.
func RenderHostPack(p *packload.Pack, homeDir string, observe bool, overlays *packoverlay.OverlaySet) ([]HostRenderResult, error) {
	// hostTarget: this Env drives render.Host, not render.Jail. Load-bearing for every
	// Target-keyed path the writers resolve — without it an empty Workspace reads as the
	// container default "/workspace" (WorkspaceDir()), so a host apply would write its
	// provenance into some jail's .yolo/prism tree and read a workspace config.lua that
	// has nothing to do with this render. See Env.hostTarget.
	e := &Env{Home: homeDir, Vars: map[string]string{}, hostTarget: true}
	// Host is the autonomy-OFF notch (§4.2): render the GUARDED posture, so a pack's
	// jail-bypass permission keys do NOT reach the real home. This is the fix for the
	// apply --host bypass leak.
	surfaces, problems := p.SurfacesFor(false)
	if len(problems) > 0 {
		return nil, fmt.Errorf("pack %s: %s", p.Name, problems[0])
	}

	var out []HostRenderResult
	for _, s := range surfaces {
		id := s.Agent + "/" + s.Name
		path := expandHomePath(e, s.Path)

		if s.ResolvedMode() == manifest.ModeUnrendered {
			continue // declared but never written, at any target
		}
		// PRUNE the ${workspace}-keyed branches rather than refusing the surface (see
		// pruneWorkspaceKeyed). What remains is target-independent and renders; what was
		// dropped is named, either in the surface's own result line or — when nothing
		// survives — in the skip reason.
		s, pruned := pruneWorkspaceKeyed(s)
		surfaceOverlays := overlays.For(s.Agent, s.Name)
		if len(pruned) > 0 && layerIsEmpty(s.Managed) && layerIsEmpty(s.Defaults) &&
			len(surfaceOverlays) == 0 {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "skipped: only ${workspace}-keyed keys, which have no host referent"})
			continue
		}
		// DYNAMIC MANAGED TABLES at the host notch. yolo owns each of these keys wholesale, so
		// they are written by replacement (regenerateManagedTables) rather than deep-merged —
		// and stripped from the managed layer so nothing merges them back. Without this an
		// http MCP entry and a stdio one declaring the same server name merged into a record
		// carrying BOTH transports, which no client can use: a "nothing was lost" merge that
		// silently breaks the server. Which keys are tables comes from the pack's own derive
		// (hostTableKeys); the CONTENT comes from its declared layers, never from liveTables.
		tables := hostTableKeys(p, s)
		tableLayer := hostTableLayer(tables, s, surfaceOverlays)
		s = stripTableKeys(s, tables)
		// The OVERLAYS are deliberately NOT stripped. They are applied before the table
		// write, so regenerateManagedTables clears whatever they merged and rewrites the block
		// from tableLayer — the merge is transient and the result identical. Leaving them
		// means rmwProvenance still sees which pack contributed the key, which is R3's
		// "an override must stay legible".
		//
		// REFUSAL PROBE, before anything else is computed and in BOTH postures. A host render
		// is pure RMW, so a surface whose codec cannot round-trip through RMW — or whose
		// existing file yolo cannot parse — must not be written; and observe's job is to say
		// so BEFORE an --assert reaches the file. Probing here rather than only at the write
		// is what makes `--dry-run` an honest preview of a refusal instead of promising a
		// render that will not happen.
		if refusal := hostRMWRefusal(s, path); refusal != nil {
			out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
				Action: "refused: " + refusal.Reason()})
			continue
		}
		// FIRST-APPLY detection, read BEFORE the write: the provenance record is the only
		// per-home mark yolo leaves at this notch, so its absence is what "yolo has never
		// asserted this surface here" means. Computed for both postures, because observe's
		// job is to tell the user what an --assert would do.
		firstApply := !hostProvenanceExists(e, s)
		// Compute which managed keys would OVERWRITE a differing existing value, from the
		// file as it stands now — before any write, so observe reports the same collisions
		// assert would cause.
		overwrites := managedOverwrites(e, s, path)
		// An overlay ASSERTS its keys on the host too, so a key it would clobber is the
		// same always-warn case a managed key is (§4.2). Attributed to the contributing
		// pack, because "which pack is about to overwrite my value" is the question R3
		// exists to keep answerable.
		overwrites = append(overwrites, overlayOverwrites(e, s, path, surfaceOverlays)...)
		// What the wholesale table write costs, per entry. Kept SEPARATE from Overwrites —
		// see HostRenderResult.EntryLosses for why the distinction is what makes the
		// confirmation gate usable rather than noise.
		losses := tableLosses(s, tables, path, tableLayer)
		// Non-value losses from the canonical re-emit (a TOML file's comments). Computed in
		// both postures for the same reason the overwrites are: the point is to see it before
		// the write.
		formatting := hostFormattingLosses(s, path)
		if observe {
			out = append(out, HostRenderResult{Surface: id, Path: path, Action: "would render",
				Overwrites: overwrites, Overlays: overlayPackNames(surfaceOverlays),
				Pruned: pruned, EntryLosses: losses,
				FirstApply: firstApply, Formatting: formatting})
			continue
		}
		// Pure RMW into the real home. The `computed` slot carries ONLY the wholesale table
		// layer built from the pack's DECLARED content above — not a jail-derived one, whose
		// values embed jail-absolute paths and have no host referent (OQ-4/§6.6). That
		// distinction is the whole reason hostTableLayer exists.
		//
		// A REFUSAL here is a per-surface result, not a pack-level error. The probe above has
		// already caught every refusal this render can predict; one arriving at the write is a
		// condition that appeared in between (or an unencodable composed value), and the file
		// is untouched either way — so the honest report is this surface's line, with the
		// remaining surfaces still rendered. Returning an error would abort the pack over a
		// file yolo deliberately left alone.
		if err := renderSurfaceRMWSurface(e, s, tableLayer, surfaceOverlays); err != nil {
			if refusal, isRefusal := asRMWRefusal(err); isRefusal {
				out = append(out, HostRenderResult{Surface: id, Path: path, Pruned: pruned,
					Action: "refused: " + refusal.Reason()})
				continue
			}
			return out, fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, HostRenderResult{Surface: id, Path: path, Action: "rendered",
			Overwrites: overwrites, Overlays: overlayPackNames(surfaceOverlays),
			Pruned: pruned, EntryLosses: losses,
			FirstApply: firstApply, Formatting: formatting})
	}
	return out, nil
}

// hostRMWRefusal reports why this surface cannot be read-modify-written in this home, or nil
// when it can. It is the OBSERVE-side half of the writer's own gate: the same two checks
// renderSurfaceRMWSurface performs (a codec RMW cannot express, and an existing file yolo
// cannot parse), run without writing.
//
// Duplicating the checks rather than "just try the write and catch the refusal" is what makes
// the dry-run truthful. `apply --host` (no --assert) must print `refused: …` for a surface an
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

// hostFormattingLosses names what a codec-canonical re-emit costs beyond values — today,
// exactly one thing: a TOML file's comments.
//
// yolo re-emits a TOML surface through the shared deterministic emitter
// (internal/agentcfg/codec), which renders values and nothing else. Every value round-trips;
// every comment is gone. Comment preservation is BACKLOG E4 (open, deliberately unbuilt), so
// this is a REPORT rather than a fix — and it has to be a report rather than silence, because
// a user whose config.toml documents each setting loses that prose while every diff-visible
// value looks correct.
//
// JSON surfaces yield nothing (JSON has no comment syntax, so there is nothing to lose), and
// so does an uncommented TOML file — the line only appears when there is a real loss.
func hostFormattingLosses(s manifest.Surface, path string) []string {
	if s.Codec != "toml" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || !tomlHasComments(raw) {
		return nil
	}
	return []string{"comments in this file are NOT preserved — yolo re-emits it from the " +
		"decoded values (every value survives; the comments do not)"}
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
//   - The CONTENT does not, and must not. A jail's derived MCP table embeds jail-absolute
//     paths (the mcp-wrappers node shim), which is exactly why a host render passes no
//     computed layer (OQ-4/§6.6). So the entries written at the host come from the pack's
//     own declared layers, never from liveTables.
//
// The alternative was a SHAPE heuristic ("an object whose values are all objects is a
// table"), which would have guessed `mcpServers` right and had no principled answer for the
// next key. Asking the pack is a declaration, and it is the same declaration the jail path
// already uses — so the two notches cannot disagree about which keys are tables.
//
// A pack with no derive, or a surface with no producer, has no tables: the result is nil and
// every key merges, which is the pre-existing behavior for every other surface.
func hostTableKeys(p *packload.Pack, s manifest.Surface) []string {
	script := loadPackDeriveScript(p)
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
		manifest.SourceMCPServers: sentinel,
		manifest.SourceLSPServers: sentinel,
	}
	derived, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, probe)
	if err != nil {
		return nil // a broken derive is the jail path's error to report, not this one's
	}
	var keys []string
	for k, v := range derived {
		// Only OBJECT-valued keys are tables, matching regenerateManagedTables exactly — a
		// tombstone or scalar is an ordinary managed key.
		if _, isObj := v.(map[string]any); isObj {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// hostTableLayer builds the wholesale content for each of the surface's table keys from the
// pack's DECLARED layers — the overlays a contributing pack asserts, then the owner's own
// managed layer on top (the §5 precedence config-overlay < managed, which is the same order
// renderSurfaceRMWSurface applies them in).
//
// Returned in the shape renderSurfaceRMWSurface's `computed` parameter takes, so the host
// reuses regenerateManagedTables verbatim rather than growing a second wholesale-replace
// implementation. A table key with no declared entries yields an EMPTY map, and that is
// meaningful rather than a no-op: it says "yolo owns this table and config declares nothing",
// which is what clears a stale block.
func hostTableLayer(tables []string, s manifest.Surface, overlays []agentcfg.Overlay) map[string]any {
	if len(tables) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, key := range tables {
		entries := map[string]any{}
		for _, ov := range overlays {
			if layer, isMap := ov.Data.(map[string]any); isMap {
				mergeEntries(entries, layer[key])
			}
		}
		if managed, isMap := s.Managed.(map[string]any); isMap {
			mergeEntries(entries, managed[key])
		}
		out[key] = entries
	}
	return out
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
// e.Stderr and the host Env has none — by design, since `apply --host` reports through its
// RESULT (so observe can show the same lines without a render having happened) rather than
// through boot-notice side effects. Wiring Stderr here instead would produce the notice only
// in assert, which is the posture where it is least useful: the point is to see the loss
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
	existing := existingSurfaceObject(s, path)
	var out []string
	for _, key := range tables {
		cur, present := existing.Get(key)
		curTable, isMap := cur.(*jsonx.OrderedMap)
		if !present || !isMap {
			continue // no such table in the user's file — every entry is an ADD
		}
		declared, _ := layer[key].(map[string]any)
		for _, name := range curTable.Keys() {
			prev, _ := curTable.Get(name)
			incoming, isDeclared := declared[name]
			if !isDeclared {
				out = append(out, fmt.Sprintf("%s.%s (dropped — not in your config)", key, name))
				continue
			}
			if !sameJSON(prev, incoming) {
				out = append(out, fmt.Sprintf("%s.%s (replaced — your version is not kept)",
					key, name))
			}
		}
	}
	sort.Strings(out)
	return out
}

// overlayPackNames lists the packs contributing overlays to a surface, in fold order —
// the provenance the caller prints so an override is legible in `apply --host` output
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
func overlayOverwrites(e *Env, s manifest.Surface, path string, overlays []agentcfg.Overlay) []string {
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
			out = append(out, k+" (config-overlay from "+ov.Pack+")")
		}
	}
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
// values `apply --host` was actually about to overwrite was the one surface that reported
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
	for _, k := range sortedKeys(managed) {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		mv := managed[k]
		cur, present := existing.Get(k)
		if !present {
			continue // an ADD, not an overwrite
		}
		if sub, isMap := mv.(map[string]any); isMap {
			if curMap, ok := cur.(*jsonx.OrderedMap); ok {
				collectOverwrites(curMap, sub, key, out)
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

// pruneWorkspaceKeyed returns s with every ${workspace}-KEYED branch removed from its
// Defaults and Managed layers, plus the sorted dotted paths it dropped.
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
func pruneWorkspaceKeyed(s manifest.Surface) (manifest.Surface, []string) {
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
