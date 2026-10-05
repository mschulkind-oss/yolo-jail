package run

// patchedforkline.go is a PATCHED fork's lines at a launch (docs/design/patched-forks.md §7, §8,
// PF-D11): its line in the "Forks this launch" block, which every launch prints above the dispatch,
// an attach included — the series it applies, the GOOD BUILD this machine runs it at, and the HELD
// SUFFIX while something holds the newest candidate back — and an attach's line naming the build the
// running jail was handed.
//
// OFFLINE, AND NO GIT (P4: a steady-state launch runs no git): each line reads the fork's series
// (file reads), its check record and, for an attach, the running jail's delivery record
// (forkhanded.go). The check, the replay and the build are the advance's, in the fresh-launch slot
// (forkDeliveriesFor), never here.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// patchedPacksStore is the pack store a patched fork's lines read its check record from.
func patchedPacksStore() *packsrc.Store { return &packsrc.Store{Dir: paths.PacksDir()} }

// GoodBuildLabel is a good build as a line names it: its tag and short commit, or the commit alone.
func GoodBuildLabel(g *packsrc.GoodBuild) string {
	return packsrc.ListEntry{Commit: g.Commit, Tag: g.Tag}.Label()
}

// PatchCount is "N patch(es)".
func PatchCount(n int) string { return fmt.Sprintf("%d %s", n, plural(n, "patch", "patches")) }

// FloorCopy is the host floor's installed copy of a patched fork's program, which its line names as
// what runs: the upstream commit and recipe it is a build of, and its label ("v1.1.0 (3f2a9c1e) + 2
// patches").
type FloorCopy struct{ Commit, Recipe, Label string }

// PatchedForkLine is a patched fork's line as `yolo host -- <bin>` prints it once the floor installed
// the program (docs/design/patched-forks.md §7: every fork line names the series, and a later launch
// carries the held suffix), and whether it is a warning. It names floor, the build that runs (PF-D53):
// while that is the good build, the jail's fork-block line, with rebuilds naming the act that builds
// an edited series there; otherwise that build, and the good build when the record names one.
func PatchedForkLine(f packload.Fork, floor FloorCopy, rebuilds string) (string, bool) {
	return patchedForkLineOf(f, &floor, rebuilds)
}

// patchedForkLine is a patched fork's line in the launch's fork block, and whether it is a warning
// (nothing runs, or something holds it): "fork <pack>: <bin> (in place of pack <base>'s) is a
// patched fork of <source> + N patches (series S), at <good build>", then the held suffix. rebuilds
// is the act that builds an edited series on this notch: a fresh launch, or a launch on a container
// backend for a macos-user one, which builds no fork (FP-D3).
func patchedForkLine(p packload.ForkPin, rebuilds string) (string, bool) {
	return patchedForkLineOf(p.Fork, nil, rebuilds)
}

// patchedForkLineOf is patchedForkLine, of floor when the host floor's copy is what runs (nil in a
// jail's fork block, where the good build is).
func patchedForkLineOf(f packload.Fork, floor *FloorCopy, rebuilds string) (string, bool) {
	head := "fork " + f.Pack + ": " + f.Bin + " (in place of pack " + f.Base + "'s) is a patched fork of " + f.Source
	series, err := f.ReadSeries()
	if err != nil {
		return head + " — its patch series cannot be read: " + err.Error(), true
	}
	head += " + " + PatchCount(series.Len()) + " (series " + series.ShortDigest() + ")"
	if config.InJail() {
		return head + " — checked and built on the host", false
	}
	rec, err := LoadPatchedRecord(patchedPacksStore(), f, series)
	var g *packsrc.GoodBuild
	if err == nil {
		g = rec.Good
	}
	if floor != nil && (g == nil || g.Commit != floor.Commit || g.Recipe != floor.Recipe) {
		return floorCopyLine(f, head, *floor, g)
	}
	if g == nil {
		return head + " — no build of it on this machine yet", true
	}
	line := head + ", at " + GoodBuildLabel(g)
	recipe := packdecl.ForkSourcePatchedRecipe(f.Source, f.Build, f.Produces, series.Digest)
	if g.Recipe != recipe {
		return line + " with another series or build recipe — " + rebuilds + " builds the edited one", true
	}
	in, _, _, _ := f.CheckWant(series).Inputs()
	if why := HeldSuffix(f, rec, in, series.Digest, recipe); why != "" {
		return line + "; " + why, true
	}
	return line, false
}

// floorCopyLine is the host's line while the floor runs a build the check record does not name as its
// good build (PF-D53): the copy a failed install of a moved good build keeps (PF-D8), named with that
// good build; or, with no good build on the record, the one the floor installed from the capture
// store, which a lost record costs nothing of (§6.2).
func floorCopyLine(f packload.Fork, head string, floor FloorCopy, g *packsrc.GoodBuild) (string, bool) {
	at, _, _ := strings.Cut(floor.Label, " + ")
	line := head + ", at " + at
	if g != nil {
		return line + " — its good build " + GoodBuildLabel(g) + " is not installed, so `yolo host` runs this build " +
			"until it is", true
	}
	if hold := PatchedForkHold(f); hold != "" {
		return line + "; held at " + at + ": " + hold + ", so no launch checks its upstream", true
	}
	return line, false
}

