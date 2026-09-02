package packload

// footprint.go computes a pack's FOOTPRINT — the list of concrete claims it makes
// on the environment — and detects collisions across packs. It is the "good
// citizen" mechanism (docs/design/pack-system.md §3): the one
// place that computes the union of what packs claim and applies the one-writer
// rule (pack-system.md §4).
//
// FootprintOf reads a pack's contributes[] and maps each contribution to a kind +
// claim; Collisions unions the claims across packs and reports a cross-pack
// duplicate on any Exclusive/Scoped target, per the combine rule
// (docs/design/pack-system.md §3), rather than silently merging it.

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
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
	// Detail is a short human note shown by `yolo pack footprint` (e.g. "machine-wide",
	// the installer URL, the merge precedence). Not used for collision.
	Detail string
	// ReviewWorthy marks a claim a human should look at before trusting the pack:
	// machine-scope state (cross-workspace leak), a host read (credential
	// boundary), or an installer URL (curl-to-shell). Per-instance, unlike the
	// kind's MayBeReviewWorthy flag.
	ReviewWorthy bool
	// RunsHostCode marks a claim whose crossing is EXECUTION ON THE USER'S MACHINE, as
	// opposed to a read of it. Strictly narrower than ReviewWorthy, which it implies.
	//
	// It exists because ReviewWorthy is ONE boolean — one severity — and it has always
	// meant "reads ~/.claude.json". A loophole's `host_daemon`/`doctor_cmd` claim is a
	// different proposition, and a reader scanning a footprint should not have to notice
	// that one of a dozen identically-flagged lines happens to say RUNS.
	//
	// HOST code, deliberately narrow. A `program` via installer is a curl-to-shell IN THE
	// JAIL, and a wrapped plugin's hook runs inside the agent's sandbox — both are
	// review-worthy and neither is the user's machine. Widening this to them would make the
	// marker mean "code runs somewhere", which is true of nearly every pack.
	RunsHostCode bool
}

// Footprint is every claim one pack makes, in declaration order.
type Footprint struct {
	Pack   string
	Claims []Claim
}

// SupersedesClaimKind is the Claim.Kind a `supersedes` entry is reported under.
//
// A DISPLAY LABEL, deliberately NOT a packdecl.Kind in the closed registry, and the
// absence is doing three jobs rather than saving a line:
//
//   - `kind: "supersedes"` inside `contributes[]` stays an UNKNOWN KIND, refused by
//     ValidateKind with the real list. Supersession is a top-level manifest key
//     (packdecl/supersedes.go says why), so an author who writes it as a contribution
//     hears about it.
//   - packload.Collisions looks the kind up in the registry and SKIPS what it does not
//     find, so two packs superseding one capability is not reported as a conflict —
//     which is the design's rule (§5: any supersession wins, deliberately no `needs`),
//     achieved by the shape rather than by a special case in the collision pass.
//   - the per-kind exhaustiveness tests (the disclosure classifier, the host-render
//     target census, the kind-docs test) enumerate packdecl.KnownKinds(), so a
//     non-kind needs no entry in four places that describe contributions.
//
// It still renders: printClaimLines formats string(c.Kind) generically, so
// `yolo pack footprint` and `yolo pack lint` print it beside every other claim with no
// change to either command.
const SupersedesClaimKind = packdecl.Kind("supersedes")

// ExecutablesClaimKind is the Claim.Kind the executables a pack SHIPS are reported under.
//
// A DISPLAY LABEL, not a packdecl.Kind in the closed registry — the same shape and for the
// same three reasons as SupersedesClaimKind above: `kind: "executables"` stays an unknown
// kind in a manifest (this is a fact about the TREE, nothing an author declares), Collisions
// skips it because two packs shipping scripts is not a conflict, and the per-kind
// exhaustiveness tests that walk packdecl.KnownKinds() need no entry for a non-kind.
//
// # Why the claim exists at all
//
// It is what remains of `allow_exec`. That key made shipping an executable a CONSUMER
// decision enforced by refusing the file; the refusal was the wrong instrument (packstage's
// package doc has the argument) and is gone, but the fact it surfaced was real and is not
// otherwise visible anywhere: a mode bit is a property of the tree, so unlike an installer
// URL or a loophole's argv there is no manifest line a reader could have found it on.
//
// NOT ReviewWorthy, deliberately. Review-worthy claims are the ones that reach the LAUNCH
// disclosure ("This launch runs pack code on your machine"), which is about crossings, and
// a pack shipping a script is not one — `bash file.sh` never needed the bit, and the
// PATH-destination refusal in packdecl is what actually keeps a shipped script from being
// invoked by name. Flagging it would put an unchanging line in front of the user on every
// launch, which is how a disclosure surface becomes wallpaper. It belongs where someone is
// deliberately inspecting a pack: `yolo pack footprint` and `yolo pack lint`.
const ExecutablesClaimKind = packdecl.Kind("executables")

