package cli

// applyhostlspplugin.go is the `yolo host apply` half of Claude's LSP route: the user's
// `lsp_servers` table rendered as ONE plugin directory, `yolo-lsp`, in every skills destination —
// the bytes a launch stages (jailcontent.RenderLSPPlugin;
// docs/reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin).
//
// OQ-HC1 (2026-09-28): "host parity with the same handling". Claude takes a language server only
// from a plugin, so before this file the host apply wrote ENABLE_LSP_TOOL into Claude's settings
// and Copilot's lsp-config.json while Claude at the host loaded no server at all.
//
// The handling, and why each piece is the jail's or differs from it:
//
//   - EVERY SKILLS DESTINATION, as a launch stages it, because core knows no agents: a destination
//     whose tool does not read plugins holds a directory it ignores. The directory is created when
//     missing, since the plugin is content and not an empty placeholder.
//   - THE TABLE IS THE COMPOSITION'S (hostInputComposition.lsp), so an entry naming a jail-only
//     path is left out here exactly as it is left out of Copilot's file, and named once by the
//     composition's own `input` line.
//   - OWNERSHIP IS THE PLUGIN'S OWN MANIFEST (jailcontent.IsLSPPlugin), never a record: the
//     composed-skills record maps a path to a PACK NAME, and droppedPackOrphans reads every owner
//     there as a pack the config dropped. Recording `yolo-lsp` would retire the plugin as a dropped
//     pack's output on the next apply.
//   - NOTHING ELSE AT THAT NAME IS OVERWRITTEN. The jail stages into a fresh directory, so writing
//     the plugin LAST is enough there; a real home can already hold a `yolo-lsp` the user made or a
//     pack composed (a flat skill of that name, or the namespaced subtree of a pack called
//     `yolo-lsp`). Either is refused by name, with the rename that lets the servers through, and
//     an --assert that met one exits 1.
//   - A PACK'S CLAIM IS THIS COMPOSITION'S, NOT THE RECORD'S (lspPluginClaimant), and what the
//     render retires is free (lspPluginPathsFreed). A dry run neither records nor archives, so
//     read from the record and the disk it disagreed with the --assert both ways: on a fresh home
//     it promised a plugin the --assert then refused for a pack's skill, and after the user renamed
//     that skill it repeated "rename that skill" while the --assert archived the old entry and
//     wrote the plugin.
//   - A RESERVED NAME IS LEFT ALONE: a pack contributing to a destination may fence `yolo-lsp` as
//     another tool's tree (packdecl.Contribution.Reserved), and a fence is never composed over.
//   - RETIRED BY ARCHIVE, unconfirmed, when the table renders nothing or no selected pack composes
//     the destination any more: every byte moved is one yolo wrote, the same asymmetry the skills
//     retire carries. A destination is judged dead only over a COMPLETE pack set, for
//     ComposeRequest.PackSetComplete's reason, and by the DIRECTORY rather than its path, so a
//     dropped pack's skills dir that links to a live one is not dead (liveSkillsDirs).
//
// Out of scope here: `yolo host apply --revert` (it does not withdraw skills of any kind) and
// `yolo config render --at host` (it renders config surfaces, and the plugin is not one).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// lspPluginArchiveAttribution is the archive subdirectory a retired plugin lands in, inside the
// skills bucket's generation: named for the config key whose output it was, since no pack owns it.
const lspPluginArchiveAttribution = "lsp_servers"

// applyHostLSPPlugin writes, keeps or retires the plugin in every destination and returns an rc
// contribution: 1 when an --assert refused a destination's plugin, which then did not deliver
// what the config asks. A dry run stays 0 (OQ-RO5): its output is the finding, and a non-zero
// observe pass would leave the launch gate unable to read the home at all.
//
// dests are the destinations this apply composes (hostskills.ComposeHostSkills over the active
// set), and rendered is what RenderHostSkills just did to them; candidates add every pack yolo
// ships, so a destination a dropped pack named is visited for a plugin left behind there. req is
// the composition's own request: its archive root and stamp group a retired plugin with the skills
// this apply retires.
func applyHostLSPPlugin(pr richtext.Printer, survey *hostApplySurvey, lsp *jsonx.OrderedMap,
	dests []hostskills.Destination, rendered []hostskills.Result, candidates []*packload.Pack,
	req hostskills.ComposeRequest, home string, write bool) int {
	manifest, render := jailcontent.RenderLSPPlugin(lsp)
	freed := lspPluginPathsFreed(rendered)
	rc := 0
	report := func(r hostskills.Result) {
		printSkillResult(pr, survey, r)
		if write && r.Action == hostskills.ActionRefused {
			rc = 1
		}
	}
	for _, d := range dests {
		path := filepath.Join(d.Dir, jailcontent.LSPPluginDir)
		if d.IsReserved(jailcontent.LSPPluginDir) {
			if render {
				report(hostskills.Result{Name: jailcontent.LSPPluginDir, Path: path,
					Action: hostskills.ActionSkippedUser,
					Detail: "a pack contributing here reserves this name for another tool's tree, " +
						"so yolo's LSP plugin is not written into " + prettyHomePath(home, d.Dir)})
			}
			continue
		}
		if render {
			report(writeHostLSPPlugin(path, manifest, lspPluginClaimant(d), freed[path], home, write))
			continue
		}
		if r, ok := retireHostLSPPlugin(path, req, home, write,
			"your lsp_servers declares no server with a command"); ok {
			report(r)
		}
	}
	if !req.PackSetComplete {
		// A pack the config names did not resolve, and it may be the one naming a destination: a
		// destination is dead only when the whole set says so.
		return rc
	}
	isLive := liveSkillsDirs(dests)
	for _, dir := range hostSkillsDirs(candidates, home) {
		if isLive(dir) {
			continue
		}
		if r, ok := retireHostLSPPlugin(filepath.Join(dir, jailcontent.LSPPluginDir), req, home, write,
			"no selected pack composes skills here any more"); ok {
			report(r)
		}
	}
	return rc
}

