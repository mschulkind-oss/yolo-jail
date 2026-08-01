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
	return targetSidecarDir(e.renderTarget())
}

// targetSidecarDir is the Target-keyed form: the sidecar tree lives under the target's
// workspace. This is the seam the host/preview targets reuse — the boot path reaches it
// through prismSidecarDir(e), which is just this over e.renderTarget().
func targetSidecarDir(t render.Target) string {
	return filepath.Join(t.Workspace, ".yolo", "prism")
}

// prismLastRenderPath is the last_render sidecar for one surface: the exact
// surface-codec bytes yolo wrote last boot (§5).
func prismLastRenderPath(e *Env, agent, name string) string {
	return filepath.Join(prismSidecarDir(e), agent+"-"+name+".last_render")
}

// prismProvenancePath is the provenance sidecar for one surface: per-key "which
// layer set this key" (Compose already computes it; this persists it). It is what
// makes config-overlay overrides legible
// ("key X: claude pack lost to house-rules overlay") and generalizes to the
// footprint's per-key record. Additive: a new file beside the surface, never a
// change to the surface bytes.
func prismProvenancePath(e *Env, agent, name string) string {
	return filepath.Join(prismSidecarDir(e), agent+"-"+name+".provenance")
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
	wsLua := filepath.Join(t.Workspace, "yolo-jail.config.lua")
	if data, err := os.ReadFile(wsLua); err == nil {
		b.Write(data)
		b.WriteByte('\n')
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
	return renderSurfaceStatefulSurface(e, surface, hostBytes, computed)
}

// renderSurfaceStatefulSurface is the surface-taking core of the stateful
// render, split out of renderSurfaceStateful so host_files can render a
// user-declared surface (Agent="user", Name=slug) that is NOT in the builtin
// manifest. The sidecar paths key on surface.Agent/surface.Name, so a user
// surface's sidecars are user-<slug>.{last_render,overlay.json} — collision-free
// with any builtin (no builtin agent is "user", and the slug is injective on the
// destination path).
func renderSurfaceStatefulSurface(e *Env, surface manifest.Surface, hostBytes []byte, computed map[string]any) (*agentcfg.StatefulOutput, error) {
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
	// Provenance sidecar: the per-key winning layer Compose already
	// computed. Additive — a new file, never a change to the surface bytes — so
	// it cannot regress the A12-fatal render. Best-effort: a provenance write
	// failure must not fail the boot (the surface itself is already written), so
	// it is logged, not returned. Empty provenance writes an empty file rather
	// than skipping, so a reader can tell "rendered, no keys" from "never rendered".
	if out.Result != nil {
		provText := strings.Join(out.Result.ProvenanceLines(), "\n")
		if provText != "" {
			provText += "\n"
		}
		if err := writeInPlaceString(prismProvenancePath(e, surface.Agent, surface.Name), provText); err != nil {
			e.warn("warning: could not write provenance sidecar for " +
				surface.Agent + "/" + surface.Name + ": " + err.Error())
		}
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
func renderSurfaceStatelessSurface(e *Env, surface manifest.Surface, hostBytes []byte, computed map[string]any) (*agentcfg.Result, error) {
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
func renderSurfaceRMWSurface(e *Env, surface manifest.Surface, computed map[string]any) error {
	surface = agentcfg.SubstituteWorkspace(surface, e.WorkspaceDir())

	path := expandHomePath(e, surface.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	obj := loadObject(path)

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
	return writeInPlaceString(path, dumpJSONIndent2(obj))
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
