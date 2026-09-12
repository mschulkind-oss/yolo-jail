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
	"os"
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

	// ownership is the user's DECLARED host-management contract (config `host_management`,
	// docs/design/config-ownership-and-promotion.md §4), set by render.Host and meaningless
	// at every other notch. It is the second half of this target's notch: Modes() is a
	// function of the PAIR, because "which mechanisms does the host run?" has three answers
	// and the user picks one.
	//
	// A FIELD, not a Modes(ownership) parameter, and not a fifth Kind. The parameter is the
	// shape the compiler would force, and it is the wrong one: ownership would become a
	// call-site argument any site could fabricate, which is the per-call-site answer
	// TestModesIsAFunctionOfTheNotchAlone exists to prevent. A fifth Kind is worse — every
	// switch over Kind in this file would need a case for it, and the one that matters
	// (ProvenanceDir) would fall into its `default:` and return "", leaving an owned host
	// with no provenance record at all, which is what §6.3's adoption story reads.
	// Unexported for the reason `kind` is: a struct literal assembled outside this package
	// cannot claim a contract, and gets OwnershipUnstated — the guarded answer.
	ownership HostOwnership
}

// HostOwnership is the declared ownership contract for the files yolo renders into a real
// home — config's `host_management` key, at the render boundary
// (docs/design/config-ownership-and-promotion.md §4.1). It answers "who owns this file",
// which is a DIFFERENT axis from Kind's "how is this target confined" (P2): collapsing the
// two is what made "the host is different" look arbitrary, since the host is differently
// CONFINED, which says nothing about who owns its files.
//
// It is meaningful at KindHost alone. Every other notch renders a home yolo provisions, so
// the question does not arise there and the census key drops it (see Target.censusNotch).
type HostOwnership int

const (
	// OwnershipUnstated is the zero value, and — like KindUnset — it is deliberately NOT a
	// contract. A Target whose constructor was given no ownership has not been told one, and
	// the census answers `undecided` for it: nothing runs, nothing records.
	//
	// It is NOT the "unset key" state. config.HostManagementMode resolves an ABSENT
	// `host_management` to `assert` (OQ-CO2, the default that carries the whole migration)
	// and an UNREADABLE user config to `none`, so it never hands this value out. Reaching it
	// here means a caller built a host Target without resolving the contract at all, which
	// is the one case that must write nothing.
	OwnershipUnstated HostOwnership = iota
	// OwnershipNone: the user owns their files entirely. The host notch renders nothing.
	OwnershipNone
	// OwnershipAssert: shared ownership — yolo owns the keys its packs declare and the user
	// owns the rest. The host notch is pure read-modify-write. Today's shipped behavior, and
	// what an absent `host_management` resolves to.
	OwnershipAssert
	// OwnershipOwn: yolo owns the file; it is derived output. The host notch composes
	// whole-file and captures edits — the jail's own mechanism, at a real home.
	OwnershipOwn
)

// ownershipNames is the ownership half of the config boundary kindNames is the notch half
// of: config.KnownHostManagements holds the vocabulary and parses it, and this table turns a
// name into the primitive on the way in (HostOwnershipFor) and back into a label on the way
// out (String). Between those edges nothing compares a name.
//
// notchnames_test.go pins the two together, so a value added to one and not the other fails
// a test rather than silently resolving to the guarded answer.
var ownershipNames = map[HostOwnership]string{
	OwnershipUnstated: "unstated",
	OwnershipNone:     "none",
	OwnershipAssert:   "assert",
	OwnershipOwn:      "own",
}

// String is the contract's name, for OUTPUT and for HostOwnershipFor's reverse lookup —
// never for a decision. An unlabelled value prints its number rather than a blank.
func (o HostOwnership) String() string {
	if n, ok := ownershipNames[o]; ok {
		return n
	}
	return "HostOwnership(" + strconv.Itoa(int(o)) + ")"
}

// DeclarableOwnerships is the set a user can write in `host_management`, in the order the
// three are explained — least yolo involvement first. OwnershipUnstated is absent because it
// is the ABSENCE of a declaration, not a value anyone may select.
func DeclarableOwnerships() []HostOwnership {
	return []HostOwnership{OwnershipNone, OwnershipAssert, OwnershipOwn}
}

