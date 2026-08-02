package packdecl

// kinds.go is the CLOSED, core-owned vocabulary of contribution kinds — the
// registry the manifest is built on (docs/design/pack-system.md §3).
//
// A pack does not DEFINE a kind; it SELECTS one, exactly as a surface selects a
// mode from knownModes or a codec from knownCodecs. The set is closed because
// core has to know each kind's FOOTPRINT (what it claims on the environment, and
// how two claims on one target combine) to check it (pack-system.md §3). A kind
// core cannot reason about is a kind whose collisions it cannot catch, which is
// the whole good-citizen guarantee lost. So an unknown kind is a loud load error,
// never a silent skip.
//
// The vocabulary lives here — dependency-free on the rest of the repo, beside the
// Manifest it types — following the placement rule packdecl already follows (see
// the package doc): both the host CLI and the in-jail entrypoint read it, so it
// may not import either.

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is one contribution kind. The set is closed (KnownKinds); a value outside
// it is a validation error, not a fallback.
type Kind string

// The closed kind set (pack-system.md §3). Each names a category of environmental
// effect, never a tool — `config`, not `claude/settings` (§0 principle 2: core
// knows the domain, not the tool).
const (
	// KindProgram: a program the jail should have on PATH (with a lazy launcher
	// in ~/.yolo-shims/). Sole-owned by bin name.
	KindProgram Kind = "program"
	// KindSkills: a skills tree merged into an agent's skills dir. Multiple packs
	// into one dir is the feature, not a conflict (ordered merge).
	//
	// It also carries a WRAPPED AGENT PLUGIN — a subtree with its own plugin manifest,
	// recognized from the filesystem rather than declared (internal/pluginpack). That has no
	// kind of its own deliberately: a plugin is skills plus components yolo does not model,
	// and a `plugin` kind would be a second name for the same destination whose combine rule
	// had to be kept in sync with this one. What it needs instead is the exclusivity a
	// per-plugin-name claim gets in the footprint, since delivery is one directory per name.
	KindSkills Kind = "skills"
	// KindBriefing: briefing prose concatenated at a path (the AGENTS.md/CLAUDE.md
	// slot). Multiple packs concatenate in order.
	KindBriefing Kind = "briefing"
	// KindFiles: an opaque file/dir tree the pack owns exclusively at a path.
	// Two packs on one path would shadow — an error.
	KindFiles Kind = "files"
	// KindConfig: a composed config surface the pack owns (path + codec + layers).
	// Sole-owned by surface identity; a second writer must be KindConfigOverlay.
	KindConfig Kind = "config"
	// KindConfigOverlay: a contribution to a config surface OWNED by another pack.
	// Ordered after the owner (later-wins), with per-key provenance recorded so an
	// override of the owner's key is legible rather than silent.
	KindConfigOverlay Kind = "config-overlay"
	// KindState: a home-relative subtree the pack writes at runtime, at a scope
	// (workspace | machine). Machine scope leaks across workspaces by design and
	// is review-worthy. Overlapping subtrees at DIFFERENT scopes conflict.
	KindState Kind = "state"
	// KindReadsHost: a host-home file mounted read-only into the jail — the
	// credential boundary. Many packs may read one file; no combine.
	KindReadsHost Kind = "reads-host"
	// KindMount: a host-home dir (or file) mounted read-only into the jail at a
	// /ctx destination. Like reads-host but the source may be a whole directory and
	// the destination is an arbitrary /ctx path (reads-host feeds a config surface
	// by basename; mount just makes the tree visible). Reads the host home, so it is
	// origin-gated exactly like reads-host — a fetched pack is refused. Many packs
	// may mount; no combine (each is an independent read).
	KindMount Kind = "mount"
	// KindEnv: static environment variables set in the jail. Values are literal
	// strings only (no interpolation, no host reads), so it is NOT origin-gated. A
	// key claimed by two packs collides.
	KindEnv Kind = "env"
	// KindLaunch: flags injected after a binary at launch. Sole-owned by bin name.
	KindLaunch Kind = "launch"
	// KindHook: a named imperative capability from core's closed hook set
	// (KnownHooks). Conflict resolution is per-hook, not generic.
	KindHook Kind = "hook"
	// KindAutonomy: the pack's two permission postures (§4.2 / env-manager plan
	// Phase 9). The confinement notch's AgentAutonomy policy selects one; its config
	// patch folds into the managed layer of the pack's OWN surfaces and its launch
	// flags merge into the binary's. Sole-owned by the pack (one autonomy declaration
	// per pack); it patches surfaces the same pack owns, so it never collides across
	// packs the way a second config writer would.
	KindAutonomy Kind = "autonomy"
)

// Combine names how two claims on the SAME target resolve — the conflict-rule
// column of the footprint table, read as one rule (pack-system.md §4: every
// file has exactly one writer).
// The CombineExclusive kinds are files a pack owns outright (a second claimant is
// a collision); the others are files no pack writes directly — a neutral owner
// combines the inputs.
type Combine int

