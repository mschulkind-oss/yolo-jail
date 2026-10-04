package cli

// patchedadvance.go is a PATCHED fork's ADVANCE (docs/design/patched-forks.md §6, §7; PF-D8,
// PF-D10, PF-D20, PF-D23, PF-D25): the act that takes a candidate to a GOOD BUILD — check the
// upstream (throttled), replay the series down the walk's list on the host, build the newest fit in
// the sealed capture jail, and, once that build is admitted, move the good build — and the decision
// of what the jail is handed.
//
// THE TERMS are the design's, defined there: the CHECK reads the upstream for a newer candidate;
// the WALK'S LIST is the entries an advance may build, newest first; the REPLAY makes the series
// commits at its base and picks them onto an entry; the NEWEST FIT is the first entry the series
// replays onto cleanly; the GOOD BUILD is this machine's record of the last build of the fork it
// admitted; a candidate is PENDING when the advance should build it; HELD means what runs is not
// following.
//
// # The ratchet, in one rule
//
// What runs moves only once a build is admitted (P2): a newer upstream that does not apply or
// build leaves the good build serving (PF-D8). The good build SERVES when its series digest and
// recipe are the manifest's and its store entry is there; a user's own edit to the series, `build`
// or `produces` is not held (PF-D23): nothing serves until the edited series builds, at the newest
// fit or, failing that, at its own base.
//
// # Who runs it, and how a Ctrl-C ends it
//
//   - A FRESH JAIL LAUNCH, in its fork-build slot (run's forkDeliveriesFor, through
//     buildForksForLaunch), never on an attach or in a jail. It waits for the advance (PF-D25):
//     with a good build serving, under an INTERRUPT SCOPE (run.InterruptScope), so a Ctrl-C ends
//     the advance — the check's and the walk's git, the build lock's wait, and the build jail,
//     which runs as a child process of its own for that reason (forkbuildchild.go) — and the jail
//     starts on the good build; the build itself is bounded at forkBuildWaitBound. A FIRST
//     advance has nothing to start on, so it runs as a plain fork's build does and a Ctrl-C ends
//     the launch (§7).
//   - `yolo capture <bin>` (force): the check forced, the pending candidate built ignoring a
//     back-off, and with none pending the good build's own inputs rebuilt; through the swap.
//   - The host floor's install is §14 step 4's (S4): advanceOptions is its seam.
//
// # Locks (§6.6, PF-D17)
//
// The build's lock, then the fork's record lock, then the mirror lock, and nothing waits for an
// earlier one while it holds a later one: the check takes the record lock and the mirror's inside
// it (packsrc.CheckPatched); the walk takes the mirror's alone; the build act takes the build lock
// and, inside it, the waiter's re-read and the replay into src/ (the mirror's); the move, the
// failure record, the hand and the reap run under the record lock alone, after the build.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// advanceOptions is who runs an advance, and how.
type advanceOptions struct {
	// platform is the build's platform: the jail's.
	platform string
	// runtime is the backend the jail runs on ("" when not a launch), for the line a build jail the
	// runtime would not start gets (Apple Container's, §9).
	runtime string
	// workspace is the launch's, whose launch.log a failed build's line names; "" for none.
	workspace string
	out, errw io.Writer
	color     bool
	// launch is a fresh jail launch's advance: the check throttled, a back-off honored, the wait
	// interruptible while a good build serves.
	launch bool
	// force is `yolo capture <bin>`'s: the check forced, a back-off ignored, the good build's own
	// inputs rebuilt when nothing is pending, the build lock not waited for.
	force bool
	// hand records what the launch hands its jail (run.ForkBuildRequest.Hand), under the record
	// lock; nil records nothing.
	hand func(bin string, h run.HandedFork) error
}

// advanceResult is what an advance did.
type advanceResult struct {
	// delivery is what the jail is handed: the good build's store key, or why there is none.
	delivery entrypoint.ForkDelivery
	// built is true when this advance admitted a build (`yolo capture`'s success).
	built bool
	// failed is true when the act's own work failed (`yolo capture`'s exit status): a build, a
	// replay, a check that names nothing, or no fit.
	failed bool
	// lost is true when this advance's admitted build lost the swap to a newer check's (§6.1).
	lost bool
}

// patchedNow and patchedYoloVersion (patchedfork.go) are the advance's clock and yolo version too.

// patchedAdvanceStore is the pack store an advance checks and replays through: the launch's (its
// fetch budget, and no terminal for git) for a launch, the user's terminal for `yolo capture`.
var patchedAdvanceStore = func(launch bool) *packsrc.Store {
	if launch {
		return packsrc.LaunchStore(paths.PacksDir())
	}
	return patchedForkStore()
}

// advance is one advance's state.
type advance struct {
	f      packload.Fork
	o      advanceOptions
	pr     richtext.Printer
	epr    richtext.Printer // errw's, for the warnings
	packs  *packsrc.Store
	store  *capture.Store
	series *packsrc.Series
	recipe string
	in     packsrc.CheckInputs
	repo   string
	subdir string
	yolo   string
	ctx    context.Context // the interrupt scope's, Background outside one
	// serving is the good build the advance started with, when it serves (its store entry).
	serving *capture.Entry
	rec     *packsrc.CheckRecord
	// boundHit is set by the child build runner when the build ran past forkBuildWaitBound.
	boundHit bool
}

