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
// # The base, when nothing serves (§6.4, PF-D23, PF-D40)
//
// With nothing to serve — a first advance, the user's own edit, the good build's entry gone — the
// series is built at its own base when the followed branch contains it and nothing better runs: the
// list holds nothing newer than the base, nothing on it takes the series, the walk stopped on an
// apply error, or the newest fit failed to build, in this same advance. The fit is tried again at
// every later fresh launch while nothing serves, and on its back-off once the base does.
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
// and, inside it, the waiter's re-read and the replay into src/ (the mirror's), and then, still
// inside it but after the build, the move, the failure record, the hand and the reap, under the
// record lock (buildMode.settle). So a waiter that takes the build lock next reads the winner's
// result, and a move's reap sees another advance's admitted build as building until it is settled.

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
	// host is the host's own render (a patched extension at `yolo host apply`, PPX-D11): the lines
	// name the host, not a jail, as what runs the build.
	host bool
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
	// gone is why this advance's admitted build could not be moved to: it left the store first.
	gone error
	// fellShort is true when the build act came to nothing a later step can use: a failed build,
	// another's failure taken, or a source that could not be put in place (the base follows, PF-D23).
	fellShort bool
	// good is the good build delivery.Key is the entry of, nil when there is none: what a patched
	// extension's per-launch copy and its lines name.
	good *packsrc.GoodBuild
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
	// seq is the sequence number of the check the walk's list came from.
	seq int64
	// boundHit is set by the child build runner when the build ran past forkBuildWaitBound.
	boundHit bool
	// replaySpent is what this advance's replays have taken of the replay's bound since its last
	// build began (PF-D44): the walk and the build's replay into src/ share one bound, and the
	// series' base, built after a walk or a build that came to nothing, has one of its own.
	replaySpent time.Duration
	// walked is set once a walk of the list ran to its end, and applyErr is the apply error it, or
	// the build's own replay, ended in (PF-D45): finish records the one, or clears the record's.
	walked   bool
	applyErr *packsrc.ApplyError
	// thenBase is set while a build runs whose failure sends this advance on to the series' base
	// (PF-D23), so its failure's lines say that rather than the steps; ownLock is that build's lock.
	thenBase bool
	ownLock  string
}

// baseWhy is why an advance builds the series at its own base (§6.4), baseNone when it does not.
type baseWhy int

const (
	baseNone baseWhy = iota
	// baseEmpty: the branch has no version newer than the series' base (PF-D27): nothing is held.
	baseEmpty
	// baseNoFit: nothing on the walk's list takes the series.
	baseNoFit
	// baseApplyErr: the walk stopped on an apply error before it found a fit.
	baseApplyErr
	// baseBuildFailed: the newest fit's build failed, or could not be put in place.
	baseBuildFailed
)

// replayElapsed is how long a replay begun at start took: a var so a test can stand in for a slow
// one.
var replayElapsed = time.Since

// advancePatchedFork runs f's advance and returns what its jail is handed.
func advancePatchedFork(f packload.Fork, o advanceOptions) advanceResult {
	a, early := newAdvance(f, o)
	if early != nil {
		return *early
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
		a.pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("%s: the advance was interrupted — this "+
			"jail starts on the good build %s; the next fresh launch tries again", f.Label(), a.goodLine())))
	}
	return res
}

