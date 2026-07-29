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

// FootprintOf reads a pack's CURRENT declarations and returns its claims. This is
// the Phase-1 shim (field → kind → claim); Phase 4 replaces the body with a read
// of contributes[] and nothing else changes.
//
// mayAccessHost gates whether host-reading claims (reads-host, an installer) are
// counted as HONORED — a fetched pack's are refused upstream, so counting them
// here would over-report. It matches Pack.MayAccessHost.
func FootprintOf(p *Pack) Footprint {
	fp := Footprint{Pack: p.Name}
	add := func(k packdecl.Kind, target, detail string, review bool) {
		fp.Claims = append(fp.Claims, Claim{Kind: k, Target: target, Pack: p.Name, Detail: detail, ReviewWorthy: review})
	}
	d := p.Decl

	// install → program (installer URL is review-worthy: curl-to-shell).
	if d.Install != nil && d.Install.Bin != "" {
		detail := d.Install.Kind
		review := false
		if d.Install.InstallerURL != "" {
			detail = "installer: " + d.Install.InstallerURL
			review = true
		} else if d.Install.Package != "" {
			detail = "npm: " + d.Install.Package
		}
		add(packdecl.KindProgram, d.Install.Bin, detail, review)
	}

	// mounts → skills | briefing | files, by the magic-string dispatch that
	// exists TODAY (prepare.go isBriefingMount / from=="skills"). The reform
	// deletes that dispatch by making the kind explicit (Phase 4); here we
	// reproduce it so the footprint is faithful to current behavior.
	for _, m := range d.Mounts {
		switch {
		case m.From == "skills":
			add(packdecl.KindSkills, m.To, "merged (built-in < pack < user)", false)
		case m.From == "AGENTS.md" || m.From == "CLAUDE.md":
			detail := "concat"
			if m.HostOverlay != "" {
				detail = "concat after host " + m.HostOverlay
			}
			add(packdecl.KindBriefing, m.To, detail, m.HostOverlay != "")
		default:
			add(packdecl.KindFiles, m.To, "read-only tree", false)
		}
	}

	// surfaces → config, keyed by surface identity "agent/name".
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

	// writableDirs → state (workspace scope).
	for _, dir := range d.WritableDirs {
		add(packdecl.KindState, dir, "per-workspace", false)
	}
	// sharedDirs → state (machine scope) — review-worthy: leaks across workspaces.
	for _, dir := range d.SharedDirs {
		add(packdecl.KindState, dir, "machine-wide (leaks across workspaces)", true)
	}

	// hostFiles → reads-host (review-worthy: the credential boundary). Only
	// counted when the origin permits it, matching what actually gets mounted.
	if p.MayAccessHost {
		for _, hf := range d.HostFiles {
			add(packdecl.KindReadsHost, hf.From, "read-only host file", true)
		}
	}

	// launchFlags → launch, keyed by bin.
	for bin, flags := range d.LaunchFlags {
		add(packdecl.KindLaunch, bin, strings.Join(flags, " "), false)
	}

	// hooks → hook, keyed by hook name.
	for _, h := range d.Hooks {
		add(packdecl.KindHook, h.Name, "", false)
	}

	// Stable order: declaration order is map-dependent for launchFlags/… , so
	// sort by (kind, target) for a deterministic --footprint and test.
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
