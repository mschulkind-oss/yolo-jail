package run

// packrefresh.go is where a HOST launch fetches and refreshes its git packs, before any
// pack is resolved or staged (maintainer ruling, 2026-09-25). The policy — the ref decides
// what moves — is packsrc.Store.Refresh's; this file is only the selection of WHICH packs,
// the in-jail exemption, and the printing.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// RefreshConfiguredPacks runs the launch-time refresh for every configured git pack:
// fetch one that was never fetched, re-fetch a branch at most hourly, never re-fetch a tag
// or a full commit SHA, check each resolved commit out, and record it in the lockfile.
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
	if config.InJail() {
		return
	}
	// Silent loader warnings: stagePacks prints them, once, with the rest of the launch.
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		return // the config error is the launch's to report, where it reports the others
	}
	var git []packsrc.RefreshPack
	for _, e := range entries {
		if e.Embedded() || e.IsLocal() {
			continue
		}
		if addr, err := packsrc.Parse(e.Source); err != nil || addr.IsLocal() {
			continue // a bad address is reported by resolution, by name
		}
		git = append(git, packsrc.RefreshPack{Name: e.Name, Source: e.Source})
	}
	if len(git) == 0 {
		return
	}
	outcomes, err := launchStore().Refresh(git, packsrc.RefreshOptions{
		LockPath: packsrc.LockPath(paths.UserConfigPath()),
		Waiting:  say,
	})
	for _, o := range outcomes {
		if line := o.Disclosure(); line != "" {
			say(line)
		}
		if line := o.Warning(); line != "" {
			warn(line)
		}
	}
	if err != nil {
		warn("packs: could not record the refreshed packs in the lockfile: " + err.Error())
	}
}

// launchStore is the pack store a launch-time refresh fetches through: the launch's
// budget per fetch (LaunchFetchTimeout rather than the store's 2-minute default), and
// Detached, so ssh cannot stop a launch at a host-key or passphrase prompt and a timeout
// kills git's transport helper along with git (packsrc.Store.Detached says why).
func launchStore() *packsrc.Store {
	return &packsrc.Store{Dir: paths.PacksDir(), Timeout: packsrc.LaunchFetchTimeout, Detached: true}
}

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
	RefreshConfiguredPacks(
		func(line string) { o.pr(o.Stdout).print(line) },
		func(line string) { o.pr(o.Stdout).print("[yellow]Warning: " + line + "[/yellow]") },
	)
}
