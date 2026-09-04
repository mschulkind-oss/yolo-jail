package packload

// plugins.go is the packload side of a pack that WRAPS an existing agent plugin: finding
// the plugin trees in the pack's skills sources, and reporting what they run.
//
// IT WAS AN ORIGIN GATE until OQ-TP9 (docs/design/trust-paths.md, 2026-09-04). A plugin
// manifest can declare `hooks`/`mcpServers`/`lspServers` — PROCESSES THAT RUN on the user's
// behalf — and a FETCHED pack's were stripped, with a printed refusal, until the user
// approved them at `yolo pack install`. The ruling deleted that: the person who put a git
// URL in `packs` in their own user config already granted strictly more than the prompt
// withheld, and `npm install -g` from the same tree ran `postinstall` ungated anyway
// (pack-execution-trust.md §2).
//
// What remains here is DISCLOSURE, which is why the split is still packload's: pluginpack
// knows what a plugin declares, packload knows which pack carries it, and FootprintOf turns
// that into a ⚠ RUNS CODE line a reader sees before selecting the pack.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// Plugins returns the plugin trees this pack wraps, recognized but NOT gated. Callers that
// only describe the pack (`pack footprint`, the install prompt) want the full picture,
// including a claim that would be refused — the point of showing a footprint is to see what a
// pack WANTS before trusting it.
//
// Scanned in the pack's resolved SKILLS SOURCE dirs (each `skills` contribution's `from`,
// else the conventional one), not a hardcoded skills/. A wrapped plugin is carried BY a
// skills contribution — that is why it has no kind of its own (kinds.go) — so a pack that
// moves its skills to `my-skills/` moves its plugins with them, and discovery that kept
// looking in skills/ would report a footprint with no plugin claim while delivery found one
// (or the reverse). Problems are dropped here: a pack whose declared source is missing has
// no plugins to report, and the refusal is printed by the render paths that act on it.
func (p *Pack) Plugins() []*pluginpack.Plugin {
	sources, _ := p.SkillsSources()
	dirs := make([]string, 0, len(sources))
	for _, src := range sources {
		// The AUDIENCE is deliberately ignored here. A wrapped plugin's NAME is exclusive
		// across packs and its components are discovered from the tree, so "which agents does
		// this plugin's skills reach" is a delivery question and this is a footprint one — a
		// plugin addressed to claude is still a plugin this pack ships, and hiding it from
		// `pack footprint` would hide exactly the line a reader came for.
		dirs = append(dirs, src.Dir)
	}
	return pluginpack.DiscoverIn(p.Root, dirs)
}

// HonoredPlugins returns the wrapped plugins this pack carries, code-running components
// included. Nothing is refused.
//
// A FETCHED pack's hooks and servers used to be stripped here, each with a printed refusal,
// until the user approved them at `yolo pack install`. OQ-TP9 deleted that gate — see the
// file header for why it was theatre — so a wrapped plugin is delivered whole whoever
// shipped it, and FootprintOf's ⚠ RUNS CODE line is what a user reads instead.
//
// The `refused` return is always nil — see packload.go's HonoredHostFiles for the family
// note on why the shape is kept.
func (p *Pack) HonoredPlugins() (granted []*pluginpack.Plugin, refused []string) {
	return p.Plugins(), nil
}
