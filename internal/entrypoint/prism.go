package entrypoint

// prism.go wires the agentcfg composition engine (the "prism") into boot,
// surface by surface. It is the boot-side counterpart of `yolo config render`:
// where that command previews a surface host-side, this RENDERS it into the jail
// home and persists the §5 sidecars so in-jail edits survive regeneration.
//
// This is the first entrypoint code to import internal/agentcfg — the
// config-composition cutover (docs/plans/agent-settings-composition.md §6,
// docs/design/config-migration-to-prism.md). Each Configure*Prism function
// replaces one bespoke Configure* writer once its surface is verified at parity;
// pi is the proof-of-concept (§4.3). The bespoke writers are deleted in Phase C.
//
// Responsibilities that stay HERE (not in the pure engine):
//   - resolving the HOST SOURCE for a surface — in-jail that is a :ro mount
//     (/ctx/host-pi/settings.json), gated by the host_*_files allow-list, which
//     is environment-dependent and so cannot live in the codec-agnostic manifest;
//   - the sidecar file layout under <workspace>/.yolo/prism/ (§5);
//   - loading the config.lua transform (user then workspace, §3.4);
//   - the one-time §4.7 orphan-file cleanup, gated on the first-migration signal.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// prismSidecarDir is the per-workspace directory holding the §5 capture-diff
// sidecars (last_render + overlay). It lives under the workspace's gitignored
// .yolo/ — the overlay is per-workspace scope (§4) and the agent never sees it.
func prismSidecarDir(e *Env) string {
	return e.renderTarget().SidecarDir()
}

// prismLastRenderPath is the last_render sidecar for one surface: the exact
// surface-codec bytes yolo wrote last boot (§5).
func prismLastRenderPath(e *Env, agent, name string) string {
	return filepath.Join(prismSidecarDir(e), agent+"-"+name+".last_render")
}

// prismProvenancePath is the provenance record for one surface: per-key "which
// layer set this key" (Compose already computes it; this persists it). It is what
// makes config-overlay overrides legible
// ("key X: claude pack lost to house-rules overlay") and generalizes to the
// footprint's per-key record. Additive: a new file beside the surface, never a
// change to the surface bytes.
//
// Target-keyed (render.Target.ProvenancePath), because this is the ONE sidecar the host
// notch keeps too — a host render is pure RMW, so it has no last_render baseline and no
// capture overlay, but it still knows which layer won each key, and without the record
// `yolo config diff` at the host has nothing to annotate from and guesses. The Target
// decides where: the jail's .yolo/prism tree, or the rendered home's state dir. See
// render.Target.ProvenanceDir for why the state dir and not the two alternatives.
func prismProvenancePath(e *Env, agent, name string) string {
	return e.renderTarget().ProvenancePath(agent, name)
}

// writeProvenanceRecord persists a surface's per-key winning layers, best-effort.
//
// Three properties, all deliberate, all shared by the jail and host callers:
//
//   - ADDITIVE. A new file, never a change to the surface bytes, so it cannot regress
//     the A12-fatal render (or perturb the render fingerprint gate).
//   - BEST-EFFORT. The surface itself is already written by the time this runs, so a
//     failure here must not fail the boot or the apply. It is warned, not returned.
//   - EMPTY IS WRITTEN, not skipped. An empty record says "rendered, and no keys were
//     attributed"; an absent one says "never rendered here". Collapsing the two would
//     make `config diff` report an unrendered surface as one where every overlay lost.
//
// A Target with nowhere to keep a record (no home at all) is a silent no-op: there is
// no user-facing decision to report, and warning on every such render would be noise.
func writeProvenanceRecord(e *Env, agent, name string, provenance map[string]string) {
	path := prismProvenancePath(e, agent, name)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.warn("warning: could not create the provenance dir for " +
			agent + "/" + name + ": " + err.Error())
		return
	}
	text := strings.Join(provenanceLines(provenance), "\n")
	if text != "" {
		text += "\n"
	}
	if err := writeInPlaceString(path, text); err != nil {
		e.warn("warning: could not write provenance for " + agent + "/" + name + ": " + err.Error())
	}
}

// provenanceLines renders a key→layer map as sorted "key\tlayer" lines — the same shape
// agentcfg.Result.ProvenanceLines produces, for the callers that build the map themselves
// because their mode has no Result to read it from (rmw has no layer fold). Sorted so a
// re-render writes byte-identical output.
func provenanceLines(provenance map[string]string) []string {
	keys := make([]string, 0, len(provenance))
	for k := range provenance {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"\t"+provenance[k])
	}
	return lines
}

// prismOverlayPath is the overlay sidecar for one surface: the accumulated
// in-jail edits, always JSON (the one codec that round-trips null tombstones).
func prismOverlayPath(e *Env, agent, name string) string {
	return filepath.Join(prismSidecarDir(e), agent+"-"+name+".overlay.json")
}