// advancePatchedFork runs f's advance and returns what its jail is handed.
func advancePatchedFork(f packload.Fork, o advanceOptions) advanceResult {
	a := &advance{f: f, o: o, pr: richtext.Printer{W: o.out, Color: o.color}, epr: richtext.Printer{W: o.errw, Color: o.color},
		packs: patchedAdvanceStore(o.launch), store: &capture.Store{Dir: paths.CapturesDir()},
		yolo: patchedYoloVersion(), ctx: context.Background(), repo: mustRepo(f.Source), subdir: subdirOf(f.Source)}
	series, err := f.ReadSeries()
	if err != nil {
		// THE SERIES CANNOT BE READ (§8.1): nothing serves under PF-D23, since the manifest's recipe
		// is not known, and the reason names the file and its fix.
		a.warn("fork %s: %v", f.Key(), err)
		return a.handReason(fmt.Sprintf("fork %s's patch series cannot be read (%v)", f.Key(), err))
	}
	a.series = series
	a.recipe = forkBuild{Fork: f, Series: series}.recipe()
	a.in, _, _, _ = f.CheckWant(series).Inputs()
	a.rec = a.loadOrRecover()
	if a.rec != nil && a.rec.Good != nil && a.rec.Good.Recipe == a.recipe {
		a.serving = a.exactGood(a.rec.Good)
	}
	if !o.launch || a.serving == nil {
		return a.run()
	}
	// A GOOD BUILD SERVES, so a Ctrl-C ends this advance and the jail starts on it (PF-D25).
	var res advanceResult
	sig := run.InterruptScope(func(ctx context.Context) {
		a.ctx = ctx
		a.packs.Ctx = ctx
		res = a.run()
	})
	if sig != nil && !res.built {
		// Every step that saw the interrupt ended in finish, which handed the good build.
		a.pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("fork %s: the advance was interrupted — this "+
			"jail starts on the good build %s; the next fresh launch tries again", f.Key(), a.goodLine())))
	}
	return res
}

// interrupted reports whether the interrupt scope's Ctrl-C has ended this advance.
func (a *advance) interrupted() bool { return a.ctx.Err() != nil }

func (a *advance) warn(format string, args ...any) {
	a.epr.Printf("[yellow]⚠ %s[/yellow]", richtext.Escape(fmt.Sprintf(format, args...)))
}

func (a *advance) say(format string, args ...any) {
	a.pr.Printf("%s", richtext.Escape(fmt.Sprintf(format, args...)))
}

func (a *advance) dim(format string, args ...any) {
	a.pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf(format, args...)))
}

// goodLine is the good build as the lines name what keeps running: "<label> + N patches".
func (a *advance) goodLine() string {
	g := a.rec.Good
	return run.GoodBuildLabel(g) + " + " + run.PatchCount(g.Patches)
}

