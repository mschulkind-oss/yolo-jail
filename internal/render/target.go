// Package render is the one place a composed config surface is written to disk,
// parameterized by an explicit Target so the same renderer serves every confinement
// level. It sits above internal/agentcfg (the pure compose engine) and below both
// internal/entrypoint (the in-jail boot render) and internal/cli (the host-side
// `yolo config` verbs) — the two callers that, before this package, were hand-copied
// implementations of "render a surface" that drifted (host-render-target.md §3.1, and
// the destructive host-side writes that drift produced, §6.1).
//
// The design (host-render-target.md §3, env-manager plan Phase 1): a Target is
// everything the renderer cannot infer — which home to write into, which workspace the
// ${workspace} placeholder resolves to, where the §5 sidecars live, and where to send
// user-facing notices. Everything else a render needs (the host layer bytes, the
// computed/derive layer) is passed in as arguments, precisely so it stays out of the
// renderer: resolving a host mount and lowering the live MCP/LSP tables are
// jail-environment concerns that core owns, not the engine's.
//
// What this package deliberately does NOT own:
//   - liveTables (the MCP/LSP source tables) — "an MCP server is a yolo config concept,
//     not an agent concept" stays in the caller that has the wide environment.
//   - host-source resolution (/ctx mounts) — jail-shaped, passed in as HostBytes.
//   - genStep's A12 fatal-collection policy — the renderer only RETURNS errors; the
//     caller decides whether a failure halts the boot (loud) or is a message (host).
package render

import "io"

// Target is everything the surface renderer cannot infer from the surface declaration
// itself — the difference between rendering into a jail, into a preview temp dir, or
// into the real host home. It is the parameter the boot render used to reach implicitly
// through an *entrypoint.Env and the host verbs used to reach implicitly through
// paths.Home(); making it explicit is what lets one renderer serve all three, and is
// the fix for the class of bug where the host path silently wrote the wrong home.
type Target struct {
	// Home is the resolved home directory a "~"-relative surface path writes into:
	// the jail home ($JAIL_HOME) on boot, the invoking user's real $HOME on a host
	// target, a temp dir for a preview. Always an already-resolved absolute path — the
	// renderer never consults the process environment to find it.
	Home string

	// Workspace is the directory the ${workspace} placeholder substitutes to, and the
	// root the §5 sidecar tree (.yolo/prism/) lives under. On boot it is the container
	// workspace (/workspace); a host target has no per-workspace referent, so a surface
	// that uses ${workspace} is refused there (env-manager plan OQ-2/§6.6) rather than
	// bound to some arbitrary dir.
	Workspace string

	// Stderr is where user-facing render notices go — the "captured N keys" and
	// "dropped a UI-added MCP server" messages. nil means discard (a preview, or a test
	// that does not assert on notices).
	Stderr io.Writer
}

// Kind names which of the three targets a Target is, for the small number of decisions
// that legitimately differ by target (e.g. whether ${workspace} has a referent, whether
// a computed layer is even supplied). It is deliberately coarse — three values, not a
// policy vector — mirroring the confinement dial's own "presets, not a matrix" rule.
type Kind int

const (
	// KindJail is the in-jail boot render: a disposable home, a computed layer built
	// from the live tables, sidecars under the container workspace.
	KindJail Kind = iota
	// KindPreview is `yolo config render`: writes nothing outside its scratch dir; used
	// to show what a render would produce without touching a real home.
	KindPreview
	// KindHost is `yolo apply --host`: the invoking user's real home, no computed layer
	// (its values embed jail-absolute paths), every surface read-modify-written so the
	// agent's own keys survive (env-manager plan OQ-4, host-render-target.md §6.3).
	KindHost
)

// Jail builds the boot-render Target from resolved home + workspace paths and the boot
// stderr. The caller (internal/entrypoint) passes its already-resolved Env.Home /
// WorkspaceDir() — this package never imports entrypoint, so the values cross as plain
// strings.
func Jail(home, workspace string, stderr io.Writer) Target {
	return Target{Home: home, Workspace: workspace, Stderr: stderr}
}

// Preview builds a Target that writes only under dir — the `yolo config render` case,
// which must not touch any real file. Workspace is dir too, so a ${workspace} surface
// resolves to something inside the scratch area rather than a real path.
func Preview(dir string) Target {
	return Target{Home: dir, Workspace: dir, Stderr: nil}
}

// Host builds the host-render Target: the real home, no workspace referent (a
// ${workspace} surface is refused, not bound), notices to the given stderr.
func Host(home string, stderr io.Writer) Target {
	return Target{Home: home, Workspace: "", Stderr: stderr}
}

// KindOf reports which target this is, from its shape. Jail and Preview both have a
// Workspace; Host does not. Preview is distinguished by Home==Workspace. This keeps
// callers from having to thread a Kind field through every construction while the
// three constructors above set the shape.
func (t Target) KindOf() Kind {
	if t.Workspace == "" {
		return KindHost
	}
	if t.Home == t.Workspace {
		return KindPreview
	}
	return KindJail
}
