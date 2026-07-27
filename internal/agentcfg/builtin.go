package agentcfg

import "github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"

// builtin.go holds CORE'S OWN surfaces plus the workspace-placeholder machinery.
//
// It used to hold ten more: the per-agent "defaults + managed + path + codec" literals for
// pi, claude, copilot, opencode, codex and agy. Those now live in the pack that owns each
// one (packs/*/pack.json), generated from these literals and proved byte-equal to them
// before the literals were deleted. What is left is mise/config — yolo's own global tool
// config, which belongs to no pack and renders unconditionally.
//
// So BuiltinManifest no longer answers "every surface yolo knows about": that depends on
// which packs are loaded, which depends on the user's config. A caller wanting the full set
// merges pack surfaces over this via ManifestWith.
//
// A surface's Path uses a leading "~/" that the CLI expands to the jail home;
// this package stays path-policy-free (it never touches the filesystem).

// WorkspacePlaceholder is the token surface DATA uses where the jail's workspace
// root belongs, resolved by SubstituteWorkspace at render time.
//
// It exists because the workspace root is NOT always "/workspace": Env.WorkspaceDir
// honors YOLO_WORKSPACE, and the macos-user backend has no /workspace at all. A
// literal in the manifest was therefore a latent correctness bug on any run whose
// workspace is elsewhere — claude would assert projects["/workspace"] while the
// agent looked under the real path.
//
// It is also what un-blocks surfaces-as-pack-data: a pack cannot ship a
// jail-specific absolute path, so the placeholder is the seam that lets this
// surface definition move out of Go unchanged.
const WorkspacePlaceholder = "${workspace}"

// SubstituteWorkspace returns a copy of s with every WorkspacePlaceholder MAP KEY
// in the Defaults and Managed layers replaced by workspace. A surface that does
// not use the placeholder is returned unchanged.
//
// It DEEP-COPIES the layers it rewrites: manifest surfaces are package-level
// values shared by every caller (Lookup returns them by value, but the maps
// inside are aliased), so rewriting in place would corrupt the manifest for the
// rest of the process — and, in tests, for every later test in the binary.
//
// Only keys are substituted, not values, because that is the only shape any real
// surface needs (claude's projects table is keyed by absolute path). Extending to
// values would invite a placeholder appearing inside arbitrary strings, which is a
// templating language rather than one named seam.
func SubstituteWorkspace(s manifest.Surface, workspace string) manifest.Surface {
	s.Defaults = substituteWorkspaceValue(s.Defaults, workspace)
	s.Managed = substituteWorkspaceValue(s.Managed, workspace)
	return s
}

// substituteWorkspaceValue deep-copies v, rewriting placeholder keys. Non-map
// values are returned as-is: they are immutable scalars or slices the caller does
// not rewrite, so sharing them is safe.
func substituteWorkspaceValue(v any, workspace string) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		if k == WorkspacePlaceholder {
			k = workspace
		}
		out[k] = substituteWorkspaceValue(val, workspace)
	}
	return out
}

// miseConfig is the global mise config surface (~/.config/mise/config.toml) —
// the first NON-agent surface ported onto the prism (docs/design/config-
// migration-to-prism.md §4.1, a HIGH stale-risk surface). The bespoke
// GenerateMiseConfig was an in-place editor that added/updated but never removed
// what it wrote, so an older yolo's default `node`/`python`/`go` `[tools]` lines
// persisted forever and shadowed the baked /bin/<tool> — the exact
// LD_LIBRARY_PATH / MCP-wrapper whack-a-mole (mise-node-dynamic-linking.md).
//
// The prism fixes this WITHOUT the special-case §4.1 pre-render scrub the older
// bespoke code carried: on the first prism boot ComposeStateful seeds from a
// fresh render with an EMPTY overlay and discards the on-disk file
// (staterender.go §3.2), so the stale lines — present in no layer — simply do
// not render. Steady-state capture then begins from that truthful baseline.
//
// The static surface is intentionally EMPTY: no Defaults (miseBaseTools is []
// — every default runtime is baked into the image, so mise is override-only,
// never a default runtime's source), no Managed (yolo asserts no mise key), no
// host mount (nothing mirrors this file from the host). The ONLY yolo-owned
// content is the DYNAMIC per-boot [tools] table injected via YOLO_MISE_TOOLS,
// which ConfigureMisePrism hands to the engine as the COMPUTED layer — above the
// captured overlay, so an injected pin wins over a stale in-jail `mise use -g`,
// while a user-added global tool is captured into the overlay and survives.
//
// SCOPE: the prism owns only this GLOBAL config. The /workspace/mise.toml
// retired-tool surgery and the `mise uninstall` subprocess stay bespoke boot
// side effects (workspace mutation + orchestration, not global-config content —
// the prism never owns workspace files, migration doc §5.3).
var miseConfig = manifest.Surface{
	Agent: "mise",
	Name:  "config",
	Path:  "~/.config/mise/config.toml",
	Codec: "toml",
}

// BuiltinManifest returns the yolo-shipped manifest of all surfaces yolo knows
// how to compose. It carries pi, claude (settings + config), gemini, copilot
// (config + mcp + lsp), opencode, codex, agy (settings + mcp), and mise
// (config). It panics on a malformed builtin (a programming error in this file,
// caught by tests), never at runtime for user input.
func BuiltinManifest() *manifest.Manifest {
	m, err := manifest.New(builtinSurfaces()...)
	if err != nil {
		panic("agentcfg: malformed builtin manifest: " + err.Error())
	}
	return m
}

// builtinSurfaces is the ordered list BuiltinManifest builds from.
//
// ONLY CORE'S OWN SURFACES remain here. mise/config is yolo's own global tool config —
// it belongs to no pack, is rendered unconditionally, and has nothing to do with any
// agent. Every other surface that used to be in this list now lives in the pack that
// owns it (packs/*/pack.json), which is what let the six per-agent Go render functions
// go.
//
// A caller wanting the FULL set (core's plus every pack's) calls ManifestWith with the
// pack-loaded surfaces. That distinction is deliberate: which packs exist depends on the
// user's config, so a package-level Go value cannot know it, and pretending otherwise is
// how the old list ended up asserting that yolo ships six agents.
func builtinSurfaces() []manifest.Surface {
	return []manifest.Surface{miseConfig}
}

// ManifestWith returns the builtin manifest with extra (data-loaded) surfaces merged
// over it: a surface sharing an (agent, name) with a builtin REPLACES it, and a new
// key is added (D3).
//
// This is how a pack contributes a surface. It goes through the same validation as the
// builtins, so a malformed pack surface is an error here — on the host, at
// `yolo check` — rather than a silently misconfigured agent.
//
// Note what this does NOT do: it does not let a pack change which host files cross the
// boundary. A surface names a JAIL destination and its layers; the host source for the
// two agent surfaces that have one is decided by agents.AgentSpec.HostFiles in Go, and
// stays there.
func ManifestWith(extra ...manifest.Surface) (*manifest.Manifest, error) {
	return manifest.Merge(builtinSurfaces(), extra...)
}
