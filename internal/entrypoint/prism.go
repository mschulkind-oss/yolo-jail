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
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// prismEnabledFor reports whether boot should render agent's surfaces through
// the prism engine (Configure*Prism) instead of the bespoke Configure* writer.
// It reads YOLO_PRISM_SURFACES — the surface-by-surface cutover control:
//
//   - ""            => bespoke for everything (the safe default; nothing changes)
//   - "all"         => prism for every agent that has a Configure*Prism port
//   - "pi,claude"   => prism for those agents' ported surfaces
//   - "pi/settings" => prism for that one surface (the agent matches on the
//     leading "agent" segment; per-surface granularity is honored by the
//     Configure*Prism dispatch, which only ports specific surfaces anyway)
//
// Entries are comma-separated, surrounding whitespace trimmed, empty entries
// (e.g. a trailing comma) ignored. This lets a surface be flipped on and
// parity-verified in a nested jail before its bespoke writer is deleted in
// Phase C — and flipped back instantly if the render diverges.
func prismEnabledFor(e *Env, agent string) bool {
	raw := strings.TrimSpace(e.Getenv("YOLO_PRISM_SURFACES"))
	if raw == "" {
		return false
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "all" {
			return true
		}
		// Match on the agent segment ("agent" or "agent/name").
		if a, _, found := strings.Cut(entry, "/"); found {
			if a == agent {
				return true
			}
		} else if entry == agent {
			return true
		}
	}
	return false
}

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

