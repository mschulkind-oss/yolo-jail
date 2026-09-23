package packload

// mergedest.go answers the question a ZERO-CEREMONY pack cannot answer for itself: where does
// its content go, when the pack never says?
//
// A pack that is just a `skills/` tree and a `briefing/` directory — no pack.json at all — is the
// entry point `yolo pack --help` and the migration guide both promote, and in a jail it works: the
// boot path collects every selected pack's governed skills sources (SkillsSources) and prose
// (run.packBriefingProses), both over GovernedSources, then merges the union into every
// destination any pack DECLARED. The host render did not, because it iterates `Decl.Contributions()` and a
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
// agent pack's business (docs/reference/agent-briefings.md#ba-p4, #the-two-halves-and-why-neither-knows-the-others-business). So the inference is no longer only the
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
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// inferrableKinds are the kinds a silent pack's content can be routed for: the two with a
// CONVENTIONAL source, which are the two the zero-ceremony promise is about ("a skills dir and
// a briefing dir at the pack root").
//
// `files` IS HERE ONLY FOR THE ADDRESSED SHAPE — `{agents: [...], from: ...}` with no `into` —
// and the distinction is load-bearing rather than tidy. It has no conventional location
// (validateContribution requires its `from` for exactly that reason), so the zero-ceremony
// borrower must never fire for it (borrowingSources guards that) and an addressed contribution
// must supply its own source. What an addressed one borrows is the DESTINATION the agent pack
// declares as its alias, exactly as `briefing` and `skills` do.
var inferrableKinds = []packdecl.Kind{packdecl.KindSkills, packdecl.KindBriefing, packdecl.KindFiles}

// Destinations is the outcome of resolving one pack's delivery destinations against the
// selected set.
type Destinations struct {
	// Pack is the pack to render: p itself when it declared everything it carries, else a
	// copy whose declaration NAMES the inferred destinations.
	Pack *Pack
	// Inferred is what was added, for the report. A destination the user never wrote down is
	// still a destination yolo writes to, so the apply says which ones and why.
	Inferred []packdecl.Contribution
	// Orphaned names each kind the pack CARRIES content for that reached no destination, and
	// WHY it reached none. Not an inference failure — a config the user has to hear about: a
	// content pack selected with no agent pack delivers nothing, which is F1 reached by a
	// different route.
	Orphaned []Orphan
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
	// EMPTY `Into` IS THE R1 CASE — and so is an Orphan carrying a non-empty Agents. **Both
	// survive, deliberately**: they were built the same day by different hands, and they are
	// not redundant. Orphan answers *"this kind reached nothing, and here is the audience to
	// blame"*, which is what the refusal message keys on. AddressedDelivery answers *"here is
	// every addressed contribution and everywhere it landed"* — including the SUCCESSFUL
	// deliveries an Orphan by definition cannot describe. A reader asking "did my audience
	// work?" needs this field; one asking "why did nothing arrive?" needs the other.
	Addressed []AddressedDelivery
}

