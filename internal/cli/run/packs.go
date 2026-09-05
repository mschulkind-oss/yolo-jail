package run

// packs.go is the host-side pack pipeline (C3): resolve the user's `packs` entries,
// stage each pack's tree, and hand the results to skills staging and briefing
// composition.
//
// Everything here runs on the HOST, before the container exists, because that is
// where the inputs live: the pack store, the user config, and (later) git
// credentials. That is the "what needs the host" half of the composition-site
// ruling — the jail never reads config and never fetches.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// packCtxDir is where the staged pack trees are mounted in the jail. The entrypoint
// finds it via YOLO_PACK_ROOT rather than hardcoding it, because on Apple Container the
// trees are read from their host path instead (no nested mount).
const packCtxDir = "/ctx/packs"

// officialStagingDir is the subdir of the staging root holding the EMBEDDED packs, the
// one name under it that is never a configured pack's slug (a slug cannot start with '_':
// Slug escapes every character outside [A-Za-z0-9.-] as "_xx", so "_official" is not
// reachable from any pack name). The prune therefore keeps it unconditionally.
const officialStagingDir = "_official"

// stagePacks stages every pack for this run — the EMBEDDED official packs plus the
// user's configured ones — and returns them loaded, so the mount assembler can act on
// their declarations.
//
// Embedded packs come FIRST so a user pack can override one: later wins, the same rule
// packs already use for same-named skills.
//
// FAIL-CLOSED (A12): a pack that cannot be staged is an error. A jail that comes up
// silently missing a pack the user asked for is the failure mode this whole cluster of
// work exists to remove — and unlike a warning, an error is seen.
//
// Sets jailcontent.SetPackSkillDirs as a side effect, which PrepareSkills consumes on the
// next call. Ordering is therefore load-bearing: stagePacks runs first.
func (o *Options) stagePacks(cname string) (string, []*packload.Pack, []jailcontent.PackBriefing, error) {
	entries, err := config.LoadPacks(func(msg string) {
		o.pr(o.Stdout).print("[yellow]Warning: packs: " + msg + "[/yellow]")
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("packs: %w", err)
	}

	stagingRoot := filepath.Join(paths.AgentsDir(), cname, "packs")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", nil, nil, err
	}

	// The OFFICIAL packs, materialized out of the binary — but only the ones the config
	// NAMED. A bare `packs: ["claude"]` entry selects one; nothing is on by default.
	//
	// Opt-in rather than always-active, and the reason is honesty about what a jail
	// contains: activating six agent packs unconditionally while the launch warning said
	// "no packs are configured, so this jail has no coding agent" was a contradiction a
	// user would only discover by looking in ~/.yolo/bin/block. An empty config now really does
	// produce a jail with no agent.
	// Materialize into a SCRATCH dir first, then copy only the selected packs into the
	// mounted staging root.
	//
	// The mount IS the filter, and it has to be: the entrypoint renders every pack it finds
	// under YOLO_PACK_ROOT, so an unselected pack left in that tree gets its surfaces
	// rendered and its hooks run in-jail. That failed loudly rather than silently (the
	// unselected packs' config dirs are not writable, so A12 halted the boot) — but "the
	// jail refuses to start because of a pack you did not ask for" is not a fix, it is the
	// same bug with a better error.
	scratch, err := os.MkdirTemp("", "yolo-official-packs-")
	if err != nil {
		return "", nil, nil, err
	}
	defer os.RemoveAll(scratch)
	available, problems := packload.MaterializeEmbedded(packs.FS, scratch)
	for _, prob := range problems {
		// A broken OFFICIAL pack is a yolo bug, not a user error, so it is fatal rather
		// than a warning the user can do nothing about.
		return "", nil, nil, fmt.Errorf("official packs: %s", prob)
	}
	byName := map[string]*packload.Pack{}
	for _, p := range available {
		byName[p.Name] = p
	}

	officialRoot := filepath.Join(stagingRoot, officialStagingDir)
	// Clear it: a pack DROPPED from config must stop being mounted, and a leftover tree
	// would keep rendering as if it were still selected.
	//
	// Wholesale removal is safe HERE and only here, because _official is DERIVED — it is
	// materialized fresh out of the binary's embed.FS every launch, so there is nothing in
	// it that re-copying does not reproduce. A CONFIGURED pack's tree is a copy of an
	// external source, so it gets the selective prune below instead. Neither one may touch
	// stagingRoot itself (packstage rule 3): a running jail's /ctx/packs bind captured that
	// dir's inode, and recreating it silently detaches the mount.
	if err := os.RemoveAll(officialRoot); err != nil {
		return "", nil, nil, err
	}
	// The SAME rule for configured packs, which is what the comment above always claimed
	// and only _official ever got: remove the staged tree of every slug the config no
	// longer names. Observed live before this existed — a deleted test pack kept
	// regenerating a broken `fzf` shim across launches, because the mount is the filter and
	// a leftover directory is therefore a fully ACTIVE pack (surfaces render, hooks run,
	// shims generate) long after the user deleted both the pack and its config entry.
	//
	// The live set comes from `entries` BEFORE any resolution, so a fetched pack whose
	// mirror could not be read this launch still counts as configured. That is the case
	// that rules out the simpler clear-everything-and-restage: it would silently discard a
	// pack the user still wants on every offline launch.
	//
	// This runs on the ATTACH path too (refreshJailBriefings is called on every
	// invocation), so dropping a pack from config and re-attaching removes its tree from
	// under a live jail's /ctx/packs — the same thing the _official clear above has always
	// done, and the honest one: config says the pack is gone.
	pruned, err := pruneDroppedPackStaging(stagingRoot, livePackSlugs(entries, byName))
	if err != nil {
		return "", nil, nil, err
	}
	// NO SILENT CAPS: a user who dropped a pack should see its tree go, rather than be left
	// wondering whether the deactivation took (which is exactly the doubt the bug created).
	for _, slug := range pruned {
		o.pr(o.Stdout).print(fmt.Sprintf(
			"[yellow]Warning: pack %s is no longer in `packs` — removed its staged tree "+
				"(a leftover tree keeps rendering in the jail as if it were still "+
				"selected)[/yellow]", slug))
	}
	var loaded []*packload.Pack
	var configured []config.PackEntry
	for _, entry := range entries {
		p, isEmbedded := byName[entry.Name]
		if !isEmbedded || !entry.Embedded() {
			configured = append(configured, entry)
			continue
		}
		dest := filepath.Join(officialRoot, p.Name)
		if err := copyTree(p.Root, dest); err != nil {
			return "", nil, nil, fmt.Errorf("official pack %s: %w", p.Name, err)
		}
		selected, probs := packload.LoadDir(dest, p.Name)
		for _, prob := range probs {
			return "", nil, nil, fmt.Errorf("official pack %s: %s", p.Name, prob)
		}
		loaded = append(loaded, selected)
	}

	var skillDirs []jailcontent.PackSkillSource
	var briefings []jailcontent.PackBriefing
	for _, p := range loaded {
		skillDirs = append(skillDirs, o.packSkillSourceDirs(p)...)
		briefings = append(briefings, o.packBriefingProses(p.Name, p)...)
	}

	// THE LOCKFILE IS NOT READ HERE, and since OQ-TP9 it is not read on the launch path at
	// all (docs/design/trust-paths.md, 2026-09-04). Its one launch-time job was the
	// host-access approval gate, which is deleted: a launch resolves a fetched pack from the
	// local mirror at the config's ref, and the mirror only moves when `yolo pack
	// install`/`update` runs, so the lock is written by install and read by `pack status`.
	// Making resolution read the lock's COMMIT instead of the mirror's ref is worth doing —
	// it is what a lockfile means everywhere else — but it is correctness-of-meaning, not a
	// gate, and it is tracked as OQ-LP8 rather than smuggled back in here.
	for _, entry := range configured {
		root, err := packRoot(entry, o.Getenv)
		if err != nil {
			return "", nil, nil, err
		}
		dest := filepath.Join(stagingRoot, entry.Slug())
		res, err := packstage.Stage(packstage.Spec{
			Root:    root,
			Dest:    dest,
			Only:    entry.Only,
			Exclude: entry.Exclude,
		})
		if err != nil {
			return "", nil, nil, fmt.Errorf("packs: %s: %w", entry.Name, err)
		}
		// NO SILENT CAPS: a pack that staged nothing is almost always an `only`/
		// `exclude` typo, and the user would otherwise just see a pack that "does
		// nothing". Say so, with the count that proves the tree was not empty.
		if len(res.Staged) == 0 {
			o.pr(o.Stdout).print(fmt.Sprintf(
				"[yellow]Warning: pack %s staged 0 files (%d excluded by only/exclude) — "+
					"check its filters[/yellow]", entry.Name, len(res.Excluded)))
		}

		// NO HOST-ACCESS GATE. A configured pack's declarations — its host files, mounts,
		// installer URLs, host-prepended briefings, wrapped plugin hooks and shipped
		// loopholes — are honored whoever shipped it, because naming the pack in `packs`
		// means editing ~/.config/yolo-jail/config.jsonc as the host user, which already
		// grants strictly more than any gate here could withhold (OQ-TP9,
		// docs/design/trust-paths.md, 2026-09-04). What the user gets instead is
		// DISCLOSURE: notePackHostAccess prints what each pack reads at every launch, and
		// startLoopholesDisclosed prints host EXECUTION before it happens.
		p, probs := packload.LoadDir(dest, entry.Name)
		for _, prob := range probs {
			return "", nil, nil, fmt.Errorf("packs: %s", prob)
		}
		loaded = append(loaded, p)

		skillDirs = append(skillDirs, o.packSkillSourceDirs(p)...)
		briefings = append(briefings, o.packBriefingProses(entry.Name, p)...)
	}
	// THE NEEDS CLOSURE (docs/design/wire-bridge.md §3.1, WB-D10): extend the
	// selected set with every pack a live `needs` entry pulls in, transitively.
	// Here — after both staging loops, because a configured pack's bins can be
	// what a when_bins condition keys on (and a configured manifest can declare
	// needs of its own), and before every pre-flight below, because "this is where
	// the pack set becomes complete" has to mean complete: a closure-added pack is
	// ordinary selection from here on (§3.1 step 5), so the exclusivity checks
	// must see it exactly as the launch will deliver it.
	//
	// The additions stage through the _official path — an added pack is always
	// EMBEDDED (needs may name only the embedded official set; ResolveNeeds
	// refuses anything wider, WB-D9), and _official is derived content, cleared
	// and rebuilt wholesale every launch, so there is no prune interaction. Staging
	// here rather than trusting the load is the mount-is-the-filter rule: the
	// entrypoint renders every pack under YOLO_PACK_ROOT, so an added pack whose
	// tree never lands is an added pack that does nothing.
	//
	// EVERY addition prints, before its staging (WB-D12: a pack nobody typed
	// joining a launch silently is the one forbidden behavior of the closure).
	// Stderr, beside the other launch-time disclosure lines.
	added, causes, err := packload.ResolveNeeds(loaded,
		func(name string) (*packload.Pack, bool) {
			p, ok := byName[name]
			return p, ok
		})
	if err != nil {
		return "", nil, nil, fmt.Errorf("packs: %w", err)
	}
	for _, cause := range causes {
		o.pr(o.Stderr).print("[dim]" + cause + "[/dim]")
	}
	for _, p := range added {
		dest := filepath.Join(officialRoot, p.Name)
		if err := copyTree(p.Root, dest); err != nil {
			return "", nil, nil, fmt.Errorf("official pack %s: %w", p.Name, err)
		}
		joined, probs := packload.LoadDir(dest, p.Name)
		for _, prob := range probs {
			return "", nil, nil, fmt.Errorf("official pack %s: %s", p.Name, prob)
		}
		loaded = append(loaded, joined)
		skillDirs = append(skillDirs, o.packSkillSourceDirs(joined)...)
		briefings = append(briefings, o.packBriefingProses(joined.Name, joined)...)
	}

	// THERE USED TO BE A CONSENT PRE-FLIGHT HERE, ahead of the mechanical ones below: every
	// claim a pack made that yolo understood and declined, folded into one fatal
	// (refusedLaunchError, OQ-TP6). OQ-TP9 deleted the gate that produced those refusals, so
	// there is nothing left to fold — the pre-flights below are all about pack MECHANICS now
	// (two packs claiming one destination, a name that shadows a reserved one).
	//
	// PRE-FLIGHT: two claims on one home destination. The mount assembler emits one bind
	// per contribution with no dedup, so this would otherwise surface as podman's
	// "duplicate mount destination" — a boot failure naming neither pack. Checked here
	// because this is where the pack set becomes complete (embedded + configured), and it
	// covers the attach path too, since a collision is a config error either way.
	//
	// FAIL-CLOSED, matching the rest of stagePacks: a `files` claim is sole-owned, so a
	// second claimant is a footprint violation and one pack's content would shadow the
	// other's. Silently mounting whichever podman happened to accept is not an option
	// this file offers anywhere else.
	if conflicts := packDestConflicts(loaded, packdecl.KindFiles); len(conflicts) > 0 {
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(conflicts, "\npacks: "))
	}
	// The OTHER files conflict, which podman cannot see: a `files` tree mounted :ro over a
	// directory an agent's config surface must be written into. Not a duplicate destination
	// (the paths differ), so the check above misses it; it surfaces as an A12 boot refusal
	// naming the surface rather than the claim that shadowed it.
	if shadowed := packFilesShadowedSurfaces(loaded); len(shadowed) > 0 {
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(shadowed, "\npacks: "))
	}
	// The same pre-flight for a config surface IDENTITY claimed twice — the one collision in
	// this cluster that no runtime error would ever announce. `files` at least ends in a
	// podman refusal naming the wrong thing; two `config` declarations resolve in Go
	// (manifest.Merge, last-writer-wins) and the jail comes up looking fine, having flipped
	// one pack's surface `mode` and silently dropped its capture sidecars
	// (docs/design/pack-config-collaboration.md R1). Refused here for the same reason the
	// checks above are: this is where the pack set becomes complete, and it covers attach too.
	//
	// Only the CONFIG collision, not packload.Collisions wholesale: a `launch` clash between
	// two packs is documented later-wins at every other call site, so widening this to the
	// whole set would refuse launches that work today.
	if cols := packload.ConfigSurfaceCollisions(loaded); len(cols) > 0 {
		var msgs []string
		for _, c := range cols {
			msgs = append(msgs, fmt.Sprintf("config surface %s claimed by %s — %s",
				c.Target, strings.Join(c.Packs, ", "), c.Reason))
		}
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(msgs, "\npacks: "))
	}
	// The FOURTH bespoke pre-flight: a loophole NAME claimed twice, or claimed against a
	// name yolo reserves (docs/design/loophole-packaging.md §3.1). Here, beside the other
	// three, for the same reason all four are here — this is where the pack set becomes
	// complete — and fatal for the reason none of the other three quite share: a shadowed
	// loophole name means A DAEMON NOBODY AUDITED RUNNING UNDER A NAME THE USER TRUSTS.
	//
	// It is a fourth PASS rather than a row in packload.Collisions, and the design priced
	// that at zero and was wrong in three measured ways:
	//
	//  1. packload.Collisions is NEVER consulted at launch. Its two callers are the `pack
	//     footprint` report (internal/cli/pack.go) and internal/cli/check/packs.go, which
	//     passes packload.Embedded() — embedded packs only. The launch pre-flight refuses
	//     exactly the three things above, under the comment saying why widening it
	//     wholesale would refuse launches that work today.
	//  2. The generic Exclusive loop SKIPS single-pack groups (`if len(packSet) < 2`), so
	//     ONE pack declaring both `a/acme` and `vendor/acme` — both basename `acme`, both
	//     valid — collides with ITSELF and is not reported. That is the exact hole which
	//     forced ConfigSurfaceCollisions to be its own exported pass.
	//  3. Collisions takes []*packload.Pack, so bundled and reserved names are not
	//     expressible there at all — they are not packs.
	//
	// And it cannot live in loopholes.Discover: that returns []*Loophole with no error
	// channel, and internal/loopholes/resolver.go states the invariant every caller relies
	// on ("Discovery never errors … so ok is always true"). The pre-flight is the home.
	if conflicts := PackLoopholeNameConflicts(packLoopholeDecls(loaded)); len(conflicts) > 0 {
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(conflicts, "\npacks: "))
	}

	// THE FIFTH bespoke pre-flight: a profile selector keyed to a CLI name no pack
	// installs (profiles-as-pack-variants.md §2.5, §8). The two spellings are
	// `--pack-profile <cli>=<name>` and `-p <name> -- <bin>`, and both key the profile
	// by a CLI name the way `use_profiles` does in config — which is validated there
	// and is NOT validated anywhere here, because a flag never reaches ValidateConfig.
	// Without this the typo passed silently: the key went into the table no derive
	// read, and the launch looked exactly like the profile working.
	//
	// Here rather than in the parse (runcmd.go), deliberately: parseRunArgs is a pure
	// fold over argv with no pack data and no error return, and this is where the pack
	// universe is knowable. FATAL, like the four above, for the same reason they are:
	// a silently inert selector is indistinguishable from a working one.
	if err := o.checkProfileTargets(); err != nil {
		return "", nil, nil, err
	}

	// THE SIXTH bespoke pre-flight: a provider NAME shipped by two declarations
	// (profiles-as-pack-variants.md §4.1, OQ-12). Beside the others, because this is
	// where the pack set becomes complete, and FATAL because the collision has no
	// runtime symptom to fall back on: the composed providers table is keyed by name, so
	// the second shipper would silently replace the first while both packs' `footprint`
	// showed one healthy provider each. `yolo pack lint` and `yolo check` report the
	// same collision through packload.Collisions' exclusive loop (the claim target is the
	// bare name); the loop is not consulted at launch, which is why this is its own pass.
	if conflicts := packProviderNameConflicts(loaded); len(conflicts) > 0 {
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(conflicts, "\npacks: "))
	}

	// THE SEVENTH bespoke pre-flight: an AGENT NAME claimed by two packs
	// (briefing-audiences.md OQ-BA6/BA7). Beside the others, for the reason all seven are
	// here — this is where the pack set becomes complete, and it covers attach too — and
	// FATAL because every consumer of the name resolves it by literal against whichever
	// declaration it happens to read: `-p claude=<profile>`, `use_profiles.claude`, and now
	// an `agents: ["claude"]` selector routing prose to "where claude reads". Two owners
	// make all three ambiguous with nothing reported.
	//
	// Its own pass rather than a row in packload.Collisions for that function's own two
	// reasons (AgentNameCollisions' docstring has them): Collisions is never consulted at
	// launch, and it could not see this claim anyway — it keys by `(kind, target)` and skips
	// non-exclusive kinds, while an agent name is claimed across FOUR kinds, two of which
	// merge by design.
	if cols := packload.AgentNameCollisions(loaded); len(cols) > 0 {
		var msgs []string
		for _, c := range cols {
			msgs = append(msgs, fmt.Sprintf("agent name %s claimed by %s — %s",
				c.Target, strings.Join(c.Packs, ", "), c.Reason))
		}
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(msgs, "\npacks: "))
	}

	// THE EIGHTH bespoke pre-flight, and the other half of the same namespace: an `agents`
	// selector naming an agent this jail does not HAVE (briefing-audiences.md P3, OQ-BA3).
	// The vocabulary is the SELECTED packs' claims and nothing wider, so a typo and a name
	// belonging to a pack the user did not select fail identically — from the jail's point of
	// view they are the same mistake, with the same two remedies.
	//
	// FATAL, like the seven above, and for the reason `checkProfileTargets` is (its nearest
	// twin — "a selector keyed to a CLI name no pack installs"): a silently inert selector is
	// indistinguishable from a working one. Prose addressed to nobody is worse than prose
	// addressed to everybody, because the author believes it was delivered.
	//
	// What is NOT refused here is a name whose pack IS selected but which declares no
	// destination of that kind — that is R1, reported through the resolution outcome, because
	// the remedy is a line in the OWNING pack (AgentAudienceProblems' package doc has the
	// split, and why keeping both severities is what makes P3 and R1 one rule).
	if probs := packload.AgentAudienceProblems(loaded); len(probs) > 0 {
		return "", nil, nil, fmt.Errorf("packs: %s", strings.Join(probs, "\npacks: "))
	}

	jailcontent.SetPackSkillDirs(skillDirs)
	// Record the pack-contributed loophole modules for every host-side consumer, with
	// each one's origin gate already evaluated. THE convergence point
	// (docs/design/loophole-packaging.md §5.1): the seven discovery surfaces used to
	// assemble seven independent DiscoverOptions, and two of them execute host code.
	// Sequencing is already right — stagePacks runs above the backend dispatch, well
	// before assembleRunCmd and startLoopholes.
	loopholes.SetPackModules(packLoopholeModules(loaded))
	// And the supersession claims, from the same staged set and for the same reason: a
	// capability a selected pack says needs no doing is a fact about THIS launch, so it
	// has to come from what actually staged rather than from what config named.
	loopholes.SetPackSupersessions(packSupersessions(loaded))
	return stagingRoot, loaded, briefings, nil
}

