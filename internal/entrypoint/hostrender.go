package entrypoint

// hostrender.go is the host-target render entry (env-manager plan Phase 4): render a
// pack's config surfaces into the invoking user's REAL home, at the `host` confinement
// notch, reusing the same boot writers via an Env pointed at that home. It is the
// implementation `yolo apply --host` calls.
//
// The resolved decisions this encodes (env-manager plan OQ-1..4, host-render-target.md
// §6.3, §6.6):
//   - PURE RMW. Every surface is read-modify-written: yolo regenerates only the keys it
//     declares (managed + dynamic tables) and leaves every key the agent wrote. No
//     whole-file compose, so no capture overlay, so no --revert (OQ-4).
//   - NO computed layer. The live MCP/LSP tables embed jail-absolute paths, so a host
//     render passes an empty computed map — a ${workspace}-derived value has no referent
//     off-container (OQ-2/§6.6) and such a surface is refused, not bound.
//   - Config kinds only. The FieldSet census: only config surfaces are target-
//     independent; mount/reads-host/state/files are refused by name upstream (the caller
//     reports them). This entry renders the surfaces; the confinement gate is the
//     caller's.
//   - User-scoped. What is rendered is a function of the pack + user config, never of a
//     workspace — so Workspace is empty and a ${workspace} surface is skipped.

import (
	"fmt"
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// HostRenderResult reports, per surface, what a host render did (or would do, in
// observe/dry-run mode) — enough for `apply --host` to print an honest summary.
type HostRenderResult struct {
	Surface string // "agent/name"
	Path    string // resolved real-home path
	Action  string // "rendered" | "would render" | "refused: <reason>"
}

// RenderHostPack renders one pack's config surfaces into homeDir (the real $HOME), pure
// RMW, no computed layer. When observe is true it computes what WOULD change and writes
// nothing (the `apply --host --dry-run` / default `observe` posture). It returns one
// result per surface and does not run hooks or touch any non-config kind.
//
// homeDir is the real home; the Env's Workspace is left empty so a ${workspace} surface
// is refused rather than bound to some arbitrary dir.
func RenderHostPack(p *packload.Pack, homeDir string, observe bool) ([]HostRenderResult, error) {
	e := &Env{Home: homeDir, Vars: map[string]string{}}
	surfaces, problems := p.Surfaces()
	if len(problems) > 0 {
		return nil, fmt.Errorf("pack %s: %s", p.Name, problems[0])
	}

	var out []HostRenderResult
	for _, s := range surfaces {
		id := s.Agent + "/" + s.Name
		path := expandHomePath(e, s.Path)

		if s.ResolvedMode() == manifest.ModeUnrendered {
			continue // declared but never written, at any target
		}
		if usesWorkspacePlaceholder(s) {
			out = append(out, HostRenderResult{Surface: id, Path: path,
				Action: "refused: uses ${workspace}, which has no referent on the host"})
			continue
		}
		if observe {
			out = append(out, HostRenderResult{Surface: id, Path: path, Action: "would render"})
			continue
		}
		// Pure RMW into the real home, NO computed layer (host gets none — OQ-4).
		if err := renderSurfaceRMWSurface(e, s, nil); err != nil {
			return out, fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, HostRenderResult{Surface: id, Path: path, Action: "rendered"})
	}
	return out, nil
}

// usesWorkspacePlaceholder reports whether a surface's data references ${workspace}.
// Detected structurally: substituting two different workspace values yields different
// surfaces iff the placeholder is present. Such a surface is per-jail and refused on a
// host target (no workspace referent).
func usesWorkspacePlaceholder(s manifest.Surface) bool {
	a := agentcfg.SubstituteWorkspace(s, "\x00A")
	b := agentcfg.SubstituteWorkspace(s, "\x00B")
	return !reflect.DeepEqual(a.Managed, b.Managed) || !reflect.DeepEqual(a.Defaults, b.Defaults)
}