// execClaimListCap bounds how many paths the executables claim names before it summarizes.
// A pack of scripts should not push its other claims off the reader's screen.
const execClaimListCap = 5

// FootprintOf reads a pack's typed contributions (via packdecl.Contributions())
// and returns its claims, dispatching on each contribution's kind.
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
		case packdecl.KindRequires:
			// A claim, but never a collision (CombineShared): many packs may require one
			// binary, and none owns a path for it. Not review-worthy either — asserting a
			// tool must exist widens no trust surface, unlike an installer URL.
			detail := "must already be on PATH (yolo installs nothing)"
			if len(c.InstallHints) > 0 {
				detail += "; hints: " + strings.Join(sortedMapKeys(c.InstallHints), "/")
			}
			add(packdecl.KindRequires, c.Bin, detail, false)
		case packdecl.KindSkills:
			// Name the SOURCE, not just the merge rule. `from` was accepted and ignored on
			// this kind, and a footprint that said only "merged" was one of the reports that
			// let it stay hidden — an author who moved their skills to `my-skills/` saw a
			// claim identical to the one a working pack makes. Resolved through the same
			// helper delivery uses, so the line cannot claim a source delivery would not read.
			add(packdecl.KindSkills, c.Into,
				"from "+c.SkillsSource()+"/ — merged (built-in < pack < user)", false)
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
		case packdecl.KindMount:
			if p.MayAccessHost {
				add(packdecl.KindMount, c.Host, "read-only → /ctx/"+c.Into, true)
			}
		case packdecl.KindEnv:
			for _, k := range sortedMapKeys(c.Vars) {
				add(packdecl.KindEnv, k, "="+c.Vars[k], false)
			}
		case packdecl.KindLaunch:
			add(packdecl.KindLaunch, c.Bin, strings.Join(c.Flags, " "), false)
		case packdecl.KindHook:
			add(packdecl.KindHook, c.Hook, "", false)
		case packdecl.KindAutonomy:
			detail := "autonomous+guarded postures"
			switch {
			case c.Autonomous == nil:
				detail = "guarded posture only"
			case c.Guarded == nil:
				detail = "autonomous posture only"
			}
			add(packdecl.KindAutonomy, p.Name, detail, false)
		case packdecl.KindProfile:
			// ONE claim per variant, and the target carries BOTH the pack and the name: the
			// name is the selector the user types, but it is owned only WITHIN the pack, so
			// `claude` and `pi` both answering to "bedrock" must not read as two claimants on
			// one target (§3.4 rules them unrelated). The pack prefix is what makes the
			// generic exclusive loop below inert for this kind — the same trick autonomy's
			// pack-name target uses, with the name readable beside it.
			//
			// Not review-worthy: a variant selects a provider this pack ships, and selects
			// nothing else — the body the kind used to carry (surfaces, launch flags, env)
			// moved to `profile:`-modified contributions of the kinds that own them, and
			// THOSE show up as their own claims here. Whether the PROVIDER is hydrated is a
			// launch pre-flight, not an approval question.
			add(packdecl.KindProfile, p.Name+"/"+c.Name,
				"selection of provider "+c.Provider, false)
		case packdecl.KindProvider:
			// The target IS the provider name, with no discriminator: the kind is
			// sole-owned per name, so the generic exclusive loop in Collisions is the
			// whole cross-pack check and two packs shipping one provider group right onto
			// it. Not review-worthy — service facts widen nothing; the credential is a
			// variable NAME the user hydrates, and the Detail says so where a reader can
			// see it without opening the manifest.
			add(packdecl.KindProvider, c.Name,
				providerClaimDetail(c.Endpoints, c.Models, c.APIKeyEnvName), false)
		}
		// KindConfig / KindConfigOverlay claims come from the decoded surfaces
		// below, where the surface identity (agent/name) is available. KindLoophole
		// likewise: its claims come from the MODULE MANIFEST, a file outside pack.json,
		// so the contribution carries only a pointer at it.
	}

	// loophole → SEVERAL claims per contribution, one for every declaration that crosses
	// the host boundary (loophole-packaging.md §3.3). The enumeration is TOTAL by rule: a
	// claim-free crossing must be unrepresentable, because the origin gate reads an empty
	// claim set as consent. See LoopholeHostAccessClaims for the table and the reasons.
	//
	// EVERY one is ReviewWorthy, which no other kind can say of all its instances. That is
	// not severity inflation: the enumeration only emits a claim for something that
	// crosses, so an unreviewable loophole claim would be a contradiction. Host EXECUTION
	// is distinguished from a host read inside the Detail — "RUNS … on your machine",
	// following pluginClaimDetail's "RUNS CODE" — because ReviewWorthy is one boolean and
	// this kind needed two severities' worth of meaning out of it.
	//
	// NOT gated on p.MayAccessHost, unlike reads-host/mount. Those two report what WILL be
	// honored; a loophole claim reports what the pack WANTS, which is the question a
	// footprint answers (`pack footprint`'s own doc: "the point of showing a footprint is to
	// see what a pack WANTS before trusting it"). Hiding a fetched pack's daemon argv from
	// the footprint would hide exactly the line the reader came for.
	for _, lc := range p.loopholeClaims() {
		fp.Claims = append(fp.Claims, Claim{
			Kind: packdecl.KindLoophole, Target: lc.target, Pack: p.Name,
			Detail: lc.detail, ReviewWorthy: true, RunsHostCode: lc.runsHostCode,
		})
	}

	// executables → ONE claim per pack, not one per file: the reader's question is "does
	// this pack ship tools", and a pack of scripts answering it thirty times would push its
	// other claims off the screen. See ExecutablesClaimKind for why this is reported rather
	// than gated.
	if execs := packExecutables(p.Root); len(execs) > 0 {
		// The COUNT is the Target and the PATHS are the Detail, so the two columns do
		// different work. Putting the pack name in Target (as the autonomy claim does)
		// would repeat the heading `yolo pack footprint` already prints above every
		// pack's block.
		target := fmt.Sprintf("%d files", len(execs))
		if len(execs) == 1 {
			target = "1 file"
		}
		shown, detail := execs, ""
		if len(shown) > execClaimListCap {
			shown = shown[:execClaimListCap]
			detail = fmt.Sprintf("%s, and %d more",
				strings.Join(shown, ", "), len(execs)-execClaimListCap)
		} else {
			detail = strings.Join(shown, ", ")
		}
		fp.Claims = append(fp.Claims, Claim{
			Kind: ExecutablesClaimKind, Target: target, Pack: p.Name, Detail: detail,
		})
	}

	// supersedes → one claim per entry, keyed by the CAPABILITY. It is a claim about the
	// ENVIRONMENT — "this job will not be done here" — which is exactly what a footprint
	// enumerates, and it is the only way a reader learns before selecting a pack that it
	// will retire a loophole they rely on. The `because` is the Detail, so the
	// justification travels with the consequence on this surface too.
	//
	// NOT ReviewWorthy and NOT RunsHostCode. Both flags mark a claim that WIDENS what the
	// pack may do to your machine; this narrows it, and there is nothing to approve — see
	// supersede.go for the full argument and for why it is absent from HostAccessClaims.
	// The line prints unconditionally regardless, because every claim does.
	for _, s := range p.Supersessions() {
		fp.Claims = append(fp.Claims, Claim{
			Kind: SupersedesClaimKind, Target: s.Capability, Pack: p.Name,
			Detail: "retires the loophole serving it — " + s.Because,
		})
	}

	// A WRAPPED PLUGIN is a claim in its own right, and one the contributions cannot
	// express: what it declares lives in ITS manifest, not in pack.json. So a plugin's
	// components are invisible to the loop above — which for `hooks`/`mcpServers` would mean a
	// pack that runs code on the user's behalf showing a footprint that says only "skills".
	// Reported under KindSkills because that is the kind that carries it (see the plugin
	// contribution note in kinds.go), with a target no real `into` path can collide with
	// (manifest paths may not contain a colon).
	for _, pl := range p.Plugins() {
		add(packdecl.KindSkills, "plugin:"+pl.Name(), pluginClaimDetail(pl), pl.RunsCode())
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

	// config-overlay → one claim per contribution, keyed by the identity it targets.
	//
	// Reported for the same reason every other kind is: the footprint is the "good
	// citizen" statement of what a pack does to its environment, and "contributes keys to
	// someone else's config file" is squarely that. It was omitted while the kind was
	// INERT, where saying nothing was accurate; now that it applies at both render paths,
	// the omission would mean the one report a user reads before trusting a pack cannot
	// show its most collaborative claim. It does not collide (CombineOverlay), so it is a
	// claim line only.
	//
	// A `profile`-gated overlay CLAIMS UNCONDITIONALLY, like every other kind, because the
	// footprint reports what a pack WANTS and selection is a launch fact — the same
	// scoping `pack footprint`'s own doc states for loophole argv. The Detail names the
	// gate, so the line under which the keys land says what has to be true for them to.
	for _, ov := range p.Decl.ConfigOverlayContributions() {
		detail := "contributes keys (owner still wins)"
		if ov.Profile != "" {
			detail = fmt.Sprintf("contributes keys when profile %q is active for the "+
				"owner's agent (owner still wins)", ov.Profile)
		}
		add(packdecl.KindConfigOverlay, ov.Surface, detail, false)
	}

	// Same rule for a `profile`-gated env contribution: it CLAIMS UNCONDITIONALLY and the
	// Detail names the gate. It could not be seen here while it lived in a kind:profile
	// body — the shrink is what surfaced it, and a bedrock key a pack ships under a
	// profile is exactly the line a reader of a footprint wants to find, with the
	// condition spelled out beside it.
	for _, gated := range p.Decl.ProfiledEnvContributions() {
		for _, k := range sortedMapKeys(gated.Vars) {
			add(packdecl.KindEnv, k, "="+gated.Vars[k]+" when profile \""+
				gated.Profile+"\" is active", false)
		}
	}

	// Stable order: contribution order is map-dependent for launch/…, so sort by
	// (kind, target) for a deterministic footprint and test.
	sort.SliceStable(fp.Claims, func(i, j int) bool {
		if fp.Claims[i].Kind != fp.Claims[j].Kind {
			return fp.Claims[i].Kind < fp.Claims[j].Kind
		}
		return fp.Claims[i].Target < fp.Claims[j].Target
	})
	return fp
}

