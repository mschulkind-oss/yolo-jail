package packload

// mergedest.go answers the question a ZERO-CEREMONY pack cannot answer for itself: where does
// its content go, when the pack never says?
//
// A pack that is just a `skills/` tree and an AGENTS.md — no pack.json at all — is the entry
// point `yolo pack --help` and the migration guide both promote, and in a jail it works: the
// boot path collects every selected pack's skills source (SkillsSourceDirs' `if !declared`
// fallback) and its prose (ComposePackBriefings), then merges the union into every destination
// any pack DECLARED. The host render did not, because it iterates `Decl.Contributions()` and a
// manifest-less pack has none — so `pack lint` said `✓ pack ok`, the apply printed nothing
// about it, and a real $HOME received zero files (docs/plans/feedback-real-pack-adoption.md F1).
//
// That was a NOTCH ASYMMETRY, not a host policy: the jail already proves the inference is
// well-defined, and the destination list already exists in the manifests. So this is that
// inference, extracted to one place, rather than a second hardcoded ".claude/skills" — which
// would have to guess the agent set, and is exactly what `into` is deliberately NOT
// conventionalized to avoid (roadmap.md §6a-3).
//
// THE DESTINATIONS COME FROM THE SELECTED PACK SET. An agent pack's `skills` contribution
// exists to NAME the directory its agent reads from (hostskills.Deliver says so), and its
// `briefing` names the file its agent reads instructions from; a content pack merges into the
// destinations those packs name. So "which destinations?" is answered by the `packs` list —
// the one place the user has already stated which agents they use — and not by core knowing
// any tool's name.
//
// A SECOND PACK NOW ASKS THE SAME QUESTION, and it asks it deliberately rather than by omission:
// an ADDRESSED contribution — `{kind: briefing, from: "prose/claude.md", agents: ["claude"]}` —
// names WHO its content is for and never WHERE it goes, because where an agent reads is that
// agent pack's business (briefing-audiences.md P4, §4.1). So the inference is no longer only the
// manifest-less pack's fallback: it is the mechanism `agents` is defined in terms of, and the two
// arrive here together (borrowingSources). What the addressed shape adds is a source of its own —
// the zero-ceremony pack has no manifest to name one in, and the code below was written when that
// was the only case, which is why the source, not the destination, is where it went wrong.
//
// The result is folded into a COPY of the pack's declaration rather than threaded through every
// render as a side channel. That is the load-bearing structural choice: after
// ResolveDestinations, a zero-ceremony pack is an ordinary pack that declares its destinations,
// so nothing downstream needs a zero-ceremony branch and nothing downstream can forget one.
// Three readers already had to agree about the conventional skills dir and did not
// (skillssource.go's opening comment) — a fourth agreement about destinations was not worth
// taking on.

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// inferrableKinds are the kinds a silent pack's content can be routed for: the two with a
// CONVENTIONAL source, which are the two the zero-ceremony promise is about ("a skills dir and
// an AGENTS.md at the pack root").
//
// `files` is deliberately absent and that is not an omission to revisit: it has no conventional
// location by design (validateContribution requires its `from` for exactly that reason), so
// there is no content to find without a declaration. It is also CombineExclusive — one owner per
// destination — so a borrowed destination would be a footprint violation rather than a merge.
var inferrableKinds = []packdecl.Kind{packdecl.KindSkills, packdecl.KindBriefing}

// Destinations is the outcome of resolving one pack's delivery destinations against the
// selected set.
type Destinations struct {
	// Pack is the pack to render: p itself when it declared everything it carries, else a
	// copy whose declaration NAMES the inferred destinations.
	Pack *Pack
	// Inferred is what was added, for the report. A destination the user never wrote down is
	// still a destination yolo writes to, so the apply says which ones and why.
	Inferred []packdecl.Contribution
	// Orphaned names each kind the pack CARRIES content for that no pack in the set declares
	// a destination for. Not an inference failure — a config the user has to hear about: a
	// content pack selected with no agent pack delivers nothing, which is F1 reached by a
	// different route.
	Orphaned []packdecl.Kind
	// Addressed is one entry per ADDRESSED contribution — the audience it declared, and the
	// destinations that audience reached.
	//
	// It DUPLICATES destinations that are already in Inferred, and that is deliberate rather
	// than sloppy: a synthesized contribution deliberately does not carry `agents`
	// (borrowedDestinations says why — after this function a resolved pack must be an
	// ORDINARY declaring pack), so Inferred alone cannot tell an addressed delivery from a
	// silent one. Without this, `yolo host apply` reports "declares no destination" about a
	// pack that declared exactly who its prose was for — which is the opposite of what it did.
	//
	// EMPTY `Into` IS THE R1 CASE and the reason this is a slice of a struct rather than a
	// []Kind like Orphaned: an addressed contribution that matched nothing is reported by
	// NAME here, so the report can say which audience found no destination instead of naming
	// only the kind. That is the half-truth briefing-audiences.md's own build note records
	// (`Orphaned []Kind` cannot express R1); Orphaned is unchanged and still carries the
	// kind-level signal every existing reader keys on.
	Addressed []AddressedDelivery
}