// run is the advance proper, from the check to the hand.
func (a *advance) run() advanceResult {
	f := a.f
	good := a.goodBuild()
	edited := good != nil && good.Recipe != a.recipe
	hold := run.PatchedForkHold(f)

	var list []packsrc.ListEntry
	seq := a.rec.Seq
	// THE GIT A CONFLICT IS KEYED BY is asked only when this launch runs git anyway (a check ran):
	// inside the throttle a recorded conflict holds whichever git recorded it, so a steady-state
	// launch runs no git process at all (P4), and a git upgrade is replayed at the next check
	// (PF-D41).
	exactGit := false
	if hold != "" && good != nil && !a.o.force {
		// HELD BY agent_updates (PF-D19): no check; a change to the series or the recipe is built at
		// the good build's commit, and a serving good build is what runs.
		if a.serving == nil {
			list = []packsrc.ListEntry{goodEntry(good)}
		}
	} else {
		res := a.packs.CheckPatched(f.CheckWant(a.series), packsrc.CheckOptions{Force: a.o.force, Now: patchedNow,
			Begin: func() (func(string), func()) {
				a.dim("checking fork %s's upstream %s", f.Key(), f.Source)
				return func(line string) { a.dim("%s", line) }, func() {}
			}})
		if a.interrupted() {
			// A CTRL-C ENDED THE CHECK, whose fetch it cut short: no fetch failed, so the stamp the
			// attempt wrote goes, and the next launch checks again rather than an hour later (§6.2:
			// an advance a Ctrl-C ended leaves the candidate pending).
			_ = a.packs.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
				r.CheckedAt = 0
				return true, nil
			})
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
		if res.Err != nil {
			a.warn("fork %s: could not check its upstream: %v", f.Key(), res.Err)
			return a.serveOr(fmt.Sprintf("fork %s's upstream could not be checked (%v)", f.Key(), res.Err))
		}
		a.rec = res.Record
		found := a.rec.Check
		seq = found.Seq
		exactGit = res.Ran
		if res.Ran && found.FetchErr != "" {
			a.warn("fork %s: could not check its upstream (%s) — %s; the next check is in an hour, or "+
				"`yolo pack update` checks now", f.Key(), found.FetchErr, a.runsNow())
		}
		if found.Problem != "" {
			if res.Ran {
				a.warn("fork %s: %s — %s", f.Key(), found.Problem, a.runsNow())
			}
			return a.serveOr(fmt.Sprintf("fork %s's upstream names nothing to build (%s)", f.Key(), found.Problem))
		}
		list = a.rec.Candidates(a.in)
		if a.serving != nil && found.FetchErr != "" {
			// PENDING, AND THE LAST FETCH FAILED (§6.2): no advance until a check fetches — the build
			// needs the network too, and its failure would start a back-off for no reason.
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
	}
	if a.serving == nil && good != nil && !onList(list, good.Commit) {
		// THE EDIT, OR THE GOOD BUILD'S ENTRY GONE (§6.1's series and recipe rows, §8.1): with nothing
		// newer that fits, the candidate is the good build's own commit, with the series and recipe
		// as they stand.
		list = append(list, goodEntry(good))
	}
	if pending := a.pendingOf(list, exactGit); a.o.force && a.serving != nil && len(pending) == 0 {
		// `yolo capture` WITH NOTHING PENDING rebuilds the good build's own inputs, as it force-
		// rebuilds a plain fork (§8.3).
		list = []packsrc.ListEntry{goodEntry(good)}
	} else {
		list = pending
	}
	atBase := false
	if len(list) == 0 {
		if a.serving != nil {
			return a.finish(nil, forkBuild{}, 0, nil, "") // nothing pending: the good build runs, no build
		}
		if !a.baseFallback() {
			return a.noFit(edited, "")
		}
		list, atBase = []packsrc.ListEntry{{Commit: a.series.Base}}, true
	}
	switch {
	case a.serving != nil && list[0].Commit != good.Commit:
		a.say("fork %s: upstream moved — %s is newer than the good build %s; replaying the series", f.Key(),
			list[0].Label(), run.GoodBuildLabel(good))
	case edited:
		a.say("fork %s: its series or build recipe changed since the good build %s; replaying the edited series",
			f.Key(), run.GoodBuildLabel(good))
	case good != nil && a.serving == nil:
		a.say("fork %s: the good build %s is gone from the capture store; building it again", f.Key(),
			run.GoodBuildLabel(good))
	case good == nil:
		a.say("fork %s: no build of it on this machine yet; replaying its %s", f.Key(), run.PatchCount(a.series.Len()))
	}
	w := a.walk(list)
	if a.interrupted() {
		return a.finish(nil, forkBuild{}, 0, nil, "")
	}
	if w.Fit < 0 && w.Base == nil && w.Err == nil && !atBase && a.serving == nil && a.lastClean(w) && a.baseFallback() {
		// THE FIRST ADVANCE'S FALLBACK, and any state with nothing to serve (§6.4): nothing on the list
		// takes the series, so it is built at its own base, where it applies by construction.
		a.say("fork %s: no version on the list takes the series, so it is built at its base %s and held there",
			f.Key(), shortSHA(a.series.Base))
		if w = a.walk([]packsrc.ListEntry{{Commit: a.series.Base}}); a.interrupted() {
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
		atBase = true
	}
	switch {
	case w.Base != nil:
		a.warn("fork %s: %s", f.Key(), w.Base.Error())
		return a.serveOr(fmt.Sprintf("fork %s's series does not apply at its own base (%s)", f.Key(), w.Base.Error()))
	case w.Fit < 0:
		if err := a.walkErr(w); err != nil {
			a.warn("fork %s: could not replay the series: %v — `yolo pack update` retries now", f.Key(), err)
			return a.serveOr(fmt.Sprintf("fork %s's series could not be replayed on the host (%v)", f.Key(), err))
		}
		return a.noFit(edited, w.Results[0].Entry.Label())
	}
	fit := w.Results[w.Fit]
	return a.build(forkBuild{Fork: f, Commit: fit.Entry.Commit, Platform: a.o.platform, Series: a.series,
		Entry: a.entryWithVersion(fit.Entry)}, seq, atBase, edited)
}

// goodBuild is the record's good build, nil for none.
func (a *advance) goodBuild() *packsrc.GoodBuild {
	if a.rec == nil {
		return nil
	}
	return a.rec.Good
}

// runsNow says what this launch runs while the advance does not move: the good build, or nothing.
func (a *advance) runsNow() string {
	if a.serving != nil {
		return "still running " + a.goodLine()
	}
	return "this jail has no " + a.f.Bin
}

// serveOr ends an advance that builds nothing: the good build when it serves, else why's reason.
func (a *advance) serveOr(why string) advanceResult {
	r := a.finish(nil, forkBuild{}, 0, nil, why+" — the next fresh launch tries again")
	r.failed = true
	return r
}

// noFit ends an advance whose list nothing on it takes: held at the good build, or nothing.
func (a *advance) noFit(edited bool, newest string) advanceResult {
	why := fmt.Sprintf("no upstream version fork %s follows takes its patch series", a.f.Key())
	if newest != "" {
		why = fmt.Sprintf("upstream %s does not take fork %s's patch series", newest, a.f.Key())
	}
	if a.serving == nil {
		next := "`yolo pack update` shows the conflict and how to rebase the series"
		if edited {
			next += "; reverting the edit brings back the good build " + run.GoodBuildLabel(a.rec.Good)
		}
		a.warn("fork %s: nothing to build — this jail has no %s; %s", a.f.Key(), a.f.Bin, next)
		r := a.finish(nil, forkBuild{}, 0, nil, why+" — "+next)
		r.failed = true
		return r
	}
	r := a.finish(nil, forkBuild{}, 0, nil, "")
	r.failed = true
	return r
}

// baseFallback reports whether nothing serving may fall back to the series' base: the followed ref
// is a branch that contains it (§6.4). A tag or a commit hold gets none, since the user named it.
func (a *advance) baseFallback() bool {
	c := a.rec.Check
	return c != nil && c.RefKind == "branch" && c.BaseOnBranch
}

// pendingOf is the walk's list as the advance walks it (§6.1, §6.2): every entry with a conflict
// recorded for the series, this yolo and this git passed over, never replayed again; and, when a
// good build serves at a launch, nothing at all once the first entry left is a failed build still
// backing off. A later entry backing off is passed over too.
func (a *advance) pendingOf(list []packsrc.ListEntry, exactGit bool) []packsrc.ListEntry {
	if len(list) == 0 {
		return nil
	}
	gitVer := ""
	if exactGit {
		gitVer, _ = a.packs.GitVersion()
	}
	backoff := a.o.launch && a.serving != nil
	var out []packsrc.ListEntry
	for _, e := range list {
		if a.conflicted(e.Commit, gitVer) {
			continue
		}
		if backoff && a.backingOff(e.Commit) {
			if len(out) == 0 {
				return nil // the first entry the walk reaches is backing off: nothing is pending
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

// conflicted reports whether a conflict is recorded for commit under the series as it stands and
// this yolo, and under gitVer — or under any git when gitVer is "" (pendingOf).
func (a *advance) conflicted(commit, gitVer string) bool {
	for _, o := range a.rec.Outcomes {
		if o.Kind == packsrc.OutcomeConflict && o.Commit == commit && o.Series == a.series.Digest &&
			o.Yolo == a.yolo && (gitVer == "" || o.Git == gitVer) {
			return true
		}
	}
	return false
}

// backingOff reports whether a build of commit with the series and recipe as they stand failed
// under this yolo and is still inside OQ-PD26's back-off: a day, doubling to a week.
func (a *advance) backingOff(commit string) bool {
	o := a.buildFailure(commit)
	return o != nil && patchedNow().Before(autoCaptureRetryAt(capture.AutoFailure{Failures: o.Count,
		Last: time.Unix(o.At, 0)}))
}

// buildFailure is the failed-build outcome recorded for commit under the series, the recipe and
// this yolo, or nil. Another candidate (another commit, series or recipe) or another yolo is a new
// key, so it resets the back-off.
func (a *advance) buildFailure(commit string) *packsrc.EntryOutcome {
	if a.rec == nil {
		return nil
	}
	for i := range a.rec.Outcomes {
		o := &a.rec.Outcomes[i]
		if o.Kind == packsrc.OutcomeBuildFailed && o.Commit == commit && o.Series == a.series.Digest &&
			o.Recipe == a.recipe && o.Yolo == a.yolo {
			return o
		}
	}
	return nil
}

// walk replays the series down list and records each outcome, and says each conflict and each
// member already upstream once, on the launch that found it.
func (a *advance) walk(list []packsrc.ListEntry) packsrc.WalkResult {
	w := a.packs.WalkSeries(a.repo, a.subdir, a.series, list, packsrc.WalkOptions{})
	if a.interrupted() {
		return w
	}
	if err := a.packs.RecordWalk(a.f.Key(), a.series.Digest, a.yolo, w, patchedNow()); err != nil {
		a.warn("fork %s: recording the replay: %v", a.f.Key(), err)
	}
	var fit *packsrc.ReplayResult
	if w.Fit >= 0 {
		fit = &w.Results[w.Fit]
	}
	for _, r := range w.Results {
		switch {
		case r.Conflict != nil:
			paths := strings.Join(r.Conflict.Paths, ", ")
			if paths == "" {
				paths = "(no path named)"
			}
			a.pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("fork %s: upstream %s does not take the patch series —",
				a.f.Key(), r.Entry.Label())))
			a.dim("  %s conflicts in %s", r.Conflict.Member, paths)
			a.dim("  %s", a.heldLine(fit))
			for _, line := range rebaseSteps(a.f, a.series, r.Entry) {
				a.dim("%s", line)
			}
		case r.Clean:
			for _, m := range r.Upstream {
				a.dim("  %s is already in upstream %s; drop it from %s", m, r.Entry.Label(), a.series.Dir)
			}
		}
	}
	return w
}

// heldLine is the conflict message's third line (§8.2): what runs while the newest does not fit.
func (a *advance) heldLine(fit *packsrc.ReplayResult) string {
	switch {
	case fit != nil:
		return "building the newest fit, " + fit.Entry.Label() + ", instead"
	case a.serving != nil:
		return "still running " + a.goodLine()
	case a.baseFallback():
		return "nothing runs yet: building the series at its base " + shortSHA(a.series.Base)
	}
	return "nothing runs: nothing on the list takes the series"
}

// lastClean reports whether a walk with no fit ended on an entry that was settled (a conflict),
// not on an apply error.
func (a *advance) lastClean(w packsrc.WalkResult) bool {
	return len(w.Results) > 0 && w.Results[len(w.Results)-1].Err == nil
}

// walkErr is a walk's apply error, nil when every entry it reached was settled.
func (a *advance) walkErr(w packsrc.WalkResult) error {
	if w.Err != nil {
		return w.Err
	}
	if len(w.Results) == 0 {
		return errors.New("nothing was replayed")
	}
	return w.Results[len(w.Results)-1].Err
}

// entryWithVersion is e with the version the good build records: its own, or for an untagged
// branch tip the newest version on the last check's list, all of which the tip contains (GoodBuild's
// "the newest version its commit contains"), so a later list is cut above it.
func (a *advance) entryWithVersion(e packsrc.ListEntry) packsrc.ListEntry {
	if e.Version != "" || !e.Tip || a.rec.Check == nil {
		return e
	}
	for _, v := range a.rec.Check.List {
		if !v.Tip && v.Version != "" {
			e.Version = v.Version
			return e
		}
	}
	return e
}

// buildFailedLines are a failed build's lines (§8.1): the error, where its output is, and the
// next steps.
func (a *advance) buildFailedLines(b forkBuild, err error, retryAt time.Time) {
	f := a.f
	what := b.Entry.Label() + " + " + run.PatchCount(b.Series.Len())
	a.warn("fork %s: the build of %s failed: %v — %s", f.Key(), what, err, a.runsNow())
	if a.o.workspace != "" {
		a.dim("  its output is above, and in %s", filepath.Join(a.o.workspace, ".yolo", "launch.log"))
	}
	switch {
	case a.serving != nil:
		a.dim("  `yolo capture %s` retries it now; a fresh launch retries it after %s", f.Bin,
			retryAt.Local().Format("2006-01-02 15:04"))
		a.dim("  to stay on the running version: `agent_updates` off for pack %s, or a tag `?ref=` in a manifest you own", f.Pack)
	default:
		a.dim("  the next fresh launch tries again, or `yolo capture %s` now", f.Bin)
		if g := a.goodBuild(); g != nil && g.Recipe != a.recipe {
			a.dim("  reverting the edit to the series or the build brings back the good build %s", run.GoodBuildLabel(g))
		}
	}
}

// build runs the build act for b and settles its outcome: the move, a recorded failure, or nothing
// recorded, and then the hand.
func (a *advance) build(b forkBuild, seq int64, atBase, edited bool) advanceResult {
	f := a.f
	switch {
	case a.serving != nil && a.o.launch:
		a.say("fork %s: %s takes the series; building it — this launch waits for it, at most %s, and a Ctrl-C "+
			"starts this jail on the good build %s instead", f.Key(), b.Entry.Label(), forkBuildWaitBound,
			run.GoodBuildLabel(a.rec.Good))
	case !atBase:
		a.say("fork %s: %s takes the series; building it", f.Key(), b.Entry.Label())
	}
	startFail := a.buildFailure(b.Commit)
	startGood := a.goodBuild()
	mode := buildMode{force: a.o.force, packs: a.packs, lock: pidlock.NoWait}
	if a.o.launch {
		mode.lock = pidlock.Mode{Wait: true, Bound: forkBuildWaitBound, Cancel: a.ctx.Done()}
		mode.afterLock = func() (*capture.Entry, error, bool) { return a.afterLock(b, startGood, startFail) }
		if a.serving != nil {
			mode.runJail = func(staging string, b forkBuild) int {
				rc, bound := forkBuildChild(a.ctx, forkBuildWaitBound, staging, b, a.o.out, a.o.errw, a.o.color)
				a.boundHit = bound
				return rc
			}
		}
	}
	entry, err := buildFork(b, mode, a.o.out, a.o.errw, a.o.color)
	var waited waiterFailure
	var lockTimeout forkLockTimeout
	var source forkSourceError
	switch {
	case err == nil:
		return a.moved(b, entry, seq, atBase, edited)
	case a.interrupted() || errors.Is(err, pidlock.ErrCanceled):
		return a.finish(nil, forkBuild{}, 0, nil, "the advance was interrupted — the next fresh launch builds it")
	case errors.As(err, &waited):
		// ANOTHER LAUNCH'S BUILD OF THIS CANDIDATE FAILED while this one waited (§6.6): its result is
		// this one's, recorded already, and nothing is rebuilt.
		a.warn("fork %s: another launch's build of %s failed while this one waited: %s — %s", f.Key(),
			b.Entry.Label(), waited.msg, a.runsNow())
		return a.serveOr(fmt.Sprintf("fork %s's build of %s failed on the host (%s)", f.Key(), b.Entry.Label(), waited.msg))
	case errors.As(err, &lockTimeout):
		a.warn("fork %s: %v — %s; the next launch takes that build's result, and if that pid has hung, "+
			"stopping it lets the next launch build", f.Key(), err, a.runsNow())
		return a.serveOr(fmt.Sprintf("fork %s: %v", f.Key(), err))
	case errors.Is(err, errForkBuildLocked):
		a.warn("fork %s: %v", f.Key(), err)
		a.dim("  %s", captureWaitStep(f.Bin))
		return a.serveOr(fmt.Sprintf("fork %s: %v", f.Key(), err))
	case errors.Is(err, errForkBuildNotStarted):
		// THE RUNTIME WOULD NOT START THE BUILD JAIL (PF-D21): not a failed build, so nothing is
		// recorded and the candidate stays pending.
		a.warn("fork %s: the build jail did not start (%v) — %s", f.Key(), err, a.runsNow())
		if a.o.runtime == "container" {
			a.dim("  On Apple Container a capture jail cannot start beside a running jail: once the other "+
				"jails stop, `yolo capture %s` builds it", f.Bin)
		} else {
			a.dim("  `yolo capture %s` builds it once the runtime starts jails again; the next fresh launch tries too", f.Bin)
		}
		return a.serveOr(fmt.Sprintf("fork %s's build jail did not start on the host (%v)", f.Key(), err))
	case errors.As(err, &source):
		// AN APPLY ERROR in the build's own replay: nothing recorded, the next check retries.
		a.warn("fork %s: %v — %s; `yolo pack update` retries now", f.Key(), err, a.runsNow())
		return a.serveOr(fmt.Sprintf("fork %s's series could not be put in place on the host (%v)", f.Key(), err))
	}
	// THE BUILD LINE RAN AND FAILED, or ran past the bound: recorded, with the back-off.
	if a.boundHit {
		err = fmt.Errorf("it ran past the %s bound and was stopped", forkBuildWaitBound)
	}
	count := 1
	if startFail != nil {
		count = startFail.Count + 1
	}
	now := patchedNow()
	o := &packsrc.EntryOutcome{Commit: b.Commit, Kind: packsrc.OutcomeBuildFailed, Series: a.series.Digest,
		Yolo: a.yolo, Recipe: a.recipe, Error: oneLineErr(err), Count: count, At: now.Unix()}
	a.buildFailedLines(b, err, autoCaptureRetryAt(capture.AutoFailure{Failures: count, Last: now}))
	r := a.finish(nil, forkBuild{}, 0, o, fmt.Sprintf("fork %s's build of %s failed on the host (%s) — the next "+
		"fresh launch tries again, or `yolo capture %s` now", f.Key(), b.Entry.Label(), oneLineErr(err), f.Bin))
	r.failed = true
	return r
}

// waiterFailure is a launch that waited for another's build of the same candidate and found it
// failed: its result, which this launch takes rather than building again (§6.6).
type waiterFailure struct{ msg string }

func (e waiterFailure) Error() string { return e.msg }

// afterLock is a launch's re-read once it holds the build lock (§6.6): the build already admitted
// (a hit, by the exact lookup), a failure recorded for this candidate since the advance began, or a
// move to it recorded meanwhile ends the advance with that result.
func (a *advance) afterLock(b forkBuild, startGood *packsrc.GoodBuild, startFail *packsrc.EntryOutcome) (*capture.Entry, error, bool) {
	if a.o.force {
		return nil, nil, false
	}
	if entry := a.exact(b.Commit); entry != nil {
		return entry, nil, true
	}
	rec, err := a.packs.LoadCheckRecord(a.f.Key())
	if err != nil {
		return nil, nil, false
	}
	a.rec.Outcomes = rec.Outcomes
	if now := a.buildFailure(b.Commit); now != nil && (startFail == nil || now.At != startFail.At || now.Count != startFail.Count) {
		return nil, waiterFailure{now.Error}, true
	}
	return nil, nil, false
}

// moved settles an admitted build: the move under the record lock, and its disclosure, which no
// flag hides (OQ-RO3).
func (a *advance) moved(b forkBuild, entry *capture.Entry, seq int64, atBase, edited bool) advanceResult {
	prev := a.goodBuild()
	r := a.finish(entry, b, seq, nil, "")
	r.built = true
	if r.lost {
		a.say("fork %s: another launch moved the good build meanwhile, from a newer check; this jail runs that one, "+
			"and this build is reaped", a.f.Key())
		return r
	}
	what := b.Entry.Label() + " + " + run.PatchCount(b.Series.Len())
	switch {
	case prev == nil:
		line := "built fork " + a.f.Key() + ": " + what + "; this jail runs it"
		if atBase {
			line += " — held at the series' base, which no version of the branch takes the series past"
		}
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape(line))
	case prev.Entry == entry.Key:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("rebuilt fork "+a.f.Key()+": "+what+"; this jail runs it"))
	case edited:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("updated fork "+a.f.Key()+": "+run.GoodBuildLabel(prev)+
			" → "+b.Entry.Label()+", the edited series ("+run.PatchCount(b.Series.Len())+", series "+
			b.Series.ShortDigest()+"); this jail runs the new build"))
	default:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("updated fork "+a.f.Key()+": "+run.GoodBuildLabel(prev)+" → "+
			b.Entry.Label()+", "+run.PatchCount(b.Series.Len())+"; this jail runs the new build"))
	}
	return r
}