// checkProfileTargets refuses a profile selector keyed to a CLI name no resolvable pack
// installs — the FIFTH launch pre-flight, and the flag half of the check
// ValidateConfig does for `use_profiles` keys.
//
// The namespace is the SAME one config validation uses (config.UseProfileCLINames), so
// a key `yolo check` accepts a launch accepts and neither can drift from what is
// actually installed. It steps aside on the same condition too: an unresolvable
// configured pack makes the namespace unknowable, and that pack already fails staging
// above with its own message.
//
// A GLOBAL -p (no command) is not checked here at all: it names no CLI, and the keys it
// generates are the selected packs' own installed bins — in the namespace by
// construction.
func (o *Options) checkProfileTargets() error {
	if o.ProfileName == "" && len(o.UseProfiles) == 0 {
		return nil
	}
	names, known := config.UseProfileCLINames()
	if !known {
		return nil
	}
	installed := map[string]bool{}
	for _, n := range names {
		installed[n] = true
	}
	have := strings.Join(names, ", ")
	var problems []string
	// --pack-profile <cli>=<name>: the KEY is the CLI name.
	clis := make([]string, 0, len(o.UseProfiles))
	for cli := range o.UseProfiles {
		clis = append(clis, cli)
	}
	sort.Strings(clis)
	for _, cli := range clis {
		if installed[cli] {
			continue
		}
		problems = append(problems, fmt.Sprintf("-p %s=%s: no pack installs a "+
			"CLI named %q (installed: %s)", cli, o.UseProfiles[cli], cli, have))
	}
	// -p <name> -- <bin>: the COMMAND's basename is the CLI name, the same keying
	// effectiveUseProfiles applies downstream.
	if o.ProfileName != "" && len(o.Args) > 0 {
		bin := filepath.Base(o.Args[0])
		if !installed[bin] {
			problems = append(problems, fmt.Sprintf("-p %s -- %s: no pack installs a CLI "+
				"named %q (installed: %s)", o.ProfileName, o.Args[0], bin, have))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("packs: %s", strings.Join(problems, "\npacks: "))
}

// packLoopholeKind is the `loophole` contribution kind.
//
// It was a wire-string literal while the kind itself was landing in a separate change —
// matching the spelling let the exclusivity pre-flight and the convergence be written and
// tested without either half blocking on the other. The kind now exists, so this is the
// constant, and packdecl owns the spelling again (a Kind IS its manifest spelling).
const packLoopholeKind = packdecl.KindLoophole

// PackLoopholeDecl is ONE `loophole` contribution, reduced to exactly what name
// exclusivity needs.
//
// Per DECLARATION, not per pack, and that is the whole difference between this and
// packload.Collisions' generic Exclusive loop: that loop groups by pack and skips a group
// of one (`if len(packSet) < 2 { continue }`), so a single pack declaring `from: "a/acme"`
// and `from: "vendor/acme"` — both basename `acme`, both individually valid — collides
// with ITSELF and is invisible there. ConfigSurfaceCollisions is per declaration for the
// identical reason.
//
// It carries the DECLARED `from` alongside the resolved dir because the refusal has to be
// actionable: a user reading "two claims on `acme`" needs to see which two lines of which
// manifests to edit, and two absolute paths inside a staging tree they have never looked
// at do not tell them that.
type PackLoopholeDecl struct {
	// Pack is the pack that declared it.
	Pack string
	// From is the pack-relative source path exactly as declared.
	From string
	// Dir is the resolved absolute module directory.
	Dir string
	// Name is the loophole's name. It EQUALS the module directory's basename —
	// loopholes' own loadManifest enforces name == basename, so the name is knowable
	// without decoding the manifest, which is what lets this pre-flight run before any
	// loophole is loaded.
	Name string
}

// PackLoopholeNameConflicts is the FOURTH launch pre-flight: loophole-name exclusivity
// across pack declarations.
//
// FATAL, and the message NAMES BOTH SOURCES — which is the point. A collision here is not
// a shadowed config key or a duplicated mount: the loser's manifest still contributes
// `--add-host`, `ca_cert`, `--device`, bind mounts and `jail_env` to the argv while the
// winner's daemon is the one that runs. The user sees one trusted name and gets a
// mixture, with nothing said (docs/design/loophole-packaging.md §3.1, §5.1).
//
// IT HAD A SECOND HALF UNTIL 2026-08-19 — pack-vs-RESERVED, against the names yolo
// answered to itself (loopholes.ReservedLoopholeNames). That set is gone because every
// name in it became a pack's own: `journal` and `cgroup-delegate` on 2026-08-18,
// `host-processes` and `audio` the same day, and `claude-oauth-broker` on 2026-08-19 when
// its manifest moved into `packs/claude`. A reservation left standing over a pack-shipped
// name is not a warning — this pre-flight is FATAL, so it refuses every launch that
// selects the pack.
//
// WHAT NOW PROTECTS `claude-oauth-broker`, the one name yolo still keys on by hand
// (`yolo broker status`, `yolo check`'s broker section, brokerEnsure, the in-jail
// terminator's endpoint variable), is this function's REMAINING half plus the origin gate,
// and both are worth stating because neither is a list:
//
//   - `packs/claude` OCCUPIES the name. Loophole names are sole-owned across packs, so a
//     second pack claiming it refuses the launch here, by name, for anyone who selected
//     claude — which is everyone the broker is for.
//   - Without claude selected, a pack MAY claim the name, and the bound is the origin
//     gate: brokerLoopholeActive asks for Honored (Active AND the pack may touch the
//     host), so an unapproved fetched pack cannot switch the terminator, the CA mount and
//     the endpoint variable on. An APPROVED pack can, which is precisely the case OQ-A3
//     already admits — "a fetched pack can declare itself on", bounded by approval rather
//     than by the declaration — and it is the same bound `cgroup-delegate` took when it
//     retired its own reservation.
//
// This cannot be a row in packload.Collisions for the reason §3.2 measures: packload
// cannot import internal/loopholes (loopholes → config → packload is a cycle), and the
// decl type it would need is assembled here from the staged tree.
//
// Returns one message per conflict, in a deterministic order (by name, then by pack), so
// the launch refusal is stable and testable.
func PackLoopholeNameConflicts(decls []PackLoopholeDecl) []string {
	byName := map[string][]PackLoopholeDecl{}
	var order []string
	for _, d := range decls {
		if _, seen := byName[d.Name]; !seen {
			order = append(order, d.Name)
		}
		byName[d.Name] = append(byName[d.Name], d)
	}
	sort.Strings(order)

	var out []string
	for _, name := range order {
		group := byName[name]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Pack != group[j].Pack {
				return group[i].Pack < group[j].Pack
			}
			return group[i].From < group[j].From
		})
		if len(group) < 2 {
			continue
		}
		out = append(out, fmt.Sprintf(
			"loophole %q is claimed by %s (%s) — a loophole name is sole-owned, and the "+
				"loser's manifest would still contribute its bind mounts, devices and "+
				"jail_env to this jail while the winner's daemon ran. Rename one of the "+
				"module directories.",
			name, declSources(group), declFroms(group)))
	}
	return out
}