// AddressedDelivery is what ONE addressed contribution resolved to: the audience it named,
// the source it named, and the destinations that matched.
//
// Per CONTRIBUTION, matching the unit of inference (ResolveDestinations' last paragraph): a
// pack addressing claude with one file and pi with another is two entries, and folding them
// into one would lose exactly the pairing the per-contribution loop exists to keep.
type AddressedDelivery struct {
	// Kind is the contribution's kind — `briefing` or `skills`.
	Kind packdecl.Kind
	// Agents is the audience the contribution declared, verbatim.
	Agents []string
	// From is the source it named, or "" for the pack's conventional one.
	From string
	// Into is every destination the audience matched, in set order. EMPTY means the audience
	// named no destination this pack set declares — nothing was delivered, which is risk R1.
	Into []string
}

// ResolveDestinations resolves this pack's delivery destinations against `set`, the packs the
// caller is rendering, and returns the pack to render plus what the inference concluded.
//
// A DECLARATION IS HONORED EXACTLY, and only silence is inferred — per kind, so a pack that
// declares `skills` and no `briefing` gets its prose routed without its skills being rerouted.
// That is narrower than the jail, deliberately: in a jail the skills source list is GLOBAL
// (every pack's skills reach every destination), so a pack declaring `into: ".claude/skills"`
// also has its skills merged into `.pi/agent/skills`. Mirroring that here would mean an
// existing manifest suddenly writes into home directories its author never named, which is not
// a fix anyone asked for. Inferring only for the kind a pack said nothing about closes F1
// without widening what a declaration means.
//
// SILENCE IS NOW PER CONTRIBUTION and only the zero-ceremony fallback is per kind — see
// borrowingSources. A contribution carrying an `into` is still honored untouched, so nothing above
// changes for it; what changed is that a pack may be silent about the destination of ONE
// contribution while declaring another's, which is the addressed shape (`agents`, no `into`) and
// was not expressible before.
//
// The pack itself is returned untouched when nothing was inferred, which keeps the common case
// (every shipped pack, every pack with a manifest) allocation-free and — more to the point —
// provably unchanged.
//
// THE UNIT OF INFERENCE IS A CONTRIBUTION, NOT A KIND, and that is what the addressed shape
// (`from` + `agents`, briefing-audiences.md §4.1) changed. A zero-ceremony pack has one implicit
// borrower per kind and nothing to distinguish, so the two readings were the same reading — until
// a pack could declare `{from: "prose/claude.md", agents: ["claude"]}` beside
// `{from: "prose/pi.md", agents: ["pi"]}`. Folding those into one question per kind loses the
// pairing: the union of the audiences yields BOTH destinations, and whichever source a
// per-kind answer picked would reach both agents. So each borrowing contribution is resolved on
// its own, against its own audience, carrying its own source.
func (p *Pack) ResolveDestinations(set []*Pack) Destinations {
	out := Destinations{Pack: p}
	orphaned := map[packdecl.Kind]bool{}
	for _, kind := range inferrableKinds {
		for _, src := range p.borrowingSources(kind) {
			if !p.carriesFor(src) {
				// Silent AND empty for this source: nothing to route. The overwhelming majority
				// of packs, including all six shipped ones for `files`-shaped content.
				continue
			}
			dests := borrowedDestinations(src, p, set)
			if len(src.Agents) > 0 {
				// Recorded whether or not anything matched, because both outcomes are things
				// the user has to be able to see: a delivery that reads as "declares no
				// destination" is a lie about an addressed pack, and one that matched nothing
				// is R1.
				into := make([]string, 0, len(dests))
				for _, d := range dests {
					into = append(into, d.Into)
				}
				out.Addressed = append(out.Addressed, AddressedDelivery{
					Kind: kind, Agents: src.Agents, From: src.From, Into: into,
				})
			}
			if len(dests) == 0 {
				// Reported, never silent (R1) — and this is the branch the old
				// conventional-source-only probe hid an ADDRESSED contribution from: it skipped
				// above, before reaching here, so a content pack that named a source the pack
				// really holds went inert with `Inferred=[] Orphaned=[]`.
				//
				// Deduplicated per KIND because that is the report's granularity (apply.go's
				// reportInferredDestinations prints one line per kind); a pack with two addressed
				// briefings, one matched and one not, still names `briefing` here — half-true is
				// the most this shape can carry, and it beats silence.
				if !orphaned[kind] {
					orphaned[kind] = true
					out.Orphaned = append(out.Orphaned, kind)
				}
				continue
			}
			out.Inferred = append(out.Inferred, dests...)
		}
	}
	if len(out.Inferred) == 0 {
		return out
	}
	// A COPY, never a mutation: p.Decl is shared — Embedded() caches its packs process-wide,
	// and the same *Pack is handed to the render loop, the prune candidates and the overlay
	// collector. Appending to the original's slice would make one pack's inference visible to
	// every later reader of it, including passes whose whole job is to compare against what
	// the pack actually declares.
	decl := *p.Decl
	decl.Contributes = append(append([]packdecl.Contribution{}, p.Decl.Contributions()...),
		out.Inferred...)
	clone := *p
	clone.Decl = &decl
	out.Pack = &clone
	return out
}