// liveSkillsDirs answers whether a skills directory is one this apply composes — by its path, or
// as the same directory under another name. A home often shares one skills tree between tools by a
// link (`~/.codex/skills -> ~/.claude/skills`), and judged by path alone that link is a destination
// no selected pack composes: the retire then archived, through it, the plugin the loop above had
// just written, on every apply.
//
// Built AFTER the live loop, since an --assert may have just created a live directory. A live one
// that does not exist holds nothing to retire, so only the existing ones are compared, and a dir
// that cannot be stat'd is judged by its path alone — IsLSPPlugin finds nothing there either.
func liveSkillsDirs(dests []hostskills.Destination) func(dir string) bool {
	byPath := map[string]bool{}
	var infos []os.FileInfo
	for _, d := range dests {
		byPath[d.Dir] = true
		if info, err := os.Stat(d.Dir); err == nil {
			infos = append(infos, info)
		}
	}
	return func(dir string) bool {
		if byPath[dir] {
			return true
		}
		info, err := os.Stat(dir)
		if err != nil {
			return false
		}
		for _, l := range infos {
			if os.SameFile(info, l) {
				return true
			}
		}
		return false
	}
}

// writeHostLSPPlugin delivers the plugin at path, or refuses when something not provably the
// plugin holds that name. claimant is the pack this composition puts a `yolo-lsp` of its own at
// path for ("" for none); freed says the skills render archives or clears what is at path, which a
// dry run leaves on disk, so it is treated as gone in both postures.
func writeHostLSPPlugin(path string, manifest []byte, claimant string, freed bool, home string,
	write bool) hostskills.Result {
	r := hostskills.Result{Name: jailcontent.LSPPluginDir, Path: path,
		Detail: "Claude's language servers, from your lsp_servers"}
	refuse := func(why string) hostskills.Result {
		r.Action, r.Detail, r.WouldChange = hostskills.ActionRefused, why, false
		return r
	}
	if claimant != "" {
		return refuse(fmt.Sprintf("pack %s composes a skill named %s at %s, the name yolo's LSP "+
			"plugin takes, so your lsp_servers do not reach this destination — rename that skill "+
			"in pack %s, then run `yolo host apply --assert` again",
			claimant, jailcontent.LSPPluginDir, prettyHomePath(home, path), claimant))
	}
	manifestPath := filepath.Join(path, filepath.FromSlash(jailcontent.LSPPluginManifestRel))
	_, err := os.Lstat(path)
	switch {
	case err == nil && freed:
		// What is there leaves with this apply's skills retire; only a dry run still sees it.
	case err == nil:
		if !jailcontent.IsLSPPlugin(path) {
			return refuse(fmt.Sprintf("%s is not yolo's LSP plugin (it holds no manifest yolo "+
				"wrote), so it is left as it is and your lsp_servers do not reach this destination — "+
				"rename or remove it, then run `yolo host apply --assert` again",
				prettyHomePath(home, path)))
		}
		if existing, rerr := os.ReadFile(manifestPath); rerr == nil && bytes.Equal(existing, manifest) {
			r.Action = hostskills.ActionUnchanged
			return r
		}
	case !os.IsNotExist(err):
		return refuse(fmt.Sprintf("cannot inspect %s (%v), so your lsp_servers do not reach this "+
			"destination", prettyHomePath(home, path), err) + lspPluginIORemedy(home, filepath.Dir(path)))
	}
	r.WouldChange = true
	if !write {
		r.Action = hostskills.ActionWouldWrite
		return r
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return refuse(fmt.Sprintf("could not create %s (%v)", prettyHomePath(home, path), err) +
			lspPluginIORemedy(home, filepath.Dir(path)))
	}
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		return refuse(fmt.Sprintf("could not write %s (%v)", prettyHomePath(home, manifestPath), err) +
			lspPluginIORemedy(home, filepath.Dir(manifestPath)))
	}
	r.Action = hostskills.ActionWrote
	return r
}

