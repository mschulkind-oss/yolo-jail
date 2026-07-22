package entrypoint

// prism_codex.go is the prism-backed replacement for ConfigureCodex. It lives in
// its own file (not prism.go) so the codex port never touches the shared prism
// wiring — merge-conflict hygiene while the surface-by-surface cutover proceeds.

import (
	"os"
	"path/filepath"
)

// ConfigureCodexPrism is the prism-backed replacement for ConfigureCodex — the
// TOML-codec analogue of the gemini reference port (§4). Codex's config.toml is
// the ONE non-JSON surface, so it also exercises the toml codec
// (internal/agentcfg/codec/toml.go) end to end. Like gemini it carries a DYNAMIC
// MCP table (mcp_servers), not just static managed keys:
//
//  1. the static force-managed scalars — approval_policy="never" and
//     sandbox_mode="danger-full-access" — come from the manifest surface
//     (codexConfig), exactly as for gemini's security/general keys;
//  2. the mcp_servers table — live shared MCP servers translated into codex's
//     TOML table shape (buildCodexMCPServers, shared with the bespoke path) — is
//     handed to the engine as the COMPUTED layer. It merges above the captured
//     overlay and below transform+managed, so yolo's freshly regenerated servers
//     win over a stale in-jail edit to the same server, while a server the USER
//     adds (never in last_render) is captured into the overlay and survives.
//
// DELETE-WHEN-EMPTY: the bespoke path deletes the whole mcp_servers key when no
// servers are configured. Codex has NO host layer (yolo owns config.toml's
// posture), so simply OMITTING the mcp_servers key from the computed layer keeps
// it absent — there is nothing below the computed slot to reintroduce it. Hence
// an empty table => computed without the key, matching the bespoke delete.
//
// The bespoke path's yolo-managed-mcp-servers.json sidecar is UNNECESSARY here:
// the §5 last_render sidecar is itself the "what yolo owned last boot" anchor, so
// a yolo server dropped from config between boots never resurrects (it always
// matched last_render → never captured into the overlay → simply absent from the
// computed layer this boot). The obsolete sidecar is deleted on the first
// migration (§4.7 orphan cleanup), mirroring gemini's/pi's deletion.
//
// CODEC-EXTENSION BYTE-SHAPE GAP (documented, harmless — see codexConfig's doc,
// FIDELITY GAP #2): the agentcfg toml codec has no inline-table output, so a
// per-server env sub-map renders as a [mcp_servers.<name>.env] SUB-TABLE header
// rather than the bespoke dumpCodexTOML's inline env = { A = "1" }. Both decode
// to the same value and codex reads either, so this is a byte-shape (not
// semantic) gap. Do NOT compare the rendered bytes to the bespoke output; decode
// the TOML and compare values instead.
//
// Codex has NO host mount (its config.toml is not in any host_*_files
// allow-list — yolo owns the posture), so hostBytes is nil.
func ConfigureCodexPrism(e *Env) error {
	if err := os.MkdirAll(e.CodexDir(), 0o755); err != nil {
		return err
	}

	// Computed layer: the yolo-owned mcp_servers table, deep-converted from jsonx
	// to the engine's plain value model. Omit the key entirely when empty so the
	// render drops mcp_servers (matching the bespoke delete-when-empty); codex has
	// no host layer, so an omitted key stays absent.
	computed := map[string]any{}
	if table := buildCodexMCPServers(e); table.Len() > 0 {
		computed["mcp_servers"] = prismMap(table)
	}

	out, err := renderSurfaceStateful(e, "codex", "config", nil, computed)
	if err != nil {
		return err
	}

	// §4.7: the bespoke managed-MCP sidecar is dead under the prism (last_render
	// is the anchor now). Delete it once, on the migration boot.
	if out.FirstMigration {
		_ = os.Remove(filepath.Join(e.CodexDir(), "yolo-managed-mcp-servers.json"))
	}
	return nil
}
