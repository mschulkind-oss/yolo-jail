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

import (
	"io"
	"path/filepath"
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

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

	// kind is the notch this target renders at, STATED by the constructor that built it
	// rather than derived from the fields above (see KindOf). Unexported because a Target
	// is only correct if a constructor chose its notch: a struct literal assembled outside
	// this package cannot claim one, and gets KindUnset — the guarded answer — instead of
	// whichever notch its shape happens to resemble.
	kind Kind
}

// Kind names which target a Target is, for the small number of decisions that legitimately
// differ by target (e.g. whether ${workspace} has a referent, whether a computed layer is
// even supplied). It is deliberately coarse — a handful of values, not a policy vector —
// mirroring the confinement dial's own "presets, not a matrix" rule.
//
// STATED, NOT INFERRED (plan §6b D2 / Q1). Kind used to be derived from a Target's shape —
// "no Workspace" meant host, "Home == Workspace" meant preview — which made the notch
// load-bearing on an ABSENCE: a `guest` target is a real home WITH a workspace and
// Home != Workspace, so it resolved to KindJail and inherited jail semantics with nothing
// recording that as a choice. Every value below is now set by a constructor, and every
// switch over them carries a case (or a default someone had to write) for each — so adding
// a notch is a question the compiler asks rather than one a struct's shape answers.
type Kind int

const (
	// KindUnset is the zero value, and it is deliberately NOT a notch: a Target nobody's
	// constructor built has not chosen one. This member is what keeps the enum's zero from
	// being a real level — with KindJail at iota 0, a bare `render.Target{}` would claim
	// the STRONGEST notch (every kind honored, autonomy on), which is D2's bug with the
	// safety inverted. Everything below treats it as the most restricted answer.
	KindUnset Kind = iota
	// KindJail is the in-jail boot render: a home yolo regenerates every boot, a computed
	// layer built from the live tables, sidecars under the container workspace.
	KindJail
	// KindGuest is the middle notch (env-manager plan Phase 7): a real home on the real
	// filesystem, confined by an LSM/Seatbelt profile rather than a container. It has NO
	// constructor yet — its shape (which home, which workspace referent) is Phase 7's to
	// state, and inventing one here would be guessing. The member exists anyway, and that
	// is the point of Q1: every switch on Kind now has to name it or fall to a default,
	// so the notch cannot be added by silently inheriting a branch it happens to land in.
	KindGuest
	// KindHost is `yolo apply --host`: the invoking user's real home, no computed layer
	// (its values embed jail-absolute paths), every surface read-modify-written so the
	// agent's own keys survive (env-manager plan OQ-4, host-render-target.md §6.3).
	KindHost
	// KindPreview is `yolo config render`: writes nothing outside its scratch dir; used
	// to show what a render would produce without touching a real home. Last because it is
	// not a point on the confinement dial at all — the three above are.
	KindPreview
)

// kindNames is where core's side of the config boundary writes a notch's name down, and it
// exists for the two jobs at the EDGES of the pipeline: turning a `confinement` value into a
// Kind on the way IN (KindForNotch), and labelling a notch in output on the way OUT (String).
// BETWEEN those edges nothing compares a name — a decision reads the Kind, and through it a
// Profile, a ModeSet or a FieldSet (plan §6c step 3). That is what "core reasons about
// primitives; only the boundary knows the names" means in code: this table is the boundary.
//
// It is deliberately NOT the config vocabulary — config.KnownConfinements is, and it stays
// there because parsing is its job (plan §6c step 3 keeps ResolveConfinement). The two must
// agree, which is asserted rather than assumed: notchnames_test.go pins every config value to
// a distinct selectable Kind and back, so a notch added to one and not the other fails a test
// instead of silently resolving to the strongest level.
//
// KindUnset and KindPreview carry names too, because both can reach OUTPUT (a message about a
// target nobody constructed, a preview's provenance label) even though neither is selectable.
var kindNames = map[Kind]string{
	KindUnset:   "unset",
	KindJail:    "jail",
	KindGuest:   "guest",
	KindHost:    "host",
	KindPreview: "preview",
}