// HostOwnershipFor resolves a `host_management` VALUE to its primitive — the inbound half of
// the boundary, and the one call a caller holding a config value makes before it stops
// thinking in names. ok is false for anything that is not a declarable contract, including
// "unstated": a caller that has not resolved one must not be handed a notch that writes.
func HostOwnershipFor(name string) (HostOwnership, bool) {
	for _, o := range DeclarableOwnerships() {
		if ownershipNames[o] == name {
			return o, true
		}
	}
	return OwnershipUnstated, false
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
	// KindHost is `yolo apply --at host`: the invoking user's real home, no computed layer
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
//
// ownership is the user's DECLARED `host_management` contract, and it is a PARAMETER
// because there is nowhere else it could honestly come from: this package must not read the
// user's config (it is the renderer, not the boundary), and inferring it from the home's
// shape is exactly the P1 violation the key exists to end. A caller that has not resolved
// one passes OwnershipUnstated and gets a target that renders nothing — see that constant.
func Host(home string, stderr io.Writer, ownership HostOwnership) Target {
	return Target{Home: home, Workspace: "", Stderr: stderr, kind: KindHost, ownership: ownership}
}

// KindOf reports which notch this target renders at — the field its constructor set, not a
// re-derivation from its shape. Kept as an accessor rather than exporting the field so the
// notch can only be CHOSEN by a constructor in this package; every caller reads it the same
// way it always did.
//
// A Target with no constructor behind it reads KindUnset, which is neither jail nor host and
// is handled as the most restricted answer wherever it can reach (see Fields, SidecarDir).
func (t Target) KindOf() Kind { return t.kind }

// Ownership reports the declared host-management contract this target renders under — the
// field its constructor was given. OwnershipUnstated at every notch but the host, where the
// question does not arise, and at a host target nobody resolved a contract for.
//
// An accessor rather than an exported field, for KindOf's reason: the contract can only be
// STATED, by a constructor, from a value the config boundary resolved.
func (t Target) Ownership() HostOwnership { return t.ownership }

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
// for its CONTENT rather than mirroring the jail's "prism", and that naming is now
// load-bearing rather than merely tidy: since `own`, the host DOES keep capture sidecars —
// in a directory of their own (hostCaptureLeaf) — and a dir called "host-prism" would have
// had to hold both or lie about one.
//
// IT DOES NOT MOVE, and the two dirs are separate because they have two LIFETIMES
// (config-ownership-and-promotion.md §6.2). Provenance is per-key attribution, written at
// EVERY host apply including under `assert`, and it is what `yolo host apply --revert`
// consumes; capture is `own`-only state a host-side `yolo config reset` is entitled to
// delete. Folding the record into the capture store would make reverting an `assert` home
// depend on a directory only `own` ever creates.
const hostProvenanceLeaf = "host-provenance"

// hostCaptureLeaf is the state-dir leaf holding the host notch's CAPTURE sidecars under
// `host_management: own` — the same three files a jail keeps under <workspace>/.yolo/prism/,
// with the same names, because they are what `stateful` composition needs and `own` is that
// composition at the host notch (config-ownership-and-promotion.md §6.2): the baseline a
// capture diffs against (last_render), the captured edits themselves (overlay.json), and the
// recorded selection (selection.json).
//
// Under the STATE dir rather than the workspace, which is what answers the privacy ruling
// that refuses host capture under the other two values: the jail's overlay lives in
// <workspace>/.yolo/prism/, which crosses into a container and plausibly into git, and a
// captured credential there is a leak. This store never crosses a boundary.
const hostCaptureLeaf = "host-capture"

// SidecarDir is where the capture sidecars (last_render, overlay, selection) for this
// target live, and it is the ONE definition of that directory — the writer (the boot and
// host renders) and every reader (the `yolo config` verbs) ask it here rather than joining
// the path themselves. The precedent is ProvenanceDir, and the hazard it avoids is the one
// the CLI's own prism* twins already are: two hand-copied path builders that agree only by
// inspection.
//
// Three answers, and the third is the one `own` added:
//
//   - jail / preview: <workspace>/.yolo/prism/, gitignored, per workspace.
//   - host under `own`: <home>/.local/share/yolo-jail/host-capture/. Whole-file composition
//     at a real home needs exactly the state a jail's does, so the host keeps it — beside
//     the provenance record, never inside it (see hostCaptureLeaf and hostProvenanceLeaf).
//   - everything else, host included: "". Under `assert` the host render is pure
//     read-modify-write, which keeps no baseline and captures no edits; under `none` it
//     writes nothing at all; guest has not stated where its sidecars live, and an unset
//     target is not a notch. "" is the honest answer in each case, and a caller reads it as
//     "this target keeps no capture state".
//
// Never relative. That is the load-bearing property: only the kinds that HAVE a root to join
// — a workspace, or a home — join one, so the join always has an absolute base, and every
// other kind returns "" instead of a bare ".yolo/prism" that would resolve against whatever
// directory the process happens to be sitting in. `yolo host apply` runs from anywhere.
//
// A SWITCH rather than the old `if KindOf() == KindHost` (Q1). While the notch was inferred
// the two spellings were the same statement — Workspace=="" was the DEFINITION of host — so
// "not host" implied "has a workspace" and the join was safe. With Kind stated, they part
// company: a Target whose kind nobody set has no workspace either, and the old `if` would
// have handed it the relative path this function exists to prevent.
func (t Target) SidecarDir() string {
	switch t.KindOf() {
	case KindJail, KindPreview:
		return filepath.Join(t.Workspace, ".yolo", "prism")
	case KindHost:
		// The CONTRACT decides, not the notch: `assert` and `none` keep no capture state, so
		// they get the same "" a guest does, and the answer changes the moment the user
		// declares `own` rather than when some caller decides the host is special today.
		if t.ownership != OwnershipOwn || t.Home == "" {
			return ""
		}
		return filepath.Join(paths.GlobalStorageUnder(t.Home), hostCaptureLeaf)
	default:
		return ""
	}
}

// SidecarDirMode is the permission the capture-sidecar DIRECTORY is created with.
//
// ⚠ 0600 IS A FILE MODE AND WOULD MAKE THE STORE UNUSABLE. The execute bit on a directory is
// the right to resolve a name inside it, so at 0600 even the owner gets EACCES opening any
// file in the store while `ls` still lists the names (measured 2026-09-12 as an unprivileged
// uid: `open` failed with EACCES at 0600 and succeeded at 0700, `listdir` worked at both).
// The host store is 0700, holding 0600 files.
//
// The jail's tree stays 0755/0644, which is not an oversight: it lives in the workspace, is
// read by the human on the host side, and is the notch whose capture mode
// config-ownership-and-promotion.md §6.2 lists for the roadmap rather than changes here.
func (t Target) SidecarDirMode() os.FileMode {
	if t.KindOf() == KindHost {
		return 0o700
	}
	return 0o755
}

// SidecarFileMode is the permission a capture sidecar FILE is written with. 0600 at the
// host, because the overlay holds whatever the user's real config held that yolo's layers do
// not — a credential included — and it sits in a real home other accounts may read.
func (t Target) SidecarFileMode() os.FileMode {
	if t.KindOf() == KindHost {
		return 0o600
	}
	return 0o644
}

// OverlayPath, LastRenderPath and SelectionPath are the three capture sidecars for one
// surface under this target, or "" when the target keeps none. They are here, beside
// ProvenancePath, so the file-naming convention has ONE definition across both notches
// rather than the hand-copied joins it had at six call sites.
func (t Target) OverlayPath(agent, name string) string {
	return t.sidecarPath(agent, name, ".overlay.json")
}

// LastRenderPath is the baseline sidecar: the exact surface-codec bytes yolo wrote last.
func (t Target) LastRenderPath(agent, name string) string {
	return t.sidecarPath(agent, name, ".last_render")
}

// SelectionPath is the selection record: the values yolo's SELECTION mechanism last wrote.
func (t Target) SelectionPath(agent, name string) string {
	return t.sidecarPath(agent, name, ".selection.json")
}

// sidecarPath joins one capture-sidecar leaf onto this target's store, or returns "" when
// there is no store — so a caller that forgets to check gets a path it cannot write rather
// than a relative one it can.
func (t Target) sidecarPath(agent, name, suffix string) string {
	dir := t.SidecarDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, agent+"-"+name+suffix)
}

// ProvenanceDir is where THIS target's per-key "which layer set this key" records go. It is
// the one sidecar every constructed target keeps — including the two host contracts that
// keep no capture state at all, which is why it is resolved separately from SidecarDir
// rather than as a file inside it:
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
//     `yolo host apply` was invoked from. A host render is user-scoped — what it writes
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