// declSources renders a conflict group's PACKS as prose, deduplicated: "pack a and pack
// b", or "pack a twice" for the self-collision the generic loop cannot see.
func declSources(group []PackLoopholeDecl) string {
	var names []string
	seen := map[string]bool{}
	for _, d := range group {
		if seen[d.Pack] {
			continue
		}
		seen[d.Pack] = true
		names = append(names, "pack "+d.Pack)
	}
	if len(names) == 1 && len(group) > 1 {
		return names[0] + " twice"
	}
	return strings.Join(names, " and ")
}

// declFroms renders the declared `from` values, which is what the user edits.
func declFroms(group []PackLoopholeDecl) string {
	var froms []string
	for _, d := range group {
		label := d.From
		if label == "" {
			label = d.Dir
		}
		froms = append(froms, d.Pack+": from "+label)
	}
	return strings.Join(froms, "; ")
}

// packLoopholeDecls projects the loaded pack set's `loophole` contributions into the
// pre-flight's input.
//
// It resolves `from` against the STAGED tree (p.Root), so the dir checked is exactly the
// one discovery would load — the same rule packSkillSourceDirs and packBriefingProse
// follow, and the reason all three go through the staged root rather than the source: an
// only/exclude filter that removed the module dir must be visible here, not as a loophole
// that "does nothing".
//
// An EMPTY `from` contributes nothing: `loophole` has no conventional source directory
// (unlike `skills` and `briefing`), so there is nothing to fall back to, and refusing it
// is the pack LAYER's job — a pack.json error, decidable by `yolo pack lint` with no
// loophole loaded (docs/design/loophole-packaging.md §3.1).
func packLoopholeDecls(loaded []*packload.Pack) []PackLoopholeDecl {
	var out []PackLoopholeDecl
	for _, p := range loaded {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packLoopholeKind || c.From == "" {
				continue
			}
			dir := filepath.Join(p.Root, filepath.FromSlash(c.From))
			out = append(out, PackLoopholeDecl{
				Pack: p.Name,
				From: c.From,
				Dir:  dir,
				Name: filepath.Base(dir),
			})
		}
	}
	return out
}