// finish is the advance's last act, under the fork's record lock (§6.1, §6.6): the move of an
// admitted build, a compare-and-swap that prefers the newer check; a failure's outcome; the hand of
// the good build as the record now names it (or why when it does not serve); and the reap of what
// the move leaves behind. reason is what the jail is handed when nothing serves.
func (a *advance) finish(built *capture.Entry, b forkBuild, seq int64, failure *packsrc.EntryOutcome, reason string) advanceResult {
	f := a.f
	var res advanceResult
	var moved, lost bool
	err := a.packs.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		changed := false
		if built != nil {
			if r.Good != nil && seq < r.Good.Seq {
				lost = true
			} else {
				read := r.Read
				r.Good = &packsrc.GoodBuild{Commit: b.Commit, Tag: b.Entry.Tag, Version: b.Entry.Version,
					Series: b.Series.Digest, Recipe: b.recipe(), Tree: a.builtTree(built), Patches: b.Series.Len(),
					Seq: seq, Read: &read, Entry: built.Key, At: patchedNow().Unix()}
				dropBuildFailure(r, b.Commit)
				moved, changed = true, true
			}
		}
		if failure != nil {
			r.SetOutcome(*failure)
			changed = true
		}
		a.rec = r
		h := run.HandedFork{Fork: f.Key()}
		if g := r.Good; g != nil && g.Recipe == a.recipe {
			if e := a.exactGood(g); e != nil {
				h.Key, h.Commit, h.Tag, h.Patches, h.Series = e.Key, g.Commit, g.Tag, g.Patches, g.Series
			}
		}
		if h.Key == "" {
			h.Reason = reason
			if h.Reason == "" {
				h.Reason = "fork " + f.Key() + " has no build on this machine yet — the next fresh launch builds it"
			}
		}
		res.delivery = entrypoint.ForkDelivery{Key: h.Key, Reason: h.Reason}
		if a.o.hand != nil {
			if err := a.o.hand(f.Bin, h); err != nil {
				a.warn("fork %s: could not record what this launch hands its jail (%v)", f.Key(), err)
			}
		}
		// THE REAP (PF-D20), after the hand, so this launch's own build is in the records it reads.
		switch {
		case moved:
			a.reapOthers(r.Good.Entry)
		case lost && built.Key != r.Good.Entry:
			a.reapOne(built.Key)
		}
		return changed, nil
	})
	if err != nil {
		a.warn("fork %s: could not update its check record: %v", f.Key(), err)
		if res.delivery.Key == "" && res.delivery.Reason == "" {
			res.delivery.Reason = fmt.Sprintf("fork %s's check record could not be updated (%v)", f.Key(), err)
		}
	}
	res.lost = lost
	return res
}