// pluginClaimDetail describes a wrapped plugin in one footprint line: the components it
// declares, with the code-running ones marked, since those are what the review flag is about.
//
// The word "RUNS CODE" is spelled out rather than left to the ⚠ marker because this claim is
// the one place a user learns that installing a pack of "skills" also starts an MCP server.
func pluginClaimDetail(pl *pluginpack.Plugin) string {
	comps := pl.Components()
	if len(comps) == 0 {
		return "wrapped agent plugin (skills only)"
	}
	var names []string
	runs := false
	for _, c := range comps {
		names = append(names, c.Name)
		if c.RunsCode {
			runs = true
		}
	}
	detail := "wrapped agent plugin declaring " + strings.Join(names, ", ")
	if runs {
		detail += " — RUNS CODE"
	}
	return detail
}

// Collision is a conflict between two claims on one target that the kind's
// combine rule forbids. Reported, never silently resolved.
type Collision struct {
	Kind   packdecl.Kind
	Target string
	Packs  []string // the packs claiming the same target, sorted
	Reason string   // human explanation from the combine rule
}

// Collisions computes the union of every pack's footprint and returns the
// conflicts the one-writer rule forbids (pack-system.md §4): two packs claiming an
// exclusively-owned target (program/files/config/launch), or overlapping
// state at different scopes. Merge/concat/shared kinds never collide — that is
// the feature — so they are not reported.
//
// config-overlay is NOT resolved here (the assembler records the override at
// render time); a config-overlay claim simply does not collide with the config
// it targets.
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
		// config has its OWN pass (ConfigSurfaceCollisions): it is the one exclusive kind
		// whose violation a pack can commit against ITSELF, and the one whose remedy is a
		// different kind rather than a different target — so its message has to teach
		// `config-overlay` rather than say "one would shadow the other".
		if k.kind == packdecl.KindConfig {
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

	// A wrapped plugin NAME is exclusive even though skills merge: a plugin is delivered as
	// one directory named for itself, so two packs wrapping same-named plugins want the same
	// directory and the later apply would silently win. The generic loop above cannot see this
	// (the claim's kind is skills, which merges by design), so it is its own pass — the same
	// shape state's scope conflict needs, and for the same reason.
	out = append(out, pluginNameCollisions(packs)...)

	// Two `config` declarations of one surface identity — the R1 hazard, finally enforced.
	out = append(out, ConfigSurfaceCollisions(packs)...)

	// Two packs shipping one loophole NAME. Its own pass for the reason plugin names need
	// one: the kind is Exclusive, but its claim TARGETS carry a discriminator
	// (`acme:device:/dev/snd`), so the generic loop above compares two packs' bind mounts
	// rather than their loophole names — and two packs shipping `acme` with different
	// crossings would collide on nothing while resolving to one name, one state dir, and one
	// endpoint. Keyed on the name alone, which is available without decoding anything (it is
	// the module dir's basename).
	out = append(out, LoopholeNameCollisions(packs)...)

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

// pluginNameCollisions finds a plugin name wrapped by two different packs. Delivery is one
// directory per plugin name, so two claimants mean one silently overwrites the other on every
// apply — reported here rather than discovered as a plugin that keeps changing its mind.
func pluginNameCollisions(packs []*Pack) []Collision {
	byName := map[string]map[string]struct{}{}
	var order []string
	for _, p := range packs {
		for _, pl := range p.Plugins() {
			name := pl.Name()
			if byName[name] == nil {
				byName[name] = map[string]struct{}{}
				order = append(order, name)
			}
			byName[name][p.Name] = struct{}{}
		}
	}
	var out []Collision
	for _, name := range order {
		if len(byName[name]) < 2 {
			continue
		}
		out = append(out, Collision{
			Kind: packdecl.KindSkills, Target: "plugin:" + name,
			Packs: sortedPackNames(byName[name]),
			Reason: "two packs wrap a plugin named " + name +
				"; a plugin is delivered as one directory under its own name, so one would " +
				"overwrite the other — rename one plugin",
		})
	}
	return out
}

// LoopholeNameCollisions finds one loophole NAME shipped by two different packs.
//
// Fatal-shaped rather than merge-shaped, and more so than any other exclusive kind: a
// shadowed loophole name is a daemon nobody audited running under a name the user trusts,
// and everything downstream keys on the name — the state dir (StateDirFor), the endpoint
// file, the `--add-host`, the `enabled` toggle in config, the approval claim strings. Two
// claimants mean the user's approval of one pack's `acme` silently covers the other's.
//
// It is per DECLARATION, like ConfigSurfaceCollisions and unlike the generic loop, so the
// report names the pack-relative `from` of each side. A pack colliding with ITSELF (two
// module dirs, one basename) is caught earlier, in LoopholeModules, where both
// declarations are in hand.
//
// EXPORTED for the same reason ConfigSurfaceCollisions is: the launch pre-flight refuses
// THIS collision specifically rather than Collisions() wholesale (a `launch` clash, for
// instance, is documented later-wins there). That pre-flight IS wired now — a collision
// between two packs is FATAL at launch (`PackLoopholeNameConflicts`, the FOURTH launch
// pre-flight, called at internal/cli/run/packs.go:313) — so this function is no longer the
// only reader; `pack footprint` and
// `pack lint` report the same collision earlier and non-fatally.
//
// The RESERVED-name half it once also needed is GONE rather than built: there is no
// reserved loophole namespace left (`internal/paths/paths.go`), so exclusivity across
// packs is the whole rule. This comment said "not yet fatal at launch" until 2026-08-23.
func LoopholeNameCollisions(packs []*Pack) []Collision {
	byName := map[string]map[string]struct{}{}
	var order []string
	for _, p := range packs {
		mods, _, _ := p.LoopholeModules()
		for _, mod := range mods {
			if byName[mod.Name] == nil {
				byName[mod.Name] = map[string]struct{}{}
				order = append(order, mod.Name)
			}
			byName[mod.Name][p.Name] = struct{}{}
		}
	}
	var out []Collision
	for _, name := range order {
		if len(byName[name]) < 2 {
			continue
		}
		out = append(out, Collision{
			Kind: packdecl.KindLoophole, Target: name,
			Packs: sortedPackNames(byName[name]),
			Reason: "two packs ship a loophole named " + name +
				"; a loophole's name is its identity everywhere — its state dir, its endpoint, " +
				"its `enabled` toggle and the host-access claim you approved — so one would run " +
				"under the other's approval. Rename one module directory",
		})
	}
	return out
}

// configSurfaceCollisions finds a config surface IDENTITY declared more than once — the
// hazard docs/design/pack-config-collaboration.md R1 rules harmful, enforced.
//
// WHY IT IS ITS OWN PASS rather than a row in the generic exclusive loop, in two parts:
//
//   - A pack can commit this against ITSELF. Every other exclusive kind collides on a
//     destination the runtime then rejects (podman's duplicate mount), so "one pack, one
//     claim" is enough to be safe; a surface identity is resolved in Go by
//     manifest.Merge's last-writer-wins, which is just as silent for two declarations
//     inside one manifest as for two packs. The generic loop skips a single-pack group by
//     design (it is asking "do two packs fight"), which is the wrong question here.
//   - The REMEDY is a different KIND, not a different target. "One would shadow the
//     other — give them different paths" is the right advice for `files` and useless
//     here: two packs wanting keys in one config file is a legitimate intent, and
//     `config-overlay` is how it is expressed. So the message has to teach the
//     conversion, which no generic reason string can.
//
// What actually goes wrong is worth stating precisely, because it is not "one pack's keys
// are dropped": the survivor of manifest.Merge brings its own `mode`, `path`, `codec` and
// `defaults`, so a second declaration can flip a surface from `stateful` to `rmw` and
// disable in-jail edit capture for a file the other pack owns. Neither author can see it,
// and the victim is whichever user's file loses its capture sidecar (R1).
//
// Reads Surfaces() (autonomy ON). The posture only patches the managed layer of surfaces
// the pack ALREADY declares — foldPostureManaged merges into the base rather than
// appending — so the identity set is the same at either notch, and an `autonomy` patch of
// the pack's own surface is correctly not a second declaration.
//
// EXPORTED, unlike its sibling passes, because three callers outside the footprint report
// refuse THIS collision specifically rather than the whole set: the launch pre-flight,
// `yolo host apply`, and `yolo check`. Narrow on purpose — a `launch` clash, for instance, is
// documented later-wins at every one of those, so widening the refusal to Collisions()
// wholesale would break overrides that work today.
func ConfigSurfaceCollisions(packs []*Pack) []Collision {
	// One entry per DECLARATION (not per pack), so a manifest declaring an identity twice
	// is visible. Keyed by identity, first-seen order for a deterministic report.
	byID := map[string][]surfaceDecl{}
	var order []string
	for _, p := range packs {
		surfaces, _ := p.Surfaces() // problems are reported by the render path, not here
		for _, s := range surfaces {
			id := s.Key().String()
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			byID[id] = append(byID[id], surfaceDecl{p.Name, s.ResolvedMode(), s.Path, s.Codec})
		}
	}

	var out []Collision
	for _, id := range order {
		decls := byID[id]
		if len(decls) < 2 {
			continue
		}
		packSet := map[string]struct{}{}
		for _, d := range decls {
			packSet[d.pack] = struct{}{}
		}
		names := sortedPackNames(packSet)
		out = append(out, Collision{
			Kind: packdecl.KindConfig, Target: id,
			Packs:  names,
			Reason: configCollisionReason(id, names, divergences(decls)),
		})
	}
	return out
}

// surfaceDecl is one `config` declaration of a surface identity: which pack made it, and
// the three surface-DEFINING fields whose silent replacement is the R1 hazard. `managed` is
// deliberately absent — the keys are what two packs legitimately both want, so they are not
// evidence of anything.
type surfaceDecl struct {
	pack  string
	mode  string
	path  string
	codec string
}

// divergences describes the ways two declarations of one identity DISAGREE, as
// "<field> (<pack>: <value> vs <pack>: <value>)" strings.
//
// It exists because the disagreement is the concrete damage, not an abstraction: two packs
// agreeing on everything but `managed` still get refused (R1 — the hazard is the mechanism,
// not the impoliteness), but a pack that flipped a `mode` deserves to be told exactly that
// rather than left to infer it from a rule. Empty when the declarations are identical
// apart from their keys.
func divergences(decls []surfaceDecl) []string {
	var out []string
	field := func(name string, pick func(int) string) {
		first := pick(0)
		for i := 1; i < len(decls); i++ {
			if pick(i) == first {
				continue
			}
			// Label by pack, except in a self-collision where both labels would be the
			// same name — there the declaration index is the only thing that tells them
			// apart.
			lhs, rhs := decls[0].pack, decls[i].pack
			if lhs == rhs {
				lhs, rhs = "declaration 1", fmt.Sprintf("declaration %d", i+1)
			}
			out = append(out, fmt.Sprintf("%s (%s: %q vs %s: %q)", name, lhs, first, rhs, pick(i)))
			return // one report per field is enough to make the point
		}
	}
	field("mode", func(i int) string { return decls[i].mode })
	field("path", func(i int) string { return decls[i].path })
	field("codec", func(i int) string { return decls[i].codec })
	return out
}

// configCollisionReason is the message a user sees, and it is deliberately the longest one
// in this file: it has to name both packs, say what silently happens, and TEACH the
// conversion, because since 2026-08-02 the correct expression exists (`config-overlay`) and
// a refusal that does not point at it just blocks a working setup.
func configCollisionReason(id string, packs, diverged []string) string {
	// Two wordings, because a self-collision has no second pack to blame and saying "the
	// other pack" there sends the author looking for one that does not exist.
	who := fmt.Sprintf("packs %s each declare it, so one silently changes how the other's "+
		"file is maintained", strings.Join(packs, " and "))
	if len(packs) == 1 {
		who = fmt.Sprintf("pack %s declares it more than once, so its own later entry "+
			"silently redefines its earlier one", packs[0])
	}
	agent, _, _ := strings.Cut(id, "/")
	msg := fmt.Sprintf("a config surface has exactly ONE owner. %s — the later declaration "+
		"REPLACES the earlier one whole, taking its mode, path, codec and defaults with it, "+
		"with nothing reported", who)
	if len(diverged) > 0 {
		msg += ", and these already disagree: " + strings.Join(diverged, "; ")
	}
	return msg + ".\n" +
		"    To contribute keys to a surface another pack owns, declare `config-overlay` " +
		"instead of a second `config`:\n" +
		"      { \"kind\": \"config-overlay\", \"surface\": \"" + id + "\",\n" +
		"        \"config\": { \"managed\": { …your keys… } } }\n" +
		"    That leaves the owner's mode/path/codec alone, folds your keys in BELOW its " +
		"managed layer (so the owner still wins a genuine conflict), and records which pack " +
		"set which key (`yolo config diff " + agent + "`)."
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
// machine-scope state, host reads, installer URLs — for the footprint summary
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

// sortedMapKeys returns a string-keyed map's keys in sorted order, so a footprint line
// built from one (an env `vars` map, a `requires` install_hints map) is deterministic —
// Go's map order is not, and these strings are compared in tests and read by humans.
func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// packExecutables returns the pack-relative, slash-separated paths of every regular file
// under root carrying an execute bit, sorted.
//
// Sorted so the claim's Detail is stable across runs: `yolo pack footprint` output is read
// as a diff between two versions of a pack as often as it is read once, and a walk's
// directory order is not a guarantee to build that on.
//
// A walk of the pack tree is not new I/O for FootprintOf — loopholeClaims already reads
// each loophole module's manifest off disk — and a pack is a content tree of skills and
// prose, not a source checkout. An unreadable root yields nothing rather than an error:
// this claim is INFORMATION, and no footprint should fail to render because one directory
// could not be listed. Refusals that must fail closed live in packstage, which walks the
// same tree at staging time.
func packExecutables(root string) []string {
	if root == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unlistable subtree is skipped, never fatal
		}
		info, ierr := d.Info()
		if ierr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}
