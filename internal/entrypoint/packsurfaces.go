package entrypoint

// packsurfaces.go renders every pack-declared surface in one loop, which is what
// replaced the switch on six hardcoded names.
//
// What was here before: `configureAgent(e, agent)` switching on "claude"/"copilot"/
// "opencode"/"pi"/"codex"/"agy" and calling six Go functions. Reading them side by side,
// five were the same three steps in a different order — mkdir the config dir, build a
// computed map, call one of two render helpers — and the sixth (claude) added imperative
// side effects. So the switch was not expressing six behaviors; it was expressing one
// behavior plus per-agent DATA, with the data trapped in Go.
//
// Now the data is in the pack:
//
//	surfaces[].mode       which engine mechanism writes the file
//	surfaces[].computed   which live table feeds it, and how to reshape it
//	surfaces[].path       what to mkdir (its parent)
//
// and this file is the loop. Core no longer knows any agent's name. Adding a seventh
// tool is a pack.json, not a Go change — which is the claim the whole transition rests
// on, so it is worth saying plainly that it is now literally true of this path.
//
// WHAT DID NOT GENERALIZE, stated because a reader will look for it: claude's
// credentials symlink, per-jail history isolation, and plugin install/uninstall are
// imperative side effects, not surface content. They live in packhooks.go behind a named
// capability a pack requests, rather than being reachable by writing an agent's name.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// LoadJailPacks reads the pack trees mounted at YOLO_PACK_ROOT.
//
// Every pack found here was already staged and origin-checked ON THE HOST, so this does
// NOT re-derive trust: the host decided which host files a pack may name, and it did so
// with access to the user config (which the jail deliberately cannot read). The jail's
// job is to render what it was given.
//
// A pack whose manifest fails to parse in-jail after parsing on the host means the
// mounted tree disagrees with what was staged — corruption, not a user error — so it is
// returned as an error and the boot fails (A12).
func LoadJailPacks(e *Env) ([]*packload.Pack, error) {
	root := e.Getenv("YOLO_PACK_ROOT")
	if root == "" {
		// No packs mounted. Legitimate: an older host launcher, macos-user, or a jail
		// started with no packs at all. Renders nothing rather than failing.
		return nil, nil
	}
	var packs []*packload.Pack
	// Two levels: <root>/_official/<name> for the embedded packs, <root>/<slug> for
	// configured ones. Walking both keeps the jail ignorant of which is which — the
	// distinction only ever mattered for the host-side origin gate.
	for _, dir := range []string{filepath.Join(root, "_official"), root} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() || ent.Name() == "_official" {
				continue
			}
			p, problems := packload.LoadDir(filepath.Join(dir, ent.Name()), ent.Name(), true)
			if len(problems) > 0 {
				return nil, fmt.Errorf("pack %s: %s", ent.Name(), problems[0])
			}
			packs = append(packs, p)
		}
	}
	return packs, nil
}

// ConfigurePackSurfaces renders every surface every loaded pack declares.
//
// Each surface is rendered by the mechanism its own `mode` names, so this function has
// no per-pack branching at all. Failures are collected through genStep, so one boot
// reports every broken surface rather than one per restart (A12).
func ConfigurePackSurfaces(e *Env, packs []*packload.Pack) {
	tables := liveTables(e)
	for _, p := range packs {
		surfaces, problems := p.Surfaces()
		for _, prob := range problems {
			// A malformed surface is fatal: rendering the rest and skipping this one
			// yields a jail whose config is quietly incomplete.
			genStep(e, "pack_"+p.Name+"_surfaces", func() error { return fmt.Errorf("%s", prob) })
		}
		// A pack's derive.lua (if any) produces every dynamic layer for its surfaces
		// — the projection Lua (docs/design/pack-system.md §7). Read once per pack;
		// absent means no surface has a dynamic layer.
		deriveScript := loadPackDeriveScript(p)
		for _, s := range surfaces {
			surface := s
			genStep(e, "configure_"+surface.Agent+"_"+surface.Name, func() error {
				return renderDeclaredSurface(e, surface, tables, deriveScript)
			})
		}
	}
}

// loadPackDeriveScript reads a pack's derive.lua (at its tree root), or "" when
// absent. The script registers per-surface producers via yolo.derive(agent,
// surface, fn); a surface with no registered derive gets no dynamic layer.
func loadPackDeriveScript(p *packload.Pack) string {
	if p.Root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(p.Root, "derive.lua"))
	if err != nil {
		return ""
	}
	return string(data)
}

