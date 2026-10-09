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
//     the launch (§7). Every build a jail launch runs is that child, a first advance's included,
//     outside any interrupt scope then, so its output can be kept off the terminal: the launch
//     shows it as one progress line (advanceOptions.report, buildreport.go, PF-D79).
//   - ONE CTRL-C ENDS THE ACT'S WHOLE WAIT (PF-D57): an act that runs several advances — every
//     patched fork, then every patched extension, at a jail launch; the extensions, then the
//     program, at `yolo host`; all of them at `yolo host apply --assert` — shares one act interrupt
//     (advanceOptions.act), and an advance that begins after a Ctrl-C ended an earlier one checks and
//     builds nothing: the good build is handed, or, with nothing serving, nothing is, naming the act
//     that builds it (actStopped).
//   - `yolo capture <bin>` (force): the check forced, the pending candidate built ignoring a
//     back-off, and with none pending the good build's own inputs rebuilt; through the swap.
//   - THE HOST FLOOR'S INSTALL (§9, PF-D14): `yolo host -- <bin>` and `yolo host apply --assert`,
//     through the floor's Advance (internal/cli's hostfloor.go). A launch's advance in every rule
//     above — it waits, interruptibly while a good build serves (PF-D25) — whose lines name `yolo
//     host` and its next launch rather than a jail (advanceOptions.host), and which hands nothing:
//     the floor installs the good build the record names once the advance returns. The floor's own
//     installed copy of the series as it stands serves as a good build does (advanceOptions.installed,
//     PF-D55): it keeps running whatever the advance does, so the advance is interruptible, honors a
//     back-off and never builds the series' base in its place, and a good build that copy already is
//     is not built again for want of its store entry.
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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
	// backgroundErrw is the real detached-process error stream, used for typed application failures
	// before a later tree lane's ordered host report can flush.
	backgroundErrw io.Writer
	color          bool
	// launch is a fresh jail launch's advance: the check throttled, a back-off honored, the wait
	// interruptible while a good build serves.
	launch bool
	// force is `yolo capture <bin>`'s: the check forced, a back-off ignored, the good build's own
	// inputs rebuilt when nothing is pending, the build lock not waited for.
	force bool
	// hand records what the launch hands its jail (run.ForkBuildRequest.Hand), under the record
	// lock; nil records nothing.
	hand func(bin string, h run.HandedFork) error
	// host is the HOST: the host floor's install act for a patched fork (§9, PF-D14: `yolo host --
	// <bin>`, or `yolo host apply --assert`), or the host's own render of a patched extension
	// (`yolo host apply`, PPX-D11). With launch set, it is a launch's advance in every rule (the check
	// throttled, a back-off honored, the wait interruptible while a good build serves: PF-D25); only
	// its lines differ, naming the host and its next act where a jail's name this jail and the next
	// fresh launch (PF-D50, PPX-D28).
	host bool
	// installed is the host floor's own copy of the program when it is a build of the series as it
	// stands — the copy a failed install keeps (PF-D8) — nil otherwise, and always for a jail, whose
	// program is a store entry, and for a patched extension. While no store entry of the good build
	// serves, it does (PF-D55).
	installed *installedCopy
	// act is the act this launch's advance is one of (run.ActInterrupt, PF-D57): a jail launch's
	// fork-build slot, a `yolo host -- <bin>`, a `yolo host apply --assert`. Its interrupt scope is
	// the act's, and once a Ctrl-C has ended any advance of the act, a later one checks and builds
	// nothing (actStopped). nil for an advance that is an act of its own.
	act *run.ActInterrupt
	// pool, ctx and started are a HOST ACT'S PARALLEL ADVANCE's (treepool.go, XB-D39): the bounds on
	// its checks and builds; the context of the one interrupt scope the whole pool runs under, which
	// this advance reads in place of opening a scope of its own (one per concurrent advance would catch
	// a Ctrl-C in the innermost alone); and the line its build says at once. All nil outside a pool,
	// and at a jail launch, whose pool is slot's.
	pool *advancePool
	ctx  context.Context
	// operationCtx carries ordinary host/Floor work cancellation and values without claiming an
	// externally owned runner lane or suppressing the launch's interrupt scope.
	operationCtx context.Context
	// readOnlyInitialization keeps the Floor's initial authority/recovery read in memory. The later
	// local detached/opaque authority probe remains enabled and retains its own recording policy.
	readOnlyInitialization bool
	started                func()
	// report is a jail launch's build report (buildreport.go, PF-D79): each build's start line
	// carrying what the lines before it said, its build jail always the fork-build-jail child with
	// every stream kept off the terminal, and the move line that build's result line. nil — `yolo
	// capture`, `yolo host` and `yolo host apply` — prints each line as its own and streams the build
	// jail's output.
	report *buildReport
	// slot is this advance's key in a jail launch's build pool (buildpool.go, XB-D10, set with
	// report): the pool's one interrupt scope, which this advance runs under rather than opening its
	// own, its first advance included (PF-D80), the check slot its check waits for, and the build slot
	// its walk and build wait for. nil runs the advance as an act of its own.
	slot *poolItem
	// background is the BACKGROUND ADVANCE's (backgroundadvance.go; docs/design/pi-extension-store-
	// builds.md §7.4, XB-D19), set with launch and a pool's lane: it runs for the next fresh launch,
	// which its lines name; it tries each lock once — the check's record and mirror locks, the walk's
	// mirror lock, the build's lock — and skips the key when one is held (§6.2 rule 5), while its
	// completed build's settle record write waits (rule 1); a stopped build jail is removed by name.
	background bool
}

// advanceActScope is the production ActInterrupt scope boundary. The pass-through default keeps
// scope ownership in run.ActInterrupt; its narrow seam lets caller-path tests count entry without
// replacing the signal arm.
var advanceActScope = func(act *run.ActInterrupt, fn func(context.Context)) os.Signal {
	return act.Scope(fn)
}

// installedCopy is a copy of the program that runs outside the capture store: the host floor's
// installed build (advanceOptions.installed).
type installedCopy struct {
	// commit and recipe are what it is a build of: the upstream commit, and the recipe hash with the
	// series digest in it.
	commit, recipe string
	// label is what a line names it: "v1.1.0 (3f2a9c1e) + 2 patches".
	label string
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
	// bypassed is a foreground explicit skip authorized by a proved compatible serving build.
	bypassed bool
	// operationError is an independently failed current check, retained beside a bypass result.
	operationError string
	// patchFailure is the current classified failure, carried beside any compatible delivery.
	patchFailure *packsrc.PatchFailure
	// patchFailureSaid is true when this advance printed patchFailure's error block itself
	// (reportPatchFailure), so a caller that also reports failures does not print it a second time.
	patchFailureSaid bool
	// continuingSaid is true when this advance printed the bypass's CONTINUING line itself, so a
	// caller that also says what continues (the host floor's preparation) does not say it again.
	continuingSaid bool
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
	// What a BACKGROUND ADVANCE's outcome record carries for the next launch's line (XB-D20): from and
	// to, the good build a move left and the one it moved to; failWhat, failErr and retryAt, a failed
	// build's, with its back-off's end; skipped, why a held lock skipped the key; retained, why a build
	// jail not known gone kept its staging and the candidate pending; and problem, a check that could
	// not run or found nothing to build, one line.
	from, to          string
	failWhat, failErr string
	retryAt           time.Time
	skipped           string
	retained          string
	problem           string
}

// forkDelivery returns the program answer while preserving current-operation evidence even when
// the record callback or a later delivery step failed.
func (r advanceResult) forkDelivery() entrypoint.ForkDelivery {
	delivery := r.delivery
	if r.patchFailure != nil {
		delivery.PatchFailure = r.patchFailure
	}
	return delivery
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
	ctx    context.Context // the effective work context: operation parent, owned lane, or scoped merge
	// serving is the good build the advance started with, when it serves (its store entry).
	serving *capture.Entry
	// legacyGood is an in-memory pre-migration identity whose old receipt still serves a read-only
	// Floor initialization; a normal advance durably re-keys it before this pointer is needed.
	legacyGood *packsrc.GoodBuild
	// installed is the host floor's copy that serves when no store entry does (PF-D55): set only with
	// serving nil, and only when it is a build of the recipe as it stands.
	installed *installedCopy
	rec       *packsrc.CheckRecord
	// seq is the sequence number of the check the walk's list came from.
	seq int64
	// boundHit is set by the child build runner when the build ran past forkBuildWaitBound.
	boundHit                       bool
	patchFailure                   *packsrc.PatchFailure
	failureReported                bool
	backgroundAuthorityUnavailable string
	replayBinding                  *packsrc.ReplaySnapshot
	repairToken                    *backgroundRepairToken
	// replayRecordAuthorized distinguishes a settled walk from a successfully changed, guarded
	// record for its current binding; only the latter may clear an older ApplyErr in finish.
	replayRecordAuthorized bool
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
	// why is, with a report, what the line before a build says without one — the upstream moved,
	// the series was edited, the good build is gone, no build yet — for the build's start line.
	why string
	// inFlight is the build under way's share of the report, nil between builds and without a
	// report.
	inFlight *buildRun
	// stopSaid is set once actStopped has said that a Ctrl-C ended this advance, which the pool's
	// interruptedLine then does not say again.
	stopSaid bool
	// problem is a check's failure as its warning said it, one line, which finish carries to the
	// result (advanceResult.problem).
	problem string
	// operationError is a current check's fetch failure, retained separately from a patch bypass.
	operationError string
}

// baseWhy is why an advance builds the series at its own base (§6.4), baseNone when it does not.
type baseWhy int

const (
	baseNone baseWhy = iota
	// baseEmpty: the branch has no version newer than the series' base (PF-D27): nothing is held.
	baseEmpty
	// baseUntagged: the branch carries no version tag the release rule reads at all (PF-D60): the
	// series stays at its base until a tag appears or `follow` changes, which the build's line says.
	baseUntagged
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
	ctx := o.ctx
	if o.slot != nil {
		ctx = o.slot.context()
	}
	if o.launch && (o.act.Interrupted() || ctx != nil && ctx.Err() != nil) {
		return a.actStopped()
	}
	if o.launch && ctx != nil {
		// IN A POOL — a jail launch's build pool (buildpool.go), a host act's parallel advance
		// (treepool.go) — the pool's one interrupt scope is this advance's, so one Ctrl-C ends every
		// key's wait at once, whether or not a good build serves: the jail starts on what serves, or
		// without what nothing does, a first build's included (XB-D10, PF-D57, PF-D80).
		a.ctx = ctx
		a.packs.Ctx = ctx
		res := a.run()
		if ctx.Err() != nil && !res.built && !a.stopSaid {
			a.interruptedLine()
		}
		return a.withOperationCancellation(res)
	}
	if !o.launch || !a.serves() {
		return a.withOperationCancellation(a.run())
	}
	// A GOOD BUILD SERVES — or the floor's copy does (PF-D55) — so a Ctrl-C ends this advance and the
	// jail, or `yolo host`, starts on it (PF-D25), and the act's later advances begin none (PF-D57).
	var res advanceResult
	sig := advanceActScope(o.act, func(scopeCtx context.Context) {
		workCtx, stopScope, cancelWork := mergeAdvanceOperationContext(o.operationCtx, scopeCtx)
		defer func() {
			stopScope()
			cancelWork()
		}()
		a.ctx = workCtx
		a.packs.Ctx = workCtx
		res = a.withOperationCancellation(a.run())
	})
	if sig != nil && !res.built {
		// Every step that saw the interrupt ended in finish, which handed the good build.
		a.interruptedLine()
	}
	return res
}