const (
	// CombineExclusive: one owner per target; a second claim is an ERROR. The
	// sole-ownership half of the one-writer rule (program, files, config, launch).
	CombineExclusive Combine = iota
	// CombineMerge: an ordered merge into one target dir; multiple packs are fine
	// and the feature (skills).
	CombineMerge
	// CombineConcat: ordered concatenation at one path; multiple packs fine
	// (briefing).
	CombineConcat
	// CombineOverlay: ordered after the target's owner, later-wins, with per-key
	// provenance required so an override is reported not silent (config-overlay).
	CombineOverlay
	// CombineShared: many claimants on one target, no combine and no conflict —
	// each is an independent read (reads-host).
	CombineShared
	// CombineScoped: exclusive PER SCOPE; overlapping subtrees at different scopes
	// are an error, the same subtree at one scope is fine (state).
	CombineScoped
	// CombinePerHook: resolution is the hook's own concern, not a generic rule
	// (hook).
	CombinePerHook
)

// Footprint is the static, core-owned description of a kind's environmental
// effect: what it claims and how claims combine. It is the data the footprint
// conflict table and `yolo pack footprint` are computed from. The
// PER-CLAIM facts (which concrete path, whether THIS state claim is machine-scope
// and thus review-worthy) are derived later from an actual contribution; this
// struct carries only what is true of the kind regardless of instance.
type Footprint struct {
	// Kind is the kind this describes.
	Kind Kind
	// Combine is how two claims on one target resolve.
	Combine Combine
	// Claims is a one-line, human description of what the kind claims on the
	// environment — the "Claims" column of the footprint table. Shown by
	// `yolo pack footprint`.
	Claims string
	// MayBeReviewWorthy is true for kinds that CAN produce a claim needing review
	// (machine-scope state, a host read, an installer program). Whether a given
	// CLAIM is review-worthy is decided per-instance from the contribution; this
	// only marks the kinds where that check applies at all, so `yolo pack
	// footprint` knows where to look.
	MayBeReviewWorthy bool
}

// footprints is the registry: every known kind, its combine rule, and its claim
// description. Closed on purpose (see the file doc). The map key is the authority
// for KnownKinds — there is deliberately no second list to drift.
var footprints = map[Kind]Footprint{
	KindProgram: {
		Kind: KindProgram, Combine: CombineExclusive, MayBeReviewWorthy: true,
		Claims: "a name on PATH and a launcher in ~/.yolo-shims/",
	},
	KindSkills: {
		Kind: KindSkills, Combine: CombineMerge,
		Claims: "a skills tree merged into an agent's skills dir (built-in < pack < user)",
	},
	KindBriefing: {
		Kind: KindBriefing, Combine: CombineConcat,
		Claims: "briefing prose concatenated at a path",
	},
	KindFiles: {
		Kind: KindFiles, Combine: CombineExclusive,
		Claims: "exclusive ownership of a file/dir tree at a path",
	},
	KindConfig: {
		Kind: KindConfig, Combine: CombineExclusive,
		Claims: "a composed config surface (path + codec + layers)",
	},
	KindConfigOverlay: {
		Kind: KindConfigOverlay, Combine: CombineOverlay,
		Claims: "a contribution to a config surface owned by another pack",
	},
	KindState: {
		Kind: KindState, Combine: CombineScoped, MayBeReviewWorthy: true,
		Claims: "a writable home subtree at a scope (workspace | machine)",
	},
	KindReadsHost: {
		Kind: KindReadsHost, Combine: CombineShared, MayBeReviewWorthy: true,
		Claims: "a host-home file mounted read-only (the credential boundary)",
	},
	KindMount: {
		Kind: KindMount, Combine: CombineShared, MayBeReviewWorthy: true,
		Claims: "a host-home dir/file mounted read-only at a /ctx path",
	},
	KindEnv: {
		Kind: KindEnv, Combine: CombineMerge,
		Claims: "static environment variables set in the jail",
	},
	KindLaunch: {
		Kind: KindLaunch, Combine: CombineExclusive,
		Claims: "launch flags for a binary",
	},
	KindHook: {
		Kind: KindHook, Combine: CombinePerHook,
		Claims: "a named imperative capability from core's closed hook set",
	},
	KindAutonomy: {
		Kind: KindAutonomy, Combine: CombineExclusive,
		Claims: "the agent's autonomous/guarded permission postures (notch-selected)",
	},
}

// FootprintOf returns the footprint descriptor for a kind, and ok=false for an
// unknown kind (the caller reports it — see ValidateKind for the standard error).
func FootprintOf(k Kind) (Footprint, bool) {
	fp, ok := footprints[k]
	return fp, ok
}

// KnownKind reports whether k is in the closed set.
func KnownKind(k Kind) bool {
	_, ok := footprints[k]
	return ok
}

// KnownKinds returns the closed kind set, sorted, for error messages and tests.
func KnownKinds() []Kind {
	out := make([]Kind, 0, len(footprints))
	for k := range footprints {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ValidateKind reports the standard problem string for an unknown kind, or "" if
// k is known — matching the "unknown X (expected …)" shape knownModes/knownCodecs
// use, so a pack author gets one consistent diagnostic across the manifest.
func ValidateKind(k Kind) string {
	if KnownKind(k) {
		return ""
	}
	names := make([]string, len(footprints))
	for i, kk := range KnownKinds() {
		names[i] = string(kk)
	}
	return fmt.Sprintf("unknown kind %q (expected one of %s)", k, strings.Join(names, ", "))
}
