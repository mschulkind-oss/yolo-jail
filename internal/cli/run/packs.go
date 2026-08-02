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

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

	officialRoot := filepath.Join(stagingRoot, "_official")
	// Clear it: a pack DROPPED from config must stop being mounted, and a leftover tree
	// would keep rendering as if it were still selected.
	if err := os.RemoveAll(officialRoot); err != nil {
		return "", nil, nil, err
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
		if skills := filepath.Join(p.Root, "skills"); isDir(skills) {
			skillDirs = append(skillDirs, skills)
		}
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
		if _, why := p.HonoredInstall(); why != "" {
			refused = append(refused, why)
		}
		for _, msg := range refused {
			o.pr(o.Stdout).print("[yellow]Warning: " + msg + "[/yellow]")
		}
		loaded = append(loaded, p)

		if skills := filepath.Join(dest, "skills"); isDir(skills) {
			skillDirs = append(skillDirs, skills)
		}
		if text, ok := readPackBriefing(dest); ok {
			briefings = append(briefings, agents.PackBriefing{Name: entry.Name, Text: text})
		}
	}
	agents.SetPackSkillDirs(skillDirs)
	return stagingRoot, loaded, briefings, nil
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
	want := p.Decl.HostAccessClaims()
	if len(want) == 0 {
		return true // reads nothing from the host; the gate is moot
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
