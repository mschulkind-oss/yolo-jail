package packload

// footprint.go computes a pack's FOOTPRINT — the list of concrete claims it makes
// on the environment — and detects collisions across packs. It is the "good
// citizen" mechanism (docs/design/pack-declaration-reform.md §1.4, §3.2): the one
// place that computes the union of what packs claim and applies the one-writer
// rule (§3.6).
//
// PHASE 1 (the plan): this reads TODAY's manifest fields — mounts / writableDirs /
// sharedDirs / hostFiles / install / surfaces / launchFlags — and maps each to a
// kind + claim. It is the compatibility shim that lets footprints exist before the
// manifest is rewritten to contributes[] (Phase 4). When that rewrite lands, only
// FootprintOf changes (to read contributes[] directly); Collisions and the CLI
// consumers stay put.
//
// It absorbs the two scattered checks that existed: HostFileConflicts (one pack,
// one kind — and never actually called) and the silent-dedup union() behind
// WritableDirs/SharedDirs. Here a cross-pack duplicate is REPORTED, per the §3.2
// combine rule, not silently merged.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Claim is one concrete claim a pack makes: a kind, a target it claims, and the
// pack that made it. It is an INSTANCE of a packdecl.Footprint (which describes
// the kind in the abstract); this carries the per-instance facts the abstract
// descriptor cannot — which path, whether this particular claim is review-worthy.
type Claim struct {
	Kind packdecl.Kind
	// Target is the thing claimed, normalized for collision comparison: a bin
	// name (program/launch), a home-relative path (files/skills/briefing/state),
	// a surface identity "agent/name" (config), a host path (reads-host), or a
	// hook name (hook).
	Target string
	// Pack is the name of the pack making the claim.
	Pack string
	// Detail is a short human note shown by --footprint (e.g. "machine-wide",
	// the installer URL, the merge precedence). Not used for collision.
	Detail string
	// ReviewWorthy marks a claim a human should look at before trusting the pack:
	// machine-scope state (cross-workspace leak), a host read (credential
	// boundary), or an installer URL (curl-to-shell). Per-instance, unlike the
	// kind's MayBeReviewWorthy flag.
	ReviewWorthy bool
}

// Footprint is every claim one pack makes, in declaration order.
type Footprint struct {
	Pack   string
	Claims []Claim
}

// FootprintOf reads a pack's typed contributions (via packdecl.Contributions(),
// which yields the declared contributes[] or synthesizes them from the legacy
// fields during the compatibility window) and returns its claims. This is the
// Phase-4 inversion of the Phase-1 shim: the footprint now reads kinds directly
// rather than reproducing the field→kind mapping.
//
// The config claim needs a surface IDENTITY (agent/name), which only the decoded
// surface carries — so config claims come from p.Surfaces(), while every other
// kind maps straight off its contribution. A reads-host claim is counted only
// when the origin permits it (matching what actually mounts).
func FootprintOf(p *Pack) Footprint {
	fp := Footprint{Pack: p.Name}
	add := func(k packdecl.Kind, target, detail string, review bool) {
		fp.Claims = append(fp.Claims, Claim{Kind: k, Target: target, Pack: p.Name, Detail: detail, ReviewWorthy: review})
	}

	for _, c := range p.Decl.Contributions() {
		switch c.Kind {
		case packdecl.KindProgram:
			detail := c.Via
			review := false
			switch c.Via {
			case "installer":
				detail, review = "installer: "+c.URL, true
			case "npm":
				detail = "npm: " + c.Package
			}
			add(packdecl.KindProgram, c.Bin, detail, review)
		case packdecl.KindSkills:
			add(packdecl.KindSkills, c.Into, "merged (built-in < pack < user)", false)
		case packdecl.KindBriefing:
			detail := "concat"
			review := strings.HasPrefix(c.After, "host:")
			if review {
				detail = "concat after " + c.After
			}
			add(packdecl.KindBriefing, c.Into, detail, review)
		case packdecl.KindFiles:
			add(packdecl.KindFiles, c.Into, "read-only tree", false)
		case packdecl.KindState:
			if c.Scope == "machine" {
				add(packdecl.KindState, c.At, "machine-wide (leaks across workspaces)", true)
			} else {
				add(packdecl.KindState, c.At, "per-workspace", false)
			}
		case packdecl.KindReadsHost:
			if p.MayAccessHost {
				add(packdecl.KindReadsHost, c.Host, "read-only host file", true)
			}
		case packdecl.KindLaunch:
			add(packdecl.KindLaunch, c.Bin, strings.Join(c.Flags, " "), false)
		case packdecl.KindHook:
			add(packdecl.KindHook, c.Hook, "", false)
		}
		// KindConfig / KindConfigOverlay claims come from the decoded surfaces
		// below, where the surface identity (agent/name) is available.
	}

	// config → one claim per decoded surface, keyed by identity "agent/name".
	if surfaces, _ := p.Surfaces(); len(surfaces) > 0 {
		for _, s := range surfaces {
			id := s.Agent + "/" + s.Name
			detail := s.Path
			if s.Path != "" {
				detail = s.ResolvedMode() + " → " + s.Path
			}
			add(packdecl.KindConfig, id, detail, false)
		}
	}

	// Stable order: contribution order is map-dependent for launch/…, so sort by
	// (kind, target) for a deterministic --footprint and test.
	sort.SliceStable(fp.Claims, func(i, j int) bool {
		if fp.Claims[i].Kind != fp.Claims[j].Kind {
			return fp.Claims[i].Kind < fp.Claims[j].Kind
		}
		return fp.Claims[i].Target < fp.Claims[j].Target
	})
	return fp
}

