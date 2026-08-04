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
		if text, ok := readPackBriefing(p.Root); ok {
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
		if text, ok := readPackBriefing(dest); ok {
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

	agents.SetPackSkillDirs(skillDirs)
	return stagingRoot, loaded, briefings, nil
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
	// The pack's own claims PLUS any wrapped plugin's code-running components. Both halves,
	// or the gate disagrees with the prompt: `pack install` approves the merged set, so
	// checking only the contributions here would grant a fetched plugin's hooks on the
	// strength of an approval that never mentioned them.
	want := append(p.Decl.HostAccessClaims(), p.PluginHostAccessClaims()...)
	sort.Strings(want)
	if len(want) == 0 {
		return true // reads nothing from the host, runs nothing on it; the gate is moot
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

// readPackBriefing reads a pack's briefing prose, accepting either AGENTS.md or
// CLAUDE.md at the pack root. Both names are in the wild, and a pack author should
// not have to know which one yolo happens to read.
func readPackBriefing(dest string) (string, bool) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if data, err := os.ReadFile(filepath.Join(dest, name)); err == nil {
			return string(data), true
		}
	}
	return "", false
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
