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
//   - A RESERVED NAME IS LEFT ALONE: a pack contributing to a destination may fence `yolo-lsp` as
//     another tool's tree (packdecl.Contribution.Reserved), and a fence is never composed over.
//   - RETIRED BY ARCHIVE, unconfirmed, when the table renders nothing or no selected pack composes
//     the destination any more: every byte moved is one yolo wrote, the same asymmetry the skills
//     retire carries. A destination is judged dead only over a COMPLETE pack set, for
//     ComposeRequest.PackSetComplete's reason.
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
// set); candidates add every pack yolo ships, so a destination a dropped pack named is visited for
// a plugin left behind there. req is the composition's own request: its records name what a pack
// composed, and its archive root and stamp group a retired plugin with the skills this apply
// retires.
func applyHostLSPPlugin(pr richtext.Printer, survey *hostApplySurvey, lsp *jsonx.OrderedMap,
	dests []hostskills.Destination, candidates []*packload.Pack, req hostskills.ComposeRequest,
	home string, write bool) int {
	manifest, render := jailcontent.RenderLSPPlugin(lsp)
	rc := 0
	report := func(r hostskills.Result) {
		printSkillResult(pr, survey, r)
		if write && r.Action == hostskills.ActionRefused {
			rc = 1
		}
	}
	live := map[string]bool{}
	for _, d := range dests {
		live[d.Dir] = true
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
			report(writeHostLSPPlugin(path, manifest, req, home, write))
			continue
		}
		if r, ok := retireHostLSPPlugin(path, req, write,
			"your lsp_servers declares no server with a command"); ok {
			report(r)
		}
	}
	if !req.PackSetComplete {
		// A pack the config names did not resolve, and it may be the one naming a destination: a
		// destination is dead only when the whole set says so.
		return rc
	}
	for _, dir := range hostSkillsDirs(candidates, home) {
		if live[dir] {
			continue
		}
		if r, ok := retireHostLSPPlugin(filepath.Join(dir, jailcontent.LSPPluginDir), req, write,
			"no selected pack composes skills here any more"); ok {
			report(r)
		}
	}
	return rc
}

// writeHostLSPPlugin delivers the plugin at path, or refuses when something not provably the
// plugin holds that name.
func writeHostLSPPlugin(path string, manifest []byte, req hostskills.ComposeRequest, home string,
	write bool) hostskills.Result {
	r := hostskills.Result{Name: jailcontent.LSPPluginDir, Path: path,
		Detail: "Claude's language servers, from your lsp_servers"}
	refuse := func(why string) hostskills.Result {
		r.Action, r.Detail, r.WouldChange = hostskills.ActionRefused, why, false
		return r
	}
	if pack, ok := lspPluginPathPackOwner(path, req); ok {
		return refuse(fmt.Sprintf("pack %s composes a skill named %s at %s, the name yolo's LSP "+
			"plugin takes, so your lsp_servers do not reach this destination — rename that skill "+
			"in pack %s, then run `yolo host apply --assert` again",
			pack, jailcontent.LSPPluginDir, prettyHomePath(home, path), pack))
	}
	manifestPath := filepath.Join(path, filepath.FromSlash(jailcontent.LSPPluginManifestRel))
	if _, err := os.Lstat(path); err == nil {
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
	} else if !os.IsNotExist(err) {
		return refuse(fmt.Sprintf("cannot inspect %s: %v", prettyHomePath(home, path), err))
	}
	r.WouldChange = true
	if !write {
		r.Action = hostskills.ActionWouldWrite
		return r
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return refuse(fmt.Sprintf("could not create %s: %v", prettyHomePath(home, path), err))
	}
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		return refuse(fmt.Sprintf("could not write %s: %v", prettyHomePath(home, manifestPath), err))
	}
	r.Action = hostskills.ActionWrote
	return r
}

// retireHostLSPPlugin archives the plugin at path, and reports false when there is no plugin of
// yolo's there to retire — absent, the user's own, or a pack's skill of the same name, whose
// retirement is the skills composition's.
func retireHostLSPPlugin(path string, req hostskills.ComposeRequest, write bool,
	why string) (hostskills.Result, bool) {
	if _, ok := lspPluginPathPackOwner(path, req); ok || !jailcontent.IsLSPPlugin(path) {
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
		r.Action, r.Detail, r.WouldChange = hostskills.ActionRefused, err.Error(), false
		return r, true
	}
	r.Action = hostskills.ActionArchived
	r.Detail += " → " + at
	return r, true
}

// lspPluginPathPackOwner names the pack a skills record says composed path, if one does: the
// composition's record, or the per-entry record a pre-composition delivery kept.
func lspPluginPathPackOwner(path string, req hostskills.ComposeRequest) (string, bool) {
	for _, m := range []*hostskills.Manifest{req.Composed, req.Legacy} {
		if m == nil {
			continue
		}
		if owner, ok := m.Owner(path); ok {
			return owner, true
		}
	}
	return "", false
}
