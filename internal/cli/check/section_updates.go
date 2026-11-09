package check

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
)

// sectionUpdates reports the last update check's cached answer: whether this
// yolo is current, or why nobody knows. It never checks itself — the dispatch
// hook may start a background release check — so a report can be up to a day
// old, and it says how old.
//
// It is also where a cached failure stays visible after the check that produced
// it, since a detached release check has no terminal to report to.
func (o *Options) sectionUpdates(r *reporter) {
	if o.inJail() {
		return // the jail's yolo is the host's; the host checks it
	}
	ch := o.UpdateChannel()
	if ch.Kind == selfupdate.KindUnknown {
		return // nothing identifies the install, so there is nothing to check
	}
	r.sectionHeader("Updates")
	if ch.Kind == selfupdate.KindSnapshot {
		// A snapshot of a checkout has no release to compare against
		// (docs/design/installed-launcher-provenance.md, OQ-IP1).
		r.skip(selfupdate.SnapshotSummary, selfupdate.SnapshotSteps)
		return
	}
	if !selfupdate.Enabled(o.Getenv) || !o.UpdateCheckEnabled() {
		r.skip("the update check is off",
			"update_check: false, YOLO_NO_UPDATE_CHECK, or CI. `yolo update --check` still checks on request.")
		return
	}
	st := selfupdate.LoadState(o.UpdateStatePath)
	if st.Identity != ch.Identity() || st.CheckedAt.IsZero() {
		r.skip("no update check has run for this build yet", "`yolo update --check` runs one now.")
		return
	}
	age := humanAge(o.Now().Sub(st.CheckedAt))
	switch {
	case st.Error != "":
		r.warn(fmt.Sprintf("the last update check failed (%s ago): %s", age, st.Error),
			"`yolo update --check` retries now.")
	case st.Available:
		remedy := "yolo update"
		switch ch.Kind {
		case selfupdate.KindArchive:
			remedy = selfupdate.ReleasesPage
		case selfupdate.KindGoInstall, selfupdate.KindPipx, selfupdate.KindUV:
			remedy = "Reinstall yolo from Homebrew or a release archive: " + selfupdate.ReleasesPage + "."
		}
		r.warn(strings.TrimSuffix(selfupdate.Notice(st), " — run `yolo update`")+" (checked "+age+" ago)", remedy)
	default:
		r.ok(fmt.Sprintf("up to date as of %s ago (%s via %s)", age, ch.Version, ch.Kind))
	}
}

// humanAge renders a duration the way a person reads a timestamp's age.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour")
	}
	return plural(int(d/(24*time.Hour)), "day")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