// loadPrismTransformScript concatenates the user then workspace config.lua
// (§3.4), user first so the workspace transform runs last. Built from the Env's
// resolved Home/Workspace (not the process $HOME) so it is testable and correct
// on a native-macOS home. A missing file contributes nothing; neither present
// means the identity transform. Mirrors internal/cli.loadTransformScript, which
// serves the host-side render — the two must stay in sync (§6: "what render
// prints is what the jail gets").
func loadPrismTransformScript(e *Env) string {
	return targetTransformScript(e.renderTarget())
}

// targetTransformScript is the Target-keyed transform loader: user config.lua (under
// the target's home) then workspace config.lua (under the target's workspace), user
// first so the workspace transform runs last. The boot path reaches it via
// loadPrismTransformScript(e). This is the convergence point the old
// "mirrors internal/cli.loadTransformScript — the two must stay in sync" comment asked
// for: one Target-keyed loader instead of two hand-copies.
func targetTransformScript(t render.Target) string {
	var b strings.Builder
	userLua := filepath.Join(t.Home, ".config", "yolo-jail", "config.lua")
	if data, err := os.ReadFile(userLua); err == nil {
		b.Write(data)
		b.WriteByte('\n')
	}
	// A host target has NO workspace (render.Host leaves it empty by definition), so
	// there is no workspace transform to load — and joining anyway would yield a bare
	// relative "yolo-jail.config.lua" read out of whatever directory the process is
	// sitting in, which is the same scatter-into-the-CWD hazard the provenance path had
	// to solve. The user config.lua above is still honored: it is keyed on the home,
	// which a host target does have.
	if t.Workspace != "" {
		wsLua := filepath.Join(t.Workspace, "yolo-jail.config.lua")
		if data, err := os.ReadFile(wsLua); err == nil {
			b.Write(data)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// surfaceScript is the Lua source for ONE surface: the global config.lua pair
// (user then workspace, §3.4) with the surface's OWN hook appended last, so a
// per-surface transform runs after — and can therefore override — the globals.
//
// A9: Surface.Transform used to be inert. It is a documented host_files key
// ("path to a Lua hook; works on every codec"), schema-validated, parsed,
// path-cleaned and copied onto the surface — but nothing read it, because every
// Inputs.Script producer filled Script from the global pair alone. A user's
// per-surface hook was silently ignored.
//
// A NAMED-BUT-UNREADABLE hook is a hard error, not a skip. The alternative fails
// open: the user asked for a transform, got none, and the file looks plausibly
// correct — the exact silent-misconfiguration class the config surface must not
// have. (An ABSENT Transform is simply the identity transform; only a named path
// that cannot be read fails.)
func surfaceScript(e *Env, surface manifest.Surface) (string, error) {
	script := loadPrismTransformScript(e)
	if surface.Transform == "" {
		return script, nil
	}
	data, err := os.ReadFile(surface.Transform)
	if err != nil {
		return "", fmt.Errorf("surface %s/%s: transform %s: %w",
			surface.Agent, surface.Name, surface.Transform, err)
	}
	return script + "\n" + string(data) + "\n", nil
}

// renderSurfaceStateful runs the §5/§3.2 stateful render for one builtin surface
// and persists the three artifacts (surface file, last_render, overlay). It
// resolves the host source via hostBytes (caller supplies, since the mount and
// allow-list are surface-specific), reads the two sidecars, composes, and writes
// everything back. It returns the StatefulOutput so the caller can act on
// FirstMigration (e.g. the §4.7 orphan cleanup).
//
// computed is yolo's per-boot DYNAMIC layer (§4 computed slot) — content derived
// from live config rather than the static manifest: the reconciled MCP-server
// table (codex/opencode), claude's LSP-driven enabledPlugins toggles and
// env.ENABLE_LSP_TOOL, and the mcpServers tombstone that strips a host block.
// jsonx-sourced content (the MCP tables) is deep-converted to the engine's plain
// value model via prismMap first; claude builds its layer as native map[string]any
// directly. It merges ABOVE the captured overlay and BELOW the transform +
// managed, so yolo's freshly regenerated data wins over a stale in-jail edit
// (regenerate-don't-reconcile) yet a managed key still wins the floor. A nil
// value inside it is an RFC-7386 tombstone: the key is deleted from the render
// and omitted from the output. Pass nil for a static-only surface (copilot/agy/pi
// settings).
//
// A recoverable on-disk condition never aborts boot (ComposeStateful self-heals
// corrupt/absent sidecars); only a genuine error (unknown codec, Lua failure)
// propagates, and boot's genStep downgrades even that to a warning.
func renderSurfaceStateful(e *Env, agent, name string, hostBytes []byte, computed map[string]any) (*agentcfg.StatefulOutput, error) {
	surface, ok := agentcfg.BuiltinManifest().Lookup(agent, name)
	if !ok {
		return nil, &missingSurfaceError{agent: agent, name: name}
	}
	// No overlays: this entry renders CORE's own surfaces (mise/config), and a
	// config-overlay names a surface a PACK owns. A pack pointing an overlay at a core
	// surface is reported as ownerless rather than honored here — see packoverlay.Collect.
	return renderSurfaceStatefulSurface(e, surface, hostBytes, computed, nil)
}

// renderSurfaceStatefulSurface is the surface-taking core of the stateful
// render, split out of renderSurfaceStateful so host_files can render a
// user-declared surface (Agent="user", Name=slug) that is NOT in the builtin
// manifest. The sidecar paths key on surface.Agent/surface.Name, so a user
// surface's sidecars are user-<slug>.{last_render,overlay.json} — collision-free
// with any builtin (no builtin agent is "user", and the slug is injective on the
// destination path).
// overlays are the config-overlay layers other packs contribute to this surface (nil for
// none, the universal case today). They fold BELOW the capture overlay, so a user's
// in-jail edit still wins over another pack's contribution.
func renderSurfaceStatefulSurface(e *Env, surface manifest.Surface, hostBytes []byte, computed map[string]any, overlays []agentcfg.Overlay) (*agentcfg.StatefulOutput, error) {
	// A11: resolve ${workspace} in the surface's layer DATA before composing. The
	// workspace root is not always "/workspace" (YOLO_WORKSPACE; macos-user has no
	// /workspace), so a literal in the manifest would assert keys under a path the
	// agent never looks at.
	surface = agentcfg.SubstituteWorkspace(surface, e.WorkspaceDir())
	surfacePath := expandHomePath(e, surface.Path)
	current, _ := os.ReadFile(surfacePath) // absent => nil, treated as no current file

	lastRenderPath := prismLastRenderPath(e, surface.Agent, surface.Name)
	lastRenderBytes, lastErr := os.ReadFile(lastRenderPath)
	overlayJSON, _ := os.ReadFile(prismOverlayPath(e, surface.Agent, surface.Name))

	script, serr := surfaceScript(e, surface)
	if serr != nil {
		return nil, serr
	}
	var vm luahook.LuaVM
	if script != "" {
		vm = &luahook.GopherLuaVM{}
	}

	out, err := agentcfg.ComposeStateful(agentcfg.StatefulInputs{
		Base: agentcfg.Inputs{
			Surface:   surface,
			HostBytes: hostBytes,
			Overlays:  overlays,
			Computed:  computed,
			Script:    script,
			VM:        vm,
		},
		CurrentBytes:      current,
		LastRenderPresent: lastErr == nil,
		LastRenderBytes:   lastRenderBytes,
		OverlayJSON:       overlayJSON,
	})
	if err != nil {
		return nil, err
	}

	// Persist the render to the jail surface path.
	if err := os.MkdirAll(filepath.Dir(surfacePath), 0o755); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(surfacePath, generatedHeader(surface)+surfaceText(surface, out.Result.Encoded)); err != nil {
		return nil, err
	}

	// Persist the two sidecars (last_render matches the surface bytes exactly, so
	// the next boot's mergeDiff has a truthful baseline). The last_render sidecar
	// must be the SAME bytes written to the surface — so it goes through the same
	// surfaceText codec-aware terminator, or the next boot's diff sees a spurious
	// change on a keyless surface.
	if err := os.MkdirAll(prismSidecarDir(e), 0o755); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(lastRenderPath, surfaceText(surface, out.LastRenderBytes)); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(prismOverlayPath(e, surface.Agent, surface.Name), string(out.OverlayJSON)+"\n"); err != nil {
		return nil, err
	}
	// Provenance record: the per-key winning layer Compose already computed. Additive,
	// best-effort, and empty-is-written — see writeProvenanceRecord for why each of the
	// three matters.
	if out.Result != nil {
		writeProvenanceRecord(e, surface.Agent, surface.Name, out.Result.Provenance)
	}
	noteCapturedOverlay(e, surface, out)
	return out, nil
}

// noteCapturedOverlay prints a one-line boot notice when a surface renders with a
// NON-EMPTY capture overlay. It is the boot-time half of `yolo config ls`: an
// overlay outranks the host layer permanently, so a divergence that is only
// recorded in a sidecar is invisible state — the notice makes it something the
// user sees at the moment it is applied, with the command that explains it.
//
// Deliberately quiet in the normal case (an empty overlay says nothing), and
// deliberately not a warning: capture is a supported mode, not a fault.
func noteCapturedOverlay(e *Env, surface manifest.Surface, out *agentcfg.StatefulOutput) {
	if out == nil || e.Stderr == nil {
		return
	}
	n := overlayEntryCount(out.OverlayJSON)
	if n == 0 {
		return
	}
	unit := "keys"
	if n == 1 {
		unit = "key"
	}
	e.warn(fmt.Sprintf("%s: %d %s from captured in-jail edits (yolo config diff %s)",
		surface.Path, n, unit, surface.Agent))
}

// overlayEntryCount reports how many captured entries an overlay sidecar holds: the
// key count for an object surface, and 1 for a non-empty KEYLESS surface (raw or
// lines, whose overlay is a whole-file scalar or list — such a file has exactly one
// "key", itself). 0 means the overlay contributes nothing.
func overlayEntryCount(overlayJSON []byte) int {
	if len(bytes.TrimSpace(overlayJSON)) == 0 {
		return 0
	}
	var v any
	if err := json.Unmarshal(overlayJSON, &v); err != nil || v == nil {
		return 0
	}
	switch t := v.(type) {
	case map[string]any:
		return len(t)
	case []any:
		if len(t) == 0 {
			return 0
		}
		return 1
	case string:
		if t == "" {
			return 0
		}
		return 1
	default:
		return 1
	}
}

// surfaceText renders a surface's encoded bytes as the exact file text to write.
// Object codecs (json/toml) emit no trailing newline, so one is appended to make
// a well-formed text file (matching the bespoke writers' dumpJSONIndent2 "+\n").
// Keyless codecs must NOT get an appended newline: raw promises a byte-exact
// Decode→Encode round-trip (a stray "\n" corrupts it), and lines already
// terminates every line with "\n" (a second one decodes back as a spurious
// trailing empty element, which would also poison the §5 last_render baseline).
func surfaceText(surface manifest.Surface, encoded []byte) string {
	if surface.Kind() == codec.KindObject {
		return string(encoded) + "\n"
	}
	return string(encoded)
}

// generatedHeader is the "yolo generated this" banner prepended to a composed
// surface FILE (A10). It exists so an agent that opens the file sees, in the file
// itself, that hand-editing it is the wrong move and where to look instead —
// steering, not enforcement (§8: an agent is a directed writer, so a legible
// signal beats a permission bit it can work around).
//
// TOML ONLY, and the constraint is real, not conservatism:
//   - json has NO comment syntax, so a banner would make the file invalid;
//   - raw promises a byte-exact Decode→Encode round-trip and lines round-trips
//     element-for-element, so ANY inserted text corrupts the contract.
//
// It is applied to the SURFACE write only, never to the last_render sidecar: that
// sidecar is the §5 capture baseline and must match what the ENGINE produced, or
// the next boot's mergeDiff sees the banner itself as a user edit. Verified by
// probe that a leading TOML comment yields an empty overlay ({}) rather than a
// captured change — the diff runs on decoded values, so the comment is invisible
// to it either way; keeping it out of the baseline is belt-and-braces plus it
// keeps the sidecar a faithful record of the render.
func generatedHeader(surface manifest.Surface) string {
	if surface.Codec != "toml" {
		return ""
	}
	return "# Generated by yolo-jail — composed at jail start; hand edits may be\n" +
		"# reverted or lost. Run `yolo config ls` to see how, and change the config\n" +
		"# input instead (`yolo config-ref`).\n"
}

// renderSurfaceStatelessSurface is the surface-taking core of the stateless
// render. Unlike renderSurfaceComputed it accepts hostBytes: host_files
// readonly/copy/once modes seed the `host` layer from a /ctx mount (or inline
// content), and dropping that layer would compose only defaults<computed<managed
// — silently losing the file's actual content. It writes ONLY the surface file
// (no sidecars) and returns the Result so a caller can chmod or inspect it.
func renderSurfaceStatelessSurface(e *Env, surface manifest.Surface, hostBytes []byte, computed map[string]any, overlays []agentcfg.Overlay) (*agentcfg.Result, error) {
	surface = agentcfg.SubstituteWorkspace(surface, e.WorkspaceDir()) // A11, see the stateful core
	script, serr := surfaceScript(e, surface)
	if serr != nil {
		return nil, serr
	}
	var vm luahook.LuaVM
	if script != "" {
		vm = &luahook.GopherLuaVM{}
	}

	res, err := agentcfg.Compose(agentcfg.Inputs{
		Surface:   surface,
		HostBytes: hostBytes,
		Overlays:  overlays,
		Computed:  computed,
		Script:    script,
		VM:        vm,
	})
	if err != nil {
		return nil, err
	}

	surfacePath := expandHomePath(e, surface.Path)
	if err := os.MkdirAll(filepath.Dir(surfacePath), 0o755); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(surfacePath, generatedHeader(surface)+surfaceText(surface, res.Encoded)); err != nil {
		return nil, err
	}
	return res, nil
}

// missingSurfaceError is returned when a requested builtin surface is absent —
// a programmer error (the manifest and the caller disagree), surfaced loudly.
type missingSurfaceError struct{ agent, name string }

func (e *missingSurfaceError) Error() string {
	return "agentcfg builtin manifest missing surface " + e.agent + "/" + e.name
}

// expandHomePath expands a leading "~/" in a manifest surface path against the
// Env's resolved Home (the jail home, or the native-macOS home). Mirrors
// internal/cli.expandHome but keyed on the Env rather than the process $HOME.
func expandHomePath(e *Env, p string) string {
	return targetExpandHome(e.renderTarget(), p)
}

// targetExpandHome resolves a "~"-relative surface path against the TARGET's home. This
// is the seam that lets a host/preview target write into a different home than the jail;
// the boot path reaches it via expandHomePath(e, p) over e.renderTarget(). It replaces
// the old "mirrors internal/cli.expandHome but keyed on Env" duplication with one
// Target-keyed resolver both sides can converge on.
func targetExpandHome(t render.Target, p string) string {
	if p == "~" {
		return t.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(t.Home, p[2:])
	}
	return p
}

// renderSurfaceRMWSurface is the surface-taking core of the RMW render, so a
// PACK-DECLARED surface reaches the identical mechanism a builtin does. See
// renderSurfaceRMW for what RMW is and why it exists.
//
// computed is the surface's derived dynamic layer (the MCP-server block in
// ~/.claude.json). yolo OWNS each top-level key of it — it regenerates the block
// wholesale from config every boot, exactly like every other agent's MCP surface
// does (regenerate-don't-reconcile). A server the user added at USER scope through
// the agent's own UI is overwritten; the boot notes what it dropped so it is not
// silent. This replaced the sidecar-tracked `reconcile` mechanism (OQ12 (d)): a
// one-of-a-kind stateful special case for Claude, against "config is the source
// of truth". Pass nil computed for a plain RMW surface with no dynamic table.
//
// overlays are the config-overlay layers other packs contribute. On an RMW surface they
// are ASSERTED (force-written), not merely defaulted, because an overlay's body says
// `managed` — a contributor asserting a key means "keep this key at this value", so
// fill-if-absent would make the fzf case work once and then never update. They are
// applied FIRST so both the derived tables and the owner's own managed layer still win
// their keys, which is the §5 precedence (config-overlay < computed < managed) expressed
// in the one mode that has no layer fold to express it with.
//
// CODEC-AWARE at both ends, via surfacecodec.go. It used to be unconditionally JSON — read
// with loadObject (which silently yields {} for anything it cannot parse) and written with
// dumpJSONIndent2 — which was invisible while the only RMW surfaces the JAIL rendered were
// JSON, and destructive the moment `apply --host` made every surface RMW: a codex user's
// TOML config.toml was read as "no keys at all" and rewritten as JSON, so every key they
// owned disappeared. Now the surface's declared codec decides the decode AND the encode, and
// a file yolo cannot parse is REFUSED (returned as *rmwRefusedError, file untouched) rather
// than replaced from an empty object.
func renderSurfaceRMWSurface(e *Env, surface manifest.Surface, computed map[string]any, overlays []agentcfg.Overlay) error {
	surface = agentcfg.SubstituteWorkspace(surface, e.WorkspaceDir())

	// Codec gate FIRST, before any mkdir: a surface whose codec cannot round-trip through
	// RMW must leave no trace at all, not an empty parent directory.
	if refusal := rmwCodecRefusal(surface); refusal != nil {
		return refusal
	}
	path := expandHomePath(e, surface.Path)
	obj, err := decodeSurfaceObject(surface, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// The file's own top-level keys BEFORE the render, snapshotted for provenance: on an
	// rmw surface the existing content is the `host` layer, and it beats defaults
	// (fill-if-absent) while losing to everything yolo force-writes. Taken here because
	// the writes below mutate obj in place.
	present := obj.Keys()

	// config-overlay contributions: below everything yolo and the owner assert, above
	// the file's existing content (see the doc comment).
	for _, ov := range overlays {
		if layer, isMap := ov.Data.(map[string]any); isMap {
			applyRMWLayer(obj, layer, true)
		}
	}
	// Dynamic managed tables (MCP servers) FIRST, so a managed key nested under the
	// same parent still wins the floor.
	regenerateManagedTables(e, surface, obj, computed)
	// Managed: yolo owns these outright, so re-assert every boot.
	if managed, isMap := surface.Managed.(map[string]any); isMap {
		applyRMWLayer(obj, managed, true)
	}
	// Defaults: user-overridable, so fill only where the key is absent.
	if defaults, isMap := surface.Defaults.(map[string]any); isMap {
		applyRMWLayer(obj, defaults, false)
	}
	text, err := encodeSurfaceObject(surface, obj)
	if err != nil {
		return err
	}
	if err := writeInPlaceString(path, text); err != nil {
		return err
	}
	// Record which layer won each key — at the notches whose census says THIS MECHANISM is
	// the one that records (render.ModeSet). True at the host, false in a jail, and the
	// asymmetry is precisely the shape of the bug it fixes.
	//
	// In a JAIL, `rmw` keeping no sidecar is a documented design decision
	// (pack-config-collaboration.md §8): rmw is one mode among four, and the surfaces that
	// matter there are `stateful`, which do record. So an absent record on a jail rmw
	// surface is expected, `config diff` says exactly that, and adding one here would both
	// falsify that message and put a new write on the A12-fatal boot path for no gain.
	//
	// At the HOST notch rmw is not one mode among four — it is the ONLY mode
	// (`apply --host` is pure RMW by resolved decision, OQ-4). So "rmw records nothing"
	// there means "the host records nothing", which is what left `config diff` inferring a
	// winner from declarations and reporting an overlay as having LOST a key it in fact WON.
	//
	// ASKED OF THE CENSUS rather than of the Kind (plan §6b D2 / Q8). This was
	// `KindOf() == render.KindHost` — the codebase's only live KindHost special-case — and
	// the two spellings agree for every notch that exists. What the census adds is that the
	// two paragraphs above are now DATA a new notch has to answer for: `guest` reads
	// undecided (records nothing) until Phase 7 states its policy, instead of inheriting
	// whichever side of an equality test its Kind happened to fall on.
	//
	// AFTER the surface write, so a provenance failure cannot cost the render; derived
	// rather than read from a Result, because rmw has no layer fold to produce one.
	if e.renderTarget().Modes().Records(manifest.ModeRMW) {
		// The PREVIOUS record, read at the notch that keeps one. It is what makes a key
		// whose owning layer has gone away distinguishable from a key the user wrote — the
		// correct attribution exists only in the record one apply earlier, so a derivation
		// that ignored it would launder yolo's own output into "the user set this" on the
		// very next apply. See rmwProvenance's `previous` parameter.
		writeProvenanceRecord(e, surface.Agent, surface.Name,
			rmwProvenance(surface, present, computed, overlays,
				readProvenanceRecord(e, surface.Agent, surface.Name)))
	}
	return nil
}

// readProvenanceRecord loads the record a PREVIOUS render of this surface left, as
// key → winning layer, or nil when there is nothing trustworthy to read.
//
// FAIL-SAFE IS THE CONTRACT, and it is why this returns nil rather than an error. Its one
// caller uses the result to decide which keys yolo may still claim as its own output; so
// every unreadable state — no record, no path, an I/O failure — must mean "prove nothing",
// which leaves every key attributed exactly as a first-ever apply would attribute it (the
// file's content is the user's). A record cannot claim a key by being broken.
//
// Corruption inside a readable file is handled one line at a time rather than by rejecting
// the file: agentcfg.ParseProvenanceRecord skips any line without a tab, and the caller
// only honors layers agentcfg.LayerAsserted recognizes — a closed set of exact tokens. So
// garbage yields no claims while the intact lines around it still count, which is strictly
// better than letting one bad byte relaunder every key in the surface.
func readProvenanceRecord(e *Env, agent, name string) map[string]string {
	path := prismProvenancePath(e, agent, name)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return agentcfg.ParseProvenanceRecord(data)
}

// rmwProvenance derives the per-key winning layer for an RMW render, by REPLAYING the
// write order the function above performs.
//
// It has to be derived rather than read off a Result, and that is the whole reason this
// exists: `rmw` has no layer fold — it merges each layer into whatever is in the file, in
// order — so Compose never runs and there is no Result.Provenance to persist. Without
// this, the one mode the HOST notch uses for every surface (`apply --host` is pure RMW by
// resolved decision) would be the one mode that records nothing, which is exactly the gap
// that let `config diff` state the opposite of what happened.
//
// Built in ASCENDING precedence so a later layer overwrites the attribution, mirroring
// both the write order and Compose's own fold:
//
//	defaults < host (the file's existing content) < config-overlay < computed < managed
//
// RETIREMENT is the one thing here that is not a replay of this render, and it is not an
// exception to the ordering so much as a correction applied after it. A key the file HAS and
// no live layer claims gets `host` from the pass above — correct for a key the user wrote,
// and a lie for one yolo wrote last apply for a pack that has since been dropped from
// config. `previous` is what tells the two apart, and the retirement pass rewrites only the
// keys it can prove yolo force-wrote (agentcfg.LayerAsserted). See RetiredLayer.
//
// Per TOP-LEVEL key, matching Compose's documented contract — a layer that sets a NESTED
// key claims the whole top-level key, so an overlay contributing a sibling under a parent
// the owner also manages reads as `managed`, exactly as it would on a stateful surface.
// One coarseness in both places beats two different ones, since one reader serves both.
//
// ── SECOND OF TWO "which layer won" derivations. UNIFY AT THE THIRD, not before. ──
//
// The other is agentcfg.Compose (internal/agentcfg/compose.go), which folds the layer stack.
// The duplication is DELIBERATE and was ruled on (docs/plans/proposed-fixes-open-findings.md
// §8): forcing one implementation means either handing RMW a synthetic layer stack it does not
// have, or making Compose simulate sequential writes — both more fiction than the duplication.
//
// What keeps them honest meanwhile is the shared-corpus parity table
// (provenanceparity_test.go): one set of layer/key fixtures asserted against BOTH, so a
// divergence in OUTCOME fails, not merely one in shape. It also RECORDS the two places the two
// renders genuinely differ (a scalar `computed` key, and an overlay null tombstone) — in both
// the record is truthful about the file its own notch wrote, so closing the gap means changing
// a render, not a derivation.
//
// THE TRIGGER: a THIRD derivation. The likely one is the `guest` notch (env-manager Phase 7,
// unbuilt) — it renders into a real home like the host but keeps a workspace like the jail, so
// it may need a derivation of its own. That is the moment to unify all three (rule of three
// applied, rather than an abstraction guessed from two cases). If you are here adding it, this
// note and its twin on Compose are the two sites to collapse.
//
// ── PARAMETERS ─────────────────────────────────────────────────────────────────────────
//
// previous is the record a prior render of this surface left (nil for a first-ever apply, or
// whenever the record cannot be trusted — see readProvenanceRecord). It is consulted ONLY by
// the retirement pass at the end, and only to keep an attribution the record already made;
// nothing about this render's own outcome is read from it. That containment is what keeps the
// parity claim above intact: on the first render into a fresh home `previous` is nil, the
// retirement pass is a no-op, and this function is the pure write-order replay the shared
// corpus compares against Compose.
func rmwProvenance(surface manifest.Surface, present []string, computed map[string]any,
	overlays []agentcfg.Overlay, previous map[string]string) map[string]string {
	prov := map[string]string{}
	// defaults: the floor, and only where the key was absent — the `host` pass below
	// corrects the ones the file already had, which is what fill-if-absent means.
	if defaults, isMap := surface.Defaults.(map[string]any); isMap {
		for k := range defaults {
			prov[k] = agentcfg.LayerDefaults
		}
	}
	// The file's own content: on rmw the existing file IS the host layer. Recorded for
	// every key it had, including the ones yolo never declares — "this key is yours, yolo
	// did not set it" is a real answer to "why does this file say that".
	for _, k := range present {
		prov[k] = agentcfg.LayerHost
	}
	for _, ov := range overlays {
		layer, isMap := ov.Data.(map[string]any)
		if !isMap {
			continue
		}
		for k := range layer {
			prov[k] = agentcfg.OverlayLayer(ov.Pack)
		}
	}
	// Only OBJECT-valued computed keys are dynamic managed tables, matching
	// regenerateManagedTables — a non-object computed value is skipped there, so claiming
	// it here would attribute a write that never happened.
	for k, v := range computed {
		if _, isObj := v.(map[string]any); isObj {
			prov[k] = agentcfg.LayerComputed
		}
	}
	if managed, isMap := surface.Managed.(map[string]any); isMap {
		for k, v := range managed {
			if v == nil {
				continue // an RFC-7386 tombstone asserts no value to attribute
			}
			prov[k] = agentcfg.LayerManaged
		}
	}
	return retireUnclaimed(prov, previous)
}

// retireUnclaimed rewrites the attributions this render derived so a key yolo FORCE-WROTE for
// a layer that has since stopped claiming it reads as `retired:<that layer>` instead of
// `host`.
//
// THE DEFECT (host-pack-drop-cleanup.md, "The real defect: provenance laundering"). rmw
// derives `host` for every key the existing file has, then upgrades the ones a live layer
// claims. That is right for a key the user wrote and wrong for one YOLO wrote: drop the pack
// whose config-overlay contributed `fileSuggestion` and, on the next apply, the key is still
// in the file and no layer claims it, so the record flips from `config-overlay:dropme` to
// `host`. yolo's own output is relabelled as user content — and self-reinforcingly, since
// every mechanism that then asks "did yolo write this?" answers no. The previous record is
// the only place the true answer survives, so this pass carries it forward.
//
// The three conditions a key must meet, each one closing a way to get this wrong:
//
//	this render says `host`   Anything yolo still claims (managed/computed/a live overlay) is
//	                          attributed by the passes above and must not be touched — a
//	                          re-added pack's key goes straight back to its overlay label.
//	                          Restricting to `host` also means the pass can only ever
//	                          overwrite the ONE attribution that is a guess.
//	previously ASSERTED       agentcfg.LayerAsserted: managed, computed, or a config-overlay.
//	                          Never `host` (retiring the user's own key is the same laundering
//	                          in reverse, and the direction that costs something) and never
//	                          `defaults`, which is fill-if-absent — yolo writes it once and
//	                          the value is the user's from then on.
//	still in the file         A key the file no longer has is simply gone; there is nothing
//	                          left to attribute, and inventing an entry for it would report a
//	                          key the reader cannot find.
//
// Retirement is STICKY, deliberately: `retired:X` is itself an asserted-through label, so a
// key stays retired across later applies rather than laundering one apply later than it used
// to. Reached via RetiredOf so the label keeps naming the ORIGINAL layer instead of nesting
// into retired:retired:X.
//
// A nil `previous` (first apply, or an unreadable record) proves nothing and changes nothing —
// the fail-safe the caller's contract rests on.
func retireUnclaimed(prov, previous map[string]string) map[string]string {
	for k, layer := range prov {
		if layer != agentcfg.LayerHost {
			continue
		}
		was, seen := previous[k]
		if !seen {
			continue
		}
		if last, wasRetired := agentcfg.RetiredOf(was); wasRetired {
			prov[k] = agentcfg.RetiredLayer(last) // already retired: keep naming the original
			continue
		}
		if agentcfg.LayerAsserted(was) {
			prov[k] = agentcfg.RetiredLayer(was)
		}
	}
	return prov
}

// regenerateManagedTables replaces each dynamic managed table on an RMW surface
// (the `mcpServers` block) with the derived layer, wholesale — yolo owns the key,
// so its previous content is disposable output, not state to preserve.
//
// Each top-level key of `computed` whose value is an object is a managed dynamic
// table: its block is cleared and rewritten from the derived value. This is
// "regenerate, don't reconcile" (§2 principle 1) for the one RMW surface with a
// dynamic table. A server present in the file but absent from the derived layer is
// REMOVED: it was either yolo's from a prior boot (stale) or a server the user
// added through the agent's UI at user scope (which belongs in yolo's
// `mcp_servers` config, reaching every agent). Either way it is dropped, and the
// drop is announced (noteDroppedManagedEntries) so it is never a silent surprise.
// Local-scope servers (nested under a project path, not the top-level key) and the
// project `.mcp.json` are untouched — yolo only ever writes this one top-level key.
func regenerateManagedTables(e *Env, surface manifest.Surface, obj *jsonx.OrderedMap, computed map[string]any) {
	for _, to := range sortedKeys(computed) {
		table, isObj := computed[to].(map[string]any)
		if !isObj {
			continue // only object-valued derived keys are dynamic tables
		}
		dest := setDefaultMap(obj, to)
		noteDroppedManagedEntries(e, surface, to, dest, table)
		// Clear the block and rewrite it from the derived layer, deterministically.
		for _, existing := range dest.Keys() {
			dest.Delete(existing)
		}
		names := make([]string, 0, len(table))
		for name := range table {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			dest.Set(name, table[name])
		}
	}
}

// noteDroppedManagedEntries prints a one-line boot notice for each entry in the
// existing managed block that config does not (re)assert — the visible-drop
// requirement of OQ12 (d). Quiet when nothing is dropped. Not a warning: dropping
// a stale/hand-added entry is the intended behavior, but a user who added a server
// through the agent's UI must be told where it went, with the fix.
func noteDroppedManagedEntries(e *Env, surface manifest.Surface, key string, dest *jsonx.OrderedMap, table map[string]any) {
	if e.Stderr == nil {
		return
	}
	var dropped []string
	for _, name := range dest.Keys() {
		if _, kept := table[name]; !kept {
			dropped = append(dropped, name)
		}
	}
	if len(dropped) == 0 {
		return
	}
	sort.Strings(dropped)
	fmt.Fprintf(e.Stderr, "%s/%s: dropping from %s (not in config): %s "+
		"— add under `mcp_servers` to keep it, reaching every agent\n",
		surface.Agent, surface.Name, key, strings.Join(dropped, ", "))
}

// sortedKeys returns layer's keys in a deterministic order, so a re-render writes
// byte-identical output rather than shuffling with Go's map iteration.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// applyRMWLayer writes layer into obj. force=true overwrites (managed semantics);
// force=false only fills absent keys (default semantics). Nested objects recurse so
// a sibling key the agent owns under the same parent survives — the deep-merge
// behavior composition's Enforce already provides.
func applyRMWLayer(obj *jsonx.OrderedMap, layer map[string]any, force bool) {
	for _, k := range sortedKeys(layer) {
		v := layer[k]
		if sub, isMap := v.(map[string]any); isMap {
			applyRMWLayer(setDefaultMap(obj, k), sub, force)
			continue
		}
		if force {
			obj.Set(k, v)
		} else {
			setDefault(obj, k, v)
		}
	}
}