// Orphan is one kind of content that reached no destination, together with the reason — which
// is the AUDIENCE the orphaned contribution named, or none.
//
// IT CARRIES THE REASON BECAUSE THERE ARE NOW TWO, and the remedies are opposites. Before the
// audience selector there was one: nobody in `packs` declared a destination for the kind at all,
// fixed by selecting an agent pack or writing an `into`. An addressed contribution
// (`{from: "prose/claude.md", agents: ["claude"]}`) can reach nothing while destinations exist
// and are being written to — because none of them declared a matching `agent` — and for that one
// `into` is not a remedy at all: a contribution names an audience or a destination, never both
// (validateContribution). A report that cannot tell the two apart sends half its readers to
// change the wrong field, so the kind alone is not enough to say.
type Orphan struct {
	// Kind is the content kind that reached nothing.
	Kind packdecl.Kind
	// Agents is the `agents` selector of the contribution that reached nothing — the launcher
	// commands its content was FOR (docs/reference/agent-briefings.md#the-two-halves-and-why-neither-knows-the-others-business). EMPTY means the contribution
	// named no audience, so it was eligible for every destination of its kind and there were
	// none.
	Agents []string
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
// A DECLARATION IS HONORED EXACTLY, and only silence is inferred — per FILE since
// docs/reference/pack-system.md#briefing-governance (per kind before it), so a pack that declares `skills` and no
// `briefing` gets its prose routed without its skills being rerouted, and a pack that declares one
// narrow briefing keeps its unnamed briefing/ files broadcasting (borrowingSources).
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
// (`from` + `agents`, docs/reference/agent-briefings.md#the-two-halves-and-why-neither-knows-the-others-business) changed. A zero-ceremony pack has one implicit
// borrower per kind and nothing to distinguish, so the two readings were the same reading — until
// a pack could declare `{from: "prose/claude.md", agents: ["claude"]}` beside
// `{from: "prose/pi.md", agents: ["pi"]}`. Folding those into one question per kind loses the
// pairing: the union of the audiences yields BOTH destinations, and whichever source a
// per-kind answer picked would reach both agents. So each borrowing contribution is resolved on
// its own, against its own audience, carrying its own source.
func (p *Pack) ResolveDestinations(set []*Pack) Destinations {
	out := Destinations{Pack: p}
	orphaned := map[string]bool{}
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
				// Deduplicated per KIND AND AUDIENCE, which is the report's granularity
				// (apply.go's reportInferredDestinations prints one line per Orphan, and the line
				// it prints depends on the audience). Per kind alone was the granularity while
				// the reason was one reason: it collapsed an unaddressed orphan and an addressed
				// one into a single entry, so whichever came first chose the message for both.
				// The audience is what the two differ by, so it belongs in the key — a pack
				// briefing claude from one file and pi from another, neither matched, is two
				// facts and gets two lines.
				key := string(kind) + "\x00" + strings.Join(src.Agents, "\x00")
				if !orphaned[key] {
					orphaned[key] = true
					out.Orphaned = append(out.Orphaned,
						Orphan{Kind: kind, Agents: src.Agents})
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
	// The clone remembers what was DECLARED, because governance must be computed from that and
	// never from `decl`, which now holds a synthesized copy of every borrower (governance.go).
	// Kept when p is itself a clone, so resolving twice cannot launder synthesized entries into
	// the declaration.
	if clone.origDecl == nil {
		clone.origDecl = p.Decl
	}
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
// For `briefing` and `skills` it is DERIVED FROM THE GOVERNANCE PREDICATE (GovernedSources) and
// keeps no gate of its own — which is the host third of docs/reference/pack-system.md#briefing-r5. Two shapes,
// one list, so everything after this point treats them identically:
//
//   - EVERY INTO-LESS GOVERNOR, returned as itself: an addressed contribution (`{from:
//     "prose/claude.md", agents: ["claude"]}`, whose audience narrows the destinations and whose
//     source says which file goes to them) or a declared broadcast (`{kind: briefing}`, neither
//     field — P2, now spellable in a manifest).
//   - THE IMPLICIT BORROWER — a synthetic zero-value contribution, returned when some source is
//     governed by NO declaration. No `from` (the files nobody named), no `agents` (broadcast).
//
// THE `declares` GATE IS GONE, and it was the trap (pack-system.md#one-governance-reader): "the pack declared a destination of its
// own for this kind" switched the implicit borrower off for the WHOLE kind, so any declaration
// removed a delivery it did not name. The contract that gate protected — a pack naming its own
// `into` must not be widened into every other agent's directory — is now kept by the FILES being
// named: a content `{kind: briefing, into: ".claude/CLAUDE.md"}` governs every unclaimed
// briefing/*.md by omission, so none is Implicit and no borrower is synthesized for it (pack-system.md#briefing-governance).
//
// A DESTINATION (`agent` set) governs nothing, so it never suppresses anything here either.
//
// In DECLARATION order, with the implicit borrower last, so the Addressed report reads in the
// order the author wrote the manifest.
func (p *Pack) borrowingSources(kind packdecl.Kind) []packdecl.Contribution {
	if kind == packdecl.KindFiles {
		// `files` HAS NO CONVENTIONAL SOURCE, so no borrower is ever synthesized for it: a
		// `{Kind: files}` carries no `from` and would route nothing while claiming a destination.
		// Only an ADDRESSED contribution (`agents`, and its own `from`) reaches borrowing.
		var out []packdecl.Contribution
		for _, c := range p.declaration() {
			if c.Kind == kind && c.Into == "" && c.Agent == "" {
				out = append(out, c)
			}
		}
		return out
	}
	sources, _ := p.GovernedSources(kind)
	type governor struct {
		c        packdecl.Contribution
		order    int
		implicit bool
	}
	var govs []governor
	seen := map[string]bool{}
	for _, s := range sources {
		key := s.By.SourceKey()
		if seen[key] || (!s.Implicit && s.By.Into != "") {
			continue
		}
		seen[key] = true
		govs = append(govs, governor{c: s.By, order: s.order, implicit: s.Implicit})
	}
	sort.SliceStable(govs, func(i, j int) bool { return govs[i].order < govs[j].order })
	out := make([]packdecl.Contribution, 0, len(govs))
	for _, g := range govs {
		if g.implicit {
			out = append(out, packdecl.Contribution{Kind: kind})
			continue
		}
		out = append(out, g.c)
	}
	return out
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
// forced. The union was answering "who is this PACK for?", and agent-briefings.md#the-two-halves-and-why-neither-knows-the-others-business's two-entry example is a pack
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
//     or the briefing/ files no declaration names (matched back by SourceKey, governance.go). That is the whole shape of the thing: the destination is borrowed, the
//     content never is. It was hardcoded to "" until an ADDRESSED contribution could name a
//     source of its own (agent-briefings.md#the-two-halves-and-why-neither-knows-the-others-business) — for the zero-ceremony pack the two spellings are the same
//     string, since it has no manifest to name a source in, but for `{from: "prose/claude.md",
//     agents: ["claude"]}` blanking it substitutes the pack's conventional prose for the file
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
// ⚠ `files` IS THE ONE KIND WHOSE BORROWED `into` IS NOT THE DECLARED ONE: the tree lands in a
// SUBDIRECTORY OF the slot, named for the contributing pack (`<into>/<pack>`). That is
// pi-pack-extensions.md's OQ-4 and §8 invariant 2 ("namespacing is the contributing pack's name;
// collisions are impossible"), and the jail notch has always done it
// (internal/cli/run/packfiles.go). This notch did not, so ONE pack.json delivered to two
// different paths — `<into>/<pack>` in the jail and `<into>` itself at the host — which is the
// divergence hostfilestree.go's own comment forbids, and at the host it landed the contributor's
// tree ON the slot root, the layout the alias-root bug is named after.
//
// The join belongs HERE rather than in the host renderer because the renderer takes an ordinary
// declaring pack by contract (the paragraph above): after this function nothing downstream knows
// an inference happened, so a notch-side join would be a second implementation of the layout, in
// the half of the tree that cannot see the audience. `briefing` and `skills` take no join —
// concat and merge compose many packs into one destination by construction, which is exactly what
// `files` (CombineExclusive) cannot do.
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
		// p ITSELF IS IN THE SET, deliberately (docs/reference/pack-system.md#briefing-p2, #briefing-p5). A broadcast
		// reaches every destination of its kind the selected set declares — the broadcasting
		// pack's own included — and an agent pack shipping prose to its own agent addresses
		// itself. The jail's nil audience already reaches those destinations, so skipping p here
		// is what made the two notches diverge.
		if other == nil {
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
			out = append(out, packdecl.Contribution{
				Kind: kind, Into: SlotLanding(kind, c.Into, p.Name), From: src.From,
			})
		}
	}
	return out
}

// SlotLanding is where one CONTRIBUTING pack's addressed content lands inside a destination the
// owning pack declared — THE authority on that layout, for every notch.
//
// One function rather than a rule each notch implements, because the two notches had already
// drifted: the jail joined the contributing pack's name onto the slot and the host wrote the slot
// root itself, so one addressed `files` contribution delivered to two different paths depending on
// where it was rendered (measured 2026-09-21). A layout spelled twice is a layout that diverges,
// and this one diverging is the alias-root class: the host variant put a contributor's whole tree
// AT the slot root, where the owner's own claims and every other contributor's live.
//
// The rule, per pi-pack-extensions.md OQ-4 / §8 invariant 2:
//
//   - `files` — `<slot>/<pack>`. The kind is CombineExclusive, so two contributors cannot share
//     one path; the per-pack subdirectory is what makes "many packs, one slot" expressible at all,
//     and it is why invariant 2 can say collisions are impossible.
//   - `briefing` and `skills` — the slot itself, unjoined. Concat and merge compose many packs
//     into one destination by construction, and a subdirectory would break the thing the agent
//     reads (a briefing FILE; a skills dir whose tier, not its contributor, decides namespacing).
//
// Kind-dispatched and never agent-dispatched: core does not know what an agent is, and nothing
// here may learn. `pack` is the CONTRIBUTING pack's name, never the owner's.
func SlotLanding(kind packdecl.Kind, slot, pack string) string {
	if kind != packdecl.KindFiles || slot == "" || pack == "" {
		return slot
	}
	return path.Join(slot, pack)
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
// for skills (hostskills.ComposeHostSkills' own call) and GovernedBriefingFor for briefing
// (ComposeHostBriefings'). A probe that computed the source itself is how three readers came to
// disagree about the conventional skills dir (skillssource.go's opening comment), and here it
// would be worse than drift — a "carries" that says yes and a render that then delivers nothing
// is a promise in a report with no file behind it.
func (p *Pack) carriesFor(c packdecl.Contribution) bool {
	switch c.Kind {
	case packdecl.KindSkills:
		// The governed tree for THIS contribution's key — the predicate's answer, not a
		// re-derived path.
		sources, _ := p.GovernedSources(packdecl.KindSkills)
		for _, s := range governedBy(sources, c.SourceKey()) {
			entries, err := os.ReadDir(s.Abs)
			if err != nil {
				continue
			}
			for _, e := range entries {
				// Stat, not the DirEntry, matching hostskills.collectSkills: a symlink to a
				// directory is a legitimate skill and an Lstat-shaped IsDir would drop it. Only
				// directories count — a loose .md file in a skills dir is not a skill to any of
				// these tools, so a pack holding only one carries nothing to deliver.
				fi, serr := os.Stat(filepath.Join(s.Abs, e.Name()))
				if serr == nil && fi.IsDir() {
					return true
				}
			}
		}
		return false
	case packdecl.KindBriefing:
		// Non-blank, matching what the briefing renders honor: the predicate returns no source for
		// a blank file, so a contribution carries prose exactly when some file it governs has any.
		// No fallback: a declared `from` that is missing carries NOTHING (P4).
		sources, _ := p.GovernedBriefingFor(c)
		return len(sources) > 0
	case packdecl.KindFiles:
		// A `files` tree carries content when its declared source exists — the same question
		// packFilesTargets asks at the jail notch. An empty `from` is the zero-ceremony shape
		// this kind must never take, so it carries nothing.
		if c.From == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(p.Root, filepath.FromSlash(c.From)))
		return err == nil
	default:
		return false
	}
}
