package check

// packs.go is D1: HOST-SIDE VALIDATION of pack contributions.
//
// Composition stays in the container (ruling 3), and A12 makes a generator failure
// fatal there — so a broken pack HALTS a jail rather than warning into a running one.
// That is correct but late: the user finds out when their jail refuses to start.
//
// This section catches the same problems on the host, at `yolo check`, where erroring
// is normal and the message can be actionable. It is defense in depth, not the only
// line of defense: everything here is re-checked in the jail.
//
// It deliberately does NOT fetch. `yolo check` must work offline and must not make a
// surprise network call, so a pack that has never been installed is reported as such
// (pointing at `yolo pack install`) rather than fetched on the spot.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sectionPacks validates the configured packs.
//
// Zero packs is the NOTABLE state, not the boring one: packs are how content —
// an agent included — reaches a jail, so an empty list means a jail with nothing in
// it. This section used to return silently there, on the reasoning that a non-pack
// user should not see output for a feature they do not use; that reasoning died with
// the `agents` key, because there is no longer a non-pack way to get an agent.
//
// The notice text is config.NoPacksMessage/NoPacksGuidance, shared with the launch-time
// warning in internal/cli/run so `yolo check` and a launch cannot tell the user
// different things.
func (o *Options) sectionPacks(r *reporter) {
	// The header and the trailing blank are now UNCONDITIONAL — every branch below
	// prints something, and the separator used to be missing because the section only
	// existed for pack users (it ran straight into "Entrypoint Dry-Run").
	r.section("Packs")
	defer r.blank()

	// Per-entry problems land on warningLine as informational "Warning:" lines. Unlike
	// the launch-side notice, check does NOT suppress the empty-packs warning when they
	// occur: the problems are printed right above it here, so the two together read as
	// "these entries were skipped, and what is left is nothing" rather than as a
	// misdiagnosis.
	entries, err := config.LoadPacks(r.warningLine)
	if err != nil {
		r.fail("Loading packs: "+err.Error(), "")
		return
	}
	// HasConfiguredPack, not len(entries): the conventional local pack arrives with no config
	// line and is content, not an agent, so a home that has only that one still warrants the
	// no-agent notice. The local pack itself is still reported per-entry by the loop below.
	if !config.HasConfiguredPack(entries) {
		r.warn(config.NoPacksMessage, config.NoPacksGuidance)
		if len(entries) == 0 {
			return
		}
	}

	lock, lockErr := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if lockErr != nil {
		r.fail("Lockfile: "+lockErr.Error(), "")
		lock = &packsrc.Lock{Packs: map[string]packsrc.LockEntry{}}
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}

	configured := map[string]string{}
	for _, e := range entries {
		configured[e.Name] = e.Source
	}

	// The loaded SELECTED set, accumulated as the loop below resolves each entry. It feeds
	// the config-surface exclusivity check after the loop, which is the one footprint rule
	// the Embedded()-only check at the bottom of this function cannot answer: a user's own
	// pack declaring a surface a shipped pack owns is invisible to a check that only ever
	// looks at what yolo ships, and that is the single most likely instance of the clash
	// (docs/design/pack-config-collaboration.md R1).
	var loaded []*packload.Pack
	byName := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		byName[p.Name] = p
	}

	for _, e := range entries {
		// An EMBEDDED pack ships inside the binary, so there is nothing to fetch, resolve,
		// or stage from a store — and its synthetic "embedded:<name>" source is not an
		// address. Reporting it PASSING rather than skipping silently: a user who wrote
		// `packs: ["claude"]` should see it acknowledged here, not wonder whether the key
		// took effect.
		if e.Embedded() {
			r.ok(e.Name + ": ships with yolo")
			if p := byName[e.Name]; p != nil {
				loaded = append(loaded, p)
			}
			continue
		}
		addr, err := packsrc.Parse(e.Source)
		if err != nil {
			r.fail(e.Name+": "+err.Error(), "")
			continue
		}
		// Offline resolve: reports "never fetched" rather than fetching.
		res, err := store.Resolve(addr)
		if err != nil {
			// A SOURCE THAT IS NOT VISIBLE FROM HERE IS NOT A BROKEN PACK.
			//
			// Run inside a jail, every pack address in the config names a HOST path or a
			// host-side store — `/home/you/.dotfiles/packs/foo` is not a directory in
			// here and never will be. Reporting that as a failure told the user three of
			// their working packs were broken while the delivered copies sat in
			// /ctx/packs, which is the opposite of what a check is for.
			//
			// What was actually delivered is the STAGED TREE the launcher mounted, so ask
			// that instead. Keyed on the filesystem rather than on "am I in a jail"
			// deliberately: the question is whether a staged copy exists, which is the
			// thing that decides whether the pack works, and it cannot misfire on a host
			// (where no staged tree is mounted, so this branch never fires).
			if staged, ok := o.stagedPackDir(e.Name); ok {
				r.ok(e.Name + ": staged at " + staged)
				r.note("  source " + res0Path(addr, e.Source) + " is host-side and not visible from in here")
				if p, probs := packload.LoadDir(staged, e.Name, e.MayGrantHostFiles()); len(probs) == 0 && p != nil {
					loaded = append(loaded, p)
				}
				continue
			}
			r.fail(e.Name+": "+err.Error(), "")
			continue
		}
		// Stage into a throwaway dir with the REAL executor, so the exec-bit and
		// escaping-symlink refusals surface here instead of at boot.
		dest, err := os.MkdirTemp("", "yolo-check-pack-")
		if err != nil {
			r.fail(e.Name+": "+err.Error(), "")
			continue
		}
		defer os.RemoveAll(dest)
		staged, err := packstage.Stage(packstage.Spec{
			Root: res.Root, Dest: dest,
			Only: e.Only, Exclude: e.Exclude, AllowExec: e.AllowExec,
		})
		if err != nil {
			r.fail(e.Name+": "+err.Error(), "")
			continue
		}
		if len(staged.Staged) == 0 {
			// Not a hard failure — a pack may legitimately be empty mid-authoring —
			// but never silent, because it is nearly always a filter typo.
			r.warn(e.Name+": stages 0 files", "check its only/exclude filters")
			continue
		}
		r.ok(fmt.Sprintf("%s: %d file(s) stage", e.Name, len(staged.Staged)))
		// Load the STAGED tree, so the declarations checked are the ones a jail would
		// render. mayAccessHost matches the launch gate closely enough for a surface
		// check: origin decides whether a host READ is honored, and a config surface's
		// identity does not depend on that.
		if p, probs := packload.LoadDir(dest, e.Name, e.MayGrantHostFiles()); len(probs) == 0 && p != nil {
			loaded = append(loaded, p)
		}
	}

	// Config-surface exclusivity over the SELECTED set — the check that would otherwise be
	// learned at launch. FATAL here for the same reason the footprint collision below is: the
	// launch refuses it, so reporting it as a warning would mean `yolo check` passing on a
	// config that cannot start a jail. Over `loaded` rather than Embedded() because the
	// interesting case is a user's pack against a shipped one.
	for _, c := range packload.ConfigSurfaceCollisions(loaded) {
		r.fail("config surface "+c.Target+" has more than one owner",
			"packs "+strings.Join(c.Packs, ", ")+" — "+c.Reason)
	}

	// Drift last, so it reads as a summary rather than interleaving with per-pack
	// results. It is a WARNING, not a failure: the jail will still start, using the
	// config address — the user just has not fetched what they asked for.
	for _, d := range lock.DriftFrom(configured) {
		r.warn(d.Name+": config address changed since install",
			"locked "+d.LockedSource+", config "+d.WantedSource+" — run `yolo pack install`")
	}

	// Footprint collision check (the one-writer rule, §3.6): compute the union of
	// what packs claim and refuse a collision on a sole-owned target before boot.
	// Runs over the embedded packs — the ones with real declarations that every
	// launch includes; a configured local pack's declarations join once the
	// footprint reads a staged tree (a later phase). A collision is FATAL here
	// because it is a pack-authoring bug that would otherwise surface as a mount
	// conflict at boot with no obvious cause (§1.4).
	if cols := packload.Collisions(packload.Embedded()); len(cols) > 0 {
		for _, c := range cols {
			r.fail(fmt.Sprintf("pack footprint collision: %s %s", c.Kind, c.Target),
				"packs "+strings.Join(c.Packs, ", ")+" — "+c.Reason)
		}
	}
}

// stagedPackDir reports where a pack was actually delivered, when it was.
//
// The launcher mounts the staged tree and names it in YOLO_PACK_ROOT rather than
// hardcoding a path, because the mount point differs by backend (Apple Container).
// Absent that variable — the ordinary host case — there is no staged tree and the
// answer is no.
func (o *Options) stagedPackDir(name string) (string, bool) {
	root := o.getenv("YOLO_PACK_ROOT")
	if root == "" {
		return "", false
	}
	dir := filepath.Join(root, name)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir, true
	}
	return "", false
}

// getenv is nil-safe: several tests drive a zero Options directly rather than
// through fillDefaults, and a nil func there would panic rather than fail.
func (o *Options) getenv(key string) string {
	if o.Getenv != nil {
		return o.Getenv(key)
	}
	return os.Getenv(key)
}

// res0Path renders the address for the note above. The parsed form is the honest
// one when it has a path; the raw source string is the fallback.
func res0Path(addr packsrc.Addr, raw string) string {
	if addr.Path != "" {
		return addr.Path
	}
	return raw
}
