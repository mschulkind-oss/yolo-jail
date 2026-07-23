package entrypoint

// prism_mise.go is the prism-backed replacement for GenerateMiseConfig — the
// first NON-agent-config surface ported onto the composition engine
// (docs/design/config-migration-to-prism.md §4.1, a HIGH stale-risk surface).
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
	"os"
	"regexp"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
)

// ConfigureMisePrism renders ~/.config/mise/config.toml through the composition
// engine and performs the two bespoke side effects the prism does NOT own:
//
//  1. COMPUTED layer — the dynamic [tools] table. yolo owns no default runtime
//     (miseBaseTools is empty; every default is baked), so the only yolo-owned
//     content is the YOLO_MISE_TOOLS injected pins. They ride the computed layer
//     (above the captured overlay, below transform+managed), so an injected pin
//     wins over a stale in-jail `mise use -g` while a user-added global tool is
//     captured into the overlay and survives. Versions are forced to strings
//     (mise versions are strings; a JSON number injected as {"node": 20} would
//     otherwise reach the TOML codec as an int and change the value's shape).
//
//  2. WORKSPACE retire surgery (bespoke) — retired agent tokens
//     (agents.AllMiseRetire) are stripped from /workspace/mise.toml in place.
//     This is a WORKSPACE-file mutation, which the prism must never own
//     (migration doc §5.3: yolo owns only user-scope config, never /workspace).
//     The `mise uninstall` subprocess side effect stays in boot.go
//     (miseUninstallRetired), exactly as it did for the bespoke path.
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
	//     staterender.go decodeObject / TestComposeStatefulEmptyLastRenderReseeds),
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

	if _, err := renderSurfaceStateful(e, "mise", "config", nil, computed); err != nil {
		return err
	}

	// Bespoke workspace-file side effect: strip retired tokens from the workspace
	// mise.toml (never owned by the prism).
	return retireWorkspaceMiseTools(e)
}

// retireWorkspaceMiseTools removes any agents.AllMiseRetire token line from the
// workspace mise.toml in place. A missing/unreadable file is a no-op. This is
// the workspace-scope half of the old GenerateMiseConfig, kept bespoke because
// the prism never mutates /workspace files (migration doc §5.3).
func retireWorkspaceMiseTools(e *Env) error {
	wsMise := workspaceMisePath
	raw, err := os.ReadFile(wsMise)
	if err != nil {
		return nil // absent/unreadable workspace mise.toml => nothing to retire
	}
	content := string(raw)
	changed := false
	for _, tool := range agents.AllMiseRetire {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(tool) + `\s*=\s*"[^"]*"\n?`)
		newWs := pattern.ReplaceAllString(content, "")
		if newWs != content {
			content = newWs
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeInPlaceString(wsMise, content)
}

// workspaceMisePath is the workspace mise.toml consulted for the retire surgery.
// A package var so tests can point it at a fixture; production is the live bind
// mount at /workspace/mise.toml.
var workspaceMisePath = "/workspace/mise.toml"