// handReason hands the jail reason with no record touched (a series that cannot be read).
func (a *advance) handReason(reason string) advanceResult {
	h := run.HandedFork{Fork: a.f.Key(), Reason: reason}
	if a.o.hand != nil {
		_ = a.o.hand(a.f.Bin, h)
	}
	return advanceResult{delivery: entrypoint.ForkDelivery{Reason: reason}, failed: true}
}

// dropBuildFailure removes commit's failed-build outcomes once a build of it is admitted.
func dropBuildFailure(r *packsrc.CheckRecord, commit string) {
	var kept []packsrc.EntryOutcome
	for _, o := range r.Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed && o.Commit == commit {
			continue
		}
		kept = append(kept, o)
	}
	r.Outcomes = kept
}

// builtTree is the patched tree an admitted build's receipt records.
func (a *advance) builtTree(e *capture.Entry) string {
	recs, _ := captureRecords(e.Root)
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Fork == a.f.Key() && recs[i].Tree != "" {
			return recs[i].Tree
		}
	}
	return ""
}

// exactGood is the store entry of good build g, by the exact lookup, or nil.
func (a *advance) exactGood(g *packsrc.GoodBuild) *capture.Entry { return a.exact(g.Commit) }

// exact is THE EXACT LOOKUP (§6.3): the newest admitted build of this fork key for this platform
// at commit, from the fork's repository and subdirectory, under the recipe as it stands — never the
// newest build of the fork compared afterwards.
func (a *advance) exact(commit string) *capture.Entry {
	entry, _, err := resolvePatchedBuild(a.store, a.f.Key(), a.f.Bin, a.o.platform, patchedBuildSource(a.f.Source),
		commit, a.recipe)
	if err != nil {
		return nil
	}
	return entry
}