// packLoopholeModules is the pre-flight's input carried forward into the converged
// discovery set.
//
// HostExecApproved IS UNCONDITIONALLY TRUE, and that is a ruling rather than a shortcut.
// It used to carry p.MayAccessHost — a fetched pack's loophole ran nothing on the host
// until the user approved its claims at `yolo pack install`. OQ-TP9 deleted that gate
// (docs/design/trust-paths.md, 2026-09-04), so every pack whose module reaches this
// function is one the host user selected in their own config, which is the authority the
// approval was standing in for.
//
// THE FIELD IS KEPT because internal/loopholes reads it as "the caller evaluated an origin
// decision at all": a plain []*Loophole carries no gate, so gateAdmitsCrossing refuses a
// SourcePack record whose caller passed no Set rather than trusting an unvetted slice.
// That protection is against a programming error, not against a user's pack, and it is
// unaffected by the ruling.
func packLoopholeModules(loaded []*packload.Pack) []loopholes.PackModule {
	var out []loopholes.PackModule
	for _, d := range packLoopholeDecls(loaded) {
		out = append(out, loopholes.PackModule{Dir: d.Dir, HostExecApproved: true})
	}
	return out
}

// init registers the LAZY FALLBACK resolver with internal/loopholes, for the discovery
// surfaces that never stage.
//
// Three of the seven census surfaces need it, and one of them needs it on the LAUNCH path:
//
//   - config.LoopholeResolver.Known() (site 6) is consulted by loadAndValidateConfig, which
//     runs BEFORE stageRunPacks — so at that moment the staged record is still empty, and
//     without this a `loopholes.<pack-loophole>.enabled` entry would take the unknown-name
//     fallback and warn "no loophole named 'x' is installed on this machine" at EVERY
//     launch. That is docs/design/loophole-packaging.md §5.2's prerequisite, and it is the
//     same sentence a user gets when a pack genuinely failed to stage.
//   - `yolo loopholes list`/`status` (site 5) and `yolo check` (site 7) never stage at all.
//
// AN init(), because the dependency runs the wrong way for a call: internal/loopholes cannot
// import packload (loopholes → config → packload is a cycle, measured), so the resolution
// has to be pushed IN from a package that can. internal/cli/run is linked into `yolo`
// (internal/cli imports it), which is what makes one registration cover every subcommand.
//
// It resolves from the STORE and is strictly OFFLINE, like packRoot: a `yolo check` must not
// depend on a reachable git server, and an unresolvable pack contributes nothing rather than
// failing the command. The staged record supersedes it the moment staging runs, because
// staging is the authoritative view — it is what the jail actually mounts, `only`/`exclude`
// filters included.
func init() {
	loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	loopholes.SetPackSupersessionResolver(resolvePackSupersessions)
}

