package prune

import (
	"fmt"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// miseWalkBudget bounds the sizing walk over the candidate versions: the same 60 s every other
// walk of the design gets (disk-levers-and-backfill.md §5.3), past which a figure is a lower bound.
const miseWalkBudget = 60 * time.Second

// renderMiseVersions is `yolo prune`'s section for the unused tool versions (miseversions.go),
// and returns the sweep so the caller can total it and fail on a decline.
//
// A WAIT IS NOT A DECLINE. For the first 30 days of recording nothing can be judged, and saying
// so is information, not a failure: a `yolo prune` that exited non-zero on every machine for a
// month after an upgrade would teach everyone to ignore its status.
func renderMiseVersions(p *printer, opts Options, store string, live runtime.LiveSet, apply bool) MiseSweep {
	p.line("")
	p.line(fmt.Sprintf("[bold]Unused tool versions[/bold]  (the shared mise store; no jail has used them for %d d)",
		int(MiseVersionsWindow.Hours()/24)))
	if opts.InJail() {
		p.line("  [dim]skipped — this jail's tools come from the host's store, and a jail cannot see " +
			"which of the host's jails are running; run `yolo prune` on the host[/dim]")
		return MiseSweep{}
	}
	if store == "" {
		p.line("  [dim]skipped — on a Mac the jails' tool store is a volume inside the container VM " +
			"(or the sandbox account's, on macos-user), which yolo cannot reach from here[/dim]")
		return MiseSweep{}
	}
	s := FindUnusedMiseVersions(store, live, opts.Now(), miseWalkBudget)
	switch {
	case s.Declined != "":
		p.line("  [bold red]FAILED — " + s.Declined + ", so nothing there was swept.[/bold red]")
		if s.Remedy != "" {
			p.line("  [dim]to fix: " + s.Remedy + "[/dim]")
		}
		return s
	case s.Waiting != "":
		p.line("  [dim]not yet — " + s.Waiting + "[/dim]")
		return s
	}
	if apply {
		s = PruneUnusedMiseVersionsGuarded(s, opts.Now(), nil)
	}
	shown, bytes := s.Candidates, s.Bytes
	if apply {
		shown, bytes = s.Removed, s.RemovedBytes
	}
	if len(shown) == 0 && len(s.Failed) == 0 {
		p.line(fmt.Sprintf("  [dim]none — every one of the %d installed version(s) was used by a jail "+
			"within %d days, or installed since[/dim]", s.Installed, int(MiseVersionsWindow.Hours()/24)))
		return s
	}
	lower := ""
	if s.Partial {
		lower = "≥ "
	}
	p.line(fmt.Sprintf("  %s: %s%s across %s version(s)", verb(apply, "would remove", "removed"),
		lower, FmtBytes(bytes), fmtComma(len(shown))))
	for _, v := range shown {
		if v.leftover {
			p.line(fmt.Sprintf("    • %s  [dim]finished an interrupted removal[/dim]", v.Rel))
			continue
		}
		p.line(fmt.Sprintf("    • %s  %s  [dim]%s[/dim]", v.Display(), FmtBytes(v.Bytes), lastUsedPhrase(v)))
	}
	for _, v := range s.Failed {
		p.line(fmt.Sprintf("    • %s  [yellow](NOT removed: %s)[/yellow]", v.Display(), v.Err))
	}
	if s.Partial {
		p.line("  [dim]sizes are a lower bound: the walk hit its time budget[/dim]")
	}
	return s
}

// lastUsedPhrase says why a version is unused, in the record's own terms.
func lastUsedPhrase(v MiseVersion) string {
	if v.LastUsed.IsZero() {
		return "no jail's record names it; installed " + v.Changed.Local().Format("2006-01-02")
	}
	return "last used " + v.LastUsed.Local().Format("2006-01-02")
}

// miseVersionsCategory is the sweep's line in `yolo prune --format json`: what a dry run would
// remove, or what an applied one did.
func miseVersionsCategory(s MiseSweep, apply bool) ReportCategory {
	c := ReportCategory{Name: "mise_versions", Bytes: s.Bytes, Count: len(s.Candidates), Unit: "versions"}
	if apply {
		c.Bytes, c.Count = s.RemovedBytes, len(s.Removed)
	}
	return c
}