// ResolveDestinations resolves every pack in the set against the set, returning the packs to
// render in the same order plus the per-pack outcomes.
//
// The set the inference reads is the ORIGINAL one, not the progressively-rewritten one, so the
// result does not depend on iteration order: a zero-ceremony pack never becomes a destination
// source for the next zero-ceremony pack. Two content packs and no agent pack must both report
// orphaned — not have the second borrow a destination the first only inherited.
func ResolveDestinations(set []*Pack) ([]*Pack, []Destinations) {
	packs := make([]*Pack, 0, len(set))
	outcomes := make([]Destinations, 0, len(set))
	for _, p := range set {
		if p == nil {
			continue
		}
		d := p.ResolveDestinations(set)
		packs = append(packs, d.Pack)
		outcomes = append(outcomes, d)
	}
	return packs, outcomes
}

// borrowingSources returns one contribution per DESTINATION THIS PACK NEEDS INFERRED for `kind`,
// each carrying the source its content is to be read from.
//
// Two shapes reach the inference, and they arrive here as one list on purpose — everything after
// this point treats them identically, which is what keeps the addressed shape from being a second
// code path with its own way to be forgotten:
//
//   - AN ADDRESSED CONTRIBUTION — `{from: "prose/claude.md", agents: ["claude"]}`. It is returned
//     as itself, `from` and `agents` intact, because both are answers only IT holds: the audience
//     narrows the destinations, and the source says which of the pack's files goes to them.
//   - THE ZERO-CEREMONY BORROWER — a synthetic zero-value contribution, returned only when the
//     pack said nothing about the kind at all. No `from` (the convention), no `agents`
//     (broadcast), which is exactly the pack with no pack.json that this whole file exists for.
//
// The zero-ceremony borrower is gated on `declares` rather than on "the list came back empty",
// and the difference is a pack that named its own `into` and nothing else: it has no borrowing
// contribution AND must not get the implicit one, or a declaration would be widened into every
// other agent's directory — the one thing ResolveDestinations' contract promises not to do.
func (p *Pack) borrowingSources(kind packdecl.Kind) []packdecl.Contribution {
	var out []packdecl.Contribution
	for _, c := range p.Decl.Contributions() {
		if c.Kind == kind && c.Into == "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 && !p.declares(kind) {
		out = append(out, packdecl.Contribution{Kind: kind})
	}
	return out
}