// resolvePackLoopholeModules resolves the configured packs' loophole modules from the pack
// STORE.
//
// Every failure is SILENT-AND-EMPTY here, which is the opposite of stagePacks' fail-closed
// contract and deliberately so: this runs behind read-only commands and behind a config
// validator, where the honest answer to "I cannot resolve your packs" is "I know of no pack
// loopholes" — never an invented one. The real diagnostics belong to the launch path, which
// fails loudly through stagePacks.
func resolvePackLoopholeModules() []loopholes.PackModule {
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		return nil
	}
	var out []loopholes.PackModule
	embedded := embeddedPacksByName()
	for _, entry := range entries {
		if entry.Embedded() {
			// AN EMBEDDED PACK CAN SHIP A LOOPHOLE, and this branch used to say it could
			// not. The official `audio` pack (loophole-packaging.md §7) is the first, and
			// the omission was measured rather than reasoned about: with `packs: ["audio"]`
			// selected, a `loopholes.audio-alsa.enabled` entry warned "no loophole named
			// 'audio-alsa' is installed on this machine" at EVERY launch — the same
			// sentence a user gets when a pack genuinely failed to stage — and
			// `yolo loopholes list` omitted it entirely. That is exactly the §5.2
			// prerequisite this resolver exists to satisfy, failing for the one pack shape
			// nobody had tried.
			//
			// The tree really does live only in the binary's embed.FS, which is what the
			// old comment got right; the answer is that packload.Embedded() materializes it
			// (once per process, cached), so there IS a path to read. Selection-gated here
			// unlike the reservation lists, because this answers "what is active on this
			// machine" rather than "what could any pack ever claim".
			p, isEmbedded := embedded[entry.Name]
			if !isEmbedded {
				continue // named an embedded pack that this build does not carry
			}
			for _, d := range packLoopholeDecls([]*packload.Pack{p}) {
				out = append(out, loopholes.PackModule{Dir: d.Dir, HostExecApproved: true})
			}
			continue
		}
		// nil getenv: these resolvers run behind read-only commands with no Options to
		// thread one from, so the store reads the real environment — which is exactly
		// right, since the staged tree it looks for is the one this process is running
		// against.
		root, rootErr := packRoot(entry, nil)
		if rootErr != nil {
			continue // never fetched, moved remote, offline — not a deactivation signal
		}
		p, probs := packload.LoadDir(root, entry.Name)
		if len(probs) > 0 || p == nil {
			continue
		}
		for _, d := range packLoopholeDecls([]*packload.Pack{p}) {
			// HostExecApproved unconditionally — see packLoopholeModules for why the field
			// survives the deletion of the gate that used to fill it.
			out = append(out, loopholes.PackModule{Dir: d.Dir, HostExecApproved: true})
		}
	}
	return out
}