// reapOthers reaps every other build of this fork for this platform that no running jail was
// handed, once the good build moved to keep (PF-D20). A delivery record that cannot be read keeps
// every build, and so does an entry any other program's record names.
func (a *advance) reapOthers(keep string) {
	handed, err := run.HandedForkKeys()
	if err != nil {
		a.dim("fork %s: kept every earlier build, since what running jails were handed cannot be read (%v)", a.f.Key(), err)
		return
	}
	scan, err := capture.Scan(a.store, captureRecords)
	if err != nil {
		return
	}
	for _, e := range scan {
		if e.Key == keep || handed[e.Key] || !a.onlyThisFork(e.Records) {
			continue
		}
		_ = a.store.ReapEntry(e.Key)
	}
}

// reapOne reaps a build that lost the swap, unless a running jail was handed it.
func (a *advance) reapOne(key string) {
	handed, err := run.HandedForkKeys()
	if err != nil || handed[key] {
		return
	}
	scan, err := capture.Scan(a.store, captureRecords)
	if err != nil {
		return
	}
	for _, e := range scan {
		if e.Key == key && a.onlyThisFork(e.Records) {
			_ = a.store.ReapEntry(key)
		}
	}
}

// onlyThisFork reports whether every record of an entry is a build of this fork for this platform:
// an entry whose bytes another program's record also names (identical output) is never reaped here.
func (a *advance) onlyThisFork(recs []capture.Record) bool {
	if len(recs) == 0 {
		return false
	}
	for _, r := range recs {
		if r.Fork != a.f.Key() || r.Platform != a.o.platform {
			return false
		}
	}
	return true
}