// declares reports whether the pack names any DESTINATION of its own for `kind`.
//
// A DESTINATION, not a contribution of the kind — and after briefing-audiences.md those are two
// different questions. A contribution that names an AUDIENCE (`agents`) and no `into` has said
// who its content is for and deliberately nothing about where that content goes, because where
// an agent reads is the agent pack's business (P4). Reading it as a declaration would skip
// inference for the kind entirely and deliver the prose NOWHERE — silently, since there is no
// destination left to notice the absence at.
//
// Equivalent to the old `c.Kind == kind` for every manifest that predates the field: `into` was
// required on all three staged-tree kinds, so a contribution of the kind always carried one.
//
// WHAT IT GUARDS IS NOW THE ZERO-CEREMONY BORROWER, not the whole kind (borrowingSources). The
// narrowing matters for the pack that declares BOTH — an `into` for one destination it does know
// about, and an addressed contribution beside it. Skipping the kind wholesale would drop the
// addressed one on the floor without a word, which is the failure this predicate was tightened to
// prevent, reached from the other side.
func (p *Pack) declares(kind packdecl.Kind) bool {
	for _, c := range p.Decl.Contributions() {
		if c.Kind == kind && c.Into != "" {
			return true
		}
	}
	return false
}

// audienceOf is the set of agent names ONE borrowing contribution addresses, or nil when it names
// none.
//
// NIL IS BROADCAST, and the distinction from an empty-but-non-nil list is the whole safety of
// landing this field ahead of any pack adopting it (P2): a zero-ceremony pack has no manifest to
// put a selector in, and a pack that declares `{kind: briefing}` with neither field is today's
// unaudienced contribution. Both must keep reaching every destination.
//
// IT ASKS ONE CONTRIBUTION, not the union across a kind, which is the change the addressed shape
// forced. The union was answering "who is this PACK for?", and §4.1's two-entry example is a pack
// that is for two agents with two different files — a question with no single answer, whose union
// broadcasts each file to both. Only into-less contributions are ever passed here, because a
// contribution carrying an `into` named its own destination and never reaches the inference.
func audienceOf(c packdecl.Contribution) map[string]bool {
	if len(c.Agents) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, a := range c.Agents {
		out[a] = true
	}
	return out
}

// borrowedDestinations is one synthesized contribution per distinct destination the OTHER packs
// in the set name for `src`'s kind.
//
// The synthesized contribution carries the declaring one's `into`, the BORROWER's own `from`, and
// NOTHING else. Each of those three is a decision:
//
//   - `into` comes from the declaring pack, because where an agent reads is that agent pack's
//     business and nothing the borrower could keep current (P4).
//   - `from` comes from `src` — the borrowing contribution — and is EMPTY exactly when the
//     borrower named no source, which resolves to the CONVENTIONAL one: this pack's own `skills/`
//     or AGENTS.md. That is the whole shape of the thing: the destination is borrowed, the
//     content never is. It was hardcoded to "" until an ADDRESSED contribution could name a
//     source of its own (§4.1) — for the zero-ceremony pack the two spellings are the same
//     string, since it has no manifest to name a source in, but for `{from: "prose/claude.md",
//     agents: ["claude"]}` blanking it substitutes the pack's conventional AGENTS.md for the file
//     the author addressed, silently. NEVER the DECLARING pack's `from`, which names a path in
//     ITS tree — that is the inheritance TestResolveDestinationsDoesNotInheritTier pins.
//   - THE TIER IS NOT INHERITED, and that inheritance is what S2 removed. It used to be, on the
//     argument that a tier is a fact about the destination TOOL and the pack naming the
//     directory is the authority on it. The consequence was the defect: a zero-ceremony pack
//     borrowing `.claude/skills` (namespaced) and `.codex/skills` (flat) inherited BOTH, so the
//     user's own local pack was namespaced in one home and flat in another and one skill had two
//     invocation names. A tier is now the PACK's own positive choice (packdecl's SkillsTier), so
//     there is nothing here to inherit: a borrowed destination is a destination, not a naming
//     policy.
//   - `after` is NOT inherited. On a `briefing` it means "prepend the user's own file", which
//     is the AGENT pack's job at that destination; copying it would have two packs both
//     prepending the same host file into one composed briefing.
//
// Deduplicated by destination, first in set order winning: several packs naming one skills dir
// is `skills`' CombineMerge feature, not a conflict, and delivering the same content twice
// would just archive one copy of itself over the other.
//
// `src`'s AUDIENCE narrows the list to destinations whose owner declared a matching `agent`, and
// naming none means every destination — which is both today's behavior and the only behavior a
// pack with no manifest can ask for (audienceOf). The match is against the string the DESTINATION
// declared about itself, never anything derived from the declaring pack's bins (OQ-BA2), so a
// destination that declares no identity is simply never named by any selector (R4).
func borrowedDestinations(src packdecl.Contribution, p *Pack, set []*Pack) []packdecl.Contribution {
	kind := src.Kind
	audience := audienceOf(src)
	var out []packdecl.Contribution
	seen := map[string]bool{}
	for _, other := range set {
		// Skipping p makes the rule literal — the destinations come from the OTHER packs —
		// rather than a consequence of the caller having already checked that p declares none.
		if other == nil || other == p {
			continue
		}
		for _, c := range other.Decl.Contributions() {
			if c.Kind != kind || c.Into == "" || seen[c.Into] {
				continue
			}
			if audience != nil && (c.Agent == "" || !audience[c.Agent]) {
				// Not `seen`-marked: another pack may own the same PATH under a name this
				// selector does name, and the audience is the question being asked, not the
				// path.
				continue
			}
			seen[c.Into] = true
			// `agents` is deliberately NOT copied onto the result. The narrowing has already
			// happened — each synthesized contribution names one destination that matched — so
			// carrying the selector forward would leave a resolved contribution holding `into`
			// AND `agents`, the pair validateContribution refuses as two answers to one question.
			// After this function a resolved pack is an ORDINARY declaring pack, which is the
			// property that keeps every downstream reader free of an inference branch.
			out = append(out, packdecl.Contribution{Kind: kind, Into: c.Into, From: src.From})
		}
	}
	return out
}