// embeddedPacksByName indexes the EMBEDDED packs by name, materialized out of the binary.
//
// Through packload.Embedded() rather than a fresh MaterializeEmbedded call: it caches once
// per process, so the read-only commands behind this resolver (`yolo loopholes list`,
// `yolo check`, config validation — each of which may consult it more than once) do not each
// pay a tree copy. Its failure mode is an empty set, which matches this resolver's
// silent-and-empty contract: the honest answer to "I cannot materialize the packs" is "I
// know of no pack loopholes", never "approved".
func embeddedPacksByName() map[string]*packload.Pack {
	out := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		out[p.Name] = p
	}
	return out
}

// packSupersessions flattens the loaded packs' supersession claims into the record
// internal/loopholes consults.
//
// Loaded packs, not configured ones: a claim from a pack that failed to stage is not a fact
// about this launch, and letting it through would let an unstageable pack silently retire a
// working loophole — the failure mode the whole design exists to avoid, arrived at from the
// other side.
func packSupersessions(packs []*packload.Pack) []loopholes.PackSupersession {
	out := []loopholes.PackSupersession{}
	for _, p := range packs {
		for _, sup := range p.Supersessions() {
			out = append(out, loopholes.PackSupersession{
				Pack: p.Name, Capability: sup.Capability, Because: sup.Because,
			})
		}
	}
	return out
}

