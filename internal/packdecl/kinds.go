package packdecl

// kinds.go is the CLOSED, core-owned vocabulary of contribution kinds — the
// registry the manifest is built on (docs/reference/pack-system.md §3).
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
// boot (docs/reference/loophole-system.md#strict-and-tolerant-and-why-both:
// an author must hear; a jail must boot).
//
// A kind can also LEAVE the set. It is then neither known nor unknown but RETIRED, and
// says so with its replacement named — see retiredKinds, which is this file's half of the
// pattern `validate.go` uses for a removed config key and `retiredFieldProblems` for a
// removed contribution field.
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
	//
	// FOR A FILE THAT IS NOT A CONFIG SURFACE'S OWN TWIN. It used to also be how a
	// config surface got its `host` layer, bound by basename; that half is a field on
	// the surface now (manifest.Surface.ReadsHost, OQ-CO10, 2026-09-12) and naming
	// one of your own surfaces here is refused with the migration. What is left is
	// what was never derivable: the user's `host_files` key, which carries arbitrary
	// host files that have no mirrored twin in the jail and so genuinely need a path.
	KindReadsHost Kind = "reads-host"
	// KindMount: a host-home dir (or file) mounted read-only into the jail at a
	// /ctx destination. Like reads-host but the source may be a whole directory and
	// the destination is an arbitrary /ctx path (mount just makes the tree visible).
	// Reads the host home, so it is review-worthy exactly like reads-host — and, like
	// reads-host, NO LONGER ORIGIN-GATED: a fetched pack's mount used to be refused
	// until OQ-TP9 (docs/design/trust-paths.md) deleted the gate on 2026-09-04, since
	// naming a pack at all means writing user-scope config as the host user, which is
	// the stronger authority the refusal was standing in for. packload.HonoredMounts
	// refuses nothing now; what bounds the grant is DISCLOSURE — every mount is named
	// on the launch banner and by `yolo pack footprint`.
	// Many packs may mount; no combine (each is an independent read).
	KindMount Kind = "mount"
	// KindEnv: static environment variables set in the jail. Values are literal
	// strings only (no interpolation, no host reads), so it is NOT origin-gated. A
	// key claimed by two packs collides.
	KindEnv Kind = "env"
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
	// `manifest.jsonc`, named by `from`
	// (docs/reference/loophole-system.md#the-loophole-contribution-kind).
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
	// packload's moduleClaims, reading through internal/loopholedecl — the same layer,
	// and for the same reason, as the wrapped-plugin components packload/plugins.go
	// reports (docs/reference/loophole-system.md#the-crossing-enumeration). What this
	// package owns is the DECLARATION:
	// `from` is required and traversal-guarded like every other path-bearing field.
	//
	// Sole-owned by loophole NAME (the module directory's basename, which loadManifest
	// already forces the manifest's own `name` to equal). Exclusivity is per NAME, not
	// per pack, so a pack shipping three loopholes is ordinary — the rule `program` has
	// per `bin`. A shadowed loophole name is a daemon nobody audited running under a
	// name the user trusts.
	KindLoophole Kind = "loophole"

	// KindService: a DAEMON a pack contributes to a namespace — a jail daemon (a
	// yolo-jaild subcommand run under `supervise`) or a host daemon (a `yolo
	// internal daemon` self-exec), or both — plus its endpoint file, its restart
	// policy and its reachability witness (docs/reference/wire-bridge.md §2.1;
	// WB-D16 rules the kind PRIMARY vocabulary, and the wire bridge is its first
	// instance).
	//
	// THE ANTI-LOOPHOLE, and saying so precisely is the kind's whole point: a
	// service holds NO grant and crosses NO boundary. It binds a jail's own
	// loopback, reads no host state, mounts nothing, and executes nothing on the
	// host — §2 draws the line (loopholes reach OUT to host capabilities the jail
	// lacks; a service serves INWARD), and the schema enforces it rather than
	// trusting the comment: the grant-shaped fields are refused on this kind
	// (validateContribution), so a service that wanted a host read is
	// unrepresentable, and the footprint marks it never review-worthy for the
	// same reason. The machinery is loophole-shaped on purpose (a supervised
	// in-jail daemon, an endpoint under /run/yolo-services/, the witness — §2.1's
	// decomposition table is the map); the TRUST is not. A daemon that DOES cross
	// is a kind "loophole" declaration with the per-crossing review that kind
	// carries — re-forming the six shipped loophole-only packs as service + boundary
	// grants is §2.1's named follow-up, not this kind's job.
	//
	// Exclusive by service NAME, and the name is the whole identity: the
	// supervisor's per-daemon log, the endpoint file at
	// /run/yolo-services/<name>.endpoint, and the claim target the collision loop
	// groups on are all keyed by it. Two packs contributing one name is the
	// collision; one pack contributing two services is the ordinary case — the
	// rule `program` has per bin and `provider` per name.
	KindService Kind = "service"

	// KindAdapter: a PROTOCOL CONVERSION served at an address — `adapts: {from, to}`
	// plus the `address` that speaks `to` (docs/design/protocol-resolution.md §3,
	// OQ-PR1).
	//
	// IT SAYS NOTHING ABOUT WHO RUNS IT, and that separation IS the ruling. The
	// leaning was a field on `service`, on the argument that every adapter is a proxy
	// and every proxy is a daemon; review round three answered it with instances
	// rather than argument — a remote gateway fronting your own credentials, a proxy
	// the user already runs on a port they name, and the plain wish to ship an adapter
	// apart from the thing it adapts. Coupling the declaration to a daemon makes all
	// three inexpressible. A pack that DOES run its adapter states that separately,
	// with the `service` contribution and the `needs` it would use anyway — which is
	// exactly what packs/wire-bridge now does, two contributions instead of a special
	// case.
	//
	// NO SHIPPED ADAPTER IS PRIVILEGED (P6). This kind is the whole mechanism: a
	// third-party pack declaring the same pair resolves identically to the one yolo
	// ships, because core selects among declarations and may not name an adapter, a
	// pack or a protocol.
	//
	// Sole-owned by the PAIR (from → to), not by a name and not by a pack. That is the
	// provider rule with a two-part key: two selected packs both claiming to turn one
	// wire into another would each be supplying "the" conversion, and the alternative
	// to refusing is core preferring one. One pack declaring several pairs is ordinary
	// — the shipped bridge declares two — which is the same per-target rather than
	// per-pack exclusivity `provider` has per name and `program` has per bin.
	//
	// NOT review-worthy: an adapter declares an ADDRESS, which is the same class of
	// fact a provider's endpoint is. It reads no host state, mounts nothing and
	// executes nothing; if a pack runs a daemon to serve the address, that daemon is
	// its own `service` (never review-worthy, by §2.1) or its own `loophole` (always
	// review-worthy, per crossing) — and either way the review question is asked of
	// that declaration, not of this one.
	KindAdapter Kind = "adapter"

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
	KindAdapter: {
		// Exclusive by the PAIR, and the claim target carries both halves, so the generic
		// exclusive loop in packload.Collisions is the whole cross-pack check: two packs
		// declaring `openai → anthropic` group right onto it, while one pack declaring two
		// different pairs is the ordinary case (the shipped bridge does exactly that). Not
		// review-worthy — the declaration is an ADDRESS, the same class of fact a provider
		// endpoint is; whatever SERVES that address declares itself, as a service or as a
		// loophole, and is reviewed there.
		Kind: KindAdapter, Combine: CombineExclusive,
		Claims: "a protocol conversion (<from> → <to>) served at an address; " +
			"nothing about who runs it",
	},
	KindService: {
		// Exclusive by service NAME (the const block's comment carries the reasoning:
		// log file, endpoint file and collision target are all name-keyed). NEVER
		// review-worthy, and that is a pin on the anti-loophole rather than an
		// omission: §2's line is that a service crosses no boundary — no grant, no
		// host read, no host execution — so a service claim that needed review would
		// be a contradiction. What the daemon RUNS is in-jail, and the Detail says
		// what, so a reader sees the argv without opening the manifest.
		Kind: KindService, Combine: CombineExclusive,
		Claims: "a jail or host daemon with an endpoint file under /run/yolo-services/ — " +
			"no host grant, no boundary crossing",
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

// retiredKinds names a kind this build has REMOVED, and the migration that replaces it.
//
// A RETIRED KIND IS NOT AN UNKNOWN ONE, and the difference is the whole reason this table
// exists rather than a plain deletion. "unknown kind (expected one of …)" is the right
// answer for a typo and the wrong answer for vocabulary that was real last release: it
// tells an author their declaration is wrong and nothing about what to write instead, and
// it reads identically whether the kind never existed or was deliberately taken away. The
// repo already answers a retired top-level CONFIG key by naming its replacement
// (validate.go's `journal` and `host_processes`) and a retired contribution FIELD the same
// way (retiredFieldProblems); a retired kind is the third member of that set.
//
// AUTHORING-LOUD, JAIL-TOLERANT, exactly like the other two. ValidateKind runs on the
// strict path, so an author hears the migration; DecodeTolerant SKIPS an entry whose kind
// this build does not know — retired or merely newer — before any validation runs, so a
// jail whose staged tree still carries one boots and says so. The asymmetry is the `tier`
// incident's lesson: an author must hear, and a jail must boot.
//
// An entry stays here until no manifest anyone could stage still carries the kind.
var retiredKinds = map[Kind]string{
	"launch": `kind "launch" has been REMOVED — its flags now live in the ` +
		`"autonomy" kind's postures, where the confinement notch can withhold them: ` +
		`{"kind": "autonomy", "autonomous": {"launch": [{"bin": "<bin>", ` +
		`"flags": ["<flag>"]}]}}. A launch flag declared outside a posture was one no ` +
		`notch could take away, which is what that policy exists to prevent; the ` +
		`nested "launch" block inside a posture is a different thing and is the ` +
		`replacement. (The kind's other half, the flag-ALIAS map, is gone with no ` +
		`replacement: a table of other spellings of one switch restates the tool's own ` +
		`flag parser in a place that cannot notice it drift.)`,
}

// RetiredKind returns the migration message for a kind this build has removed, or "" for
// any other kind — including a known one and a name that was never a kind here.
func RetiredKind(k Kind) string { return retiredKinds[k] }

// ValidateKind reports the standard problem string for an unknown kind, or "" if
// k is known — matching the "unknown X (expected …)" shape knownModes/knownCodecs
// use, so a pack author gets one consistent diagnostic across the manifest.
//
// A RETIRED kind gets its own message instead, naming the replacement rather than the
// whole vocabulary (retiredKinds). One message, not two: the retirement text is returned
// in place of the generic one so a manifest that has not been migrated yet reads as the
// migration it needs and not as a typo beside the whole kind list.
func ValidateKind(k Kind) string {
	if KnownKind(k) {
		return ""
	}
	if msg := RetiredKind(k); msg != "" {
		return msg
	}
	names := make([]string, len(footprints))
	for i, kk := range KnownKinds() {
		names[i] = string(kk)
	}
	return fmt.Sprintf("unknown kind %q (expected one of %s)", k, strings.Join(names, ", "))
}
