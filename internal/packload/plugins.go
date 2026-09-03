package packload

// plugins.go is the ORIGIN GATE over a pack that wraps an existing agent plugin.
//
// A wrapped plugin is a different trust question from ordinary pack content, and the
// difference is not the file format — it is that a plugin manifest can declare
// `hooks`/`mcpServers`/`lspServers`, i.e. PROCESSES THAT RUN on the user's behalf. That is
// the same line the pack system already draws between an npm package (content the user
// already trusts a registry for) and a curl-piped installer (arbitrary code from a URL): a
// FETCHED pack cannot introduce one without the user's explicit approval.
//
// This is here rather than in internal/pluginpack because it is the same shape as
// HonoredHostFiles / HonoredMounts / HonoredInstall and belongs beside them: pluginpack knows
// what a plugin declares, packload knows whether this pack's ORIGIN permits it. Getting that
// split wrong is how plugin-as-pack would have become the one path by which a fetched tree
// runs code with no approval — which is precisely the hole the trust gate exists to close.

import (
	"fmt"

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

// HonoredPlugins returns the wrapped plugins whose CODE-RUNNING components this pack's origin
// permits, and one reported refusal per component that was denied.
//
// A plugin declaring no hooks and no servers is honored regardless of origin: skills, commands
// and sub-agents are content, and content is the thing a pack distributes. A plugin that runs
// code is honored only for a pack whose origin carries the user's own authority — embedded,
// local, or a fetched pack whose claims the user approved at `yolo pack install` (which is
// what MayAccessHost already encodes at launch, per commit, from the lockfile).
//
// The plugin is still DELIVERED when its code-running components are refused: the skills are
// the reason the user wrapped it, and withholding those too would punish them for a hook they
// did not ask for. What the refusal buys is that the components which RUN are named, not
// quietly delivered — the caller strips them and says so.
func (p *Pack) HonoredPlugins() (granted []*pluginpack.Plugin, refused []string) {
	plugins := p.Plugins()
	if len(plugins) == 0 {
		return nil, nil
	}
	for _, pl := range plugins {
		if !pl.RunsCode() || p.MayAccessHost {
			granted = append(granted, pl)
			continue
		}
		for _, c := range pl.Components() {
			if !c.RunsCode {
				continue
			}
			refused = append(refused, fmt.Sprintf(
				"pack %s: refused plugin %s's %q — it %s, and a FETCHED pack cannot run code "+
					"you have not approved. Run `yolo pack install` to review and approve it; "+
					"the plugin's skills are delivered either way.",
				p.Name, pl.Name(), c.Name, c.Detail))
		}
		granted = append(granted, pl)
	}
	return granted, refused
}

// PluginHostAccessClaims returns the specific approval strings every wrapped plugin's
// code-running components make, for the `pack install` prompt and the lockfile. Merged into
// the pack's own HostAccessClaims by the caller, so a plugin gaining a hook on a later commit
// re-prompts exactly the way a pack gaining a `reads-host` does.
func (p *Pack) PluginHostAccessClaims() []string {
	var out []string
	for _, pl := range p.Plugins() {
		out = append(out, pl.HostAccessClaims()...)
	}
	return out
}