func (a *advance) withOperationCancellation(result advanceResult) advanceResult {
	if a.o.operationCtx == nil || a.o.operationCtx.Err() == nil {
		return result
	}
	message := a.o.operationCtx.Err().Error()
	if !strings.Contains(result.operationError, message) {
		if result.operationError == "" {
			result.operationError = message
		} else {
			result.operationError += "; " + message
		}
	}
	return result
}

// mergeAdvanceOperationContext retains the operation parent's values and deadline while forwarding
// scope cancellation. Stop and cancel are returned separately so cleanup can prevent a late scope
// callback from reaching work after its operation has returned.
func mergeAdvanceOperationContext(parent, scope context.Context) (context.Context, func() bool, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	workCtx, cancel := context.WithCancel(parent)
	stop := func() bool { return true }
	if scope != nil {
		stop = context.AfterFunc(scope, cancel)
		if scope.Err() != nil {
			cancel()
		}
	}
	return workCtx, stop, cancel
}

// interruptedLine says that a Ctrl-C ended this advance, and what the jail starts on: the good
// build, or — in a pool, whose scope a first advance runs under too (PF-D80) — nothing, until the
// next fresh launch builds it.
func (a *advance) interruptedLine() {
	f := a.f
	if a.serves() {
		a.pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("%s: the advance was interrupted — %s "+
			"%s; %s tries again", f.Label(), a.startsOn(), a.servingName(), a.next())))
		return
	}
	a.warn("%s: not built — a Ctrl-C ended %s's wait for its patched builds, and %s; %s builds it, or "+
		"`yolo capture %s` now", f.Label(), a.waiter(), a.hasNo(), a.next(), f.CaptureArg())
}

// actStopped ends an advance that an earlier advance's Ctrl-C in the same act stopped before it began
// (PF-D57): no check, no replay and no build, since the user asked once to stop waiting for the act.
// What serves is handed, as the interrupted advance hands its own; with nothing serving, nothing is,
// and the reason names the act that builds it — a first build would be a new wait, of up to
// forkBuildWaitBound, that the user had just declined.
func (a *advance) actStopped() advanceResult {
	f := a.f
	a.stopSaid = true
	failure := a.deliveryPatchFailure(false)
	if failure.State == "unavailable" {
		return a.unavailableDetachedAuthority(failure)
	}
	if failure.State == "failure" && failure.Failure != nil {
		return a.patchFailureResult(failure.Failure)
	}
	if a.serves() {
		a.dim("%s: not checked — a Ctrl-C ended %s's wait for its patched builds; %s %s, and %s checks it",
			f.Label(), a.waiter(), a.startsOn(), a.servingName(), a.next())
		return a.finish(nil, forkBuild{}, 0, nil, "")
	}
	a.warn("%s: not built — a Ctrl-C ended %s's wait for its patched builds, and %s; %s builds it, or "+
		"`yolo capture %s` now", f.Label(), a.waiter(), a.hasNo(), a.next(), f.CaptureArg())
	r := a.finish(nil, forkBuild{}, 0, nil, fmt.Sprintf("%s was not built: a Ctrl-C ended the wait for its "+
		"patched builds — %s builds it, or `yolo capture %s` now", f.Label(), a.next(), f.CaptureArg()))
	r.failed = true
	return r
}

// newAdvance reads what f's advance starts from — its series, its recipe, its check record (or one
// recovered from the store) and the good build that serves — or, when the series cannot be read,
// the advance's whole result.
func newAdvance(f packload.Fork, o advanceOptions) (*advance, *advanceResult) {
	a := &advance{f: f, o: o, pr: richtext.Printer{W: o.out, Color: o.color}, epr: richtext.Printer{W: o.errw, Color: o.color},
		packs: patchedAdvanceStore(o.launch), store: &capture.Store{Dir: paths.CapturesDir()},
		yolo: patchedYoloVersion(), ctx: context.Background(), repo: mustRepo(f.Source), subdir: subdirOf(f.Source)}
	if o.background {
		// A BACKGROUND HOLDER NEVER WAITS (§6.2 rule 5): its check and walk try each lock once.
		bg := *a.packs
		bg.NoWait = true
		a.packs = &bg
	}
	if o.operationCtx != nil {
		// Ordinary operation context is evidence/cancellation, not ownership. Give this advance its own
		// shallow Store copy before loadOrRecover consumes the context, preserving every store policy.
		a.ctx = o.operationCtx
		if a.packs != nil {
			local := *a.packs
			local.Ctx = o.operationCtx
			a.packs = &local
		}
	}
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
	if a.rec != nil && a.rec.PatchFailure != nil &&
		(a.rec.PatchFailure.Series != a.series.Digest || a.rec.PatchFailure.Inputs != a.in) {
		if !o.readOnlyInitialization {
			_ = a.packs.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
				if r.PatchFailure != nil && (r.PatchFailure.Series != a.series.Digest || r.PatchFailure.Inputs != a.in) {
					r.PatchFailure = nil
					return true, nil
				}
				return false, nil
			})
		}
		a.rec.PatchFailure = nil
	}
	if a.rec != nil && a.rec.Good != nil && a.rec.Good.Recipe == a.recipe {
		a.serving = a.exactGood(a.rec.Good)
	}
	if a.serving == nil && o.installed != nil && o.installed.recipe == a.recipe {
		// THE FLOOR'S COPY SERVES (PF-D55): no store entry of the good build does, and the floor holds
		// a build of the series as it stands, which keeps running whatever this advance does.
		a.installed = o.installed
	}
	return a, nil
}

// records is the store used ONLY to settle a completed build's move or failure under its build
// lock. That compare-and-swap may wait (§6.2 rule 1); initialization, replay records and all other
// background work use a.packs and skip a busy lock (§6.2 rule 5).
func (a *advance) records() *packsrc.Store {
	if !a.packs.NoWait {
		return a.packs
	}
	s := *a.packs
	s.NoWait = false
	return &s
}

func (a *advance) replaySnapshot() (packsrc.ReplaySnapshot, error) {
	return a.rec.ReplaySnapshot(a.in, a.series.Digest)
}

func (a *advance) recordReplay(snapshot packsrc.ReplaySnapshot, walk packsrc.WalkResult) packsrc.RecordReplayResult {
	a.replayRecordAuthorized = false
	result := a.packs.RecordReplay(snapshot, a.yolo, walk, patchedNow())
	if result.Err != nil {
		a.warn("%s: recording the replay: %v", a.f.Label(), result.Err)
	}
	if errors.Is(result.Err, packsrc.ErrStaleReplay) {
		fresh, err := a.packs.LoadCheckRecord(a.f.Key())
		if err != nil {
			a.patchFailure = nil
			result.Err = errors.Join(result.Err,
				fmt.Errorf("replay authority changed and the current check record could not be read: %w", err), result.Failure)
			return result
		}
		a.rec = fresh
		if current := fresh.CurrentPatchFailure(a.in, a.series.Digest); current != nil {
			a.patchFailure = current
			result.Failure = current
			result.Err = nil
		} else {
			a.patchFailure = nil
			result.Failure = nil
		}
		return result
	}
	if result.Failure != nil {
		a.patchFailure = result.Failure
	}
	if result.Recorded && result.Err == nil {
		if fresh, err := a.packs.LoadCheckRecord(a.f.Key()); err == nil && fresh.Seq == snapshot.Seq && fresh.Read == snapshot.Inputs {
			a.rec = fresh
			a.patchFailure = fresh.CurrentPatchFailure(a.in, a.series.Digest)
			if next, err := a.replaySnapshot(); err == nil {
				a.replayBinding = &next
				a.replayRecordAuthorized = reflect.DeepEqual(fresh.PatchFailure, snapshot.Failure) &&
					reflect.DeepEqual(fresh.ApplyErr, snapshot.ApplyErr)
			}
		}
		if err := recordBackgroundPatchRepair(a.ctx, a.repairToken, a.packs, snapshot, a.yolo, walk, result, a.o.background); err != nil {
			a.warn("%s: its clean replay was recorded, but the matching detached failure evidence could not be resolved (%v) — that evidence remains unresolved, and the next launch checks it again",
				a.f.Label(), err)
		}
	}
	return result
}

// deliveryPatchFailure consumes classified authority and, only when requested, locally retries one
// selected opaque legacy apply diagnosis. It never infers failure from legacy text.
func (a *advance) recordSourceReplay(snapshot packsrc.ReplaySnapshot, walk packsrc.WalkResult) packsrc.RecordReplayResult {
	if a.replayBinding == nil || !reflect.DeepEqual(*a.replayBinding, snapshot) {
		a.replayRecordAuthorized = false
	}
	result := a.packs.RecordReplay(snapshot, a.yolo, walk, patchedNow())
	if result.Err != nil {
		a.replayRecordAuthorized = false
	}
	if result.Recorded && result.Err == nil {
		fresh, err := a.packs.LoadCheckRecord(a.f.Key())
		if err != nil || fresh.Seq != snapshot.Seq || fresh.Read != snapshot.Inputs || fresh.Check == nil ||
			fresh.Check.Seq != snapshot.Seq || !reflect.DeepEqual(fresh.PatchFailure, snapshot.Failure) ||
			!reflect.DeepEqual(fresh.ApplyErr, snapshot.ApplyErr) {
			a.replayRecordAuthorized = false
		} else {
			a.replayRecordAuthorized = true
		}
		if err := recordBackgroundPatchRepair(a.ctx, a.repairToken, a.packs, snapshot, a.yolo, walk, result, a.o.background); err != nil {
			a.warn("%s: its clean source replay was recorded, but the matching detached failure evidence could not be resolved (%v) — that evidence remains unresolved, and the next launch checks it again",
				a.f.Label(), err)
		}
	}
	if result.Err != nil && !errors.Is(result.Err, packsrc.ErrStaleReplay) {
		a.warn("%s: recording the source replay: %v", a.f.Label(), result.Err)
	}
	if errors.Is(result.Err, packsrc.ErrStaleReplay) {
		fresh, err := a.packs.LoadCheckRecord(a.f.Key())
		if err != nil {
			result.Err = errors.Join(result.Err,
				fmt.Errorf("replay authority changed and the current check record could not be read: %w", err), result.Failure)
			return result
		}
		a.rec = fresh
		if current := fresh.CurrentPatchFailure(a.in, a.series.Digest); current != nil {
			result.Failure = current
			result.Err = nil
		} else {
			result.Failure = nil
		}
	}
	return result
}

