package entrypoint

import (
	"os"
	"path/filepath"
)

// ConfigureClaudePrism is the prism-backed replacement for the settings.json
// portion of ConfigureClaude — the SUBTLEST surface port (the widest surface,
// with both static managed keys and per-boot dynamic content). It renders ONLY
// ~/.claude/settings.json through the composition engine; everything else claude
// needs stays bespoke RUNTIME STATE, shared with ConfigureClaude via extracted
// helpers so the two paths cannot drift:
//
//   - ~/.claude.json (writeClaudeJSON): the user-scoped MCP + workspace-project
//     file. It is claude's runtime-state config and the AGENTS.md invariant is
//     that it must NEVER be wiped, so it is NOT a prism surface — it is rebuilt
//     bespoke every boot (mcpServers reconciled against the managed sidecar,
//     workspace project force-trusted).
//   - credentials symlink, host-file staging, per-jail history isolation
//     (configureClaudeSideEffects): filesystem side effects, not surface
//     content — untouched by the prism.
//
// What the PRISM owns here is settings.json, composed as
// defaults<host<overlay<computed<transform<managed:
//
//   - The STATIC managed block (permissions {allow:[],deny:[],
//     defaultMode:acceptEdits,additionalDirectories:["/"]},
//     skipDangerousModePermissionPrompt:true, preferences.autoUpdaterStatus:
//     "disabled") lives in the claudeSettings manifest (agentcfg/builtin.go).
//
//   - The HOST layer replaces the bespoke three-way merge: host settings.json is
//     read from the :ro mount (/ctx/host-claude/settings.json — the yolo-declared
//     claude host file, agents.AgentSpec.HostFiles) and composed in, while the §5
//     last_render+overlay sidecars carry in-jail edits forward. The obsolete
//     yolo-host-synced-settings.json snapshot is therefore dead and deleted once,
//     on the first-migration boot (§4.7).
//
//   - The COMPUTED layer supplies the three DYNAMIC concerns the manifest
//     deliberately omits (its FIDELITY GAPS #2 and #3), each an RFC-7386 map that
//     folds above the captured overlay and below transform+managed:
//
//     1. mcpServers tombstone. The bespoke path does an unconditional
//     settings.Delete("mcpServers") — mcpServers belongs in .claude.json, never
//     settings.json. computed["mcpServers"]=nil deletes a host-provided block
//     from the render AND is omitted from the output when absent (the engine's
//     null-tombstone provenance handles both).
//     2. enabledPlugins. For each {lsp,plugin} in claudeLSPPluginOrder: the
//     plugin key is true when that LSP is configured, else a nil tombstone
//     (matching the bespoke Set/Delete).
//     3. env.ENABLE_LSP_TOOL. "1" iff any LSP server is configured, else a nil
//     tombstone.
//
// BYTE-SHAPE GAP (documented, harmless): the bespoke path PRUNES an emptied env
// block entirely, whereas a computed tombstone on the sole env key leaves an
// empty env:{} object in the render when there are no user env keys (the engine
// materializes the env object before deleting ENABLE_LSP_TOOL inside it, per the
// RFC-7386 object-patch rule). The same is true of enabledPlugins:{} when no LSP
// is configured. claude reads an empty object identically to an absent one, so
// this is a formatting-only divergence, not a semantic one; we do NOT hack the
// engine to special-case it.
func ConfigureClaudePrism(e *Env) error {
	if err := os.MkdirAll(e.ClaudeDir(), 0o755); err != nil {
		return err
	}

	// .claude.json is rebuilt bespoke from the reconciled MCP table (below).
	configured := e.LoadMCPServers()

	// Runtime-state side effects (credentials/host-files/history) — same order
	// and behavior as the bespoke path.
	if err := configureClaudeSideEffects(e); err != nil {
		return err
	}

	// Resolve the host source — the prism host layer replacing the bespoke
	// three-way merge. settings.json is the yolo-declared claude host file
	// (agents.AgentSpec.HostFiles), so the CLI binds it at /ctx/host-claude/ (==
	// hostClaudeDir) whenever it exists on the host. Read fail-open: a missing
	// mount (host file absent, or macos-user with no /ctx) yields nil and the
	// render falls back to defaults<overlay<computed<managed.
	hostBytes, _ := os.ReadFile(filepath.Join(hostClaudeDir, "settings.json"))

	// Build the computed layer: the three dynamic concerns the manifest omits.
	lspServers := LoadLSPServers(e)

	enabledPlugins := make(map[string]any, len(claudeLSPPluginOrder))
	for _, pm := range claudeLSPPluginOrder {
		if _, ok := lspServers.Get(pm.lsp); ok {
			enabledPlugins[pm.plugin] = true
		} else {
			enabledPlugins[pm.plugin] = nil // tombstone (matches bespoke Delete)
		}
	}

	var envBlock map[string]any
	if lspServers.Len() > 0 {
		envBlock = map[string]any{"ENABLE_LSP_TOOL": "1"}
	} else {
		envBlock = map[string]any{"ENABLE_LSP_TOOL": nil} // tombstone
	}

	computed := map[string]any{
		"mcpServers":     nil, // strip a host block; mcpServers lives in .claude.json
		"enabledPlugins": enabledPlugins,
		"env":            envBlock,
	}

	out, err := renderSurfaceStateful(e, "claude", "settings", hostBytes, computed)
	if err != nil {
		return err
	}

	// §4.7: the three-way-merge snapshot is dead under the prism. Delete it once,
	// on the migration boot, so a stale file never confuses a future reader
	// (mirrors ConfigurePiPrism).
	if out.FirstMigration {
		_ = os.Remove(e.ClaudeHostSettingsSnapshotPath())
	}

	// ~/.claude.json + managed sidecar stay bespoke runtime state.
	return writeClaudeJSON(e, configured)
}