// HeldSuffix is the HELD suffix a patched fork's line carries while something holds its newest
// candidate back (§8), naming the next step, or "": what the record says stopped it, or the
// `agent_updates` hold, with nothing to replay.
func HeldSuffix(f packload.Fork, rec *packsrc.CheckRecord, in packsrc.CheckInputs, series, recipe string) string {
	at := "held at " + GoodBuildLabel(rec.Good)
	if h := rec.HeldAt(in, series, recipe); h != nil {
		switch h.Kind {
		case packsrc.OutcomeConflict:
			return at + ": upstream " + h.Entry.Label() + " does not take " + h.Member +
				" — `yolo pack rebase " + shquote.QuoteDisplay(f.Key()) + "`"
		case packsrc.OutcomeBuildFailed:
			return at + ": the build of upstream " + h.Entry.Label() + " failed — `yolo capture " + f.CaptureArg() +
				"` retries it now"
		case packsrc.HeldByApplyError:
			return at + ": the series could not be replayed at upstream " + h.Entry.Label() + " (" + h.Error +
				") — the next check, in an hour, retries it, or `yolo pack update` now"
		default:
			return at + ": " + h.Error
		}
	}
	if hold := PatchedForkHold(f); hold != "" {
		return at + ": " + hold + ", so no launch checks its upstream"
	}
	return ""
}

// PatchedForkHold is what holds a patched fork's upstream at its good build, "" when nothing does:
// `agent_updates` off for the fork pack or for its base (PF-D19) — or, for a patched extension, for
// its contributing pack, its owning agent pack or a fork of the owner's programs (PPX-D9).
func PatchedForkHold(f packload.Fork) string {
	wire := config.AgentUpdatesWire()
	for _, pack := range f.HoldPacks() {
		if !entrypoint.PackPolicyAllows(wire, pack) {
			return "`agent_updates` holds pack " + pack
		}
	}
	return ""
}

// noteAttachHandedBuilds is an attach's lines for what its jail was handed, from the running jail's
// delivery record, read ONCE for both of its halves: each patched fork's (noteAttachForkBuilds) and
// each patched extension's (noteAttachTreeBuilds). A record that cannot be read is said once, with
// what follows. Silent for a jail whose tree this attach could not find, and for one launched before
// delivery records.
func (o *Options) noteAttachHandedBuilds(view attachPackView) {
	if view.unfound != "" || view.unreadable || view.staged.root == "" {
		return
	}
	f, err := readHandedFile(view.staged.root)
	if err != nil {
		o.pr(o.Stderr).printf("[yellow]Warning: could not read what this jail was handed for its forks and "+
			"patched extensions (%v)[/yellow] — the jail itself is unaffected, and this attach cannot say which "+
			"builds it runs; the next fresh launch, once this jail stops, writes a new one", err)
		return
	}
	if f == nil {
		return
	}
	o.noteAttachForkBuilds(f.Forks)
	o.noteAttachTreeBuilds(f.Trees)
}

// noteAttachForkBuilds is an attach's line for each patched fork its jail was handed (§7: "An
// attach says what the running jail runs"), from the running jail's delivery record: the build it
// runs, and, when this machine's good build has moved since the jail booted, that the next fresh
// launch, once this jail stops, runs that one.
func (o *Options) noteAttachForkBuilds(handed map[string]HandedFork) {
	bins := make([]string, 0, len(handed))
	for bin := range handed {
		bins = append(bins, bin)
	}
	sort.Strings(bins)
	out := o.pr(o.Stderr)
	for _, bin := range bins {
		h := handed[bin]
		if h.Fork == "" {
			continue // a plain fork's program: its pin, in the fork block above, is what it runs
		}
		if h.Key == "" {
			out.printf("[yellow]this jail has no %s from fork %s: %s[/yellow]", bin, h.Fork, richtext.Escape(h.Reason))
			continue
		}
		runs := packsrc.ListEntry{Commit: h.Commit, Tag: h.Tag}.Label() + " + " + PatchCount(h.Patches)
		line := "this jail runs fork " + h.Fork + " at " + runs
		if rec, err := patchedPacksStore().LoadCheckRecord(h.Fork); err == nil && rec.Good != nil && rec.Good.Entry != h.Key {
			line += "; " + GoodBuildLabel(rec.Good) + " + " + PatchCount(rec.Good.Patches) + " is built, and the " +
				"next fresh launch, once this jail stops, runs it"
		}
		out.printf("[dim]%s[/dim]", richtext.Escape(line))
	}
}
