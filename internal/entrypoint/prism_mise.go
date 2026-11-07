package entrypoint

// prism_mise.go is the prism-backed replacement for GenerateMiseConfig — the
// first NON-agent-config surface ported onto the composition engine
// (docs/reference/config-migration-to-prism.md §4.1, a HIGH stale-risk surface).
//
// Why the port kills the stale-runtime bug for free. The bespoke
// GenerateMiseConfig was an in-place editor: it added and updated `[tools]`
// lines but never removed a departed default, so an older yolo's `node = "22"`
// / `python = "3.13"` / `go = "latest"` lines persisted in the persistent jail
// home forever and shadowed the baked /bin/<tool> — the exact
// LD_LIBRARY_PATH / MCP-wrapper whack-a-mole (mise-node-dynamic-linking.md). It
// carried a special-case §4.1 pre-render scrub to strip those lines.
//
// Under the prism that scrub is UNNECESSARY (§4.1 final ¶). On the first prism
// boot for this surface ComposeStateful (staterender.go §3.2) seeds from a
// fresh render with an EMPTY overlay and DISCARDS the on-disk file — so the
// stale lines, present in no layer, simply do not render. Steady-state capture
// then begins from that truthful baseline, and an intentional pin re-lands via
// the computed layer below. There is nothing to scrub.
//
// Accepted one-time cost (§3.2): on the single migration boot, a hand-added
// GLOBAL tool (`mise use -g <tool>`, captured in ~/.config/mise/config.toml but
// in NO yolo layer) is dropped, because a pre-migration edit is indistinguishable
// from stale generator output with no last_render to diff against. From the next
// boot it would be re-captured into the overlay and preserved. Workspace pins
// (/workspace/mise.toml) and YOLO_MISE_TOOLS pins are unaffected — they are live
// layers, re-applied every boot.

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"os"
	"path/filepath"
	"regexp"
)

// ConfigureMisePrism renders ~/.config/mise/config.toml through the composition
// engine and performs the two bespoke side effects the prism does NOT own:
//
//  1. COMPUTED layer — the dynamic [tools] table. yolo owns no default runtime
//     (miseBaseTools is empty; every default is baked), so the only yolo-owned
//     content is the YOLO_MISE_TOOLS injected pins. They ride the computed layer
//     (above the captured overlay, below managed), so an injected pin
//     wins over a stale in-jail `mise use -g` while a user-added global tool is
//     captured into the overlay and survives. Versions are forced to strings
//     (mise versions are strings; a JSON number injected as {"node": 20} would
//     otherwise reach the TOML codec as an int and change the value's shape).
//
//  2. WORKSPACE retired-tool notice — a retired agent token
//     (packload.EmbeddedRetireMiseTools()) in <workspace>/mise.toml is NAMED, never stripped:
//     the workspace is the user's project, and yolo does not edit it as a side effect of a
//     launch (the owner, 2026-11-07: "it's VERY surprising when your own settings get changed
//     for you"). The `mise uninstall` subprocess side effect stays in boot.go
//     (miseUninstallRetired).
func ConfigureMisePrism(e *Env) error {
	// Computed [tools] table: injected pins only (no baked defaults), versions
	// coerced to strings for the TOML codec.
	injected := loadInjectedTools(e)
	tools := map[string]any{}
	for _, tool := range injected.Keys() {
		v, _ := injected.Get(tool)
		tools[tool] = miseValueString(v)
	}

	// ALWAYS emit the [tools] table, even when empty. Two reasons:
	//
	//  1. A mise config with no yolo-owned tools would otherwise render to an
	//     EMPTY document. The stateful engine treats an empty-decoding
	//     last_render sidecar as untrusted (a corruption-recovery guard —
	//     staterender.go decodeKind / TestComposeStatefulEmptyLastRenderReseeds),
	//     so it would re-seed as a first migration EVERY boot and never capture
	//     in-jail edits. A user's hand-added global tool (`mise use -g <t>`) would
	//     be wiped on every boot, not just the one migration boot. Emitting a
	//     (possibly empty) [tools] table keeps last_render non-empty and trusted,
	//     so steady-state capture works. This also matches the historical bespoke
	//     output, which always wrote a `[tools]` header.
	//  2. An empty `tools: {}` computed layer is an RFC-7386 empty object patch:
	//     it merges OVER the captured overlay without deleting the user's tools
	//     (engine.go mergeValue), while an injected pin still wins its own key.
	computed := map[string]any{"tools": tools}

	// [tools] is REGENERATED IN FULL (CO13): every entry comes from YOLO_MISE_TOOLS, so on a
	// first migration a runtime under it that this boot did not inject is an older yolo's
	// output (the §4.1 stale-runtime class) rather than the user's. This is the Go spelling
	// of a pack derive's ctx.in_full — mise/config is core's own surface and has no derive.
	// An EMPTY [tools] still claims nothing (agentcfg.dropComputedTables), so a hand-added
	// `mise use -g` tool on a jail with no pin is adopted.
	if _, err := renderSurfaceStateful(e, "mise", "config", nil, computed, []string{"tools"}); err != nil {
		return err
	}

	noteRetiredWorkspaceMiseTools(e)
	return nil
}

// noteRetiredWorkspaceMiseTools warns, once per boot, for each packload.EmbeddedRetireMiseTools()
// token line in the workspace mise.toml, naming the line to delete. It reads the file and never
// writes it: the workspace is the user's. A missing or unreadable file says nothing.
func noteRetiredWorkspaceMiseTools(e *Env) {
	raw, err := os.ReadFile(filepath.Join(e.WorkspaceDir(), "mise.toml"))
	if err != nil {
		return
	}
	for _, tool := range packload.EmbeddedRetireMiseTools() {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(tool) + `\s*=\s*"[^"]*"`)
		if line := pattern.FindString(string(raw)); line != "" {
			e.warnOnce(fmt.Sprintf("The workspace mise.toml pins %s, which yolo no longer installs through mise (its pack does). "+
				"yolo left the file as it is; delete the line `%s` from mise.toml so mise stops installing it.", tool, line))
		}
	}
}