// Collision is a conflict between two claims on one target that the kind's
// combine rule forbids (§3.2). Reported, never silently resolved.
type Collision struct {
	Kind   packdecl.Kind
	Target string
	Packs  []string // the packs claiming the same target, sorted
	Reason string   // human explanation from the combine rule
}

// Collisions computes the union of every pack's footprint and returns the
// conflicts the one-writer rule forbids (§3.6): two packs claiming an
// exclusively-owned target (program/files/config/launch), or overlapping
// state at different scopes. Merge/concat/shared kinds never collide — that is
// the feature — so they are not reported.
//
// This is the single-pass replacement for the scattered HostFileConflicts (one
// pack, one kind) and union()'s silent dedup. config-overlay is NOT resolved here
// (that is Phase 2, where the assembler records the override); a config-overlay
// claim simply does not collide with the config it targets.
func Collisions(packs []*Pack) []Collision {
	// Group claims by (kind, target), preserving which packs made each.
	type key struct {
		kind   packdecl.Kind
		target string
	}
	groups := map[key][]Claim{}
	var order []key
	for _, p := range packs {
		for _, c := range FootprintOf(p).Claims {
			k := key{c.Kind, c.Target}
			if _, seen := groups[k]; !seen {
				order = append(order, k)
			}
			groups[k] = append(groups[k], c)
		}
	}

	var out []Collision
	for _, k := range order {
		claims := groups[k]
		// Distinct packs claiming this exact target.
		packSet := map[string]struct{}{}
		for _, c := range claims {
			packSet[c.Pack] = struct{}{}
		}
		if len(packSet) < 2 {
			continue // one pack (or one pack repeating) — not a cross-pack collision
		}
		fp, ok := packdecl.FootprintOf(k.kind)
		if !ok {
			continue
		}
		// Only EXCLUSIVE-combine kinds collide on an identical target. Merge,
		// concat, shared, and overlay are designed for multiple contributors;
		// scoped state collides only across differing scopes, handled below.
		if fp.Combine != packdecl.CombineExclusive {
			continue
		}
		out = append(out, Collision{
			Kind:   k.kind,
			Target: k.target,
			Packs:  sortedPackNames(packSet),
			Reason: fmt.Sprintf("%s is sole-owned; two packs claim it — one would shadow the other",
				k.kind),
		})
	}

	// State: overlapping subtrees at DIFFERENT scopes conflict (a path that is
	// per-workspace in one pack and machine-wide in another is ambiguous). Same
	// scope is fine (union). Detail carries the scope ("machine-wide …").
	out = append(out, stateScopeCollisions(packs)...)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Target < out[j].Target
	})
	return out
}

// stateScopeCollisions finds state paths claimed at two different scopes across
// packs (one workspace, one machine) — an ambiguity the scoped-combine rule
// forbids. A path under another path is treated as overlapping.
func stateScopeCollisions(packs []*Pack) []Collision {
	type scoped struct {
		pack    string
		machine bool
	}
	byPath := map[string][]scoped{}
	var order []string
	for _, p := range packs {
		for _, c := range FootprintOf(p).Claims {
			if c.Kind != packdecl.KindState {
				continue
			}
			if _, seen := byPath[c.Target]; !seen {
				order = append(order, c.Target)
			}
			byPath[c.Target] = append(byPath[c.Target], scoped{c.Pack, strings.Contains(c.Detail, "machine")})
		}
	}
	var out []Collision
	for _, tgt := range order {
		entries := byPath[tgt]
		hasWS, hasMachine := false, false
		packSet := map[string]struct{}{}
		for _, e := range entries {
			packSet[e.pack] = struct{}{}
			if e.machine {
				hasMachine = true
			} else {
				hasWS = true
			}
		}
		if hasWS && hasMachine {
			out = append(out, Collision{
				Kind:   packdecl.KindState,
				Target: tgt,
				Packs:  sortedPackNames(packSet),
				Reason: "state claimed at two scopes (workspace and machine-wide) — ambiguous which backing store wins",
			})
		}
	}
	return out
}

func sortedPackNames(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ReviewWorthy returns the claims across all packs a human should inspect —
// machine-scope state, host reads, installer URLs — for the --footprint summary
// line. Deterministically ordered.
func ReviewWorthy(packs []*Pack) []Claim {
	var out []Claim
	for _, p := range packs {
		for _, c := range FootprintOf(p).Claims {
			if c.ReviewWorthy {
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pack != out[j].Pack {
			return out[i].Pack < out[j].Pack
		}
		return out[i].Target < out[j].Target
	})
	return out
}