// deriveComputedLayer runs a surface's derive producer to build its dynamic
// (computed) layer — the map that feeds Inputs.Computed and, for an RMW surface,
// the managed dynamic table. Returns nil when the pack ships no derive or none is
// registered for this surface (the identity: no dynamic layer). A Lua error is
// fatal, matching the old BuildComputed error contract.
func deriveComputedLayer(e *Env, surface manifest.Surface, deriveScript string, tables map[string]map[string]any) (map[string]any, error) {
	if deriveScript == "" {
		return nil, nil
	}
	out, err := (luahook.GopherLuaVM{}).Derive(deriveScript, &luahook.DeriveCtx{
		Agent:   surface.Agent,
		Surface: surface.Name,
		Tables:  tables,
	})
	if err != nil {
		return nil, fmt.Errorf("surface %s/%s: derive: %w", surface.Agent, surface.Name, err)
	}
	return out, nil
}

// liveTables gathers the live config tables a surface's `computed` declarations may draw
// from, lowered into the engine's plain value model.
//
// CORE owns this list, and that is the division of labor that makes the rest work: an
// MCP server is a yolo config concept, not an agent concept, so core knows how to
// produce the table and a pack only says which one it wants and what shape it needs.
func liveTables(e *Env) map[string]map[string]any {
	return map[string]map[string]any{
		manifest.SourceMCPServers: prismMap(e.LoadMCPServers()),
		manifest.SourceLSPServers: prismMap(LoadLSPServers(e)),
	}
}

// renderDeclaredSurface writes one declared surface by the mechanism its mode names.
func renderDeclaredSurface(e *Env, surface manifest.Surface, tables map[string]map[string]any, deriveScript string) error {
	if surface.ResolvedMode() == manifest.ModeUnrendered {
		// Declared so `yolo config ls` can describe the file and so host_files cannot
		// claim its path, but yolo does not write it. Skipping silently is correct here
		// — "unrendered" is the declaration's whole meaning.
		return nil
	}

	// The config dir. Was an os.MkdirAll per agent in the six Go functions; the surface
	// path already says where the file goes, so the directory is derivable rather than
	// declared.
	if err := os.MkdirAll(filepath.Dir(expandHomePath(e, surface.Path)), 0o755); err != nil {
		return err
	}

	// The dynamic (computed) layer: produced by the surface's derive function over the
	// live tables (docs/design/pack-system.md §7). One map serves both the compose
	// path (as Inputs.Computed) and the RMW path (as the managed dynamic table).
	computed, err := deriveComputedLayer(e, surface, deriveScript, tables)
	if err != nil {
		return err
	}

	switch surface.ResolvedMode() {
	case manifest.ModeComputed:
		_, err := renderSurfaceStatelessSurface(e, surface, hostSurfaceBytes(e, surface), computed)
		return err
	case manifest.ModeRMW:
		return renderSurfaceRMWSurface(e, surface, computed)
	default:
		out, err := renderSurfaceStatefulSurface(e, surface,
			hostSurfaceBytes(e, surface), computed)
		if err != nil {
			return err
		}
		if out != nil && out.FirstMigration {
			retireOrphanSidecars(e, surface)
		}
		return nil
	}
}

// hostSurfaceBytes reads the surface's host source from its /ctx mount, if it has one.
//
// The path is DERIVED from the surface's own file name under the pack's /ctx dir, which
// is how a surface's host layer stopped needing a Go constant per agent (hostClaudeDir,
// hostPiDir). The pack declares `hostFiles: [{from: ".claude/settings.json"}]`, the host
// mounts it at /ctx/host-<pack>/settings.json if the origin gate allows, and this finds
// it there.
//
// Read FAIL-OPEN, and deliberately: an absent mount means the user has no such host file
// (or this is macos-user, with no /ctx at all), and the render falls back to its lower
// layers. Treating that as an error would make a jail refuse to start because the user
// had never configured the tool on the host.
func hostSurfaceBytes(e *Env, surface manifest.Surface) []byte {
	if surface.HostSource == "" {
		return nil
	}
	data, _ := os.ReadFile(remapCtx(surface.HostSource))
	return data
}

// ctxRoot is where host-file mounts appear in this process's filesystem. It is
// packload.CtxRoot in a real jail; a var so a test can point it at a temp dir, which is
// what hostClaudeDir/hostPiDir used to be for.
var ctxRoot = packload.CtxRoot

// remapCtx rewrites a /ctx path onto ctxRoot. A no-op in a real jail.
func remapCtx(p string) string {
	if ctxRoot == packload.CtxRoot {
		return p
	}
	return filepath.Join(ctxRoot, strings.TrimPrefix(p, packload.CtxRoot+"/"))
}

// retireOrphanSidecars deletes the pre-prism sidecars a surface declares, on the boot
// that migrates it. Failures are IGNORED: the file is already unread, so a stale copy is
// untidy rather than wrong, and failing the boot over it would be a worse outcome than
// leaving it.
func retireOrphanSidecars(e *Env, surface manifest.Surface) {
	dir := filepath.Dir(expandHomePath(e, surface.Path))
	for _, name := range surface.RetireOnFirstRender {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
