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
	"strings"

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
// The pack itself is returned untouched when nothing was inferred, which keeps the common case
// (every shipped pack, every pack with a manifest) allocation-free and — more to the point —
// provably unchanged.
func (p *Pack) ResolveDestinations(set []*Pack) Destinations {
	out := Destinations{Pack: p}
	for _, kind := range inferrableKinds {
		if p.declares(kind) {
			continue
		}
		if !p.carries(kind) {
			// Silent AND empty for this kind: nothing to route. The overwhelming majority of
			// packs, including all six shipped ones for `files`-shaped content.
			continue
		}
		dests := borrowedDestinations(kind, p, set)
		if len(dests) == 0 {
			out.Orphaned = append(out.Orphaned, kind)
			continue
		}
		out.Inferred = append(out.Inferred, dests...)
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

// declares reports whether the pack names any destination of its own for `kind`.
func (p *Pack) declares(kind packdecl.Kind) bool {
	for _, c := range p.Decl.Contributions() {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// borrowedDestinations is one synthesized contribution per distinct destination the OTHER packs
// in the set name for `kind`.
//
// The synthesized contribution carries the declaring one's `into` and NOTHING else, and each
// omission is a decision:
//
//   - `from` stays EMPTY so it resolves to the CONVENTIONAL source — this pack's own `skills/`
//     or AGENTS.md. That is the whole shape of the thing: the destination is borrowed, the
//     content never is.
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
func borrowedDestinations(kind packdecl.Kind, p *Pack, set []*Pack) []packdecl.Contribution {
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
			seen[c.Into] = true
			out = append(out, packdecl.Contribution{Kind: kind, Into: c.Into})
		}
	}
	return out
}

// carries reports whether the pack's tree actually holds content for `kind` at the CONVENTIONAL
// location — the question that separates a zero-ceremony pack from an empty directory.
//
// Conventional only, and that is not a shortcut: this is consulted exactly when the pack
// declared nothing for the kind, so there is no `from` to honor. A pack that names a source
// names a destination in the same breath.
func (p *Pack) carries(kind packdecl.Kind) bool {
	switch kind {
	case packdecl.KindSkills:
		dir, prob := p.SkillsSourceDir(packdecl.Contribution{Kind: packdecl.KindSkills})
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
		for _, name := range packdecl.DefaultBriefingFiles() {
			data, err := os.ReadFile(filepath.Join(p.Root, name))
			if err != nil {
				continue
			}
			// Non-blank, matching what the briefing renders honor: a whitespace-only file
			// yields no block, so counting it as content would promise a delivery that then
			// reports "ships no briefing prose".
			if strings.TrimSpace(string(data)) != "" {
				return true
			}
		}
		return false
	default:
		return false
	}
}
