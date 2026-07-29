package packdecl

// kinds.go is the CLOSED, core-owned vocabulary of contribution kinds — the
// registry the reform (docs/design/pack-declaration-reform.md §3.1) is built on.
//
// A pack does not DEFINE a kind; it SELECTS one, exactly as a surface selects a
// mode from knownModes or a codec from knownCodecs. The set is closed because
// core has to know each kind's FOOTPRINT (what it claims on the environment, and
// how two claims on one target combine) to check it — §3.2. A kind core cannot
// reason about is a kind whose collisions it cannot catch, which is the whole
// good-citizen guarantee (§1.4) lost. So an unknown kind is a loud load error,
// never a silent skip.
//
// Phase 0 (the plan): this file lands the registry + footprint descriptors +
// tests, and nothing reads it yet. The Manifest is untouched; contributes[]
// parsing arrives in a later phase. Keeping the vocabulary here — dependency-free
// on the rest of the repo, beside the Manifest it will eventually type — is the
// same placement rule packdecl already follows (see the package doc): both the
// host CLI and the in-jail entrypoint read it, so it may not import either.

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is one contribution kind. The set is closed (KnownKinds); a value outside
// it is a validation error, not a fallback.
type Kind string

// The closed kind set (§3.2). Each names a category of environmental effect,
// never a tool — `config`, not `claude/settings` (§4.1: core knows the domain,
// not the tool).
const (
	// KindProgram: a program the jail should have on PATH (with a lazy launcher
	// in ~/.yolo-shims/). Sole-owned by bin name.
	KindProgram Kind = "program"
	// KindSkills: a skills tree merged into an agent's skills dir. Multiple packs
	// into one dir is the feature, not a conflict (ordered merge).
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
	// KindConfigOverlay: a contribution to a config surface OWNED by another pack
	// (§3.2, OQ2). Ordered after the owner (later-wins), with per-key provenance
	// recorded so an override of the owner's key is legible rather than silent.
	KindConfigOverlay Kind = "config-overlay"
	// KindState: a home-relative subtree the pack writes at runtime, at a scope
	// (workspace | machine). Machine scope leaks across workspaces by design and
	// is review-worthy (§3.1). Overlapping subtrees at DIFFERENT scopes conflict.
	KindState Kind = "state"
	// KindReadsHost: a host-home file mounted read-only into the jail — the
	// credential boundary (§3.1). Many packs may read one file; no combine.
	KindReadsHost Kind = "reads-host"
	// KindLaunch: flags injected after a binary at launch. Sole-owned by bin name.
	KindLaunch Kind = "launch"
	// KindHook: a named imperative capability from core's closed hook set
	// (KnownHooks). Conflict resolution is per-hook, not generic.
	KindHook Kind = "hook"
)

// Combine names how two claims on the SAME target resolve — the conflict-rule
// column of §3.2, read as one rule (§3.6: every file has exactly one writer).
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
// effect: what it claims and how claims combine. It is the data the §3.2
// conflict table and `yolo pack explain --footprint` are computed from. The
// PER-CLAIM facts (which concrete path, whether THIS state claim is machine-scope
// and thus review-worthy) are derived later from an actual contribution; this
// struct carries only what is true of the kind regardless of instance.
type Footprint struct {
	// Kind is the kind this describes.
	Kind Kind
	// Combine is how two claims on one target resolve.
	Combine Combine
	// Claims is a one-line, human description of what the kind claims on the
	// environment — the "Claims" column of §3.2. Shown by --footprint.
	Claims string
	// MayBeReviewWorthy is true for kinds that CAN produce a claim needing review
	// (machine-scope state, a host read, an installer program). Whether a given
	// CLAIM is review-worthy is decided per-instance in Phase 1; this only marks
	// the kinds where that check applies at all, so --footprint knows where to look.
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
	KindLaunch: {
		Kind: KindLaunch, Combine: CombineExclusive,
		Claims: "launch flags for a binary",
	},
	KindHook: {
		Kind: KindHook, Combine: CombinePerHook,
		Claims: "a named imperative capability from core's closed hook set",
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