func (a *advance) deliveryPatchFailure(probe bool) packsrc.LegacyReplayResult {
	result := packsrc.LegacyReplayResult{State: "not-needed"}
	if a.rec == nil || a.series == nil {
		return result
	}
	if failure, fresh, handled := a.backgroundPatchFailureFromOutcome(); handled {
		if a.backgroundAuthorityUnavailable != "" {
			result.State, result.Diagnostic = "unavailable", a.backgroundAuthorityUnavailable
			return result
		}
		if fresh != nil {
			a.rec = fresh
		}
		if failure != nil {
			result.State, result.Failure = "failure", failure
			return result
		}
	} else if failure := a.rec.CurrentPatchFailure(a.in, a.series.Digest); failure != nil {
		result.State, result.Failure = "failure", failure
		return result
	}
	if !probe || a.rec.ApplyErrAtLastCheck() == nil {
		return result
	}
	snapshot, err := a.replaySnapshot()
	if err != nil {
		result.State, result.Diagnostic = "unavailable", err.Error()
		return result
	}
	candidates := a.rec.Candidates(a.in)
	pending := a.pendingOf(candidates, false)
	var target packsrc.ListEntry
	if len(pending) > 0 {
		target = pending[0]
	} else if a.baseFallback() {
		target = a.baseEntry()
	} else {
		result.State, result.Diagnostic = "unavailable", "no selected pending target is available for local replay"
		return result
	}
	result = a.packs.ReclassifyLegacyApplyError(snapshot, a.series, target, a.yolo,
		packsrc.LegacyReplayOptions{Now: patchedNow()})
	if errors.Is(result.Err, packsrc.ErrStaleReplay) {
		fresh, readErr := a.packs.LoadCheckRecord(a.f.Key())
		if readErr != nil {
			result.State, result.Failure = "unavailable", nil
			result.Diagnostic = fmt.Sprintf("replay authority changed and the current check record could not be read: %v", readErr)
			return result
		}
		a.rec = fresh
		if failure := fresh.CurrentPatchFailure(a.in, a.series.Digest); failure != nil {
			result.State, result.Failure, result.Err = "failure", failure, nil
		} else {
			result.State, result.Failure, result.Err = "stale", nil, packsrc.ErrStaleReplay
		}
		return result
	}
	if result.State == "failure" && result.Failure != nil {
		a.patchFailure = result.Failure
	}
	if result.Recorded {
		if fresh, readErr := a.packs.LoadCheckRecord(a.f.Key()); readErr == nil {
			a.rec = fresh
		}
	}
	if result.Diagnostic != "" {
		a.warn("%s: could not classify its earlier replay error locally: %s", a.f.Label(), result.Diagnostic)
	}
	return result
}

// backgroundPatchFailureFromSaid remains the typed failure accessor; delivery uses the richer result
// below so a resolved artifact also refreshes an older in-memory CheckRecord.
func (a *advance) backgroundPatchFailureFromSaid() *packsrc.PatchFailure {
	failure, _, _ := a.backgroundPatchFailureFromOutcome()
	return failure
}

// backgroundPatchFailureFromOutcome reads classified evidence from the current generation's .json,
// .said or key-owned claim only after fresh CheckRecord authority has been read. A current typed
// failure wins before any detached candidate or its repair marker.
func (a *advance) backgroundPatchFailureFromOutcome() (*packsrc.PatchFailure, *packsrc.CheckRecord, bool) {
	if a.o.background || a.packs == nil || a.series == nil {
		return nil, nil, false
	}
	a.backgroundAuthorityUnavailable = ""
	fresh, err := a.packs.LoadCheckRecord(a.f.Key())
	missingRecord := errors.Is(err, packsrc.ErrNoCheckRecord)
	if err != nil && !missingRecord {
		a.backgroundAuthorityUnavailable = err.Error()
		return nil, nil, true
	}
	if fresh != nil && fresh.Owner != a.f.Key() {
		a.backgroundAuthorityUnavailable = "the current CheckRecord belongs to another key"
		return nil, nil, true
	}
	if fresh != nil && fresh.Read == a.in {
		if current := fresh.CurrentPatchFailure(a.in, a.series.Digest); current != nil {
			return current, fresh, true
		}
	}
	var candidate *backgroundOutcomeFile
	var current *packsrc.PatchFailure
	err = withBackgroundArtifact(a.f.Key(), backgroundArtifactWait(a.ctx), "recovery.read", func() error {
		fresh, err = a.packs.LoadCheckRecord(a.f.Key())
		if errors.Is(err, packsrc.ErrNoCheckRecord) {
			fresh, err = nil, nil
		}
		if err != nil {
			return err
		}
		if fresh != nil && fresh.Owner != a.f.Key() {
			return errors.New("the current CheckRecord belongs to another key")
		}
		if fresh != nil && fresh.Read == a.in {
			current = fresh.CurrentPatchFailure(a.in, a.series.Digest)
			if current != nil {
				return nil
			}
		}
		files, err := backgroundOutcomeFilesUnlocked(a.f.Key())
		if err != nil {
			return err
		}
		candidate = latestBackgroundPatchFailure(files)
		if candidate != nil && fresh == nil {
			return errors.New("a detached typed failure has no current CheckRecord for relevance validation")
		}
		return nil
	})
	if err != nil {
		a.backgroundAuthorityUnavailable = err.Error()
		return nil, nil, true
	}
	if current != nil {
		return current, fresh, true
	}
	if candidate == nil {
		if fresh == nil {
			if missingRecord {
				return nil, nil, false
			}
			a.backgroundAuthorityUnavailable = "the current CheckRecord disappeared during detached recovery"
			return nil, nil, true
		}
		return nil, fresh, true
	}
	if candidate.Outcome.Generation == 0 {
		a.backgroundAuthorityUnavailable = "detached patch-failure evidence has no allocated generation"
		return nil, fresh, true
	}
	if fresh.Read != a.in {
		return nil, fresh, true
	}
	if current := fresh.CurrentPatchFailure(a.in, a.series.Digest); current != nil {
		return current, fresh, true
	}
	failure := cloneBackgroundPatchFailure(candidate.Outcome.PatchFailure, candidate.Outcome.Log)
	if failure.Owner != a.f.Key() || failure.Seq <= 0 || failure.Target.Commit == "" || failure.Kind == "" ||
		failure.Inputs != a.in || failure.Series != a.series.Digest || fresh.Seq < failure.Seq ||
		fresh.Check == nil || fresh.Check.Seq != fresh.Seq {
		return nil, fresh, true
	}
	if backgroundResolutionCurrent(candidate.Outcome, failure, fresh, a.in, a.series.Digest) {
		return nil, fresh, true
	}
	// Candidate relevance is evaluated on a copy: replaySnapshot and RecordReplay must retain only
	// the CheckRecord's original disk authority, never a detached observation.
	candidateRecord := *fresh
	candidateRecord.PatchFailure = failure
	return candidateRecord.CurrentPatchFailure(a.in, a.series.Digest), fresh, true
}

func (a *advance) patchFailureText(pf *packsrc.PatchFailure) string {
	var report strings.Builder
	writePatchFailure(&report, pf, a.f.Key(), a.f.Bin, a.o.host, "")
	return report.String()
}

func (a *advance) reportPatchFailure(pf *packsrc.PatchFailure, text string) {
	if pf == nil || a.failureReported {
		return
	}
	if a.o.slot != nil {
		_, _ = io.WriteString(a.o.slot.pool.writer(), text)
	} else {
		w := a.o.errw
		if a.o.background && a.o.backgroundErrw != nil {
			w = a.o.backgroundErrw
		}
		_, _ = io.WriteString(w, text)
	}
	if a.o.report != nil {
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			a.o.report.logLine(a.f.Key(), line)
		}
	}
	a.failureReported = true
}

func (a *advance) patchFailureResult(pf *packsrc.PatchFailure) advanceResult {
	a.patchFailure = pf
	text := a.patchFailureText(pf)
	a.reportPatchFailure(pf, text)
	if allowPatchFailures() && !a.o.background && a.serves() {
		r := a.finish(nil, forkBuild{}, 0, nil, "")
		if a.serving != nil && r.delivery.Key == "" {
			r.failed = true
			r.patchFailure = pf
			r.delivery.PatchFailure = pf
			if r.delivery.Reason == "" {
				r.delivery.Reason = pf.Error()
			}
			return r
		}
		if r.delivery.Key != "" {
			admitted := r.delivery.Key
			if good := a.goodBuild(); good != nil {
				admitted = fmt.Sprintf("%s (%s; %s)", a.servingLine(), good.Commit, admitted)
			}
			a.dim("CONTINUING: using intact admitted build %s; skips this subject's advance.", admitted)
			r.bypassed, r.continuingSaid = true, true
		} else if a.installed != nil {
			a.dim("CONTINUING: using installed build %s; skips this subject's advance.", a.installed.label)
			r.bypassed, r.continuingSaid = true, true
		}
		return r
	}
	r := a.finish(nil, forkBuild{}, 0, nil, pf.Error())
	r.failed = true
	return r
}

func (a *advance) unavailableDetachedAuthority(result packsrc.LegacyReplayResult) advanceResult {
	message := fmt.Sprintf("detached patch-failure evidence is unavailable (%s); no clean replay authority was assumed, and the next launch checks it again", result.Diagnostic)
	a.warn("%s: %s", a.f.Label(), message)
	out := advanceResult{failed: true, problem: message, operationError: message, good: a.goodBuild(),
		delivery: entrypoint.ForkDelivery{Reason: message}}
	if a.serving != nil {
		out.delivery.Key = a.serving.Key
	}
	return out
}

// for it, and the next background advance tries again.
func (a *advance) skip(err error) advanceResult {
	why := oneLineErr(err)
	a.dim("%s: skipped — %s; %s tries again", a.f.Label(), why, a.next())
	return advanceResult{skipped: why}
}

// lockHeldSkip reports whether err is a held lock a background advance skips its key for: a NoWait
// store's (packsrc.ErrLockHeld), or the build's own lock (errForkBuildLocked).
func (a *advance) lockHeldSkip(err error) bool {
	return a.o.background && (errors.Is(err, packsrc.ErrLockHeld) || errors.Is(err, errForkBuildLocked))
}

// serves reports whether something runs while this advance does not move: the good build's store
// entry, or the floor's installed copy (PF-D55).
func (a *advance) serves() bool { return a.serving != nil || a.installed != nil }