// carriesFor reports whether the pack's tree actually holds content for ONE borrowing
// contribution — the question that separates a zero-ceremony pack from an empty directory, and
// an addressed contribution from one whose `from` names nothing.
//
// IT TAKES THE CONTRIBUTION, not just the kind, and that is the correction the addressed shape
// forced. The old signature asked only about the CONVENTIONAL location, on the reasoning that it
// "is consulted exactly when the pack declared nothing for the kind, so there is no `from` to
// honor — a pack that names a source names a destination in the same breath". That reasoning was
// exactly true of every pack that could exist when it was written and is false now: `{from:
// "prose/claude.md", agents: ["claude"]}` names a source and NO destination, on purpose (P4), so
// the two are no longer one breath. A conventional-only probe answers "no content" for it and
// ResolveDestinations skips it before the orphan report — delivering nothing and saying nothing,
// which is F1's own signature reached through the new field.
//
// Both arms ask THE RESOLVER THE RENDER WILL ASK, rather than re-deriving a path: SkillsSourceDir
// for skills (hostskills.ComposeHostSkills' own call) and BriefingProseFor for briefing
// (ComposeHostBriefings'). A probe that computed the source itself is how three readers came to
// disagree about the conventional skills dir (skillssource.go's opening comment), and here it
// would be worse than drift — a "carries" that says yes and a render that then delivers nothing
// is a promise in a report with no file behind it.
func (p *Pack) carriesFor(c packdecl.Contribution) bool {
	switch c.Kind {
	case packdecl.KindSkills:
		dir, prob := p.SkillsSourceDir(c)
		if dir == "" || prob != "" {
			return false
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			// Stat, not the DirEntry, matching hostskills.collectSkills: a symlink to a
			// directory is a legitimate skill and an Lstat-shaped IsDir would drop it. Only
			// directories count — a loose .md file in a skills dir is not a skill to any of
			// these tools, so a pack holding only one carries nothing to deliver.
			fi, serr := os.Stat(filepath.Join(dir, e.Name()))
			if serr == nil && fi.IsDir() {
				return true
			}
		}
		return false
	case packdecl.KindBriefing:
		// Non-blank, matching what the briefing renders honor: a whitespace-only file yields no
		// block, so counting it as content would promise a delivery that then reports "ships no
		// briefing prose". BriefingProseFor applies the same emptiness test (it right-trims and
		// falls through), over BriefingCandidates rather than DefaultBriefingFiles alone — so a
		// declared `from` is read first and the convention remains the fallback, which is that
		// kind's contractual chain and not a widening invented here.
		text, _ := p.BriefingProseFor(c)
		return text != ""
	default:
		return false
	}
}