// String is the notch's name, for OUTPUT and for KindForNotch's reverse lookup — never for a
// decision. A Kind with no entry prints its number rather than an empty string, so an
// unlabelled notch shows up in the message instead of leaving a blank in it.
func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// SelectableNotches is the dial: the Kinds a user can name in the `confinement` config key, in
// strongest-first order. KindPreview is absent because `yolo config render` is not a
// confinement level (see its doc), and KindUnset because it is the ABSENCE of a choice —
// admitting either would let a config select a notch with no enforcement story behind it.
func SelectableNotches() []Kind { return []Kind{KindJail, KindGuest, KindHost} }

// KindForNotch resolves a confinement notch's NAME to its Kind — the inbound half of the
// boundary, and the one call a caller holding a config value makes before it stops thinking
// in names. ok is false for anything that is not a selectable notch, INCLUDING "preview" and
// "unset": they have labels for output's sake, and letting a config name one would be the
// asymmetry ProfileFor argues against, in the selection direction.
//
// A caller that has already defaulted an absent/unknown value (config.ResolveConfinement does)
// will never see ok=false; one that has not must not treat the zero Kind as a notch — that is
// what KindUnset exists to prevent.
func KindForNotch(name string) (Kind, bool) {
	for _, k := range SelectableNotches() {
		if kindNames[k] == name {
			return k, true
		}
	}
	return KindUnset, false
}

// Jail builds the boot-render Target from resolved home + workspace paths and the boot
// stderr. The caller (internal/entrypoint) passes its already-resolved Env.Home /
// WorkspaceDir() — this package never imports entrypoint, so the values cross as plain
// strings.
func Jail(home, workspace string, stderr io.Writer) Target {
	return Target{Home: home, Workspace: workspace, Stderr: stderr, kind: KindJail}
}

// Preview builds a Target that writes only under dir — the `yolo config render` case,
// which must not touch any real file. Workspace is dir too, so a ${workspace} surface
// resolves to something inside the scratch area rather than a real path.
func Preview(dir string) Target {
	return Target{Home: dir, Workspace: dir, Stderr: nil, kind: KindPreview}
}

// Host builds the host-render Target: the real home, no workspace referent (a
// ${workspace} surface is refused, not bound), notices to the given stderr.
func Host(home string, stderr io.Writer) Target {
	return Target{Home: home, Workspace: "", Stderr: stderr, kind: KindHost}
}

// KindOf reports which notch this target renders at — the field its constructor set, not a
// re-derivation from its shape. Kept as an accessor rather than exporting the field so the
// notch can only be CHOSEN by a constructor in this package; every caller reads it the same
// way it always did.
//
// A Target with no constructor behind it reads KindUnset, which is neither jail nor host and
// is handled as the most restricted answer wherever it can reach (see Fields, SidecarDir).
func (t Target) KindOf() Kind { return t.kind }

// Profile is the confinement preset this target renders under, and therefore the single
// source of the §4.2 AgentAutonomy policy for every render path (plan §6c step 1). A caller
// that has a Target has the policy; it never picks a true/false for itself.
//
// A method rather than a field so the two halves of the notch cannot drift: Kind is stated
// once, by a constructor, and the preset follows from it through ProfileFor's one table.
func (t Target) Profile() Profile { return ProfileFor(t.KindOf()) }

// inferKindFromShape is the derivation KindOf USED to be, kept only so a test can assert the
// explicit field agrees with it for the three constructors that existed before Q1 — proving
// the refactor behavior-preserving rather than assuming it. It is not a fallback: the whole
// point of the explicit field is that a shape no longer decides a notch, and a guest target
// is exactly the case this function gets wrong (a real home with a workspace, so it says
// "jail").
func inferKindFromShape(t Target) Kind {
	if t.Workspace == "" {
		return KindHost
	}
	if t.Home == t.Workspace {
		return KindPreview
	}
	return KindJail
}

// hostProvenanceLeaf is the state-dir leaf holding host-render provenance records. Named
// for its CONTENT rather than mirroring the jail's "prism", because it will never hold the
// jail's other two sidecars: the host notch is pure RMW by resolved decision (OQ-4,
// host-render-target.md §6.3), so there is no last_render baseline and no capture overlay
// to keep. A dir called "host-prism" would promise a tree that does not exist.
const hostProvenanceLeaf = "host-provenance"

