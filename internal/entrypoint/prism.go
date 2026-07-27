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
)

// prismSidecarDir is the per-workspace directory holding the §5 capture-diff
// sidecars (last_render + overlay). It lives under the workspace's gitignored
// .yolo/ — the overlay is per-workspace scope (§4) and the agent never sees it.
func prismSidecarDir(e *Env) string {
	return filepath.Join(e.WorkspaceDir(), ".yolo", "prism")
}

// prismLastRenderPath is the last_render sidecar for one surface: the exact
// surface-codec bytes yolo wrote last boot (§5).
func prismLastRenderPath(e *Env, agent, name string) string {
	return filepath.Join(prismSidecarDir(e), agent+"-"+name+".last_render")
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
	var b strings.Builder
	userLua := filepath.Join(e.Home, ".config", "yolo-jail", "config.lua")
	if data, err := os.ReadFile(userLua); err == nil {
		b.Write(data)
		b.WriteByte('\n')
	}
	wsLua := filepath.Join(e.WorkspaceDir(), "yolo-jail.config.lua")
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
	if p == "~" {
		return e.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(e.Home, p[2:])
	}
	return p
}

// renderSurfaceRMWSurface is the surface-taking core of the RMW render, so a
// PACK-DECLARED surface reaches the identical mechanism a builtin does. See
// renderSurfaceRMW for what RMW is and why it exists.
//
// tables supplies the live data for a `reconcile` declaration; pass nil for a surface
// that has none (the static-key case RMW was originally built for).
func renderSurfaceRMWSurface(e *Env, surface manifest.Surface, tables map[string]map[string]any) error {
	surface = agentcfg.SubstituteWorkspace(surface, e.WorkspaceDir())

	path := expandHomePath(e, surface.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	obj := loadObject(path)

	// Reconciled dynamic tables FIRST, so a managed key nested under the same parent
	// still wins the floor.
	if err := reconcileRMWTables(e, surface, obj, tables); err != nil {
		return err
	}
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

// reconcileRMWTables applies each `reconcile` computed declaration to an RMW surface:
// drop the entries yolo asserted last time, add the ones it asserts now, leave every
// other entry alone.
//
// The SIDECAR is the whole mechanism. Without a record of what yolo put there, removing
// a dropped entry is indistinguishable from deleting one the agent added itself — so a
// reconcile with a missing sidecar removes NOTHING and only adds. That is the safe
// direction: a stale entry is a wrong config the user can see and fix, while deleting an
// agent's own MCP server is data loss they cannot.
func reconcileRMWTables(e *Env, surface manifest.Surface, obj *jsonx.OrderedMap, tables map[string]map[string]any) error {
	for _, c := range surface.Computed {
		if !c.Reconcile {
			continue
		}
		if c.To == "" {
			return fmt.Errorf("surface %s/%s: reconcile needs a \"to\"", surface.Agent, surface.Name)
		}
		table := tables[c.From]
		if c.Project != nil {
			projected, err := c.Project.Apply(table)
			if err != nil {
				return fmt.Errorf("surface %s/%s: %w", surface.Agent, surface.Name, err)
			}
			table = projected
		}
		dest := setDefaultMap(obj, c.To)
		sidecar := rmwManagedPath(e, surface, c.To)
		if err := os.MkdirAll(filepath.Dir(sidecar), 0o755); err != nil {
			return err
		}
		for _, name := range loadManagedSet(sidecar) {
			dest.Delete(name)
		}
		names := make([]string, 0, len(table))
		for name := range table {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// A plain map[string]any goes in as-is: the jsonx encoder handles it and
			// sorts its keys, so the write is deterministic without a lift back into
			// an OrderedMap.
			dest.Set(name, table[name])
		}
		if err := writeInPlaceString(sidecar, managedSidecar(names)); err != nil {
			return err
		}
	}
	return nil
}

// rmwManagedPath is the sidecar recording which entries yolo asserted into one reconciled
// table.
//
// It lives in the prism sidecar dir (<workspace>/.yolo/prism/) — NOT beside the surface
// file, which was the first attempt and which a real jail rejected immediately: the jail
// home is mounted :ro except for the dirs a pack declares writable, so a surface at the
// home ROOT (~/.claude.json) has no writable directory beside it at all. The failure was
// `read-only file system`, and A12 correctly halted the boot rather than shipping a jail
// with a silently unrecorded managed set.
//
// The `.managed.json` suffix keeps it out of `yolo config reset`'s way, which removes
// exactly `<agent>-<name>.overlay.json` and `.last_render`. That separation is deliberate
// rather than incidental: those two are the CAPTURE artifacts of a composed surface and
// resetting them restores the pure render, while this is bookkeeping for a file yolo does
// not own. Deleting it would make yolo forget what it added and ORPHAN every entry it had
// put there — a leak, not a reset.
func rmwManagedPath(e *Env, surface manifest.Surface, key string) string {
	return filepath.Join(prismSidecarDir(e),
		surface.Agent+"-"+surface.Name+"-"+key+".managed.json")
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