// installedIsGood reports whether the floor's copy that serves is the good build itself, whose store
// entry is then not built again for it (PF-D55).
func (a *advance) installedIsGood() bool {
	g := a.goodBuild()
	return a.installed != nil && g != nil && a.installed.commit == g.Commit && a.installed.recipe == g.Recipe
}

// servingLine is what runs while this advance does not move, as the lines name it: the good build,
// "<label> + N patches", or the floor's copy's label.
func (a *advance) servingLine() string {
	if a.serving == nil && a.installed != nil {
		return a.installed.label
	}
	return a.goodLine()
}

// servingName is servingLine with what it is: "the good build <…>", or "the installed <…>" for a
// floor copy the good build has moved past.
func (a *advance) servingName() string {
	if a.serving == nil && a.installed != nil && !a.installedIsGood() {
		return "the installed " + a.installed.label
	}
	return "the good build " + a.servingLine()
}

// wantsBackground reports whether a next-launch key this launch handed its good build needs the
// background advance (XB-D19): never while `agent_updates` holds it; when its check is due; or when
// its last check found a candidate still pending — by the advance's own early exits: no fetch that
// failed, no problem, no apply error at that check, and a pending entry left once conflicts and a
// back-off are passed over. It runs no git, as the launch it serves runs none.
func (a *advance) wantsBackground() bool {
	if run.PatchedForkHold(a.f) != "" {
		return false
	}
	if due, _ := packsrc.CheckDue(a.rec, a.in, patchedNow(), 0); due {
		return true
	}
	c := a.rec.Check
	if c == nil || c.FetchErr != "" || c.Problem != "" || a.rec.ApplyErrAtLastCheck() != nil {
		return false
	}
	return len(a.pendingOf(a.rec.Candidates(a.in), false)) > 0
}

// interrupted reports whether the interrupt scope's Ctrl-C has ended this advance.
func (a *advance) interrupted() bool { return a.ctx.Err() != nil }

// checkSlot runs fn, the advance's check, under a check slot of its pool — a jail launch's build pool
// (slot) or a host act's parallel advance (pool) — and at once outside one. It reports false only
// when a Ctrl-C ended a jail launch's wait for a slot before fn ran; a host act's ended wait is seen
// by interrupted, as one during the check is.
func (a *advance) checkSlot(fn func()) bool {
	if a.o.slot != nil {
		return a.o.slot.check(fn)
	}
	a.o.pool.check(a.ctx, fn)
	return true
}

func (a *advance) warn(format string, args ...any) {
	a.epr.Printf("[yellow]⚠ %s[/yellow]", richtext.Escape(fmt.Sprintf(format, args...)))
}

func (a *advance) say(format string, args ...any) {
	a.pr.Printf("%s", richtext.Escape(fmt.Sprintf(format, args...)))
}

func (a *advance) dim(format string, args ...any) {
	a.pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf(format, args...)))
}

// sayUnlessReport is say for a line a jail launch's report folds into the next build's start line.
func (a *advance) sayUnlessReport(format string, args ...any) {
	if a.o.report == nil {
		a.say(format, args...)
	}
}

// goodLine is the good build as the lines name what keeps running: "<label> + N patches".
func (a *advance) goodLine() string {
	g := a.rec.Good
	return run.WithPatches(run.GoodBuildLabel(g), g.Patches)
}