// SidecarDir is where the §5 capture sidecars (last_render + overlay) for this target
// live: under the target's workspace, in the gitignored .yolo/. EMPTY at the host target,
// and that is the honest answer rather than a gap — a host render is pure RMW, so it keeps
// no baseline and captures no edits, and there is no per-workspace referent to put them
// under anyway.
//
// Never relative. That is the load-bearing property: only the two kinds that HAVE a
// workspace join one, so the join always has an absolute root, and every other kind returns
// "" instead of a bare ".yolo/prism" that would resolve against whatever directory the
// process happens to be sitting in. `yolo apply --host` runs from anywhere.
//
// A SWITCH rather than the old `if KindOf() == KindHost` (Q1). While the notch was inferred
// the two spellings were the same statement — Workspace=="" was the DEFINITION of host — so
// "not host" implied "has a workspace" and the join was safe. With Kind stated, they part
// company: a Target whose kind nobody set has no workspace either, and the old `if` would
// have handed it the relative path this function exists to prevent. So the kinds that
// produce a sidecar tree are named, and everything else — guest until Phase 7 states where
// its sidecars live, and any unset target — gets "".
func (t Target) SidecarDir() string {
	switch t.KindOf() {
	case KindJail, KindPreview:
		return filepath.Join(t.Workspace, ".yolo", "prism")
	default:
		return ""
	}
}

// ProvenanceDir is where THIS target's per-key "which layer set this key" records go. It
// is the one sidecar every constructed target keeps:
//
//   - jail / preview: beside the other sidecars, under <workspace>/.yolo/prism/.
//   - host: under the STATE dir of the home being rendered into,
//     <home>/.local/share/yolo-jail/host-provenance/.
//   - guest / unset: "" — nowhere, which the caller reads as "nothing to record". Guest is
//     a real home with a workspace, so BOTH answers above are mechanically available and
//     that is precisely why it must not be defaulted into one: which of the two a guest
//     keeps is Phase 7's decision, and inheriting the jail's by falling through an `if`
//     is the D2 bug this file's explicit Kind exists to make impossible.
//
// Why the state dir at the host, and not the two alternatives:
//
//   - NOT the workspace. `render.Host()` leaves Workspace empty on purpose, so there is no
//     workspace to put it under; joining
//     anyway yields a RELATIVE ".yolo/prism" that scatters records into whatever directory
//     `yolo apply --host` was invoked from. A host render is user-scoped — what it writes
//     is a function of the pack plus the user config, never of a workspace — so keying its
//     bookkeeping to a workspace would be wrong even if one were available.
//   - NOT beside the rendered file (~/.claude/.yolo-provenance/…). Discoverable, but it
//     puts yolo's bookkeeping inside the user's own config directory, which is the one
//     thing the host notch is most careful not to do: a real $HOME is not a jail home, and
//     a stray dir in ~/.claude is indistinguishable to the agent (and to the user) from
//     config. The state dir is already where "what did yolo do to this home?" lives — the
//     host-skills ownership manifest and the apply archive are both there.
//
// Derived from t.Home rather than paths.GlobalStorage(): the target has already resolved
// which home it is writing into, and re-deriving it from the process $HOME would send the
// record to the invoking user's real state dir whenever the two differ — which is every
// test with a t.TempDir() home. Empty when the target has no home to key on (an
// unusable Target); the caller treats that as "nowhere to record".
func (t Target) ProvenanceDir() string {
	switch t.KindOf() {
	case KindJail, KindPreview:
		return t.SidecarDir()
	case KindHost:
		if t.Home == "" {
			return ""
		}
		return filepath.Join(paths.GlobalStorageUnder(t.Home), hostProvenanceLeaf)
	default:
		return ""
	}
}

// ProvenancePath is the provenance record for one surface under this target, or "" when
// the target has nowhere to keep one. The file name is the same agent-name.provenance the
// jail sidecar tree uses, so one reader serves both notches.
func (t Target) ProvenancePath(agent, name string) string {
	dir := t.ProvenanceDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, agent+"-"+name+".provenance")
}