// renderSurfaceStateful runs the §5/§3.2 stateful render for one builtin surface
// and persists the three artifacts (surface file, last_render, overlay). It
// resolves the host source via hostBytes (caller supplies, since the mount and
// allow-list are surface-specific), reads the two sidecars, composes, and writes
// everything back. It returns the StatefulOutput so the caller can act on
// FirstMigration (e.g. the §4.7 orphan cleanup).
//
// computed is yolo's per-boot DYNAMIC layer (§4 computed slot) — the reconciled
// MCP-server table and any LSP-derived toggles — already deep-converted to the
// engine's plain value model via prismMap. It merges ABOVE the captured overlay
// and BELOW the transform + managed, so yolo's freshly regenerated data wins
// over a stale in-jail edit (regenerate-don't-reconcile) yet a managed key still
// wins the floor. Pass nil for a static-only surface (copilot/agy/pi settings).
//
// A recoverable on-disk condition never aborts boot (ComposeStateful self-heals
// corrupt/absent sidecars); only a genuine error (unknown codec, Lua failure)
// propagates, and boot's genStep downgrades even that to a warning.
func renderSurfaceStateful(e *Env, agent, name string, hostBytes []byte, computed map[string]any) (*agentcfg.StatefulOutput, error) {
	surface, ok := agentcfg.BuiltinManifest().Lookup(agent, name)
	if !ok {
		return nil, &missingSurfaceError{agent: agent, name: name}
	}

	surfacePath := expandHomePath(e, surface.Path)
	current, _ := os.ReadFile(surfacePath) // absent => nil, treated as no current file

	lastRenderPath := prismLastRenderPath(e, agent, name)
	lastRenderBytes, lastErr := os.ReadFile(lastRenderPath)
	overlayJSON, _ := os.ReadFile(prismOverlayPath(e, agent, name))

	script := loadPrismTransformScript(e)
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

	// Persist the render to the jail surface path (codecs emit no trailing
	// newline; append one so the file is a well-formed text file, matching the
	// bespoke writers' dumpJSONIndent2 "+ \n").
	if err := os.MkdirAll(filepath.Dir(surfacePath), 0o755); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(surfacePath, string(out.Result.Encoded)+"\n"); err != nil {
		return nil, err
	}

	// Persist the two sidecars (last_render matches the surface bytes exactly, so
	// the next boot's mergeDiff has a truthful baseline).
	if err := os.MkdirAll(prismSidecarDir(e), 0o755); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(lastRenderPath, string(out.LastRenderBytes)+"\n"); err != nil {
		return nil, err
	}
	if err := writeInPlaceString(prismOverlayPath(e, agent, name), string(out.OverlayJSON)+"\n"); err != nil {
		return nil, err
	}
	return out, nil
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

// ConfigurePiPrism is the prism-backed replacement for ConfigurePi (§4.3, the
// proof-of-concept surface). It:
//
//  1. stages the non-settings host_pi_files tree exactly as before (models.json
//     et al. still land where pi reads them — the prism owns settings.json, not
//     its sibling files);
//  2. renders ~/.pi/agent/settings.json through the engine with §5 overlay
//     capture and the §3.2 first-migration bootstrap;
//  3. on the first migration only, deletes the obsolete
//     yolo-host-synced-settings.json snapshot (§4.7 orphan cleanup).
//
// The host source is /ctx/host-pi/settings.json, read only when settings.json is
// declared in YOLO_HOST_PI_FILES (fail-closed staging, §4) — an undeclared host
// file is never read, so the render falls back to defaults<managed.
func ConfigurePiPrism(e *Env) error {
	if err := os.MkdirAll(e.PiDir(), 0o755); err != nil {
		return err
	}
	// Sibling files still stage the old way (the prism owns only settings.json).
	if err := e.syncHostPiFiles(); err != nil {
		return err
	}

	// Resolve the host source, gated by the host_pi_files allow-list.
	var hostBytes []byte
	if contains(e.hostPiFiles(), "settings.json") {
		hostBytes, _ = os.ReadFile(filepath.Join(hostPiDir, "settings.json"))
	}

	out, err := renderSurfaceStateful(e, "pi", "settings", hostBytes, nil)
	if err != nil {
		return err
	}

	// §4.7: the three-way-merge snapshot is dead under the prism. Delete it once,
	// on the migration boot, so a stale file never confuses a future reader.
	if out.FirstMigration {
		_ = os.Remove(e.PiHostSettingsSnapshotPath())
	}
	return nil
}

// ConfigureCopilotPrism is the prism-backed replacement for ConfigureCopilot
// (§4.6, the zero-stale surface — the cleanest first non-agent-config port). It:
//
//  1. renders ~/.copilot/config.json through the engine with §5 overlay capture
//     and the §3.2 first-migration bootstrap. Copilot has NO host mount — the
//     file is purely yolo-owned — so hostBytes is nil and the render is
//     defaults<overlay<managed (the sole default being {"yolo": true});
//  2. writes the dynamic mcp-config.json / lsp-config.json siblings exactly as
//     the bespoke path does (they are pure overwrites regenerated from live
//     config every boot — the prism owns only the static config.json).
//
// There is no orphan-file cleanup here: copilot never had a snapshot sidecar
// (nothing to migrate away from), which is precisely why it is the zero-stale
// first porting target.
func ConfigureCopilotPrism(e *Env) error {
	if err := os.MkdirAll(e.CopilotDir(), 0o755); err != nil {
		return err
	}
	// config.json: no host source (yolo owns it outright), no computed layer.
	if _, err := renderSurfaceStateful(e, "copilot", "config", nil, nil); err != nil {
		return err
	}
	// Dynamic siblings stay bespoke, shared with ConfigureCopilot.
	return writeCopilotDynamicConfigs(e, e.CopilotDir())
}

// ConfigureGeminiPrism is the prism-backed replacement for ConfigureGemini —
// the REFERENCE PORT for the computed layer (§4). Gemini's settings.json is the
// first ported surface that carries a DYNAMIC mcpServers table, not just static
// managed keys, so it is where the computed-layer seam is proven:
//
//  1. the static security defaults + general force-offs come from the manifest
//     surface (geminiSettings), exactly as for pi/copilot;
//  2. the mcpServers table — live shared MCP servers plus LSP-as-MCP wrappers
//     (buildGeminiMCPServers, shared with the bespoke path) — is handed to the
//     engine as the COMPUTED layer. It merges above the captured overlay and
//     below transform+managed, so yolo's freshly regenerated servers win over a
//     stale in-jail edit to the same server, while a server the USER adds (never
//     in last_render) is captured into the overlay and survives.
//
// The bespoke path's yolo-managed-mcp-servers.json sidecar is UNNECESSARY here:
// the §5 last_render sidecar is itself the "what yolo owned last boot" anchor,
// so a yolo server dropped from config between boots never resurrects (it always
// matched last_render → never captured into the overlay → simply absent from the
// computed layer this boot). The obsolete sidecar is deleted on the first
// migration (§4.7 orphan cleanup), mirroring pi's snapshot deletion.
//
// Gemini has NO host mount (its settings.json is not in any host_*_files
// allow-list — yolo owns the MCP/security posture), so hostBytes is nil.
func ConfigureGeminiPrism(e *Env) error {
	if err := os.MkdirAll(e.GeminiDir(), 0o755); err != nil {
		return err
	}

	// Computed layer: the full yolo-owned mcpServers table, deep-converted from
	// jsonx to the engine's plain value model.
	computed := map[string]any{
		"mcpServers": prismMap(buildGeminiMCPServers(e)),
	}

	out, err := renderSurfaceStateful(e, "gemini", "settings", nil, computed)
	if err != nil {
		return err
	}

	// §4.7: the bespoke managed-MCP sidecar is dead under the prism (last_render
	// is the anchor now). Delete it once, on the migration boot.
	if out.FirstMigration {
		_ = os.Remove(e.GeminiManagedMCPPath())
	}
	return nil
}

// ConfigureAgyPrism configures the Google Antigravity CLI (agy). AGY is a
// brand-new agent with zero legacy bespoke state, so — unlike the migrating
// agents that sit behind the YOLO_PRISM_SURFACES gate while their bespoke
// writers are retired — it is born DIRECTLY on the prism: there is no bespoke
// ConfigureAgy and no gate. boot.go calls this unconditionally. It:
//
//  1. renders ~/.gemini/antigravity-cli/settings.json through the engine with §5
//     overlay capture and the §3.2 first-migration bootstrap. agy has NO host
//     mount (yolo owns the file, like copilot's config.json — §4.6), so
//     hostBytes is nil and the render is defaults<overlay<managed; the sole
//     managed key permissionMode="allow" is the YOLO posture (agy never
//     re-prompts — the container is the sandbox), so a user edit reverts;
//  2. writes the dynamic mcp_config.json sibling from live MCP config — a pure
//     per-boot overwrite (no in-jail edits preserved), exactly like copilot's
//     mcp-config.json. The prism owns only the static settings.json.
//
// There is no orphan-file cleanup: agy never had a bespoke snapshot sidecar
// (nothing to migrate away from) — the same zero-stale property that made
// copilot the first non-agent-config port.
func ConfigureAgyPrism(e *Env) error {
	if err := os.MkdirAll(e.AgyDir(), 0o755); err != nil {
		return err
	}
	// settings.json: no host source (yolo owns it outright), no computed layer.
	if _, err := renderSurfaceStateful(e, "agy", "settings", nil, nil); err != nil {
		return err
	}
	// Dynamic mcp_config.json sibling: a pure overwrite regenerated from live MCP
	// config every boot (no in-jail edits preserved), mirroring copilot's
	// mcp-config.json.
	mcpConfig := jsonx.NewOrderedMap()
	mcpConfig.Set("mcpServers", e.LoadMCPServers())
	return writeInPlaceString(filepath.Join(e.AgyDir(), "mcp_config.json"), dumpJSONIndent2(mcpConfig))
}