// resolvePackSupersessions is the LAZY FALLBACK, mirroring resolvePackLoopholeModules: the
// read-only surfaces (`yolo loopholes list`, `yolo check`, config validation) run before
// staging or without it, and a superseded loophole that reports itself active there would
// contradict the launch it is describing.
//
// NO ORIGIN GATE, and that asymmetry is deliberate. A module dir is gated because honoring it
// RUNS the pack's code; a supersession only ever withholds yolo's own loophole. An unapproved
// pack that turns something OFF grants itself nothing, and refusing to read the claim would
// mean `list` disagreeing with the launch about what is active. Offline and silent-and-empty
// like its sibling — the honest answer to "I cannot read the packs" is "I know of no
// supersessions", which leaves every loophole running.
func resolvePackSupersessions() []loopholes.PackSupersession {
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		return nil
	}
	var out []loopholes.PackSupersession
	embedded := embeddedPacksByName()
	for _, entry := range entries {
		var p *packload.Pack
		if entry.Embedded() {
			var ok bool
			if p, ok = embedded[entry.Name]; !ok {
				continue
			}
		} else {
			root, rootErr := packRoot(entry, nil) // read-only surface; see above
			if rootErr != nil {
				continue
			}
			loaded, probs := packload.LoadDir(root, entry.Name)
			if len(probs) > 0 || loaded == nil {
				continue
			}
			p = loaded
		}
		out = append(out, packSupersessions([]*packload.Pack{p})...)
	}
	return out
}

// packSkillSourceDirs is the pack's skills source dirs for THIS launch, honoring each
// `skills` contribution's `from` and falling back to the conventional dir.
//
// The `from` resolution and the zero-ceremony fallback both live in packload
// (Pack.SkillsSourceDirs); this wrapper exists only to print the problems, because a pack
// whose declared source is missing gets no skills and must not learn that from an empty
// destination. NO SILENT CAPS, the same rule the 0-files-staged warning above follows.
//
// Reads p.Root, which for both branches of the caller is the STAGED tree — so an
// only/exclude filter that removed the skills dir is reported here rather than surfacing as
// a pack that "does nothing".
func (o *Options) packSkillSourceDirs(p *packload.Pack) []jailcontent.PackSkillSource {
	sources, problems := p.SkillsSources()
	for _, prob := range problems {
		o.pr(o.Stdout).print("[yellow]Warning: " + prob + "[/yellow]")
	}
	// The two types are field-identical on purpose (jailcontent.PackSkillSource says why the
	// second one exists), so the conversion is a copy and there is nothing here to get wrong.
	out := make([]jailcontent.PackSkillSource, 0, len(sources))
	for _, src := range sources {
		out = append(out, jailcontent.PackSkillSource{Dir: src.Dir, Agents: src.Agents})
	}
	return out
}

// livePackSlugs is the set of staging-dir names the CURRENT config still claims.
//
// Computed from the config entries BEFORE any resolution, which is the whole point: a
// fetched pack whose mirror cannot be read this launch (offline, moved remote, never
// installed) is still CONFIGURED, and pruning it would silently discard content the user
// asked for — on every offline launch, no less. Resolution failure is reported later by
// packRoot as a fatal error naming `yolo pack install`; it is emphatically not a
// deactivation signal.
//
// embedded names are excluded because an embedded pack does not live at <root>/<slug> at
// all: it is staged under _official, which is cleared and rebuilt wholesale. Including it
// here would be harmless (no such dir exists) but would misstate the rule.
func livePackSlugs(entries []config.PackEntry, embedded map[string]*packload.Pack) map[string]bool {
	live := map[string]bool{}
	for _, entry := range entries {
		if _, isEmbedded := embedded[entry.Name]; isEmbedded && entry.Embedded() {
			continue
		}
		live[entry.Slug()] = true
	}
	return live
}

// pruneDroppedPackStaging removes the staged tree of every configured-pack slug under
// stagingRoot that `live` does not name, and returns the removed slugs sorted.
//
// CONTENTS, NEVER THE DIR (packstage rule 3, and the reason this is not a one-line
// os.RemoveAll(stagingRoot)): a running jail's /ctx/packs bind captured stagingRoot's
// inode, so removing and recreating that directory detaches the mount — the jail keeps
// reading a tree nothing writes to any more. Only per-slug children are removed here;
// stagingRoot itself is never touched, and neither is _official (which the caller has
// already rebuilt from the embed.FS).
//
// Only a REAL directory is pruned (DirEntry.IsDir is Lstat-shaped, so a symlink reports
// false and is skipped). Nothing this code writes puts a file or a link at the top level,
// so an unrecognized entry there is somebody else's — and deleting one to tidy up a mount
// source is not a trade this function is entitled to make. It also cannot render as a pack
// anyway: LoadJailPacks skips every non-directory entry.
func pruneDroppedPackStaging(stagingRoot string, live map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // nothing staged yet; the caller creates it
		}
		return nil, err
	}
	var pruned []string
	for _, e := range entries {
		name := e.Name()
		if name == officialStagingDir || !e.IsDir() || live[name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(stagingRoot, name)); err != nil {
			// FAIL-CLOSED, like the rest of stagePacks: a tree that could not be removed
			// is a tree that WILL render in the jail, so launching anyway would deliver
			// the pack the user just dropped while claiming it was gone.
			return nil, fmt.Errorf("packs: removing the staged tree of dropped pack %s: %w",
				name, err)
		}
		pruned = append(pruned, name)
	}
	sort.Strings(pruned)
	return pruned, nil
}

