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

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
// Sets agents.SetPackSkillDirs as a side effect, which PrepareSkills consumes on the
// next call. Ordering is therefore load-bearing: stagePacks runs first.
func (o *Options) stagePacks(cname string) (string, []*packload.Pack, []agents.PackBriefing, error) {
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
	// user would only discover by looking in ~/.yolo-shims. An empty config now really does
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
		selected, probs := packload.LoadDir(dest, p.Name, true)
		for _, prob := range probs {
			return "", nil, nil, fmt.Errorf("official pack %s: %s", p.Name, prob)
		}
		loaded = append(loaded, selected)
	}

	var skillDirs []string
	var briefings []agents.PackBriefing
	for _, p := range loaded {
		skillDirs = append(skillDirs, o.packSkillSourceDirs(p)...)
		if text := o.packBriefingProse(p); text != "" {
			briefings = append(briefings, agents.PackBriefing{Name: p.Name, Text: text})
		}
	}

	// The lockfile records which fetched packs the user approved host access for, and
	// at which commit. Launch reads it (never writes it) to gate a fetched pack's host
	// access on that approval rather than on origin alone. A missing lock is normal
	// (nothing approved yet) — LoadLock returns an empty one.
	lock, lockErr := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if lockErr != nil {
		// A corrupt lock must not silently grant OR silently deny; surface it and treat
		// every fetched pack as unapproved (fail-closed) for this launch.
		o.pr(o.Stdout).print("[yellow]Warning: " + lockErr.Error() + "[/yellow]")
		lock = nil
	}

	for _, entry := range configured {
		root, err := packRoot(entry)
		if err != nil {
			return "", nil, nil, err
		}
		dest := filepath.Join(stagingRoot, entry.Slug())
		res, err := packstage.Stage(packstage.Spec{
			Root:      root,
			Dest:      dest,
			Only:      entry.Only,
			Exclude:   entry.Exclude,
			AllowExec: entry.AllowExec,
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

		// A configured pack's host access is gated: an embedded or local pack always
		// may (its origin already carries the user's authority); a FETCHED pack may
		// only for the host-access claims the user approved at `yolo pack install`,
		// recorded per-commit in the lockfile. This is the approval model that lets a
		// shared pack mount a host dir or set env, without a fetched ref silently
		// gaining access it did not have when the user last looked.
		mayHost := packMayAccessHost(entry, dest, lock)
		p, probs := packload.LoadDir(dest, entry.Name, mayHost)
		for _, prob := range probs {
			return "", nil, nil, fmt.Errorf("packs: %s", prob)
		}
		// Report every refused declaration. A pack silently not getting what it asked
		// for changes what the jail contains, so the user has to be told.
		_, refused := p.HonoredHostFiles()
		if _, mountRefused := p.HonoredMounts(); len(mountRefused) > 0 {
			refused = append(refused, mountRefused...)
		}
		// Per contribution: a pack mixing an npm install with a curl-to-shell installer
		// gets exactly one refusal, for the installer.
		if _, installRefused := p.HonoredInstalls(); len(installRefused) > 0 {
			refused = append(refused, installRefused...)
		}
		for _, msg := range refused {
			o.pr(o.Stdout).print("[yellow]Warning: " + msg + "[/yellow]")
		}
		loaded = append(loaded, p)

		skillDirs = append(skillDirs, o.packSkillSourceDirs(p)...)
		if text := o.packBriefingProse(p); text != "" {
			briefings = append(briefings, agents.PackBriefing{Name: entry.Name, Text: text})
		}
	}
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

	agents.SetPackSkillDirs(skillDirs)
	// Record the pack-contributed loophole modules for every host-side consumer, with
	// each one's origin gate already evaluated. THE convergence point
	// (docs/design/loophole-packaging.md §5.1): the seven discovery surfaces used to
	// assemble seven independent DiscoverOptions, and two of them execute host code.
	// Sequencing is already right — stagePacks runs above the backend dispatch, well
	// before assembleRunCmd and startLoopholes.
	loopholes.SetPackModules(packLoopholeModules(loaded))
	return stagingRoot, loaded, briefings, nil
}

// packLoopholeKind is the `loophole` contribution kind, spelled as a value rather than
// referenced as a packdecl constant.
//
// Deliberate, and temporary: the kind lands in internal/packdecl separately, and matching
// on the WIRE STRING lets the exclusivity pre-flight and the convergence be written,
// tested and reviewed without either half blocking on the other. `packdecl.KindLoophole`
// will equal this string by definition (a Kind IS its manifest spelling), so the switch to
// the constant is a one-token edit here with no behaviour change — and until the kind
// exists this adapter simply finds nothing, because packdecl.Decode refuses an unknown
// kind before a contribution can reach here.
const packLoopholeKind = packdecl.Kind("loophole")

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
// across pack declarations AND against the names yolo reserves for itself.
//
// FATAL for both cases, and the message NAMES BOTH SOURCES in both — which is the point.
// A collision here is not a shadowed config key or a duplicated mount: the loser's
// manifest still contributes `--add-host`, `ca_cert`, `--device`, bind mounts and
// `jail_env` to the argv while the winner's daemon is the one that runs. The user sees one
// trusted name and gets a mixture, with nothing said. That is why the pack-vs-reserved
// half exists at all: `startLoopholes` special-cases `claude-oauth-broker`, `journal` and
// `cgroup-delegate` BY NAME, so a manifest claiming one of those names is not an override,
// it is half a loophole (docs/design/loophole-packaging.md §3.1, §5.1).
//
// The reserved half is also why this cannot be a row in packload.Collisions: that takes
// []*packload.Pack, and a bundled loophole is not a pack. It is here, in the run package,
// rather than exported from packload for the second reason §3.2 measures — packload cannot
// import internal/loopholes (loopholes → config → packload is a cycle), so the reserved
// set is not nameable there.
//
// Returns one message per conflict, in a deterministic order (by name, then by pack), so
// the launch refusal is stable and testable.
func PackLoopholeNameConflicts(decls []PackLoopholeDecl) []string {
	reserved := map[string]string{}
	for _, r := range loopholes.ReservedLoopholeNames() {
		reserved[r.Name] = r.Origin
	}

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
		// PACK-VS-RESERVED first: it is the stronger refusal, and reporting it as a
		// pack-vs-pack clash as well would name the same mistake twice.
		if origin, isReserved := reserved[name]; isReserved {
			out = append(out, fmt.Sprintf(
				"loophole %q is %s, and %s claims it (%s) — a pack cannot ship a loophole "+
					"under a name yolo answers to itself: the launch would mount that "+
					"manifest's binds, devices and jail_env while running yolo's own daemon "+
					"under the same name. Rename the loophole's directory.",
				name, origin, declSources(group), declFroms(group)))
			continue
		}
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
// discovery set, with each module's ORIGIN GATE evaluated here — where the answer is
// known — rather than at the seven places that consume it.
//
// The gate is p.MayAccessHost, which packMayAccessHost already decided per pack: an
// embedded or local pack always may (its origin carries the user's own authority); a
// FETCHED pack only for the host-access claims the user approved at `yolo pack install`.
// Reusing that decision is deliberate — a loophole's doctor_cmd and host_daemon are host
// execution, which is strictly more than the host READS the gate was built for, so a pack
// that may not read the host certainly may not run a daemon on it. It is the SAME gate,
// not a second one that could disagree.
func packLoopholeModules(loaded []*packload.Pack) []loopholes.PackModule {
	byPack := map[string]bool{}
	for _, p := range loaded {
		byPack[p.Name] = p.MayAccessHost
	}
	var out []loopholes.PackModule
	for _, d := range packLoopholeDecls(loaded) {
		out = append(out, loopholes.PackModule{Dir: d.Dir, HostExecApproved: byPack[d.Pack]})
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
}

// resolvePackLoopholeModules resolves the configured packs' loophole modules from the pack
// STORE, with each one's origin gate evaluated against the same lockfile the launch uses.
//
// Every failure is SILENT-AND-EMPTY here, which is the opposite of stagePacks' fail-closed
// contract and deliberately so: this runs behind read-only commands and behind a config
// validator, where the honest answer to "I cannot resolve your packs" is "I know of no pack
// loopholes" — not a refused preflight and not, ever, a loophole treated as approved. The
// real diagnostics belong to the launch path, which fails loudly through stagePacks.
func resolvePackLoopholeModules() []loopholes.PackModule {
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		return nil
	}
	lock, lockErr := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if lockErr != nil {
		lock = nil // fail-closed: a corrupt lock approves nothing
	}
	var out []loopholes.PackModule
	for _, entry := range entries {
		if entry.Embedded() {
			// An embedded pack ships no loophole today, and its tree lives only in the
			// binary's embed.FS until staging materializes it — so there is no store path to
			// read here. Covered by the staged record on the launch path.
			continue
		}
		root, rootErr := packRoot(entry)
		if rootErr != nil {
			continue // never fetched, moved remote, offline — not a deactivation signal
		}
		p, probs := packload.LoadDir(root, entry.Name, packMayAccessHost(entry, root, lock))
		if len(probs) > 0 || p == nil {
			continue
		}
		for _, d := range packLoopholeDecls([]*packload.Pack{p}) {
			out = append(out, loopholes.PackModule{Dir: d.Dir, HostExecApproved: p.MayAccessHost})
		}
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
func (o *Options) packSkillSourceDirs(p *packload.Pack) []string {
	dirs, problems := p.SkillsSourceDirs()
	for _, prob := range problems {
		o.pr(o.Stdout).print("[yellow]Warning: " + prob + "[/yellow]")
	}
	return dirs
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

// packMayAccessHost decides whether a configured pack gets host access at launch.
//
//   - Embedded or local (file://) pack → always: its origin carries the user's own
//     authority, exactly as before the approval model existed.
//   - Fetched (git) pack → only when the lockfile records approval for EVERY
//     host-access claim the staged pack currently makes. A pack that reads nothing
//     from the host trivially passes (there is nothing to approve). A claim the user
//     has not approved (a fresh install never run through `pack install`, or a pin
//     that moved and gained access) fails closed here, and packload refuses those
//     claims with a printed notice pointing at `yolo pack install`.
//
// dest is the STAGED tree, so the claims checked are exactly what would be honored.
func packMayAccessHost(entry config.PackEntry, dest string, lock *packsrc.Lock) bool {
	if entry.MayGrantHostFiles() {
		return true // embedded or local — origin permits
	}
	// Fetched. Read the staged pack's host-access claims and check them against the
	// lockfile approval. A nil lock (missing or corrupt) approves nothing.
	p, _ := packload.LoadDir(dest, entry.Name, false)
	if p == nil {
		return false
	}
	// EVERY producer's claims, through the ONE merged helper (packload.Pack.HostAccessClaims):
	// pack.json's contributions, a wrapped plugin's code-running components, and a shipped
	// loophole's daemon/intercepts/binds/devices. Both ends of the approval must compute the
	// same union or the gate disagrees with the prompt — `pack install` approves what the
	// helper returns, so checking a hand-built subset here would grant a fetched pack's
	// plugin hooks (or its host daemon) on the strength of an approval that never mentioned
	// them. hostaccessgates_test.go fails if this line reaches for a producer directly.
	want := p.HostAccessClaims()
	if len(want) == 0 {
		// Reads nothing from the host, runs nothing on it; the gate is moot. Note what this
		// branch demands of every producer: a crossing that emits NO claim arrives here and is
		// GRANTED, so "the enumeration is total" is a precondition of this line rather than a
		// nicety. It was violated once — the `loophole` kind's first draft attached claims to
		// the daemon argv and the intercepts only, so a loophole declaring just
		// host_bind_mounts + host_devices landed here and put an arbitrary absolute host path
		// into a UID-0 jail with no prompt (loophole-packaging.md §3.3).
		return true
	}
	if lock == nil {
		return false
	}
	le, ok := lock.Get(entry.Name)
	if !ok {
		return false
	}
	return le.HostAccessApproved(want)
}

// packRoot resolves a pack entry to a directory on disk.
//
// LAUNCH IS STRICTLY OFFLINE (C5): it resolves from the store and never fetches. A
// jail start must not depend on a reachable git server, and a missing pin must be a
// clear error pointing at `yolo pack install` rather than a surprise network call
// mid-boot — or worse, a 30-second askpass hang that reads as yolo wedging.
func packRoot(entry config.PackEntry) (string, error) {
	addr, err := packsrc.Parse(entry.Source)
	if err != nil {
		return "", fmt.Errorf("packs: %s: %w", entry.Name, err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	res, err := store.Resolve(addr)
	if err != nil {
		return "", fmt.Errorf("packs: %s: %w", entry.Name, err)
	}
	return res.Root, nil
}

// packBriefingProse is the pack's briefing prose for THIS launch, honoring each briefing
// contribution's declared `from` and warning about one that cannot be honored.
//
// It replaces a reader that took a DIRECTORY and scanned `AGENTS.md`/`CLAUDE.md`
// unconditionally, so a pack declaring `from: "house-rules.md"` had it honored at the host
// notch and silently IGNORED here (outstanding-work.md §6a-4). Both readers now go through
// packload, which is the same convergence `skills` needed for the same reason — three
// hardcoded conventional-source joins are how the field came to be validated and ignored.
func (o *Options) packBriefingProse(p *packload.Pack) string {
	text, problems := p.BriefingProse()
	for _, prob := range problems {
		o.pr(o.Stdout).print("[yellow]Warning: " + prob + "[/yellow]")
	}
	return text
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