// loadOrRecover is the check record, or one recovered from the store when it is gone or cannot be
// read (§6.2): the newest admitted build of this fork key for this platform whose recipe is the
// manifest's becomes the good build. Losing the record costs a lookup, not a rebuild.
func (a *advance) loadOrRecover() *packsrc.CheckRecord {
	rec, err := a.packs.LoadCheckRecord(a.f.Key())
	if err == nil && rec.Good != nil {
		return rec
	}
	g := a.recoverGood()
	if g == nil {
		if rec == nil {
			rec = &packsrc.CheckRecord{Schema: packsrc.CheckRecordSchema, Owner: a.f.Key()}
		}
		return rec
	}
	var out *packsrc.CheckRecord
	_ = a.packs.WithCheckRecord(a.f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		out = r
		if r.Good != nil {
			return false, nil
		}
		r.Good = g
		return true, nil
	})
	if out == nil {
		out = &packsrc.CheckRecord{Schema: packsrc.CheckRecordSchema, Owner: a.f.Key(), Good: g}
	}
	why := "it had none"
	if err != nil && !errors.Is(err, packsrc.ErrNoCheckRecord) {
		why = "it could not be read: " + err.Error()
	} else if errors.Is(err, packsrc.ErrNoCheckRecord) {
		why = "it was gone"
	}
	a.dim("fork %s: recovered its good build %s from the capture store into its check record (%s)", a.f.Key(),
		run.GoodBuildLabel(out.Good), why)
	return out
}