// packRoot resolves a pack entry to a directory on disk.
//
// LAUNCH IS STRICTLY OFFLINE (C5): it resolves from the store and never fetches. A
// jail start must not depend on a reachable git server, and a missing pin must be a
// clear error pointing at `yolo pack install` rather than a surprise network call
// mid-boot — or worse, a 30-second askpass hang that reads as yolo wedging.
//
// It passes the entry's SLUG to Resolve, which is what makes a NESTED launch work: a
// jail's inherited config names the host path a pack came from, so resolution from the
// address fails for every local pack in here and Resolve falls back to the tree the
// outer launcher delivered under YOLO_PACK_ROOT. That fallback used to exist only in
// `yolo check`, so `yolo run` refused the launch and the nested verification AGENTS.md
// mandates was impossible with a local pack selected (docs/design/storage-and-config.md
// §10). Deliberately silent here, unlike check: staging the delivered copy is the
// NORMAL case for a nested launch, not a degradation worth a line of output.
//
// getenv is threaded for testability and may be nil (the store then reads the real
// environment, which is what a launch wants).
func packRoot(entry config.PackEntry, getenv func(string) string) (string, error) {
	addr, err := packsrc.Parse(entry.Source)
	if err != nil {
		return "", fmt.Errorf("packs: %s: %w", entry.Name, err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: getenv}
	res, err := store.Resolve(addr, entry.Slug())
	if err != nil {
		return "", fmt.Errorf("packs: %s: %w", entry.Name, err)
	}
	return res.Root, nil
}

// packBriefingProses is every briefing prose this pack delivers into a JAIL — one entry per
// briefing CONTRIBUTION that resolves to content, each carrying the AUDIENCE that
// contribution named.
//
// It honors each contribution's declared `from` and warns about one that cannot be honored,
// which is what it replaced a reader for: that reader took a DIRECTORY and scanned
// `AGENTS.md`/`CLAUDE.md` unconditionally, so a pack declaring `from: "house-rules.md"` had it
// honored at the host notch and silently IGNORED here (roadmap.md §6a-4). Both readers go
// through packload, which is the same convergence `skills` needed for the same reason — three
// hardcoded conventional-source joins are how the field came to be validated and ignored.
//
// ONE ENTRY PER CONTRIBUTION, where packload.BriefingProse returns one per PACK. That
// function's own docstring records why: "the jail's composition takes one (pack, text) pair
// per pack, so a pack declaring two briefing contributions with two different `from` files
// cannot deliver both there … making the jail match would mean composing per destination,
// which is a larger change". This is that larger change (briefing-audiences.md §5), so the
// per-pack reader is no longer the right one and is no longer called from the launch path.
//
// IDENTICAL PROSE IS DELIVERED ONCE, with the audiences UNIONED and a BROADCAST absorbing
// every audience. That is not tidiness, it is the regression the per-contribution reading
// would otherwise introduce: two contributions that name no `from` both resolve to the same
// conventional AGENTS.md, so a pack naming two destinations and no source would have its prose
// composed TWICE into every briefing — something the old first-non-empty-wins reader could not
// do. Deduping on the resolved TEXT rather than on the source path is deliberate: the source a
// contribution resolved to is not returned by BriefingProseFor (its precedence is a fallback
// chain), and two files with identical content are one delivery either way.
func (o *Options) packBriefingProses(name string, p *packload.Pack) []jailcontent.PackBriefing {
	var out []jailcontent.PackBriefing
	index := map[string]int{}
	add := func(text string, agents []string) {
		text = strings.TrimRight(text, " \t\r\n")
		if text == "" {
			return
		}
		if i, seen := index[text]; seen {
			if len(out[i].Agents) == 0 || len(agents) == 0 {
				out[i].Agents = nil // a broadcast reaches everywhere an audience could
				return
			}
			out[i].Agents = append(out[i].Agents, agents...)
			return
		}
		index[text] = len(out)
		out = append(out, jailcontent.PackBriefing{Name: name, Text: text, Agents: agents})
	}
	warn := func(prob string) {
		if prob != "" {
			o.pr(o.Stdout).print("[yellow]Warning: " + prob + "[/yellow]")
		}
	}
	declared := false
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindBriefing {
			continue
		}
		declared = true
		text, prob := p.BriefingProseFor(c)
		warn(prob)
		add(text, c.Agents)
	}
	if !declared {
		// THE ZERO-CEREMONY PACK, and the fallback lives here for the reason
		// packload.BriefingProse's does: the call site that forgot it would silently drop
		// every manifest-less pack's prose. It has no manifest to name a source OR an
		// audience in, so it is a broadcast of the conventional file — which is exactly what
		// P2 promises keeps working untouched.
		text, prob := p.BriefingProseFor(packdecl.Contribution{Kind: packdecl.KindBriefing})
		warn(prob)
		add(text, nil)
	}
	return out
}

// copyTree copies a staged pack tree to dest at mode 0o644, or 0o755 for a file whose
// source carries an execute bit.
//
// The exec bit is PRESERVED here, matching packstage's rule for a configured pack: the
// trust question an exec bit raises is "may this content ship an executable at all", and
// for an embedded pack the answer is yes by construction — it ships with yolo and is
// reviewed with the release. Stripping the bit at copy time would only mean a shipped pack
// cannot carry a working script while a user's own pack can.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// Carry the source's exec bit (0o111 only — the read/write bits stay 0o644).
		// An EMBEDDED pack ships with yolo, so its content is reviewed with the release
		// and there is no third party to gate: the consumer opt-in that governs a
		// configured pack has no analogue here, and forcing 0o644 would mean a shipped
		// pack could never carry a working script while a user's own pack could.
		mode := os.FileMode(0o644)
		if fi, statErr := d.Info(); statErr == nil && fi.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return err
		}
		// Explicit chmod: WriteFile's mode is masked by umask and ignored entirely for an
		// existing file, so neither path reliably lands the exec bit on its own.
		return os.Chmod(target, mode)
	})
}

// packProviderNameConflicts is the launch half of provider-name exclusivity: one message
// per provider name shipped by more than one DECLARATION, in a deterministic order.
//
// Per declaration, not per pack, for the reason the loophole pre-flight states: the
// generic exclusive loop in packload.Collisions skips a group of one, so a single manifest
// declaring `zai` twice would be invisible there — and it is exactly as silent at the
// compose, where the second entry would replace the first. The one-pack case is refused
// at authoring time too (packdecl's validateContributions); this pass is the launch-time
// backstop for a pack that arrives already broken.
func packProviderNameConflicts(loaded []*packload.Pack) []string {
	type decl struct{ pack string }
	byName := map[string][]decl{}
	var order []string
	for _, p := range loaded {
		for _, prov := range p.Decl.Providers() {
			if _, seen := byName[prov.Name]; !seen {
				order = append(order, prov.Name)
			}
			byName[prov.Name] = append(byName[prov.Name], decl{pack: p.Name})
		}
	}
	sort.Strings(order)

	var out []string
	for _, name := range order {
		group := byName[name]
		if len(group) < 2 {
			continue
		}
		var names []string
		seen := map[string]bool{}
		for _, d := range group {
			if !seen[d.pack] {
				seen[d.pack] = true
				names = append(names, "pack "+d.pack)
			}
		}
		who := strings.Join(names, " and ")
		if len(names) == 1 {
			who = names[0] + " twice"
		}
		out = append(out, fmt.Sprintf(
			"provider %q is shipped by %s — a provider name is sole-owned: it is the key in "+
				"the composed providers table and what a profile's `provider` names, so "+
				"two shippers would each be supplying \"the\" %s and one would silently replace "+
				"the other. Drop one of the declarations; overrides of the survivor are lines "+
				"of providers.%s in user config.",
			name, who, name, name))
	}
	return out
}