// newAdvance reads what f's advance starts from — its series, its recipe, its check record (or one
// recovered from the store) and the good build that serves — or, when the series cannot be read,
// the advance's whole result.
func newAdvance(f packload.Fork, o advanceOptions) (*advance, *advanceResult) {
	a := &advance{f: f, o: o, pr: richtext.Printer{W: o.out, Color: o.color}, epr: richtext.Printer{W: o.errw, Color: o.color},
		packs: patchedAdvanceStore(o.launch), store: &capture.Store{Dir: paths.CapturesDir()},
		yolo: patchedYoloVersion(), ctx: context.Background(), repo: mustRepo(f.Source), subdir: subdirOf(f.Source)}
	series, err := f.ReadSeries()
	if err != nil {
		// THE SERIES CANNOT BE READ (§8.1): nothing serves under PF-D23, since the manifest's recipe
		// is not known, and the reason names the file and its fix.
		a.warn("%s: %v", f.Label(), err)
		r := a.handReason(fmt.Sprintf("%s's patch series cannot be read (%v)", f.Label(), err))
		return nil, &r
	}
	a.series = series
	a.recipe = forkBuild{Fork: f, Series: series}.recipe()
	a.in, _, _, _ = f.CheckWant(series).Inputs()
	a.rec = a.loadOrRecover()
	if a.rec != nil && a.rec.Good != nil && a.rec.Good.Recipe == a.recipe {
		a.serving = a.exactGood(a.rec.Good)
	}
	return a, nil
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

// here is what an advance's lines name as what runs the build: "this jail" at a jail launch (and
// for `yolo capture`, whose build serves the next one), "the host" at the host's render
// (advanceOptions.host).
func (a *advance) here() string {
	if a.o.host {
		return "the host"
	}
	return "this jail"
}

// run is the advance proper, from the check to the hand.
func (a *advance) run() advanceResult {
	f := a.f
	good := a.goodBuild()
	edited := good != nil && good.Recipe != a.recipe
	hold := run.PatchedForkHold(f)

	var list []packsrc.ListEntry
	a.seq = a.rec.Seq
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
				a.dim("checking %s's upstream %s", f.Label(), f.Source)
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
			a.warn("%s: could not check its upstream: %v", f.Label(), res.Err)
			return a.serveOr(fmt.Sprintf("%s's upstream could not be checked (%v)", f.Label(), res.Err))
		}
		a.rec = res.Record
		found := a.rec.Check
		a.seq = found.Seq
		exactGit = res.Ran
		if res.Ran && found.FetchErr != "" {
			a.warn("%s: could not check its upstream (%s) — %s; the next check is in an hour, or "+
				"`yolo pack update` checks now", f.Label(), found.FetchErr, a.runsNow())
		}
		if found.Problem != "" {
			if res.Ran {
				a.warn("%s: %s — %s", f.Label(), found.Problem, a.runsNow())
			}
			return a.serveOr(fmt.Sprintf("%s's upstream names nothing to build (%s)", f.Label(), found.Problem))
		}
		list = a.rec.Candidates(a.in)
		if a.serving != nil && found.FetchErr != "" {
			// PENDING, AND THE LAST FETCH FAILED (§6.2): no advance until a check fetches — the build
			// needs the network too, and its failure would start a back-off for no reason.
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
		if a.serving != nil && !res.Ran && !a.o.force && a.rec.ApplyErrAtLastCheck() != nil {
			// THE LAST WALK OF THIS CHECK'S LIST ENDED IN AN APPLY ERROR (§6.2's row, PF-D45), said on
			// the launch that met it: the good build runs, the fork's line carries the held suffix,
			// and the next check retries — never every launch inside the hour, each paying the walk.
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
	}
	if a.serving == nil && good != nil && !onList(list, good.Commit) {
		// THE EDIT, OR THE GOOD BUILD'S ENTRY GONE (§6.1's series and recipe rows, §8.1): with nothing
		// newer that fits, the candidate is the good build's own commit, with the series and recipe
		// as they stand.
		list = append(list, goodEntry(good))
	}
	listed := len(list) > 0
	if pending := a.pendingOf(list, exactGit); a.o.force && a.serving != nil && len(pending) == 0 {
		// `yolo capture` WITH NOTHING PENDING rebuilds the good build's own inputs, as it force-
		// rebuilds a plain fork (§8.3).
		list = []packsrc.ListEntry{goodEntry(good)}
	} else {
		list = pending
	}
	base := baseNone
	if len(list) == 0 {
		if a.serving != nil {
			return a.finish(nil, forkBuild{}, 0, nil, "") // nothing pending: the good build runs, no build
		}
		if !a.baseFallback() {
			return a.noFit(edited, "")
		}
		// NOTHING TO WALK, AND NOTHING SERVES (§6.4): the branch has no version newer than the
		// series' base (PF-D27, no hold), or every entry on it is a recorded conflict.
		base = baseNoFit
		if !listed {
			base = baseEmpty
		}
		list = []packsrc.ListEntry{a.baseEntry()}
	}
	switch {
	case a.serving != nil && list[0].Commit != good.Commit:
		a.say("%s: upstream moved — %s is newer than the good build %s; replaying the series", f.Label(),
			list[0].Label(), run.GoodBuildLabel(good))
	case edited:
		a.say("%s: its series or build recipe changed since the good build %s; replaying the edited series",
			f.Label(), run.GoodBuildLabel(good))
	case good != nil && a.serving == nil:
		a.say("%s: the good build %s is gone from the capture store; building it again", f.Label(),
			run.GoodBuildLabel(good))
	case good == nil:
		a.say("%s: no build of it on this machine yet; replaying its %s", f.Label(), run.PatchCount(a.series.Len()))
	}
	w := a.walk(list, base == baseNone)
	if a.interrupted() {
		return a.finish(nil, forkBuild{}, 0, nil, "")
	}
	if w.Fit < 0 && w.Base == nil && w.Err == nil && base == baseNone && a.serving == nil && a.baseFallback() {
		// THE FIRST ADVANCE'S FALLBACK, and any state with nothing to serve (§6.4): nothing on the list
		// takes the series, or the walk stopped on an apply error before it found what does, so it is
		// built at its own base, where it applies by construction — with a replay bound of its own,
		// since a walk the bound stopped has none left (PF-D44).
		if err := a.walkErr(w); err != nil {
			a.warn("%s: could not replay the series: %v — building it at its base %s meanwhile; the next "+
				"check retries the rest, or `yolo pack update` now", f.Label(), err, shortSHA(a.series.Base))
			base = baseApplyErr
		} else {
			a.say("%s: no version on the list takes the series, so it is built at its base %s and held there",
				f.Label(), shortSHA(a.series.Base))
			base = baseNoFit
		}
		a.replaySpent = 0
		if w = a.walk([]packsrc.ListEntry{a.baseEntry()}, false); a.interrupted() {
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
	}
	switch {
	case w.Base != nil:
		a.warn("%s: %s", f.Label(), w.Base.Error())
		return a.serveOr(fmt.Sprintf("%s's series does not apply at its own base (%s)", f.Label(), w.Base.Error()))
	case w.Fit < 0:
		if err := a.walkErr(w); err != nil {
			a.warn("%s: could not replay the series: %v — %s; %s", f.Label(), err, a.runsNow(), a.retryStep())
			return a.serveOr(fmt.Sprintf("%s's series could not be replayed on the host (%v)", f.Label(), err))
		}
		return a.noFit(edited, w.Results[0].Entry.Label())
	}
	fit := w.Results[w.Fit]
	return a.build(forkBuild{Fork: f, Commit: fit.Entry.Commit, Platform: a.o.platform, Series: a.series,
		Entry: a.entryWithVersion(fit.Entry)}, base, edited)
}

// baseEntry is the series' base as an entry of the walk's list: the list's own entry for it when the
// last check listed it (its version tag), else the commit alone.
func (a *advance) baseEntry() packsrc.ListEntry {
	if c := a.rec.Check; c != nil {
		for _, e := range c.List {
			if e.Commit == a.series.Base {
				return e
			}
		}
	}
	return packsrc.ListEntry{Commit: a.series.Base}
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
	return a.here() + " has no " + a.f.Thing()
}

// retryStep is when an apply error is replayed again (PF-D45): at the next check while the good build
// serves, at the next fresh launch while nothing does; and `yolo pack update` at once.
func (a *advance) retryStep() string {
	if a.serving != nil {
		return "the next check, in an hour, retries it, or `yolo pack update` now"
	}
	return "the next fresh launch retries it, or `yolo pack update` now"
}

// serveOr ends an advance that builds nothing: the good build when it serves, else why's reason.
func (a *advance) serveOr(why string) advanceResult {
	r := a.finish(nil, forkBuild{}, 0, nil, why+" — the next fresh launch tries again")
	r.failed = true
	return r
}

// noFit ends an advance whose list nothing on it takes: held at the good build, or nothing.
func (a *advance) noFit(edited bool, newest string) advanceResult {
	why := fmt.Sprintf("no upstream version %s follows takes its patch series", a.f.Label())
	if newest != "" {
		why = fmt.Sprintf("upstream %s does not take %s's patch series", newest, a.f.Label())
	}
	if a.serving == nil {
		next := "`yolo pack update` shows the conflict and how to rebase the series"
		if edited {
			next += "; reverting the edit brings back the good build " + run.GoodBuildLabel(a.rec.Good)
		}
		a.warn("%s: nothing to build — %s has no %s; %s", a.f.Label(), a.here(), a.f.Thing(), next)
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
// member already upstream once, on the launch that found it. ofList marks a walk of the check's list
// (not the series' base alone), whose apply error finish records (PF-D45). It takes what is left of
// the replay's bound (PF-D44).
func (a *advance) walk(list []packsrc.ListEntry, ofList bool) packsrc.WalkResult {
	start := time.Now()
	w := a.packs.WalkSeries(a.repo, a.subdir, a.series, list, packsrc.WalkOptions{Spent: a.replaySpent})
	a.replaySpent += replayElapsed(start)
	if a.interrupted() {
		return w
	}
	if ofList {
		a.walked, a.applyErr = true, nil
		if err := a.walkErr(w); w.Fit < 0 && w.Base == nil && err != nil {
			a.applyErr = &packsrc.ApplyError{Seq: a.seq, Error: oneLineErr(err)}
		}
	}
	if err := a.packs.RecordWalk(a.f.Key(), a.series.Digest, a.yolo, w, patchedNow()); err != nil {
		a.warn("%s: recording the replay: %v", a.f.Label(), err)
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
			a.pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("%s: upstream %s does not take the patch series —",
				a.f.Label(), r.Entry.Label())))
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
// next steps — or, when the series' base is built next (thenBase), that.
func (a *advance) buildFailedLines(b forkBuild, err error, retryAt time.Time) {
	f := a.f
	what := b.Entry.Label() + " + " + run.PatchCount(b.Series.Len())
	tail := a.runsNow()
	if a.thenBase {
		tail = a.baseNext()
	}
	a.warn("%s: the build of %s failed: %v — %s", f.Label(), what, err, tail)
	if a.o.workspace != "" {
		a.dim("  its output is above, and in %s", filepath.Join(a.o.workspace, ".yolo", "launch.log"))
	}
	switch {
	case a.thenBase:
	case a.serving != nil:
		a.dim("  `yolo capture %s` retries it now; a fresh launch retries it after %s", f.CaptureArg(),
			retryAt.Local().Format("2006-01-02 15:04"))
		a.dim("  to stay on the running version: `agent_updates` off for pack %s, or a tag `?ref=` in a manifest you own", f.Pack)
	default:
		a.dim("  the next fresh launch tries again, or `yolo capture %s` now", f.CaptureArg())
		if g := a.goodBuild(); g != nil && g.Recipe != a.recipe {
			a.dim("  reverting the edit to the series or the build brings back the good build %s", run.GoodBuildLabel(g))
		}
	}
}

// baseNext is what a failed build's line says follows it when the series' base is built next.
func (a *advance) baseNext() string {
	return a.here() + " has no " + a.f.Thing() + " from it, so the series is built at its base " +
		a.baseEntry().Label() + " instead"
}

// build runs the build act for b and settles its outcome under the build's lock (settle): the
// move, a recorded failure, or nothing recorded, and then the hand. With nothing serving, a fit
// whose build fails or cannot be put in place sends the advance on to the series' base (PF-D23).
func (a *advance) build(b forkBuild, base baseWhy, edited bool) advanceResult {
	f := a.f
	switch {
	case a.serving != nil && a.o.launch:
		a.say("%s: %s takes the series; building it — this launch waits for it, at most %s, and a Ctrl-C "+
			"starts this jail on the good build %s instead", f.Label(), b.Entry.Label(), forkBuildWaitBound,
			run.GoodBuildLabel(a.rec.Good))
	case base == baseNone:
		a.say("%s: %s takes the series; building it", f.Label(), b.Entry.Label())
	}
	a.thenBase = base == baseNone && a.serving == nil && a.baseFallback() && b.Commit != a.series.Base
	startFail := a.buildFailure(b.Commit)
	startGood := a.goodBuild()
	a.boundHit, a.ownLock = false, b.lockPath()
	mode := buildMode{force: a.o.force, packs: a.packs, lock: pidlock.NoWait, replaySpent: a.replaySpent}
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
	var settled *advanceResult
	mode.settle = func(entry *capture.Entry, err error) {
		r := a.settle(b, entry, err, base, edited)
		settled = &r
	}
	_, err := buildFork(b, mode, a.o.out, a.o.errw, a.o.color)
	r := advanceResult{}
	if settled != nil {
		r = *settled
	} else {
		r = a.settle(b, nil, err, base, edited) // the lock was never taken
	}
	if !a.thenBase || !r.fellShort || a.interrupted() {
		return r
	}
	// THE SERIES' BASE, IN THIS SAME ADVANCE (PF-D23, §8.1): nothing serves and the newest fit came
	// to nothing, so the base is tried before the program goes, with a replay bound of its own.
	a.thenBase = false
	a.replaySpent = 0
	w := a.walk([]packsrc.ListEntry{a.baseEntry()}, false)
	switch {
	case a.interrupted():
		return a.finish(nil, forkBuild{}, 0, nil, "")
	case w.Base != nil:
		a.warn("%s: %s", f.Label(), w.Base.Error())
		return a.serveOr(fmt.Sprintf("%s's series does not apply at its own base (%s)", f.Label(), w.Base.Error()))
	case w.Fit < 0:
		err := a.walkErr(w)
		a.warn("%s: could not replay the series at its base: %v — %s", f.Label(), err, a.runsNow())
		return a.serveOr(fmt.Sprintf("%s's series could not be replayed on the host (%v)", f.Label(), err))
	}
	res := a.build(forkBuild{Fork: f, Commit: a.series.Base, Platform: a.o.platform, Series: a.series,
		Entry: w.Results[w.Fit].Entry}, baseBuildFailed, edited)
	res.failed = true // what was asked for did not build, whatever the base did
	return res
}

// settle is the build act's result, settled while the build's lock is held (buildMode.settle,
// §6.6): the move of an admitted build; the record of a build line that failed or ran past the
// bound; and for everything else, which records nothing, its line. It hands the jail what runs.
func (a *advance) settle(b forkBuild, entry *capture.Entry, err error, base baseWhy, edited bool) advanceResult {
	f := a.f
	if err == nil {
		return a.moved(b, entry, base, edited)
	}
	if a.interrupted() || errors.Is(err, pidlock.ErrCanceled) {
		return a.finish(nil, forkBuild{}, 0, nil, "the advance was interrupted — the next fresh launch builds it")
	}
	if a.boundHit {
		// STOPPED AT THE BOUND (PF-D38, §8.1): a failed build, with its back-off, whether or not the
		// build line had started — a jail still booting at the bound (its own image build, say) took
		// as long as one that hung, and the next launch must not wait the whole bound again.
		return a.buildFailed(b, fmt.Errorf("it ran past the %s bound and was stopped", forkBuildWaitBound))
	}
	var waited waiterFailure
	var lockTimeout forkLockTimeout
	var source forkSourceError
	switch {
	case errors.As(err, &waited):
		// ANOTHER LAUNCH'S BUILD OF THIS CANDIDATE FAILED while this one waited (§6.6): its result is
		// this one's, recorded already, and nothing is rebuilt.
		tail := a.runsNow()
		if a.thenBase {
			tail = a.baseNext()
		}
		a.warn("%s: another launch's build of %s failed while this one waited: %s — %s", f.Label(),
			b.Entry.Label(), waited.msg, tail)
		r := a.serveOrUnlessBase(fmt.Sprintf("%s's build of %s failed on the host (%s)", f.Label(), b.Entry.Label(), waited.msg))
		r.fellShort = true
		return r
	case errors.As(err, &lockTimeout):
		a.warn("%s: %v — %s; the next launch takes that build's result, and if that pid has hung, "+
			"stopping it lets the next launch build", f.Label(), err, a.runsNow())
		return a.serveOr(fmt.Sprintf("%s: %v", f.Label(), err))
	case errors.Is(err, errForkBuildLocked):
		a.warn("%s: %v", f.Label(), err)
		a.dim("  %s", captureWaitStep(f.CaptureArg()))
		return a.serveOr(fmt.Sprintf("%s: %v", f.Label(), err))
	case errors.Is(err, errForkBuildNotStarted):
		// THE RUNTIME WOULD NOT START THE BUILD JAIL (PF-D21): not a failed build, so nothing is
		// recorded and the candidate stays pending.
		a.warn("%s: the build jail did not start (%v) — %s", f.Label(), err, a.runsNow())
		if a.o.runtime == "container" {
			a.dim("  On Apple Container a capture jail cannot start beside a running jail: once the other "+
				"jails stop, `yolo capture %s` builds it", f.CaptureArg())
		} else {
			a.dim("  `yolo capture %s` builds it once the runtime starts jails again; the next fresh launch tries too", f.CaptureArg())
		}
		return a.serveOr(fmt.Sprintf("%s's build jail did not start on the host (%v)", f.Label(), err))
	case errors.As(err, &source):
		// AN APPLY ERROR in the build's own replay: nothing recorded against the entry, and the next
		// check retries it (PF-D45).
		a.applyErr = &packsrc.ApplyError{Seq: a.seq, Error: oneLineErr(err)}
		tail := a.runsNow() + "; " + a.retryStep()
		if a.thenBase {
			tail = a.baseNext()
		}
		a.warn("%s: %v — %s", f.Label(), err, tail)
		r := a.serveOrUnlessBase(fmt.Sprintf("%s's series could not be put in place on the host (%v)", f.Label(), err))
		r.fellShort = true
		return r
	}
	// THE BUILD LINE RAN AND FAILED: recorded, with the back-off.
	return a.buildFailed(b, err)
}

// buildFailed records b's failed build — counted against the failures the record holds for its key
// (OQ-PD26's back-off) — and says it. Under the build's lock (settle), so a launch that waited on
// it reads the failure before it can take the lock (§6.6).
func (a *advance) buildFailed(b forkBuild, err error) advanceResult {
	f := a.f
	now := patchedNow()
	o := &packsrc.EntryOutcome{Commit: b.Commit, Kind: packsrc.OutcomeBuildFailed, Series: a.series.Digest,
		Yolo: a.yolo, Recipe: a.recipe, Error: oneLineErr(err), Count: 1, At: now.Unix()}
	var r advanceResult
	if a.thenBase {
		// The base is built next, and its own settle hands the jail what runs.
		a.recordOnly(o)
	} else {
		r = a.finish(nil, forkBuild{}, 0, o, fmt.Sprintf("%s's build of %s failed on the host (%s) — the next "+
			"fresh launch tries again, or `yolo capture %s` now", f.Label(), b.Entry.Label(), oneLineErr(err), f.CaptureArg()))
	}
	a.buildFailedLines(b, err, autoCaptureRetryAt(capture.AutoFailure{Failures: o.Count, Last: now}))
	r.failed, r.fellShort = true, true
	return r
}

// recordOnly writes a failed build's outcome, counted against the record's, and nothing else.
func (a *advance) recordOnly(o *packsrc.EntryOutcome) {
	err := a.packs.WithCheckRecord(a.f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		setFailure(r, o)
		a.rec = r
		return true, nil
	})
	if err != nil {
		a.warn("%s: could not update its check record: %v", a.f.Label(), err)
	}
}

// setFailure sets o on r, its count one more than the failures r holds for its key (commit,
// series, recipe, yolo).
func setFailure(r *packsrc.CheckRecord, o *packsrc.EntryOutcome) {
	for _, prev := range r.Outcomes {
		if prev.Kind == o.Kind && prev.Commit == o.Commit && prev.Series == o.Series && prev.Recipe == o.Recipe &&
			prev.Yolo == o.Yolo {
			o.Count = prev.Count + 1
		}
	}
	r.SetOutcome(*o)
}

// serveOrUnlessBase is serveOr, unless the series' base is built next (thenBase): then nothing is
// handed yet, since the base's own settle hands the jail what runs.
func (a *advance) serveOrUnlessBase(why string) advanceResult {
	if a.thenBase {
		return advanceResult{failed: true}
	}
	return a.serveOr(why)
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
func (a *advance) moved(b forkBuild, entry *capture.Entry, base baseWhy, edited bool) advanceResult {
	prev := a.goodBuild()
	r := a.finish(entry, b, a.seq, nil, "")
	if r.gone != nil {
		// THE BUILD WENT BEFORE IT COULD BE HANDED: never moved to (§6.7, "never moves the good build
		// to a build this machine has not admitted"), and the jail runs what the record names.
		a.warn("%s: the build of %s left the capture store before this launch could hand it (%v) — %s; the "+
			"next fresh launch builds it again", a.f.Label(), b.Entry.Label(), r.gone, a.handedNow(r))
		r.failed = true
		return r
	}
	r.built = true
	if r.lost {
		a.say("%s: another launch moved the good build meanwhile, from a newer check; "+a.here()+" runs that one, "+
			"and this build is reaped", a.f.Label())
		return r
	}
	what := b.Entry.Label() + " + " + run.PatchCount(b.Series.Len())
	switch {
	case prev == nil:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("built "+a.f.Label()+": "+what+"; "+a.here()+" runs it"+a.baseClause(base)))
	case prev.Entry == entry.Key:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("rebuilt "+a.f.Label()+": "+what+"; "+a.here()+" runs it"+a.baseClause(base)))
	case edited:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("updated "+a.f.Label()+": "+run.GoodBuildLabel(prev)+
			" → "+b.Entry.Label()+", the edited series ("+run.PatchCount(b.Series.Len())+", series "+
			b.Series.ShortDigest()+"); "+a.here()+" runs the new build"+a.baseClause(base)))
	default:
		a.pr.Printf("[bold]%s[/bold]", richtext.Escape("updated "+a.f.Label()+": "+run.GoodBuildLabel(prev)+" → "+
			b.Entry.Label()+", "+run.PatchCount(b.Series.Len())+"; "+a.here()+" runs the new build"+a.baseClause(base)))
	}
	return r
}

// baseClause is the move line's last clause for a build at the series' base: why it is there, and
// whether that holds anything (PF-D27: a branch with nothing newer holds nothing).
func (a *advance) baseClause(base baseWhy) string {
	switch base {
	case baseEmpty:
		return " — no version of the branch is newer than the series' base"
	case baseNoFit:
		return " — held at the series' base, which no version of the branch takes the series past"
	case baseApplyErr:
		return " — held at the series' base until the series replays at a newer version; the next check retries"
	case baseBuildFailed:
		return " — held at the series' base, since the newest version the series takes did not build"
	}
	return ""
}

// handedNow says what the jail was handed by r: the good build, or nothing.
func (a *advance) handedNow(r advanceResult) string {
	if r.delivery.Key != "" && a.rec.Good != nil {
		return a.here() + " runs " + a.goodLine()
	}
	return a.here() + " has no " + a.f.Thing()
}

// finish is the advance's last act, under the fork's record lock (§6.1, §6.6) — and, after a build,
// under its build lock too (settle): the move of an admitted build, a compare-and-swap that prefers
// the newer check, and only to a build still in the store; a failure's outcome; the walk's apply
// error; the hand of the good build as the record now names it (or why when it does not serve); and
// the reap of what the move leaves behind. reason is what the jail is handed when nothing serves.
func (a *advance) finish(built *capture.Entry, b forkBuild, seq int64, failure *packsrc.EntryOutcome, reason string) advanceResult {
	f := a.f
	var res advanceResult
	var moved, lost bool
	err := a.packs.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		changed := false
		if built != nil {
			_, gone := a.store.Resolve(built.Key)
			switch {
			case gone != nil:
				res.gone = gone
			case r.Good != nil && seq < r.Good.Seq:
				lost = true
			default:
				read := r.Read
				r.Good = &packsrc.GoodBuild{Commit: b.Commit, Tag: b.Entry.Tag, Version: b.Entry.Version,
					Series: b.Series.Digest, Recipe: b.recipe(), Tree: a.builtTree(built), Patches: b.Series.Len(),
					Seq: seq, Read: &read, Entry: built.Key, At: patchedNow().Unix()}
				dropBuildFailure(r, b.Commit)
				moved, changed = true, true
			}
		}
		if failure != nil {
			setFailure(r, failure)
			changed = true
		}
		switch {
		case a.applyErr != nil:
			r.ApplyErr, changed = a.applyErr, true
		case a.walked && r.ApplyErr != nil:
			r.ApplyErr, changed = nil, true
		}
		a.rec = r
		h := run.HandedFork{Fork: f.Key()}
		if g := r.Good; g != nil && g.Recipe == a.recipe {
			if e := a.exactGood(g); e != nil {
				h.Key, h.Commit, h.Tag, h.Patches, h.Series = e.Key, g.Commit, g.Tag, g.Patches, g.Series
				good := *g
				res.good = &good
			}
		}
		if h.Key == "" {
			h.Reason = reason
			if h.Reason == "" {
				h.Reason = f.Label() + " has no build on this machine yet — the next fresh launch builds it"
			}
		}
		res.delivery = entrypoint.ForkDelivery{Key: h.Key, Reason: h.Reason}
		if a.o.hand != nil {
			if err := a.o.hand(f.Bin, h); err != nil && h.Key != "" {
				a.warn("%s: could not record what this launch hands its jail (%v) — until it is recorded, "+
					"another launch's move of this fork may remove the build before this jail first runs %s; "+
					"if %s then cannot start, a fresh launch delivers the good build", f.Label(), err, f.Thing(), f.Thing())
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
		a.warn("%s: could not update its check record: %v", f.Label(), err)
		if res.delivery.Key == "" && res.delivery.Reason == "" {
			res.delivery.Reason = fmt.Sprintf("%s's check record could not be updated (%v)", f.Label(), err)
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
// every build, and so does an entry any other program's record names, and one whose build's lock is
// held: another advance admitted it and has not settled it yet, and its own swap, or its own reap
// when it loses, decides it (PF-D46).
func (a *advance) reapOthers(keep string) {
	handed, err := a.handedKeys()
	if err != nil {
		a.dim("%s: kept every earlier build, since what running jails were handed cannot be read (%v)", a.f.Label(), err)
		return
	}
	scan, err := capture.Scan(a.store, captureRecords)
	if err != nil {
		return
	}
	for _, e := range scan {
		if e.Key == keep || handed[e.Key] || !a.onlyThisFork(e.Records) || a.building(e.Records) {
			continue
		}
		_ = a.store.ReapEntry(e.Key)
	}
}

// handedKeys are the builds of this key a move keeps for the running jails (PF-D20): for a fork,
// every key a launch recorded handing a jail, since a jail materializes its program at its first
// run. A PATCHED EXTENSION's jails each hold a copy made at the launch and read no store entry, so
// a move reaps every other build of it at once (PPX-D7): a copy a launch was making when the move
// reaped its entry fails its completion-marker check and is made again.
func (a *advance) handedKeys() (map[string]bool, error) {
	if a.f.IsTree() {
		return map[string]bool{}, nil
	}
	return run.HandedForkKeys()
}

// building reports whether a record of an entry names a build whose lock another holds right now
// (patchedBuildID, from the receipt's fork, source, revision, recipe and platform). This advance's
// own build lock is not another's.
func (a *advance) building(recs []capture.Record) bool {
	for _, r := range recs {
		lock := forkBuildLockPath(patchedBuildID(r.Fork, r.Source, r.Revision, r.Recipe, r.Platform))
		if lock != a.ownLock && pidlock.Held(lock) {
			return true
		}
	}
	return false
}

// reapOne reaps a build that lost the swap, unless a running jail was handed it.
func (a *advance) reapOne(key string) {
	handed, err := a.handedKeys()
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
	a.dim("%s: recovered its good build %s from the capture store into its check record (%s)", a.f.Label(),
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
// launch's store. It is the fit's second replay, under the build's lock (which is keyed on the fit,
// so only after the walk found it), and takes what the walk left of the replay's bound (spent,
// PF-D44).
func replayIntoSource(packs *packsrc.Store, b forkBuild, dst string, spent time.Duration) (string, error) {
	if packs == nil {
		packs = packsrc.LaunchStore(paths.PacksDir())
	}
	a, err := packsrc.Parse(b.Fork.Source)
	if err != nil {
		return "", err
	}
	w := packs.WalkSeries(a.Repo, a.Path, b.Series, []packsrc.ListEntry{b.Entry}, packsrc.WalkOptions{
		Spent: spent, OnFit: func(tree string) error { return copySourceTree(tree, dst) }})
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
