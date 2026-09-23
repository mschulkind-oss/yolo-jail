package jailcontent

// lspplugin.go renders ONE yolo-authored Claude plugin whose `lspServers` comes from the user's own
// `lsp_servers` table — option D, ruled by OQ-LSP1 (docs/reference/mcp-configuration.md#oq-lsp1).
//
// Architecture and invariants: docs/reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin.
//
// # What it replaces
//
// Three hardcoded language→plugin pairs, duplicated in Go and in Lua, naming plugins from the
// `claude-plugins-official` marketplace: python→pyright-lsp, typescript→typescript-lsp, go→gopls-lsp.
// That was an arbitrary, five-language-short subset of a table yolo already owns — and since the
// `claude_plugins` hook was deleted, the derive still ENABLED those three while nothing installed
// them.
//
// # Why a plugin at all, and why this path
//
// MEASURED against Claude Code 2.1.278 (OQ-LSP3, docs/reference/mcp-configuration.md#oq-lsp3, 2026-09-22), statically from its bundle:
//
//   - Claude accepts LSP servers ONLY from a plugin. Nothing in settings.json takes an `lspServers`
//     table, so there is no non-plugin route (that was option E, refuted).
//   - A plugin does NOT need a marketplace. The plugin set is assembled from five independent arms,
//     and plugins are auto-loaded from `~/.claude/skills/*` under a `skills-dir` sentinel — which is
//     a tree yolo ALREADY stages, so delivery needs nothing new.
//   - Such a plugin is ENABLED BY DEFAULT: with no settings entry the value falls back to the
//     manifest's own default, and for that sentinel the settings test is opt-OUT. So yolo writes no
//     `enabledPlugins` id for it, which is why the derive's three toggles could go entirely.
//   - ONE plugin may declare MANY servers, keyed by server name. The only conflict rule is two
//     servers claiming one file extension, and that is a warning rather than a load failure.
//
// ⚠ The `.claude-plugin/` segment is MANDATORY on that path: the loader resolves
// `join(dir, ".claude-plugin", "plugin.json")` and passes no fallback candidates on that arm, so a
// manifest at the directory root is simply not found.

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LSPPluginDir is the plugin's directory name inside a skills destination.
//
// It is yolo's own, and `IsYoloPluginDir` already keeps the host-side adoption walk off any
// directory carrying the managed marker below — so this needs no entry in a pack's `reserved` list.
const LSPPluginDir = "yolo-lsp"

// lspServers is the table the caller injects, in declaration order so the rendered manifest is
// deterministic. Nil means "no LSP configured", which writes nothing at all.
var lspServers *jsonx.OrderedMap

// SetLSPServers hands over the user's `lsp_servers` table. Same injection shape as
// SetPackSkillTargets, and for the same reason: this package is called from the run pipeline and
// does not read config itself.
//
// ⚠ It is therefore PER-LAUNCH process-wide state, and the launch must scope it: auto-capture runs
// the same pipeline in-process, so a captured launch's table would otherwise leak into the parent's
// staging. run.packRecordScope snapshots it through LSPServers below, and
// TestEveryPerLaunchPackRecordIsScoped is the tripwire that caught this being missing.
func SetLSPServers(t *jsonx.OrderedMap) { lspServers = t }

// LSPServers returns the recorded table, for the launch scope's snapshot.
func LSPServers() *jsonx.OrderedMap { return lspServers }

// writeLSPPlugin renders the plugin into skillsDir, or removes a stale one when nothing is
// configured. Returns nil when there is nothing to do.
//
// # Why it is written LAST, after the pack skills
//
// Every other layer here is content a pack or the built-in suite supplies, and the precedence rule
// is that a later layer wins. This is not one of those layers — it is yolo's own generated output,
// and a pack shipping a directory of the same name must not be able to replace it with something
// Claude would load as an LSP declaration. Last is the only position that holds.
//
// # Why a stale one is REMOVED rather than left
//
// Dropping the last `lsp_servers` entry must stop the language servers reaching Claude. The staging
// dir is cleared per launch, so in the common path there is nothing to remove — this is the belt for
// a caller that stages without clearing.
func writeLSPPlugin(skillsDir string) error {
	dir := filepath.Join(skillsDir, LSPPluginDir)
	servers := renderLSPServers(lspServers)
	if len(servers) == 0 {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	manifest := map[string]any{
		"name":        LSPPluginDir,
		"description": "Language servers declared in this jail's lsp_servers config, rendered by yolo.",
		"lspServers":  servers,
		// The ownership marker IsYoloPluginDir reads. Without it the host-side adoption walk
		// cannot prove this directory is yolo's and would offer to migrate it into the user's
		// local pack — the same class of defect the sync-root fence exists for.
		"x-yolo-managed-by": yoloPluginManagedBy,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"),
		append(data, '\n'), 0o644)
}

// yoloPluginManagedBy mirrors hostskills' marker value. Duplicated rather than imported because
// jailcontent must not depend on the host-skills composer; a drift test pins the two together.
const yoloPluginManagedBy = "yolo-jail"

// renderLSPServers translates yolo's `lsp_servers` into the plugin's `lspServers` shape.
//
// The translation is one renamed key: yolo's `fileExtensions` (extension → language id) is the
// plugin's `extensionToLanguage`. `command` and `args` pass through unchanged, and `args` is omitted
// when empty rather than written as `[]`, so a hand-read manifest shows only what was declared.
//
// An entry with no `command` is SKIPPED, not rendered empty: a server yolo cannot spawn is not a
// server, and writing it would make Claude report a failure yolo could have declined to cause.
func renderLSPServers(table *jsonx.OrderedMap) map[string]any {
	if table == nil {
		return nil
	}
	out := map[string]any{}
	for _, name := range table.Keys() {
		raw, ok := table.Get(name)
		if !ok {
			continue
		}
		entry, ok := raw.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		cmd, _ := entry.Get("command")
		cmdStr, _ := cmd.(string)
		if cmdStr == "" {
			continue
		}
		server := map[string]any{"command": cmdStr}
		if args, ok := entry.Get("args"); ok {
			if list, ok := args.([]any); ok && len(list) > 0 {
				server["args"] = list
			}
		}
		if exts, ok := entry.Get("fileExtensions"); ok {
			if m, ok := exts.(*jsonx.OrderedMap); ok && len(m.Keys()) > 0 {
				conv := map[string]any{}
				for _, k := range m.Keys() {
					if v, ok := m.Get(k); ok {
						conv[k] = v
					}
				}
				server["extensionToLanguage"] = conv
			}
		}
		out[name] = server
	}
	return out
}
