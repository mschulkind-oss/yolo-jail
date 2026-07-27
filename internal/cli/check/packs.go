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

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
	if len(entries) == 0 {
		r.warn(config.NoPacksMessage, config.NoPacksGuidance)
		return
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

	for _, e := range entries {
		// An EMBEDDED pack ships inside the binary, so there is nothing to fetch, resolve,
		// or stage from a store — and its synthetic "embedded:<name>" source is not an
		// address. Reporting it PASSING rather than skipping silently: a user who wrote
		// `packs: ["claude"]` should see it acknowledged here, not wonder whether the key
		// took effect.
		if e.Embedded() {
			r.ok(e.Name + ": ships with yolo")
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
	}

	// Drift last, so it reads as a summary rather than interleaving with per-pack
	// results. It is a WARNING, not a failure: the jail will still start, using the
	// config address — the user just has not fetched what they asked for.
	for _, d := range lock.DriftFrom(configured) {
		r.warn(d.Name+": config address changed since install",
			"locked "+d.LockedSource+", config "+d.WantedSource+" — run `yolo pack install`")
	}
}