// lspPluginIORemedy is the next step a failed read or write of the plugin names: the directory
// whose permissions decide it, then the rerun that delivers the servers.
func lspPluginIORemedy(home, dir string) string {
	return " — check that " + prettyHomePath(home, dir) + " is a directory you can write, then run " +
		"`yolo host apply --assert` again"
}

// retireHostLSPPlugin archives the plugin at path, and reports false when there is no plugin of
// yolo's there to retire — absent, the user's own, or a pack's skill of the same name, whose
// retirement is the skills composition's. The manifest alone decides (IsLSPPlugin): a pack's skill
// or namespaced subtree named yolo-lsp carries no `lspServers`.
func retireHostLSPPlugin(path string, req hostskills.ComposeRequest, home string, write bool,
	why string) (hostskills.Result, bool) {
	if !jailcontent.IsLSPPlugin(path) {
		return hostskills.Result{}, false
	}
	r := hostskills.Result{Name: jailcontent.LSPPluginDir, Path: path, Detail: why, WouldChange: true}
	if !write {
		r.Action = hostskills.ActionWouldArchive
		r.Detail += " (would move to the archive)"
		return r, true
	}
	at, err := hostskills.Archive(req.ArchiveRoot, req.Stamp, lspPluginArchiveAttribution, path)
	if err != nil {
		// Archive can fail on either side — creating the generation under the archive root, or
		// removing the original once a cross-device copy landed — so the remedy names both.
		r.Action, r.WouldChange = hostskills.ActionRefused, false
		r.Detail = fmt.Sprintf("could not move %s into the archive at %s (%v) — check that you can "+
			"write both, then run `yolo host apply --assert` again",
			prettyHomePath(home, path), prettyHomePath(home, string(req.ArchiveRoot)), err)
		return r, true
	}
	r.Action = hostskills.ActionArchived
	r.Detail += " → " + at
	return r, true
}

// lspPluginProbe is the layer lspPluginClaimant adds to a destination to ask who else claims the
// name: a namespaced layer whose one wrapped plugin is called yolo-lsp, which claims exactly that
// top-level name and nothing else. Its pack name can be no pack's: a configured pack's name never
// holds a "/" (config's checkPackName), since it becomes a staging directory, and a shipped one is
// its own directory's.
const lspPluginProbe = "lsp_servers/" + jailcontent.LSPPluginDir

// lspPluginClaimant names the pack whose layer in THIS composition puts an entry called yolo-lsp at
// d's top level, "" for none: a flat skill of that name, the namespaced subtree of a pack called
// yolo-lsp, or a wrapped plugin of that name.
//
// Asked of hostskills.Collisions with the plugin added as one more layer, because that is the one
// authority on which top-level names a composition creates (layerClaims, which mirrors the
// render's own deliveries). Read from the destination's LAYERS, not from the skills record or the
// disk, because a dry run writes neither: the record names what an earlier --assert composed, so a
// claim this composition makes for the first time is absent from it, and one the user just renamed
// away is still in it. A real collision between two packs never reaches here — applyHostSkills
// refuses it first — so at most one pack claims the name, and a destination with an unresolved
// layer, which the render leaves alone, reports none.
func lspPluginClaimant(d hostskills.Destination) string {
	layers := append(append([]hostskills.Layer(nil), d.Layers...), hostskills.Layer{
		Pack: lspPluginProbe, Tier: hostskills.TierNamespaced,
		Plugins: []*pluginpack.Plugin{{Dir: jailcontent.LSPPluginDir}},
	})
	for _, c := range hostskills.Collisions([]hostskills.Destination{{Dir: d.Dir, Layers: layers}}) {
		if c.Name != jailcontent.LSPPluginDir {
			continue
		}
		for _, cl := range c.Claims {
			if cl.Pack != lspPluginProbe {
				return cl.Pack
			}
		}
	}
	return ""
}

// lspPluginPathsFreed is every path the skills render archives or clears, in either posture: a
// `yolo-lsp` a pack no longer composes, which the --assert moves out before the plugin is written,
// and which a dry run must therefore not report as standing in the plugin's way. The LAST result
// at a path decides, since a delivery can clear a dangling link and then write over it.
func lspPluginPathsFreed(rendered []hostskills.Result) map[string]bool {
	last := map[string]hostskills.Action{}
	for _, r := range rendered {
		last[r.Path] = r.Action
	}
	freed := map[string]bool{}
	for path, a := range last {
		switch a {
		case hostskills.ActionArchived, hostskills.ActionWouldArchive,
			hostskills.ActionCleared, hostskills.ActionWouldClear:
			freed[path] = true
		}
	}
	return freed
}
