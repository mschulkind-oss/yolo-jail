package entrypoint

// prism_opencode.go is the opencode config.json port onto the agentcfg
// composition engine — the third computed-layer surface after the gemini
// reference (see prism.go ConfigureGeminiPrism). It lives in its own file
// (rather than in prism.go) to keep the per-agent ports merge-conflict-free.

import (
	"os"
	"path/filepath"
)

// ConfigureOpencodePrism is the prism-backed replacement for ConfigureOpencode.
// It mirrors the gemini reference port (ConfigureGeminiPrism): opencode.json
// carries a DYNAMIC mcp table (each shared MCP server translated into
// opencode's native {type:"local", command:[cmd, ...args], enabled:true,
// environment:{...}} schema), not just static managed keys, so it uses the
// COMPUTED-layer seam.
//
//  1. the static defaults + managed keys come from the manifest surface
//     (opencodeConfig): $schema="https://opencode.ai/config.json" as a Default
//     (a user value silently wins) and permission="allow" as Managed (the YOLO
//     posture — opencode never re-prompts), exactly as for the other agents;
//  2. the mcp table — the live shared MCP servers translated to opencode's
//     native schema (buildOpencodeMCPServers, shared with the bespoke path) —
//     is handed to the engine as the COMPUTED layer. It merges above the
//     captured overlay and below transform+managed, so yolo's freshly
//     regenerated servers win over a stale in-jail edit to the same server,
//     while a server the USER adds (never in last_render) is captured into the
//     overlay and survives.
//
// The bespoke path's yolo-managed-mcp-servers.json sidecar is UNNECESSARY here:
// the §5 last_render sidecar is itself the "what yolo owned last boot" anchor,
// so a yolo server dropped from config between boots never resurrects (it
// always matched last_render → never captured into the overlay → simply absent
// from the computed layer this boot). The obsolete sidecar is deleted on the
// first migration (§4.7 orphan cleanup), mirroring gemini's managed-MCP sidecar
// and pi's snapshot deletion.
//
// DELETE-WHEN-EMPTY: when no MCP servers are configured, the computed layer
// OMITS the "mcp" key entirely (rather than supplying an empty object). opencode
// has NO host mount (yolo owns the file, like gemini/copilot), so there is no
// host layer that could carry a stale mcp block — an omitted computed key stays
// absent from the render, matching the bespoke path's "delete the mcp key when
// the reconciled table is empty".
func ConfigureOpencodePrism(e *Env) error {
	if err := os.MkdirAll(e.OpencodeDir(), 0o755); err != nil {
		return err
	}

	// Computed layer: the translated mcp table, deep-converted from jsonx to the
	// engine's plain value model. Omit the key entirely when empty so the render
	// matches the bespoke delete-when-empty (no host layer to carry a stale block).
	computed := map[string]any{}
	if table := buildOpencodeMCPServers(e); table.Len() > 0 {
		computed["mcp"] = prismMap(table)
	}

	// opencode has NO host mount (yolo owns the file), so hostBytes is nil.
	out, err := renderSurfaceStateful(e, "opencode", "config", nil, computed)
	if err != nil {
		return err
	}

	// §4.7: the bespoke managed-MCP sidecar is dead under the prism (last_render
	// is the anchor now). Delete it once, on the migration boot.
	if out.FirstMigration {
		_ = os.Remove(filepath.Join(e.OpencodeDir(), "yolo-managed-mcp-servers.json"))
	}
	return nil
}
