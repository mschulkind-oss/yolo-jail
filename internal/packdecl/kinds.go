package packdecl

// kinds.go is the CLOSED, core-owned vocabulary of contribution kinds — the
// registry the manifest is built on (docs/design/pack-system.md §3).
//
// A pack does not DEFINE a kind; it SELECTS one, exactly as a surface selects a
// mode from knownModes or a codec from knownCodecs. The set is closed because
// core has to know each kind's FOOTPRINT (what it claims on the environment, and
// how two claims on one target combine) to check it (pack-system.md §3). A kind
// core cannot reason about is a kind whose collisions it cannot catch, which is
// the whole good-citizen guarantee lost. So at AUTHORING time an unknown kind is
// a loud load error, never a silent skip — while across the VERSION BOUNDARY
// (DecodeTolerant, the in-jail read) it is skipped AND reported, because a kind
// only a newer build knows is skew, not structure, and refusing it failed the
// boot (loophole-packaging §3.3a: an author must hear; a jail must boot).
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
	// KindProgram: a program the jail should have on PATH (with a lazy installer/updater
	// in ~/.yolo/bin/launch/, which is ordered SECOND on PATH since B2 — ahead of the
	// install prefixes, so the launcher mediates every invocation and not just the first.
	// No launcher is written for a name the image already provides). Sole-owned by bin name.
	KindProgram Kind = "program"
	// KindRequires: a binary the pack needs to EXIST, which it does not install.
	//
	// The distinction from KindProgram is presence-vs-install, and it is not cosmetic.
	// `program` means "yolo installs this, and owns a launcher path for it"; `requires`
	// means "this must be on PATH, from wherever your environment gets it". A pack wanting
	// a tool the image already bakes (fd, fzf) or the user already has (jq, psql) had only
	// `program`, so it either lied — declaring an npm install for a baked binary — or said
	// nothing and lost the host-notch hints entirely.
	//
	// In a jail it asserts presence at boot and reports a missing bin BY NAME; it generates
	// NO launcher, so nothing it declares can shadow anything. At the host it feeds
	// check-deps / host apply through exactly the same install_hints plumbing `program`
	// does — which is its whole host-side purpose, and what lets a CONTENT-only pack carry
	// remedies.
	//
	// NOT CombineExclusive, unlike program: many packs may require one binary (that is the
	// normal case for a common tool), and no pack owns a path for it. program is exclusive
	// precisely because it owns a launcher filename.
	KindRequires Kind = "requires"
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
	// KindBriefing: briefing prose concatenated at a path (the briefing-file slot,
	// wherever the pack puts it). Multiple packs concatenate in order.
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
	// KindProfile: a NAMED SELECTION OVER A PROVIDER
	// (docs/reference/providers.md §5.2) — `name` is the selector `-p` sets and
	// `provider` is what it selects. THAT IS THE WHOLE BODY, since OQ-PT8 shrank the kind
	// (the sibling doc's §5.4 note is the ruling): everything a `kind: "profile"` used to
	// carry besides — a config patch, launch flags, a static env map — was never a profile
	// at all, but a contribution GATED ON a profile name, and it now lives that way in the
	// kinds that own those channels, under the `profile` modifier. packs/claude's
	// `bedrock` is the decomposition's one worked case, table for table.
	//
	// NOT origin-gated, like autonomy: it names an entry of the composed `providers`
	// table — a reference into the user's config, not a read of it — so it makes no
	// host-access claim; the credential behind that name is a variable the user hydrates,
	// and whether it is hydrated is the launch pre-flight's question (parent §6.2), not an
	// approval `pack install` can grant.
	//
	// Exclusive by (pack, name) — the claim target carries BOTH, deliberately, because
	// unlike a provider name a profile name is NOT globally owned: `bedrock` in packs/claude
	// and `bedrock` in packs/pi are unrelated declarations that happen to share a selector
	// value, and neither can touch the other's surfaces (§3.4). Within one pack the same
	// name twice is a load error (validateProfileNames), which is what makes the key
	// exclusive at all.
	KindProfile Kind = "profile"
	// KindProvider: a NAMED PROVIDER'S SERVICE FACTS — the endpoints that speak each
	// wire protocol, and the model aliases an agent can ask for by name
	// (profiles-as-pack-variants.md §4.1 as ruled, OQ-12). The pack composes them INTO
	// the user's `providers` config table (pack defaults < user overrides, per field),
	// and the composed table feeds the unchanged YOLO_PROVIDERS → ctx.providers chain
	// the three derives already read; the pack authorship a user never has to repeat is
	// the point, so overrides — not authoring — are what user config is for.
	//
	// THE CREDENTIAL IS NOT A DECLARATION, and the schema cannot express one: the only
	// key-shaped field is APIKeyEnvName, which names a variable the USER hydrates
	// (env_sources or the invoking environment). Endpoints are facts about the service —
	// z.ai's URLs are the same for everyone — which is exactly what a shareable pack may
	// carry; a key is a fact about this machine, which is not.
	//
	// Exclusive by provider NAME, and the name is the whole identity: it is the key the
	// entry lands under in the composed table, what a profile's `provider` names, and
	// what the derives emit as the provider/model id. Two packs shipping one
	// name would each be supplying "the" zai, so the second is a collision — the same
	// name-keyed exclusivity `program` has per bin, and unlike `program` nothing is
	// installed, so there is no filesystem the collision could otherwise surface in.
	// A pack shipping TWO providers is ordinary: the exclusivity is per NAME, not per
	// pack.
	KindProvider Kind = "provider"
	// KindLoophole: a loophole MODULE the pack ships — a directory holding a
	// `manifest.jsonc`, named by `from` (loophole-packaging.md §3).
	//
	// It POINTS AT the module rather than inlining the manifest, so the on-disk shape
	// is the one a bundled or user loophole already has: one loader reads all four
	// sources, and an author can develop a loophole standalone and then drop it into a
	// pack unchanged.
	//
	// THE FIRST KIND WHOSE CLAIM IS HOST CODE EXECUTION rather than a host read, which
	// is why its claims cannot be computed here. `from` names a directory; the daemon
	// argv, the intercepts, the binds and the devices live in a file OUTSIDE pack.json,
	// and this package has zero internal imports by design (see the file doc) and no
	// pack root to resolve a relative path against. So the claim producer is
	// packload.Pack.LoopholeHostAccessClaims, reading through internal/loopholedecl —
	// the same layer, and for the same reason, as PluginHostAccessClaims
	// (loophole-packaging.md §3.3). What this package owns is the DECLARATION: `from`
	// is required and traversal-guarded like every other path-bearing field.
	//
	// Sole-owned by loophole NAME (the module directory's basename, which loadManifest
	// already forces the manifest's own `name` to equal). Exclusivity is per NAME, not
	// per pack, so a pack shipping three loopholes is ordinary — the rule `program` has
	// per `bin`. A shadowed loophole name is a daemon nobody audited running under a
	// name the user trusts.
	KindLoophole Kind = "loophole"

	// KindBlockedTool refuses a tool inside the jail, printing a message and an
	// alternative instead of running it.
	//
	// A PACK CONCERN, not a core one, since 2026-09-04. Core used to block `grep -r`
	// and `find` by DEFAULT — a default that silently assumed the image bakes `rg` and
	// `fd`, which is true of the container backends and false of macos-user, where
	// nothing is baked and a blocked tool's suggestion named a binary that did not
	// exist. Moving the list into a pack makes the assumption explicit: the pack that
	// blocks a tool is the pack that can say what replaces it, and selecting it is the
	// opt-in.
	KindBlockedTool Kind = "blocked-tool"
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
	KindBlockedTool: {
		// EXCLUSIVE: the blocker is a FILE at ~/.yolo/bin/block/<bin>, so two packs
		// blocking the same tool would fight over one path — the same reason `program`
		// is exclusive. (`requires` is shared because it generates nothing.)
		// NOT review-worthy: the install prompt asks "what does this pack reach on your
		// machine", and a blocker reaches nothing — it writes a refusing shim INSIDE the
		// jail and crosses no boundary. A fetched pack blocking a tool can make a jail
		// less useful; it cannot make it less contained.
		Kind: KindBlockedTool, Combine: CombineExclusive,
		Claims: "a refusing shim at ~/.yolo/bin/block/<bin>, ahead of the real tool on PATH",
	},
	KindProgram: {
		Kind: KindProgram, Combine: CombineExclusive, MayBeReviewWorthy: true,
		Claims: "a name on PATH and a launcher in ~/.yolo/bin/launch/",
	},
	KindRequires: {
		// CombineShared, not Exclusive: a required binary is an independent ASSERTION by
		// each pack, not a path any of them owns, so two packs requiring `jq` is the
		// ordinary case rather than a collision. (program is exclusive because it owns a
		// launcher filename; requires generates nothing.)
		Kind: KindRequires, Combine: CombineShared,
		Claims: "a binary that must already be on PATH (asserted, never installed)",
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
	KindProfile: {
		// Exclusive by (pack, name), and the target carries both — the pack prefix is what
		// keeps the generic exclusive loop in packload.Collisions from ever firing, because
		// two packs selecting the same NAME are the unrelated-coincidence case §3.4 rules
		// legal. Not review-worthy: a variant narrows or retunes what the pack already
		// ships, and the env half is literal strings exactly like `env`.
		Kind: KindProfile, Combine: CombineExclusive,
		Claims: "a named variant of the pack's own surfaces, launch flags and env, " +
			"selected at launch",
	},
	KindProvider: {
		// Exclusive by provider NAME, not by pack: one pack shipping two providers is the
		// ordinary multi-provider case, and two packs shipping ONE name is the collision.
		// The claim target is the bare name (no discriminator), so the generic exclusive
		// loop in packload.Collisions compares the names directly — no dedicated pass, the
		// way loophole's needs.
		Kind: KindProvider, Combine: CombineExclusive,
		Claims: "a named provider's endpoints, wire protocols and model aliases; " +
			"the credential is supplied by user config",
	},
	KindLoophole: {
		// Exclusive by loophole NAME (the module dir's basename). MayBeReviewWorthy is
		// true and, unlike every other kind, it is true of EVERY instance: §3.3's rule is
		// that a claim-free loophole must be unrepresentable, so each declaration a
		// loophole makes that crosses the boundary — the daemon argv, an intercept, a bind,
		// a socket bind, a device — emits its own review-worthy claim. A loophole
		// declaring none of them crosses nothing and is a manifest with no effect.
		Kind: KindLoophole, Combine: CombineExclusive, MayBeReviewWorthy: true,
		Claims: "a loophole module: a host daemon, TLS intercepts, host binds and devices",
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
