package run

// packrefresh.go is where a HOST launch fetches and refreshes its git packs, before any
// pack is resolved or staged (maintainer ruling, 2026-09-25). The policy — the ref decides
// what moves — is packsrc.Store.Refresh's; this file is only the selection of WHICH packs,
// the in-jail exemption, and the printing.

import (
	"fmt"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// RefreshConfiguredPacks runs the launch-time refresh for every configured git pack:
// fetch one that was never fetched, probe a branch at every launch and fetch it only when it moved
// (all mirrors at once, under one deadline: a check that does not finish runs the held copy and
// warns), never re-fetch a tag or a full commit SHA, check each resolved commit out, and record it in the lockfile.
//
// say gets each DISCLOSURE ("Fetched pack …", "Updated pack …") and warn each non-fatal
// problem (a failed fetch with a cached copy in use, a lockfile that could not be
// written). Neither is optional at a call site: the disclosure is the trust boundary for a
// pack that runs host code, and a launch has no quiet mode.
//
// IT NEVER FAILS A LAUNCH. A pack it could not make usable is left to resolution, which
// fails fatally by name exactly as it always has — and whose message now carries this
// refresh's fetch error (packsrc.Store's fetch-failure record).
//
// IN-JAIL IT DOES NOTHING: the jail has no pack store and no git credentials, and
// resolution in there reads the tree the outer launcher staged. Embedded and file:// packs
// never reach the network — they are not handed to the store at all.
//
// EXPORTED FOR THE HOST NOTCH (`yolo host -- <bin>` and `yolo host apply`), which must
// fetch the way a launch fetches. Read-only surfaces — `yolo check`, the agent footer,
// config validation — must never call it.
func RefreshConfiguredPacks(say, warn func(string)) {
	refreshConfiguredPacks(say, warn, nil)
}

// refreshConfiguredPacks is RefreshConfiguredPacks with the git step bracketed by
// during: called just before the refresh runs (it can fetch over the network, for
// up to LaunchFetchTimeout per mirror) with the refresh's lock-wait notice, it
// returns the function that closes the bracket. nil brackets nothing.
func refreshConfiguredPacks(say, warn func(string), during func() (waiting func(string), tail packsrc.TailOpener, end func())) {
	if config.InJail() {
		return
	}
	// Silent loader warnings: stagePacks prints them, once, with the rest of the launch.
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		return // the config error is the launch's to report, where it reports the others
	}
	// Before anything reads a pin: a pack renamed since the last launch finds its records under
	// its new handle (docs/reference/pack-system.md#pack-identity), so the refresh below neither re-resolves
	// it nor prunes its old entry.
	ReconcilePackRecords(entries, say, warn)
	var git []packsrc.RefreshPack
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		if e.Embedded() || e.IsLocal() {
			continue
		}
		if addr, err := e.PackAddress(); err != nil || addr.IsLocal() {
			continue // a bad address is reported by resolution, by name
		}
		git = append(git, e.RefreshSpec(false))
	}
	if len(git) == 0 {
		return
	}
	waiting, end := say, func() {}
	var tail packsrc.TailOpener
	if during != nil {
		waiting, tail, end = during()
	}
	check, cancelCheck := packCheckStart()
	outcomes, err := launchStore().Refresh(git, packsrc.RefreshOptions{
		LockPath: packsrc.LockPath(paths.UserConfigPath()),
		Waiting:  waiting,
		Check:    check,
		Tail:     tail,
	})
	cancelCheck()
	end()
	sayOutcomes(outcomes, say, warn)
	if err != nil {
		warn("packs: could not record the refreshed packs in the lockfile: " + err.Error())
	}
}

// sayOutcomes prints what a launch's refresh did: each pack's disclosure and warning, and, for a pack with a
// `local` clone, the line that says which copy runs, always, since its bytes may be uncommitted work, which is a
// disclosure (docs/design/local-pack-clones.md §4.4). A line that is a warning goes to warn.
func sayOutcomes(outcomes []packsrc.Outcome, say, warn func(string)) {
	for _, o := range outcomes {
		if line := o.Disclosure(); line != "" {
			say(line)
		}
		if line := o.Warning(); line != "" {
			warn(line)
		}
		if line, loud := o.LocalLine(); line != "" {
			if loud {
				warn(line)
			} else {
				say(line)
			}
		}
	}
}

// packCheckStart opens the launch's pack-check phase: one packsrc.PackCheckDeadline for every held
// mirror's lock wait, probe and fetch, checked at the same time (docs/design/local-pack-clones.md
// LPC-D3). A var only so a test can hand the phase a context it controls; nothing in production assigns it.
var packCheckStart = packsrc.NewPackCheck

// launchStore is the pack store a launch-time refresh fetches through: the launch's
// budget per fetch (LaunchFetchTimeout rather than the store's 2-minute default), and
// Detached, so ssh cannot stop a launch at a host-key or passphrase prompt and a timeout
// kills git's transport helper along with git (packsrc.Store.Detached says why).
func launchStore() *packsrc.Store { return packsrc.LaunchStore(paths.PacksDir()) }

// refreshPacks is the launch's call, printing through the launch's own console.
//
// A --dry-run launch skips it: a dry run materializes nothing (which is why it is exempt
// from the repo-root gate), and a fetch, a checkout and a lockfile rewrite are all
// materializing. A dry run's resolution then reads the store as it stands, the way `yolo
// check` does.
func (o *Options) refreshPacks() {
	if o.DryRun {
		return
	}
	refreshConfiguredPacks(
		func(line string) { o.pr(o.Stderr).print(line) },
		func(line string) { o.pr(o.Stderr).print("[yellow]Warning: " + line + "[/yellow]") },
		// A fetch (a probe per branch pack at every launch) is a
		// network round trip bounded at a minute, so it gets a progress line. The
		// lock-wait notice goes THROUGH the line, so it cannot tear it.
		func() (func(string), packsrc.TailOpener, func()) {
			span := o.Perf.Span("launch.refresh_configured_packs")
			line := o.progressConfigCovering("launch.refresh_configured_packs").Start(o.Stderr, "Checking fetched packs for updates")
			return line.Println, lineTailOpener(line), func() { line.Done(""); span.End() }
		},
	)
}

// ReconcilePackRecords moves the selected packs' records to the handles they are selected under
// (configmigrate.ReconcilePackRecords), saying each pack whose records moved or started over. A
// failure is a warning: the records stay where they were, and the next read tries again.
func ReconcilePackRecords(entries []config.PackEntry, say, warn func(string)) {
	lines, err := configmigrate.ReconcilePackRecords(filepath.Dir(paths.UserConfigPath()),
		configmigrate.IdentifiedPacks(entries))
	for _, l := range lines {
		say(l)
	}
	if err != nil {
		warn(fmt.Sprintf("could not move the pack records of a renamed or moved pack (%v); the next launch "+
			"or `yolo pack install` tries again, and until then that pack re-pins as a new one", err))
	}
}