// run is the advance proper, from the check to the hand.
func (a *advance) run() advanceResult {
	f := a.f
	good := a.goodBuild()
	edited := good != nil && good.Recipe != a.recipe
	hold := run.PatchedForkHold(f)

	var list []packsrc.ListEntry
	a.seq = a.rec.Seq
	// THE NO-VERSION NOTE IS SAID ONCE (PF-D60): not again by a check that finds what the last check
	// of the same inputs found.
	notedBefore := a.rec.Check != nil && a.rec.Check.NoVersion != "" && a.rec.Read == a.in
	// THE GIT A CONFLICT IS KEYED BY is asked only when this launch runs git anyway (a check ran):
	// inside the throttle a recorded conflict holds whichever git recorded it, so a steady-state
	// launch runs no git process at all (P4), and a git upgrade is replayed at the next check
	// (PF-D41).
	exactGit := false
	cachedFailure := a.deliveryPatchFailure(false)
	if cachedFailure.State == "unavailable" {
		return a.unavailableDetachedAuthority(cachedFailure)
	}
	if hold != "" && good != nil && !a.o.force {
		// A hold suppresses Git and repair retry, never already-classified failure authority.
		if cachedFailure.State == "failure" && cachedFailure.Failure != nil {
			return a.patchFailureResult(cachedFailure.Failure)
		}
		// HELD BY agent_updates (PF-D19): no check; a change to the series or the recipe is built at
		// the good build's commit, and a serving good build is what runs.
		if !a.serves() {
			list = []packsrc.ListEntry{goodEntry(good)}
		}
	} else {
		// Under a CHECK SLOT of the pool (XB-D10): a Ctrl-C while it waits for one ends it unchecked, as
		// one during the check does.
		var res packsrc.CheckResult
		checked := a.checkSlot(func() {
			res = a.packs.CheckPatched(f.CheckWant(a.series), packsrc.CheckOptions{Force: a.o.force, Now: patchedNow,
				Begin: func() (func(string), func()) {
					if a.o.slot != nil {
						// IN A JAIL LAUNCH'S POOL THE CHECK IS ITS LINE'S "checking N" (buildpool.go), and a
						// wait for a lock another key or launch holds is said there, beside the key, and kept
						// in launch.log (PF-D79).
						return func(line string) {
							a.o.report.logLine(f.Key(), line)
							a.o.slot.setNote(line, true)
						}, func() { a.o.slot.setNote("", false) }
					}
					a.dim("checking %s's upstream %s", f.Label(), f.Source)
					return func(line string) { a.dim("%s", line) }, func() {}
				}})
		})
		if !checked {
			// A CTRL-C ENDED THE POOL'S WAIT before a check slot was free: nothing was asked.
			return a.actStopped()
		}
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
			failure := a.deliveryPatchFailure(false)
			if failure.State == "unavailable" {
				return a.unavailableDetachedAuthority(failure)
			}
			if failure.State == "failure" && failure.Failure != nil {
				return a.patchFailureResult(failure.Failure)
			}
			if a.lockHeldSkip(res.Err) {
				return a.skip(res.Err)
			}
			a.warn("%s: could not check its upstream: %v", f.Label(), res.Err)
			a.problem = "could not check its upstream: " + oneLineErr(res.Err)
			return a.serveOr(fmt.Sprintf("%s's upstream could not be checked (%v)", f.Label(), res.Err))
		}
		a.rec = res.Record
		found := a.rec.Check
		a.seq = found.Seq
		if res.Ran && found.FetchErr != "" {
			a.operationError = found.FetchErr
			a.warn("%s: could not check its upstream (%s) — %s; the next check is in an hour, or "+
				"`yolo pack update` checks now", f.Label(), found.FetchErr, a.runsNow())
		}
		cachedFailure = a.deliveryPatchFailure(false)
		if cachedFailure.State == "unavailable" {
			return a.unavailableDetachedAuthority(cachedFailure)
		}
		if cachedFailure.State == "failure" && cachedFailure.Failure != nil {
			if !res.Ran {
				return a.patchFailureResult(cachedFailure.Failure)
			}
			target := cachedFailure.Failure.Target
			selected := false
			if cachedFailure.Failure.Kind == "base" {
				selected = a.baseFallback() && target.Commit == a.series.Base
			} else if found != nil {
				for _, e := range found.List {
					if e.Commit == target.Commit {
						target, selected = e, true
						break
					}
				}
			}
			if !selected {
				return a.patchFailureResult(cachedFailure.Failure)
			}
			if cachedFailure.Failure.Kind == "base" {
				target = a.baseEntry()
			}
			retry := a.walk([]packsrc.ListEntry{target}, false)
			if a.interrupted() {
				return a.finish(nil, forkBuild{}, 0, nil, "")
			}
			if errors.Is(retry.Err, packsrc.ErrStaleReplay) {
				return a.serveOr("the replay's check authority changed; the next launch rereads it before building")
			}
			if a.patchFailure != nil {
				return a.patchFailureResult(a.patchFailure)
			}
			failure := a.deliveryPatchFailure(false)
			if failure.State == "unavailable" {
				return a.unavailableDetachedAuthority(failure)
			}
			if failure.State == "failure" && failure.Failure != nil {
				return a.patchFailureResult(failure.Failure)
			}
		}
		if !res.Ran {
			legacy := a.deliveryPatchFailure(true)
			if legacy.State == "unavailable" {
				return a.unavailableDetachedAuthority(legacy)
			}
			if legacy.State == "failure" && legacy.Failure != nil {
				return a.patchFailureResult(legacy.Failure)
			}
		}
		found = a.rec.Check
		a.seq = found.Seq
		exactGit = res.Ran
		if res.Ran && found.FetchErr != "" {
			a.problem = "could not check its upstream: " + found.FetchErr
		}
		if found.Problem != "" {
			if res.Ran {
				a.warn("%s: %s — %s", f.Label(), found.Problem, a.runsNow())
				a.problem = found.Problem
			}
			return a.serveOr(fmt.Sprintf("%s's upstream names nothing to build (%s)", f.Label(), found.Problem))
		}
		list = a.rec.Candidates(a.in)
		if res.Ran && found.NoVersion != "" && !notedBefore {
			// A RELEASE RULE OVER A BRANCH WITH NO VERSION TAG (PF-D60), said once: where the fork stays.
			// With no good build the series' base is built, and that build's line says it (baseClause).
			switch {
			case a.serves():
				a.warn("%s: %s", f.Label(), found.NoVersionLine(a.servingName()))
			case good != nil:
				a.warn("%s: %s", f.Label(), found.NoVersionLine("upstream "+goodEntry(good).Label()))
			}
		}
		if a.serves() && found.FetchErr != "" {
			// PENDING, AND THE LAST FETCH FAILED (§6.2): no advance until a check fetches — the build
			// needs the network too, and its failure would start a back-off for no reason.
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
		if a.serves() && !res.Ran && !a.o.force && a.rec.ApplyErrAtLastCheck() != nil {
			// THE LAST WALK OF THIS CHECK'S LIST ENDED IN AN APPLY ERROR (§6.2's row, PF-D45), said on
			// the launch that met it: the good build runs, the fork's line carries the held suffix,
			// and the next check retries — never every launch inside the hour, each paying the walk.
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
	}
	if a.serving == nil && good != nil && !onList(list, good.Commit) && !a.installedIsGood() {
		// THE EDIT, OR THE GOOD BUILD'S ENTRY GONE (§6.1's series and recipe rows, §8.1): with nothing
		// newer that fits, the candidate is the good build's own commit, with the series and recipe
		// as they stand — unless the floor's copy that serves is that build (PF-D55), which needs no
		// store entry to run.
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
		if a.serves() {
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
			if c := a.rec.Check; c != nil && c.NoVersion != "" {
				base = baseUntagged
			}
		}
		list = []packsrc.ListEntry{a.baseEntry()}
	}
	// WHY A BUILD MAY FOLLOW: its own line without a report, the build's start line's clause with one.
	switch {
	case a.serves() && good != nil && list[0].Commit != good.Commit:
		a.why = ", upstream moved past the good build " + run.GoodBuildLabel(good)
		a.sayUnlessReport("%s: upstream moved — %s is newer than the good build %s; %s", f.Label(),
			list[0].Label(), run.GoodBuildLabel(good), a.replaying("replaying the series"))
	case edited:
		if a.unmodified() {
			a.why = ", its build recipe edited since the good build " + run.GoodBuildLabel(good)
			a.sayUnlessReport("%s: its build recipe changed since the good build %s; building it again", f.Label(),
				run.GoodBuildLabel(good))
		} else {
			a.why = ", its series or build recipe edited since the good build " + run.GoodBuildLabel(good)
			a.sayUnlessReport("%s: its series or build recipe changed since the good build %s; replaying the edited series",
				f.Label(), run.GoodBuildLabel(good))
		}
	case good != nil && a.serving == nil:
		a.why = ", the good build " + run.GoodBuildLabel(good) + " being gone from the capture store"
		a.sayUnlessReport("%s: the good build %s is gone from the capture store; building it again", f.Label(),
			run.GoodBuildLabel(good))
	case good == nil:
		a.why = ", the first build of it on this machine"
		a.sayUnlessReport("%s: no build of it on this machine yet; %s", f.Label(),
			a.replaying("replaying its "+run.PatchCount(a.series.Len())))
	}
	// A BUILD SLOT OF THE POOL (XB-D10), for the walk, the replay and the build, which are CPU's: taken
	// as soon as this key's own check named something to walk, never after another key's check.
	release, ok := a.o.slot.build()
	if !ok {
		return a.finish(nil, forkBuild{}, 0, nil, "") // a Ctrl-C ended the wait for one: interruptedLine says it
	}
	defer release()
	w := a.walk(list, base == baseNone)
	if a.interrupted() {
		return a.finish(nil, forkBuild{}, 0, nil, "")
	}
	if errors.Is(w.Err, packsrc.ErrStaleReplay) {
		return a.serveOr("the replay's check authority changed; the next launch rereads it before building")
	}
	if a.patchFailure != nil {
		return a.patchFailureResult(a.patchFailure)
	}
	if a.lockHeldSkip(w.Err) {
		return a.skip(w.Err)
	}
	if w.Fit < 0 && w.Base == nil && w.Err == nil && base == baseNone && !a.serves() && a.baseFallback() {
		// THE FIRST ADVANCE'S FALLBACK, and any state with nothing to serve (§6.4): nothing on the list
		// takes the series, or the walk stopped on an apply error before it found what does, so it is
		// built at its own base, where it applies by construction — with a replay bound of its own,
		// since a walk the bound stopped has none left (PF-D44).
		if err := a.walkErr(w); err != nil {
			a.warn("%s: could not replay the series: %v — building it at its base %s meanwhile; the next "+
				"check retries the rest, or `yolo pack update` now", f.Label(), err, shortSHA(a.series.Base))
			base = baseApplyErr
		} else {
			// With a report, the base build's start line carries this (baseClause).
			a.sayUnlessReport("%s: no version on the list takes the series, so it is built at its base %s and held there",
				f.Label(), shortSHA(a.series.Base))
			base = baseNoFit
		}
		a.replaySpent = 0
		if w = a.walk([]packsrc.ListEntry{a.baseEntry()}, false); a.interrupted() {
			return a.finish(nil, forkBuild{}, 0, nil, "")
		}
		if a.patchFailure != nil {
			return a.patchFailureResult(a.patchFailure)
		}
		if a.lockHeldSkip(w.Err) {
			return a.skip(w.Err)
		}
	}
	switch {
	case w.Base != nil:
		a.warn("%s: %s", f.Label(), w.Base.Error())
		return a.serveOr(fmt.Sprintf("%s's series does not apply at its own base (%s)", f.Label(), w.Base.Error()))
	case w.Fit < 0:
		var old *packsrc.GitTooOldError
		if errors.As(w.Err, &old) {
			return a.gitTooOld(old)
		}
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

// gitTooOld ends an advance whose walk refused this host's git as too old for the replay (PF-D58).
// Every replay, the series' base included, meets the same git, so the next step is updating it: the
// line says so in place of a retry, and so does the reason the jail is handed. With a good build
// serving, the walk's apply error holds a re-walk to the next check, which the line names after the
// update.
func (a *advance) gitTooOld(e *packsrc.GitTooOldError) advanceResult {
	f := a.f
	next := "update git, and " + a.next() + " builds it"
	if a.serves() {
		next = "update git, then " + a.retryStep()
	}
	a.warn("%s: %s, and this host's git is %s — %s; %s", f.Label(), e.Need(), e.Have, a.runsNow(), next)
	why := fmt.Sprintf("%s's series could not be replayed on the host (%s, and "+
		"the host's git is %s) — update git on the host, and %s builds it", f.Label(), e.Need(), e.Have, a.next())
	a.problem = oneLineErr(errors.New(why))
	r := a.finish(nil, forkBuild{}, 0, nil, why)
	r.failed = true
	return r
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
	if a.serves() {
		return "still running " + a.servingLine()
	}
	return a.hasNo()
}

// THE NOTCH'S WORDS (PF-D50, PPX-D28): what this advance's lines call the place the program or the
// extension runs, and the act that tries again — a jail and its next fresh launch; `yolo host` and
// its next launch of the program, whose floor installs what a patched fork's advance leaves; or the
// host and its next `yolo host apply --assert`, whose acting posture is where a patched extension's
// host advance runs (advanceOptions.host). A jail's `yolo capture` speaks as a jail launch, whose
// build serves the next one.

// hostTree reports whether this advance is a patched extension's at the host's render.
func (a *advance) hostTree() bool { return a.o.host && a.f.IsTree() }

// runner is what runs the build this advance readies: "this jail", "`yolo host`" for a patched
// fork at the host floor, or "the host" for a patched extension at the host's render.
func (a *advance) runner() string {
	switch {
	case a.hostTree():
		return "the host"
	case a.o.host:
		return "`yolo host`"
	case a.o.background:
		return "the next fresh launch"
	}
	return "this jail"
}

// hasNo says the program or the extension is missing where it runs.
func (a *advance) hasNo() string {
	if a.o.host && !a.f.IsTree() {
		return "yolo's floor has no " + a.f.Bin
	}
	if a.o.background {
		return "this machine has no build of " + a.f.Thing()
	}
	return a.runner() + " has no " + a.f.Thing()
}

// startsOn is how a launch that stops waiting goes on, followed by what serves: "this jail starts
// on", "`yolo host` starts <bin> on", or "the host keeps", where no jail is starting.
func (a *advance) startsOn() string {
	switch {
	case a.hostTree():
		return "the host keeps"
	case a.o.host:
		return "`yolo host` starts " + a.f.Bin + " on"
	case a.o.background:
		return "the next fresh launch starts on"
	}
	return "this jail starts on"
}

// waiter is what waits for a build while something serves: "this launch", "`yolo host`", or "yolo"
// at the host's render, where no launch is waiting.
func (a *advance) waiter() string {
	switch {
	case a.hostTree():
		return "yolo"
	case a.o.host:
		return "`yolo host`"
	case a.o.background:
		return "the background advance"
	}
	return "this launch"
}

// ctrlCStarts is what a Ctrl-C during the build does, followed by what serves: "starts this jail
// on", "starts <bin> on", or "keeps the host on".
func (a *advance) ctrlCStarts() string {
	switch {
	case a.hostTree():
		return "keeps the host on"
	case a.o.host:
		return "starts " + a.f.Bin + " on"
	}
	return "starts this jail on"
}

// next is the act that tries again: the next fresh launch, the next `yolo host -- <bin>`, or the
// next `yolo host apply --assert`.
func (a *advance) next() string {
	switch {
	case a.hostTree():
		if config.HostManagementMode() != config.HostManagementOwn {
			// Under "none" that apply writes nothing (hostApplyStep).
			return "the next `yolo host apply --assert` once `\"host_management\": \"own\"` is set"
		}
		return "the next `yolo host apply --assert`"
	case a.o.host:
		return "the next `yolo host -- " + a.f.Bin + "`"
	case a.o.background:
		return "the next background advance"
	}
	return "the next fresh launch"
}

// later is next without its article, for a retry after a back-off: "a fresh launch", "`yolo host --
// <bin>`", or "`yolo host apply --assert`".
func (a *advance) later() string {
	switch {
	case a.hostTree():
		if config.HostManagementMode() != config.HostManagementOwn {
			return "`yolo host apply --assert` once `\"host_management\": \"own\"` is set"
		}
		return "`yolo host apply --assert`"
	case a.o.host:
		return "`yolo host -- " + a.f.Bin + "`"
	case a.o.background:
		return "the next background advance"
	}
	return "a fresh launch"
}

// retryStep is when an apply error is replayed again (PF-D45): at the next check while the good build
// serves, at the next fresh launch while nothing does; and `yolo pack update` at once.
func (a *advance) retryStep() string {
	if a.serves() {
		return "the next check, in an hour, retries it, or `yolo pack update` now"
	}
	return a.next() + " retries it, or `yolo pack update` now"
}

// serveOr ends an advance that builds nothing: the good build when it serves, else why's reason.
func (a *advance) serveOr(why string) advanceResult {
	if a.problem == "" {
		// Successful last-good delivery clears its no-build reason; retain the act's failure
		// independently so the background outcome and next-launch report keep the real cause.
		a.problem = oneLineErr(errors.New(why))
	}
	r := a.finish(nil, forkBuild{}, 0, nil, why+" — "+a.next()+" tries again")
	r.failed = true
	return r
}

// noFit ends an advance whose list nothing on it takes: held at the good build, or nothing.
func (a *advance) noFit(edited bool, newest string) advanceResult {
	why := fmt.Sprintf("no upstream version %s follows takes its patch series", a.f.Label())
	if newest != "" {
		why = fmt.Sprintf("upstream %s does not take %s's patch series", newest, a.f.Label())
	}
	if !a.serves() {
		next := "`" + rebaseCommand(a.f, packsrc.ListEntry{}, true) + "` rebases the series onto the upstream it does not fit"
		if edited {
			next += "; reverting the edit brings back the good build " + run.GoodBuildLabel(a.rec.Good)
		}
		r := a.finish(nil, forkBuild{}, 0, nil, why+" — "+next)
		r.failed = true
		if !a.leaveToLaunch(&r) {
			a.warn("%s: nothing to build — %s; %s", a.f.Label(), a.hasNo(), next)
		}
		return r
	}
	a.problem = oneLineErr(errors.New(why))
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
	backoff := a.o.launch && a.serves()
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
	a.replayRecordAuthorized = false
	start := time.Now()
	snapshot, err := a.replaySnapshot()
	if err != nil {
		return packsrc.WalkResult{Fit: -1, Err: fmt.Errorf("binding replay to its original check: %w", err)}
	}
	a.replayBinding = &snapshot
	if a.repairToken, err = observeBackgroundRepairToken(a.f.Key(), a.packs, snapshot, list, a.ctx, a.o.background); err != nil {
		if !a.o.background {
			return packsrc.WalkResult{Fit: -1, Err: fmt.Errorf("reading detached failure evidence before replay: %w", err)}
		}
		a.repairToken = nil
		a.warn("%s: could not observe detached failure evidence before this replay (%v); it remains unresolved for a later advance",
			a.f.Label(), err)
	}
	w := a.packs.WalkSeries(a.repo, a.subdir, a.series, list, packsrc.WalkOptions{Snapshot: &snapshot,
		Spent: a.replaySpent, StopOnPatchFailure: true})
	a.replaySpent += replayElapsed(start)
	if pf := w.PatchFailure(); pf != nil {
		pf.Owner, pf.Inputs, pf.Series, pf.Seq = a.f.Key(), a.in, a.series.Digest, snapshot.Seq
		if pf.Log == "" && a.o.workspace != "" {
			pf.Log = filepath.Join(a.o.workspace, ".yolo", "launch.log")
		}
		a.patchFailure = pf
		a.reportPatchFailure(pf, a.patchFailureText(pf))
	}
	if a.interrupted() || a.lockHeldSkip(w.Err) {
		return w // a held mirror lock replayed nothing: nothing is recorded, and the key is skipped
	}
	if ofList {
		a.walked, a.applyErr = true, nil
		if err := a.walkErr(w); w.Fit < 0 && w.Base == nil && err != nil {
			a.applyErr = &packsrc.ApplyError{Seq: snapshot.Seq, Error: oneLineErr(err)}
		}
	}
	recorded := a.recordReplay(snapshot, w)
	if errors.Is(recorded.Err, packsrc.ErrStaleReplay) {
		w.Err, w.Fit = recorded.Err, -1
		return w
	}
	if a.patchFailure != nil {
		return w
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
			a.dim("  rebase the series: %s", rebaseCommand(a.f, r.Entry,
				isRebaseDefault(a.rec, a.in, a.series.Digest, r.Entry)))
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
	case a.serves():
		return "still running " + a.servingLine()
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
	what := run.WithPatches(b.Entry.Label(), b.Series.Len())
	tail := a.runsNow()
	if a.thenBase {
		tail = a.baseNext()
	}
	a.warn("%s: the build of %s failed: %v — %s", f.Label(), what, err, tail)
	if !a.printRunFailure() && a.o.workspace != "" {
		a.dim("  its output is above, and in %s", filepath.Join(a.o.workspace, ".yolo", "launch.log"))
	}
	switch {
	case a.thenBase:
	case a.serves():
		a.dim("  `yolo capture %s` retries it now; %s retries it after %s", f.CaptureArg(), a.later(),
			retryAt.Local().Format("2006-01-02 15:04"))
		a.dim("  to stay on the running version: `agent_updates` off for pack %s, or a tag `?ref=` in a manifest you own", f.Pack)
	default:
		a.dim("  %s tries again, or `yolo capture %s` now", a.next(), f.CaptureArg())
		if g := a.goodBuild(); g != nil && g.Recipe != a.recipe {
			a.dim("  reverting the edit to the series or the build brings back the good build %s", run.GoodBuildLabel(g))
		}
	}
}

// baseNext is what a failed build's line says follows it when the series' base is built next.
func (a *advance) baseNext() string {
	return a.hasNo() + " from it, so the series is built at its base " +
		a.baseEntry().Label() + " instead"
}

// build runs the build act for b and settles its outcome under the build's lock (settle): the
// move, a recorded failure, or nothing recorded, and then the hand. With nothing serving, a fit
// whose build fails or cannot be put in place sends the advance on to the series' base (PF-D23).
func (a *advance) build(b forkBuild, base baseWhy, edited bool) advanceResult {
	f := a.f
	// ONE CAUSE, ONCE (buildcauses.go): a build jail sealed as an earlier one of this act was, whose
	// own config was refused, would be refused the same way, so it is not started.
	if refused := a.o.report.refusedSeal(f); refused != nil {
		a.o.report.skipLine(f, refused, a.o.errw)
		return a.notStarted(refused.cause)
	}
	wait := ""
	switch {
	case a.serves() && a.o.launch && !a.o.background:
		runs := "the good build " + run.GoodBuildLabel(a.rec.Good)
		if a.serving == nil {
			runs = a.servingName()
		}
		wait = fmt.Sprintf("%s waits for it, at most %s, and a Ctrl-C %s %s instead", a.waiter(), forkBuildWaitBound,
			a.ctrlCStarts(), runs)
		a.sayUnlessReport("%s: %s; building it — %s", f.Label(), a.takes(b.Entry), wait)
	case base == baseNone:
		a.sayUnlessReport("%s: %s; building it", f.Label(), a.takes(b.Entry))
	}
	a.thenBase = base == baseNone && !a.serves() && a.baseFallback() && b.Commit != a.series.Base
	startFail := a.buildFailure(b.Commit)
	startGood := a.goodBuild()
	a.boundHit, a.ownLock = false, b.lockPath()
	mode := buildMode{force: a.o.force, packs: a.packs, lock: pidlock.NoWait, replaySpent: a.replaySpent,
		runtime: a.o.runtime}
	if a.replayBinding != nil {
		snapshot := *a.replayBinding
		mode.sourceReplay = sourceReplayOptions{Snapshot: &snapshot, Record: func(w packsrc.WalkResult) packsrc.RecordReplayResult {
			return a.recordSourceReplay(snapshot, w)
		}}
	}
	if a.o.report != nil {
		// THE BUILD'S START LINE (PF-D79): what the lines above say without a report, before the build
		// line runs — what is built, why, the wait, the log and the build's disclosures.
		why := a.why
		if base != baseNone {
			why = a.baseClause(base)
		}
		what := run.WithPatches(b.Entry.Label(), b.Series.Len())
		if b.Series.Len() > 0 {
			what += " (series " + b.Series.ShortDigest() + ")"
		}
		a.inFlight = a.o.report.begin(buildStart{fork: f, why: why, wait: wait, what: what, line: b.buildLine()}, a.o.slot)
		mode.run = a.inFlight
	}
	if a.o.host {
		// AT THE HOST the build jail's own stdout is this process's stderr (hostJailStdout): a
		// `yolo host` launch's stdout is the agent's. A child's build jail writes to the child's own
		// streams, the writers runForkBuildChild hands it.
		mode.jailStdout = hostJailStdout()
	}
	if a.o.launch {
		mode.lock = pidlock.Mode{Wait: true, Bound: forkBuildWaitBound, Cancel: a.ctx.Done()}
		if a.o.background {
			mode.lock = pidlock.NoWait // a held build lock skips the key (§6.2 rule 5)
		}
		mode.afterLock = func() (*capture.Entry, error, bool) { return a.afterLock(b, startGood, startFail) }
		// A CHILD whenever a Ctrl-C must end the build and not the launch: a good build serves
		// (PF-D25), or the advance is one of a pool's (XB-D10), whose builds run side by side — two
		// in-process build jails would share this process's signal arms and pack-record scope; and at
		// every jail launch, so its output can be kept off the terminal (PF-D79). In a pool a.ctx is
		// the pool's one interrupt scope's, which a first advance's runs under too (PF-D80).
		if a.serves() || a.o.ctx != nil || a.o.report != nil {
			mode.runJail = func(staging string, b forkBuild, s captureStreams) int {
				rc, bound := forkBuildChild(a.ctx, forkBuildWaitBound, staging, b, s, a.o.color)
				a.boundHit = bound
				if a.o.background && a.ctx.Err() != nil {
					// A SIGTERM OR SIGHUP STOPPED THE BACKGROUND ADVANCE (XB-D19): its build child's group
					// is gone, and nothing stops a build jail when the yolo that started it dies, so it is
					// removed by name before the build's staging is looked at.
					a.removeBuildJailOf(staging)
				}
				return rc
			}
		}
	}
	if jail := mode.runJail; jail != nil && a.o.pool != nil {
		// Under a BUILD SLOT (XB-D10), saying at once that it started; a Ctrl-C while it waits for one
		// ends it unbuilt, as one during the build does (130, read by settle as the interrupt).
		mode.runJail = func(staging string, b forkBuild, s captureStreams) int {
			rc := 130
			a.o.pool.build(a.ctx, func() {
				if a.o.started != nil {
					a.o.started()
				}
				rc = jail(staging, b, s)
			})
			return rc
		}
	}
	var settled *advanceResult
	mode.settle = func(entry *capture.Entry, err error) {
		a.endRun(err)
		r := a.settle(b, entry, err, base, edited)
		settled = &r
	}
	_, err := buildFork(b, mode, a.o.out, a.o.errw, a.o.color)
	r := advanceResult{}
	if settled != nil {
		r = *settled
	} else {
		a.endRun(err)
		r = a.settle(b, nil, err, base, edited) // the lock was never taken
	}
	a.inFlight.fail("done") // a no-op once the result line is written, which every path above writes
	a.inFlight = nil
	if !a.thenBase || !r.fellShort || a.interrupted() {
		return r
	}
	// THE SERIES' BASE, IN THIS SAME ADVANCE (PF-D23, §8.1): nothing serves and the newest fit came
	// to nothing, so the base is tried before the program goes, with a replay bound of its own.
	a.thenBase = false
	a.replaySpent = 0
	w := a.walk([]packsrc.ListEntry{a.baseEntry()}, false)
	if errors.Is(w.Err, packsrc.ErrStaleReplay) {
		return a.serveOr("the replay's check authority changed; the next launch rereads it before building")
	}
	switch {
	case a.interrupted():
		return a.finish(nil, forkBuild{}, 0, nil, "")
	case a.patchFailure != nil:
		return a.patchFailureResult(a.patchFailure)
	case a.lockHeldSkip(w.Err):
		return a.skip(w.Err)
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

// endRun closes the build's progress line on a build that came to nothing, with what ended it, so
// the lines settle prints for it follow it whole. An admitted build's result line is its move line
// (moved), so a nil err leaves the line to it.
func (a *advance) endRun(err error) {
	if a.inFlight == nil || err == nil {
		return
	}
	var waited waiterFailure
	var lockTimeout forkLockTimeout
	var source forkSourceError
	switch {
	case a.interrupted() || errors.Is(err, pidlock.ErrCanceled):
		a.inFlight.fail("interrupted")
	case a.boundHit:
		a.inFlight.fail("stopped at the " + forkBuildWaitBound.String() + " bound")
	case errors.As(err, &waited):
		a.inFlight.fail("another launch's build of it failed")
	case errors.As(err, &lockTimeout), errors.Is(err, errForkBuildLocked):
		a.inFlight.fail("another build of it holds its lock")
	case errors.Is(err, errForkBuildNotStarted):
		var ns forkBuildNotStarted
		errors.As(err, &ns)
		a.inFlight.failSaying(err.Error(), ns.buildCause(sealPacks(a.f)).Lines)
	case errors.As(err, &source):
		a.inFlight.fail("its source could not be put in place")
	default:
		a.inFlight.fail("failed")
	}
}

// printRunFailure prints, under a failed build's line, its last lines and where its whole output
// is (buildRun.failureLines), and reports whether there was a run to print.
func (a *advance) printRunFailure() bool {
	if a.inFlight == nil {
		return false
	}
	for _, l := range a.inFlight.failureLines() {
		a.pr.Print(l)
	}
	return true
}

// settle is the build act's result, settled while the build's lock is held (buildMode.settle,
// §6.6): the move of an admitted build; the record of a build line that failed or ran past the
// bound; and for everything else, which records nothing, its line. It hands the jail what runs.
func (a *advance) settle(b forkBuild, entry *capture.Entry, err error, base baseWhy, edited bool) advanceResult {
	f := a.f
	if err == nil {
		return a.moved(b, entry, base, edited)
	}
	if a.lockHeldSkip(err) && !a.interrupted() {
		return a.skip(err)
	}
	if a.interrupted() || errors.Is(err, pidlock.ErrCanceled) {
		return a.finish(nil, forkBuild{}, 0, nil, "the advance was interrupted — "+a.next()+" builds it")
	}
	var patchFailure *packsrc.PatchFailure
	if errors.Is(err, packsrc.ErrStaleReplay) {
		a.warn("%s: replay authority changed while preparing its build source; %s", f.Label(), a.runsNow())
		return a.serveOr(fmt.Sprintf("%s's replay authority changed before its source could be built", f.Label()))
	}
	if errors.As(err, &patchFailure) {
		patchFailure.Owner, patchFailure.Inputs, patchFailure.Series, patchFailure.Seq = a.f.Key(), a.in, a.series.Digest, a.seq
		a.patchFailure = patchFailure
		a.reportPatchFailure(patchFailure, a.patchFailureText(patchFailure))
		return a.patchFailureResult(patchFailure)
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
	case errors.Is(err, errForkBuildWorkspaceRetained):
		// A PREVIOUS OR THIS BUILD'S JAIL IS NOT YET KNOWN GONE: no verdict on the commit, so nothing
		// is recorded, no back-off starts, and the next launch tries again (pi-startup-cancellation §3).
		a.warn("%s: %v — %s", f.Label(), err, a.runsNow())
		r := a.serveOr(fmt.Sprintf("%s: %v", f.Label(), err))
		r.retained = run.WithPatches(b.Entry.Label(), b.Series.Len()) + " pending — " + oneLineErr(err)
		return r
	case errors.Is(err, errForkBuildNotStarted):
		// THE BUILD JAIL STOPPED BEFORE ITS BUILD LINE RAN (PF-D21): not a failed build, so nothing is
		// recorded and the candidate stays pending. What stopped it is the jail's to say, and the
		// error relays what it said (forkBuildNotStarted, PPX-D39, PPX-D42): a launch pre-flight's
		// refusal, a runtime that would not start it, or a boot that failed, whose record names each
		// failed generator's pack and file.
		var ns forkBuildNotStarted
		errors.As(err, &ns)
		cause := ns.buildCause(sealPacks(f))
		if a.inFlight != nil && a.inFlight.logPath != "" {
			cause.Log = a.inFlight.logPath
		}
		if ns.configRefused(f) {
			a.o.report.noteRefused(f, cause)
		}
		if a.o.report == nil {
			// AN ACT OF ITS OWN (`yolo capture`, `yolo host`) says it here, the cause on lines of its own.
			a.warn("%s: %v — %s", f.Label(), err, a.runsNow())
			for _, l := range notStartedLines(err, sealPacks(f), "  ", a.retryNotStarted()) {
				a.epr.Print(richtext.Escape(l))
			}
			if a.o.runtime == "container" {
				a.dim("  On Apple Container a capture jail cannot start beside a running jail: if that is what "+
					"stopped it, `yolo capture %s` builds it once the other jails stop", f.CaptureArg())
			}
		}
		return a.notStarted(cause)
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

// notStarted ends an advance whose build jail stopped before its build line, or would have, with
// cause: the good build when it serves, held by the cause, which a jail launch's report says once
// per cause when its act ends (buildReport.hold); otherwise nothing, the cause carried to the
// launch, whose refusal or warning says it once (internal/cli/run's missingbuilds.go). The reason
// names no key, so a jail's gate says it once for every extension that shares it.
func (a *advance) notStarted(cause *entrypoint.BuildCause) advanceResult {
	why := "its build jail exited before its build line ran on the host"
	if cause != nil && len(cause.Lines) > 0 {
		why = "its build jail refused to start on the host"
	}
	r := a.serveOr(why)
	if r.problem == "" {
		r.problem = why
	}
	switch {
	case r.delivery.Key == "" && r.delivery.Reason != "":
		r.delivery.Cause = cause
		a.leaveToLaunch(&r)
	case a.serves() && cause != nil:
		a.o.report.hold(a.f, a.runsNow(), a.retryNotStarted(), a.o.runtime, cause)
	}
	return r
}

// retryNotStarted is what builds a build whose jail stopped before its build line, once its cause
// is fixed: its capture, and the act that tries again.
func (a *advance) retryNotStarted() string {
	return "`yolo capture " + a.f.CaptureArg() + "` builds it; " + a.next() + " tries too"
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
		next := a.next() + " tries again, or `yolo capture " + f.CaptureArg() + "` now"
		if g := a.goodBuild(); a.o.report != nil && g != nil && g.Recipe != a.recipe {
			// What buildFailedLines would say beside it, for the launch that says this reason instead.
			next = "reverting the edit to the series or the build brings back the good build " +
				run.GoodBuildLabel(g) + "; " + next
		}
		r = a.finish(nil, forkBuild{}, 0, o, fmt.Sprintf("%s's build of %s failed on the host (%s) — %s",
			f.Label(), b.Entry.Label(), oneLineErr(err), next))
	}
	if a.leaveToLaunch(&r) {
		// The launch says the cause (missingbuilds.go); the build's own output is the act's to show.
		a.printRunFailure()
	} else {
		a.buildFailedLines(b, err, autoCaptureRetryAt(capture.AutoFailure{Failures: o.Count, Last: now}))
	}
	r.failed, r.fellShort = true, true
	r.failWhat, r.failErr = run.WithPatches(b.Entry.Label(), b.Series.Len()), oneLineErr(err)
	r.retryAt = autoCaptureRetryAt(capture.AutoFailure{Failures: o.Count, Last: now})
	return r
}

// leaveToLaunch reports whether r, an advance's end, leaves its reason to the launch: a jail
// launch's act whose advance hands the jail nothing, so the launch's refusal or warning
// (internal/cli/run's missingbuilds.go) says the reason, once, and the act prints no line of its own
// for it. It marks r so (ForkDelivery.Unsaid).
func (a *advance) leaveToLaunch(r *advanceResult) bool {
	if a.o.report == nil || r.delivery.Key != "" || r.delivery.Reason == "" {
		return false
	}
	r.delivery.Unsaid = true
	return true
}

// recordOnly writes a failed build's outcome, counted against the record's, and nothing else.
func (a *advance) recordOnly(o *packsrc.EntryOutcome) {
	err := a.records().WithCheckRecord(a.f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
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
		a.inFlight.fail("left the capture store before it was handed")
		a.warn("%s: the build of %s left the capture store before this launch could hand it (%v) — %s; %s "+
			"builds it again", a.f.Label(), b.Entry.Label(), r.gone, a.handedNow(r), a.next())
		r.failed = true
		return r
	}
	r.built = true
	r.to = run.WithPatches(b.Entry.Label(), b.Series.Len())
	if prev != nil {
		r.from = run.GoodBuildLabel(prev)
	}
	if r.lost {
		lost := richtext.Escape(fmt.Sprintf("%s: another launch moved the good build meanwhile, from a newer "+
			"check; %s runs that one, and this build is reaped", a.f.Label(), a.runner()))
		a.result(lost)
		return r
	}
	// THE MOVE LINE (PF-D8), the disclosure of what now runs, which no flag hides (OQ-RO3): with a
	// report, the build's result line, with the store's key, paths and size folded in.
	what := run.WithPatches(b.Entry.Label(), b.Series.Len())
	var line string
	switch {
	case prev == nil:
		line = "built " + a.f.Label() + ": " + what + "; " + a.runner() + " runs it" + a.baseClause(base)
	case prev.Entry == entry.Key:
		line = "rebuilt " + a.f.Label() + ": " + what + "; " + a.runner() + " runs it" + a.baseClause(base)
	case edited:
		line = "updated " + a.f.Label() + ": " + run.GoodBuildLabel(prev) + " → " + b.Entry.Label() +
			a.editedSeries(b) + "; " + a.runner() + " runs the new build" + a.baseClause(base)
	default:
		count := ""
		if b.Series.Len() > 0 {
			count = ", " + run.PatchCount(b.Series.Len())
		}
		line = "updated " + a.f.Label() + ": " + run.GoodBuildLabel(prev) + " → " + b.Entry.Label() + count + "; " +
			a.runner() + " runs the new build" + a.baseClause(base)
	}
	a.result("[bold]" + richtext.Escape(line) + "[/bold]")
	return r
}

// unmodified reports whether this advance is an UNMODIFIED EXTENSION's: an empty series, which every
// entry of the walk's list fits, so nothing is replayed and nothing is held at a base
// (docs/design/pi-extension-store-builds.md §4.2).
func (a *advance) unmodified() bool { return a.series != nil && a.series.Len() == 0 }

// replaying is what the advance does next with the upstream: patched, the replay its lines name;
// unmodified, a build of the upstream as it is.
func (a *advance) replaying(patched string) string {
	if a.unmodified() {
		return "building its upstream"
	}
	return patched
}

// takes is the line that names the entry the advance builds: "<entry> takes the series", or for an
// unmodified extension the entry alone, which nothing needs to take.
func (a *advance) takes(e packsrc.ListEntry) string {
	if a.unmodified() {
		return "upstream " + e.Label()
	}
	return e.Label() + " takes the series"
}

// editedSeries is the move line's account of an edited recipe: the series' count and digest for a
// patched build, nothing for an unmodified one, whose series cannot change.
func (a *advance) editedSeries(b forkBuild) string {
	if b.Series.Len() == 0 {
		return ", the edited build"
	}
	return ", the edited series (" + run.PatchCount(b.Series.Len()) + ", series " + b.Series.ShortDigest() + ")"
}

// result is a build's result line, in markup: the run's, among its key's lines with its time, with a
// report; a line of its own without one.
func (a *advance) result(markup string) {
	if a.inFlight != nil {
		a.inFlight.done(markup)
		return
	}
	a.pr.Print(markup)
}

// baseClause is the move line's last clause for a build at the series' base: why it is there, and
// whether that holds anything (PF-D27: a branch with nothing newer holds nothing).
func (a *advance) baseClause(base baseWhy) string {
	switch base {
	case baseEmpty:
		return " — no version of the branch is newer than the series' base"
	case baseUntagged:
		return " — " + a.rec.Check.NoVersionLine("the series' base")
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
	switch {
	case r.delivery.Key != "" && a.rec.Good != nil:
		return a.runner() + " runs " + a.goodLine()
	case a.installed != nil:
		return a.runner() + " runs " + a.servingLine()
	}
	return a.hasNo()
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
	packs := a.packs
	if built != nil || failure != nil {
		packs = a.records() // only a completed build's settle may wait
	}
	err := packs.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
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
		if a.patchFailure != nil {
			if snapshot := a.replayBinding; snapshot != nil && r.Seq == snapshot.Seq && r.Read == snapshot.Inputs &&
				r.Seq == a.seq && r.Read == a.in && reflect.DeepEqual(r.PatchFailure, snapshot.Failure) &&
				reflect.DeepEqual(r.ApplyErr, snapshot.ApplyErr) {
				r.PatchFailure = a.patchFailure
				changed = true
			}
		}
		if snapshot := a.replayBinding; snapshot != nil && r.Seq == snapshot.Seq && r.Read == snapshot.Inputs &&
			r.Seq == a.seq && r.Read == a.in && reflect.DeepEqual(r.PatchFailure, snapshot.Failure) &&
			reflect.DeepEqual(r.ApplyErr, snapshot.ApplyErr) {
			switch {
			case a.applyErr != nil:
				r.ApplyErr, changed = a.applyErr, true
			case a.walked && a.replayRecordAuthorized && r.ApplyErr != nil:
				// The opaque diagnosis belongs to an older check, but may be cleared only after
				// RecordReplay changed this exact guarded authority successfully.
				r.ApplyErr, changed = nil, true
			}
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
				h.Reason = f.Label() + " has no build on this machine yet — " + a.next() + " builds it"
			}
		}
		res.delivery = entrypoint.ForkDelivery{Key: h.Key, Reason: h.Reason, PatchFailure: a.patchFailure}
		res.patchFailure = a.patchFailure
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
		if a.lockHeldSkip(err) {
			return a.skip(err)
		}
		a.warn("%s: could not update its check record: %v", f.Label(), err)
		if res.delivery.Key == "" && res.delivery.Reason == "" {
			res.delivery.Reason = fmt.Sprintf("%s's check record could not be updated (%v)", f.Label(), err)
		}
	}
	res.lost = lost
	res.problem = a.problem
	res.operationError = a.operationError
	res.patchFailure = a.patchFailure
	res.patchFailureSaid = a.patchFailure != nil && a.failureReported
	if res.delivery.Key == "" && a.serving != nil && a.rec != nil && a.rec.Good != nil &&
		a.rec.Good.Entry == a.serving.Key {
		if current := a.exactGood(a.rec.Good); current != nil && current.Key == a.serving.Key {
			res.delivery.Key = current.Key
			res.delivery.PatchFailure = a.patchFailure
		}
	}
	if a.patchFailure != nil {
		res.delivery.PatchFailure = a.patchFailure
	}
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
func (a *advance) exactGood(g *packsrc.GoodBuild) *capture.Entry {
	if entry := a.exact(g.Commit); entry != nil {
		return entry
	}
	legacy := a.legacyGood
	if !a.o.readOnlyInitialization || legacy == nil || legacy.Commit != g.Commit {
		return nil
	}
	entry, _, err := resolvePatchedBuild(a.store, a.f.Key(), a.f.Bin, a.o.platform, patchedBuildSource(a.f.Source),
		legacy.Commit, legacy.Recipe)
	if err != nil {
		return nil
	}
	return entry
}

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
	var rec *packsrc.CheckRecord
	var err error
	if a.o.readOnlyInitialization {
		rec, err = a.packs.LoadCheckRecord(a.f.Key())
	} else {
		rec, err = run.LoadPatchedRecord(a.packs, a.f, a.series)
	}
	if err == nil && rec.Good != nil {
		if a.o.readOnlyInitialization {
			return a.rekeyLegacyGoodInMemory(rec)
		}
		return rec
	}
	g := a.recoverGood()
	if g == nil {
		if rec == nil {
			rec = &packsrc.CheckRecord{Schema: packsrc.CheckRecordSchema, Owner: a.f.Key()}
		}
		return rec
	}
	if a.o.readOnlyInitialization {
		if rec == nil {
			rec = &packsrc.CheckRecord{Schema: packsrc.CheckRecordSchema, Owner: a.f.Key()}
		}
		rec.Good = g
		why := "it had none"
		if err != nil && !errors.Is(err, packsrc.ErrNoCheckRecord) {
			why = "it could not be read: " + err.Error()
		} else if errors.Is(err, packsrc.ErrNoCheckRecord) {
			why = "it was gone"
		}
		a.dim("%s: recovered its good build %s from the capture store for this read (%s)", a.f.Label(),
			run.GoodBuildLabel(g), why)
		return a.rekeyLegacyGoodInMemory(rec)
	}
	var out *packsrc.CheckRecord
	err = a.packs.WithCheckRecord(a.f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		out = r
		if r.Good != nil {
			return false, nil
		}
		r.Good = g
		return true, nil
	})
	if a.lockHeldSkip(err) {
		// Recovery is initialization, not a completed build's settle. Leave it to the next launch;
		// the check's own NoWait attempt will skip the key too while this lock stays busy.
		return &packsrc.CheckRecord{Schema: packsrc.CheckRecordSchema, Owner: a.f.Key()}
	}
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
	// A BUILD RECEIPTED UNDER THE SERIES' LEGACY DIGEST is recovered as that, and re-keyed (PF-D62).
	return run.RekeyLegacyGood(a.packs, a.f, a.series, out)
}

// rekeyLegacyGoodInMemory applies the same legacy digest migration to this private read's record
// without writing either the record or the capture receipt. A serving lookup can still use the old
// receipt through exactGood below; a later ordinary advance performs the durable migration.
func (a *advance) rekeyLegacyGoodInMemory(rec *packsrc.CheckRecord) *packsrc.CheckRecord {
	if rec == nil || rec.Good == nil || a.series.LegacyDigest == "" || a.series.LegacyDigest == a.series.Digest {
		return rec
	}
	oldRecipe := run.PatchedRecipe(a.f, a.series.LegacyDigest)
	if rec.Good.Series != a.series.LegacyDigest || rec.Good.Recipe != oldRecipe {
		return rec
	}
	legacy := *rec.Good
	a.legacyGood = &legacy
	rec.RekeySeries(a.series.LegacyDigest, oldRecipe, a.series.Digest, a.recipe)
	return rec
}

// recoverGood is the good build the capture store holds for this fork as the manifest asks for it,
// or nil: the newest admitted build of the fork key for this platform under the recipe as it stands,
// else under the recipe the series' LEGACY digest gives, a build of these very files admitted before
// PF-D61, which loadOrRecover then re-keys (PF-D62).
func (a *advance) recoverGood() *packsrc.GoodBuild {
	if g := recoverGoodBuild(a.store, a.f.Key(), a.o.platform, a.recipe, a.series.Len()); g != nil {
		return g
	}
	return recoverGoodBuild(a.store, a.f.Key(), a.o.platform, run.PatchedRecipe(a.f, a.series.LegacyDigest),
		a.series.Len())
}

// recoverGoodBuild is recoverGood's lookup, which writes nothing: the newest admitted build of fork
// for platform under recipe, as a good build of patches members, or nil. The host floor's offline
// read (floorPatchedState) asks it too, so a machine whose check record is gone and which cannot
// build still installs the build its store holds.
func recoverGoodBuild(store *capture.Store, fork, platform, recipe string, patches int) *packsrc.GoodBuild {
	scan, err := capture.Scan(store, captureRecords)
	if err != nil {
		return nil
	}
	var best *capture.Record
	bestKey := ""
	for _, e := range scan {
		for i := range e.Records {
			r := &e.Records[i]
			if r.Fork != fork || r.Platform != platform || r.Recipe != recipe {
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
		Recipe: best.Recipe, Tree: best.Tree, Patches: patches, Entry: bestKey, At: best.Time.Unix()}
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

type sourceReplayOptions struct {
	Snapshot *packsrc.ReplaySnapshot
	Record   func(packsrc.WalkResult) packsrc.RecordReplayResult
}

// replayIntoSource replays b's series onto b.Entry in a scratch repository outside the build's
// workspace (§5.1, PF-D18), under the mirror's lock, and copies the patched source subdirectory into
// dst with the copy that never follows a link; it returns the PATCHED TREE (§5.3). packs nil is the
// launch's store. It is the fit's second replay, under the build's lock (which is keyed on the fit,
// so only after the walk found it), and takes what the walk left of the replay's bound (spent,
// PF-D44).
func replayIntoSource(packs *packsrc.Store, b forkBuild, dst string, spent time.Duration, options sourceReplayOptions) (string, error) {
	if packs == nil {
		packs = packsrc.LaunchStore(paths.PacksDir())
	}
	repo := packsrc.CheckRepo(b.Fork.Source)
	if repo == "" {
		return "", fmt.Errorf("%s names no upstream to check out", b.Fork.Source)
	}
	w := packs.WalkSeries(repo, subdirOf(b.Fork.Source), b.Series, []packsrc.ListEntry{b.Entry}, packsrc.WalkOptions{
		Snapshot: options.Snapshot, Spent: spent, StopOnPatchFailure: true,
		OnFit: func(tree string) error { return copySourceTree(tree, dst) }})
	var record packsrc.RecordReplayResult
	if options.Record != nil {
		record = options.Record(w)
	}
	if errors.Is(record.Err, packsrc.ErrStaleReplay) {
		return "", record.Err
	}
	if record.Failure != nil && w.PatchFailure() == nil {
		return "", record.Failure
	}
	if failure := w.PatchFailure(); failure != nil {
		if record.Failure != nil {
			failure = record.Failure
		}
		if record.Err != nil {
			return "", errors.Join(failure, fmt.Errorf("recording patch application failure: %w", record.Err))
		}
		return "", failure
	}
	if record.Err != nil {
		return "", record.Err
	}
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