// recoverGood is the good build the capture store holds for this fork as the manifest asks for it,
// or nil: the newest admitted build of the fork key for this platform under the recipe as it stands.
func (a *advance) recoverGood() *packsrc.GoodBuild {
	scan, err := capture.Scan(a.store, captureRecords)
	if err != nil {
		return nil
	}
	var best *capture.Record
	bestKey := ""
	for _, e := range scan {
		for i := range e.Records {
			r := &e.Records[i]
			if r.Fork != a.f.Key() || r.Platform != a.o.platform || r.Recipe != a.recipe {
				continue
			}
			if best == nil || r.Time.After(best.Time) || (r.Time.Equal(best.Time) && e.Key > bestKey) {
				best, bestKey = r, e.Key
			}
		}
	}
	if best == nil {
		return nil
	}
	return &packsrc.GoodBuild{Commit: best.Revision, Tag: best.Tag, Version: best.Version, Series: best.Series,
		Recipe: best.Recipe, Tree: best.Tree, Patches: a.series.Len(), Entry: bestKey, At: best.Time.Unix()}
}

// resolvePatchedBuild is THE EXACT LOOKUP of a patched build (§6.3, PF-D7): among every admitted
// build whose record names fork for (bin, platform), from source (the repository and subdirectory,
// patchedBuildSource) at commit under recipe, the newest — never the newest build of the fork
// compared afterwards, which two forks of one upstream, a `yolo capture` of a candidate and a swap
// a launch lost would each move.
func resolvePatchedBuild(store *capture.Store, fork, bin, platform, source, commit, recipe string) (*capture.Entry, *capture.Record, error) {
	if recipe == "" {
		return nil, nil, fmt.Errorf("no build of fork %s is asked for with no series read", fork)
	}
	scan, err := capture.Scan(store, captureRecords)
	if err != nil {
		return nil, nil, err
	}
	var best *capture.Record
	bestKey := ""
	for _, e := range scan {
		for i := range e.Records {
			r := &e.Records[i]
			if r.Fork != fork || r.Bin != bin || r.Platform != platform || r.Source != source ||
				r.Revision != commit || r.Recipe != recipe {
				continue
			}
			if best == nil || r.Time.After(best.Time) || (r.Time.Equal(best.Time) && e.Key > bestKey) {
				best, bestKey = r, e.Key
			}
		}
	}
	if best == nil {
		return nil, nil, fmt.Errorf("nothing in %s records a build of fork %s at %s", store.Dir, fork, shortSHA(commit))
	}
	entry, err := store.Resolve(bestKey)
	if err != nil {
		return nil, nil, err
	}
	return entry, best, nil
}

// goodEntry is a good build as a list entry: its commit, tag and version.
func goodEntry(g *packsrc.GoodBuild) packsrc.ListEntry {
	return packsrc.ListEntry{Commit: g.Commit, Tag: g.Tag, Version: g.Version}
}

// onList reports whether commit is an entry of list.
func onList(list []packsrc.ListEntry, commit string) bool {
	for _, e := range list {
		if e.Commit == commit {
			return true
		}
	}
	return false
}

// replayIntoSource replays b's series onto b.Entry in a scratch repository outside the build's
// workspace (§5.1, PF-D18), under the mirror's lock, and copies the patched source subdirectory into
// dst with the copy that never follows a link; it returns the PATCHED TREE (§5.3). packs nil is the
// launch's store.
func replayIntoSource(packs *packsrc.Store, b forkBuild, dst string) (string, error) {
	if packs == nil {
		packs = packsrc.LaunchStore(paths.PacksDir())
	}
	a, err := packsrc.Parse(b.Fork.Source)
	if err != nil {
		return "", err
	}
	w := packs.WalkSeries(a.Repo, a.Path, b.Series, []packsrc.ListEntry{b.Entry}, packsrc.WalkOptions{
		OnFit: func(tree string) error { return copySourceTree(tree, dst) }})
	switch {
	case w.Base != nil:
		return "", w.Base
	case w.Err != nil:
		return "", w.Err
	case len(w.Results) == 0:
		return "", errors.New("nothing was replayed")
	}
	r := w.Results[0]
	switch {
	case r.Err != nil:
		return "", r.Err
	case r.Conflict != nil:
		return "", fmt.Errorf("%s conflicts in %s", r.Conflict.Member, strings.Join(r.Conflict.Paths, ", "))
	case !r.Clean:
		return "", errors.New("the series did not replay")
	}
	return r.Tree, nil
}

// oneLineErr is err's first line.
func oneLineErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
