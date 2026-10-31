package cli

// Request leases belong to launch processes; production belongs to the detached host worker.
// The kernel releases a lease on requester exit. A PID or an old request file is never demand.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const sharedDemandPoll = 50 * time.Millisecond

// sharedDemandAdmissionHook is a deterministic pause seam for exercising real authorization
// transactions under state.lock contention.
var sharedDemandAdmissionHook func(preparationDemand)

// sharedDemandMonitorHook runs inside the worker's demand monitor while it holds state.lock: a
// pause seam for the worker contending with its own admission.
var sharedDemandMonitorHook func()

// sharedWorkerLockContendedHook runs when a starting worker finds worker.lock taken: a seam for
// forcing a liveness probe's momentary hold to overlap the worker's acquisition.
var sharedWorkerLockContendedHook func()

// sharedWorkerStartGrace bounds how long a requester waits for a worker it has never seen hold
// worker.lock. A working worker holds that lock for its whole run, so a requester that has seen
// neither the lock held nor a result for this long has no worker, and never will.
var sharedWorkerStartGrace = time.Minute

var errSharedPreparationForegroundOnly = errors.New("the user-scope selection does not authorize detached preparation for this declaration")

type sharedDemandStateLockHeld struct{ cause error }

func (e *sharedDemandStateLockHeld) Error() string { return e.cause.Error() }
func (e *sharedDemandStateLockHeld) Unwrap() []error {
	return []error{packsrc.ErrLockHeld, e.cause}
}

func isSharedDemandStateLockHeld(err error) bool {
	var held *sharedDemandStateLockHeld
	return errors.As(err, &held)
}

type preparationDemand struct {
	Key, Runtime, Platform, Identity string
	Kind                             string
	AfterSeq                         uint64
	CheckBoundary                    packsrc.CheckStart
	SubscriptionGeneration           uint64
	Fresh, Host, Background          bool
}

// demandBoundaryAfter keeps ordinary npm capture's sequence separate from the
// source-check epoch ledger. Different epochs never batch a historical answer.
func demandBoundaryAfter(candidate, current preparationDemand) bool {
	if candidate.Kind == preparationOrdinaryProgramKind {
		return candidate.AfterSeq > current.AfterSeq
	}
	return candidate.CheckBoundary.Epoch != current.CheckBoundary.Epoch || candidate.CheckBoundary.Seq > current.CheckBoundary.Seq
}

type preparationDemandResult struct {
	NodeFloorUnavailable                                    *run.NodeFloorUnavailable `json:"node_floor_unavailable,omitempty"`
	Pending                                                 *appleBuildPending        `json:"apple_build_pending,omitempty"`
	UnsupportedNPM                                          bool                      `json:"unsupported_npm,omitempty"`
	Program                                                 *npmCaptureRecord         `json:"program,omitempty"`
	Delivery                                                entrypoint.ForkDelivery
	Built, Failed, Bypassed, Lost, FellShort                bool
	OperationError                                          string
	PatchFailure                                            *packsrc.PatchFailure
	Good                                                    *packsrc.GoodBuild
	From, To, FailWhat, FailErr, Skipped, Retained, Problem string
	RetryAt                                                 time.Time
}

// A structured refusal may accompany only a failed ordinary result, never success facts.
func (r preparationDemandResult) ordinaryFloorFailure(in npmCaptureInput) error {
	e := r.NodeFloorUnavailable
	if e == nil {
		return nil
	}
	if !r.Failed || !e.Valid() || e.Owner != in.Owner || e.Bin != in.Bin || e.Floor != in.NodeFloor || !reflect.DeepEqual(r, preparationDemandResult{Failed: true, OperationError: r.OperationError, NodeFloorUnavailable: e}) {
		return fmt.Errorf("invalid or mixed ordinary failure result — retry selected preparation")
	}
	return e
}

func demandResult(r advanceResult) preparationDemandResult {
	return preparationDemandResult{Pending: r.pending, Delivery: r.delivery, Built: r.built, Failed: r.failed, Bypassed: r.bypassed, Lost: r.lost, FellShort: r.fellShort, OperationError: r.operationError, PatchFailure: r.patchFailure, Good: r.good, From: r.from, To: r.to, FailWhat: r.failWhat, FailErr: r.failErr, Skipped: r.skipped, Retained: r.retained, Problem: r.problem, RetryAt: r.retryAt}
}
func (r preparationDemandResult) advance() advanceResult {
	return advanceResult{pending: r.Pending, delivery: r.Delivery, built: r.Built, failed: r.Failed, bypassed: r.Bypassed, lost: r.Lost, fellShort: r.FellShort, operationError: r.OperationError, patchFailure: r.PatchFailure, good: r.Good, from: r.From, to: r.To, failWhat: r.FailWhat, failErr: r.FailErr, skipped: r.Skipped, retained: r.Retained, problem: r.Problem, retryAt: r.RetryAt}
}
func sharedDemandDir(key, rt, platform string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + rt + "\x00" + platform))
	return filepath.Join(paths.BackgroundAdvanceDir(), "demands", hex.EncodeToString(sum[:]))
}
func writeDemandJSON(path string, value any) error {
	return writePreparationJSON(path, value)
}
func withDemandState(ctx context.Context, dir string, wait bool, fn func() error) error {
	mode := pidlock.NoWait
	if wait {
		mode = pidlock.Mode{Wait: true, Cancel: ctx.Done()}
	}
	lock, err := pidlock.Acquire(filepath.Join(dir, "state.lock"), mode, nil)
	if err != nil {
		if errors.Is(err, pidlock.ErrHeld) {
			return &sharedDemandStateLockHeld{cause: err}
		}
		return err
	}
	defer lock.Release()
	if err = ctx.Err(); err != nil {
		return err
	}
	return fn()
}

// demandLeaseHeld is deliberately tri-state: unreadable is not an expired requester.
func demandLeaseHeld(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}

type livePreparationDemand struct {
	path string
	preparationDemand
}

// Called only in the short state transaction. Expired requests are removed only after a
// positive unlocked witness; absent/unreadable leases refuse dispatch, never cancel live work.
func livePreparationDemands(dir string) ([]livePreparationDemand, error) {
	files, err := filepath.Glob(filepath.Join(dir, "request-*.json"))
	if err != nil {
		return nil, err
	}
	var live []livePreparationDemand
	for _, path := range files {
		held, err := demandLeaseHeld(path + ".lease")
		if err != nil {
			return nil, err
		}
		if !held {
			_ = os.Remove(path)
			_ = os.Remove(path + ".result")
			_ = os.Remove(path + ".lease")
			continue
		}
		if _, err := os.Stat(path + ".result"); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		var d preparationDemand
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		if sharedDemandDir(d.Key, d.Runtime, d.Platform) != dir {
			return nil, errors.New("preparation demand binding changed — preserve the demand record and retry `yolo --update`")
		}
		live = append(live, livePreparationDemand{path, d})
	}
	return live, nil
}

var sharedPreparationSpawn = func(ctx context.Context, d preparationDemand) error {
	if flag.Lookup("test.v") != nil {
		return errBackgroundFromTest
	}
	if err := os.MkdirAll(filepath.Dir(backgroundAdvanceLog()), 0700); err != nil {
		return err
	}
	environment, err := sharedPreparationEnvironment(ctx)
	if err != nil {
		return err
	}
	f, err := notty.OpenDetachLog(backgroundAdvanceLog())
	if err != nil {
		return err
	}
	defer f.Close()
	argv := backgroundAdvanceArgv([]string{d.Key}, d.Runtime, d.Platform)
	argv = append(argv, "--shared-demand")
	cmd := exec.Command(run.SelfExecPath(), argv...)
	cmd.Env = environment
	cmd.Stdin = nil
	cmd.Stdout = f
	cmd.Stderr = f
	return notty.StartDetached(cmd)
}

func (a *advance) waitSharedPreparation(ctx context.Context) advanceResult {
	o := a.o
	if ctx.Err() != nil {
		return a.freshRefusal("the preparation requester was interrupted", ctx.Err())
	}
	identity, err := freshPreparationIdentity(a.f, o.platform)
	if err != nil {
		return a.freshRefusal("could not register shared preparation", err)
	}
	if o.freshCheck && identity != o.freshIdentity {
		return a.freshRefusal("selected inputs changed after registration", errPreparationAdmissionStale)
	}
	d := preparationDemand{Key: a.f.Key(), Runtime: o.runtime, Platform: o.platform, Identity: identity, CheckBoundary: o.freshBoundary, Fresh: o.freshCheck, Host: o.host, Background: o.background, SubscriptionGeneration: o.sharedSubscriptionGeneration}
	if d.Runtime == "" {
		d.Runtime = captureRuntime()
	}
	probe := d
	probe.Identity = ""
	selected, drop, selectionErr := resolveDemandFork(ctx, probe)
	var selectedIdentity string
	if selectionErr == nil {
		selectedIdentity, selectionErr = freshPreparationIdentity(selected, d.Platform)
	}
	drop()
	if errors.Is(selectionErr, errSharedPreparationForegroundOnly) || selectionErr == nil && selectedIdentity != d.Identity {
		a.dim("%s: foreground-only preparation; the user selection does not authorize a detached worker for these inputs — select this declaration in the user config to share preparation", a.f.Label())
		foreground := o
		foreground.sharedForegroundOnly = true
		foreground.ctx = ctx
		return advancePatchedFork(a.f, foreground)
	}
	if selectionErr != nil && !errors.Is(selectionErr, packsrc.ErrLockHeld) {
		return a.freshRefusal("could not authorize shared preparation", selectionErr)
	}

	result, err := waitSharedDemandResult(ctx, d, o.sharedAutomatic, func(pid int) {
		a.dim("%s: waiting for pid %d's shared preparation — interrupting detaches this requester", a.f.Label(), pid)
	})
	if err != nil {
		return a.freshRefusal("shared preparation did not complete", err)
	}
	r := result.advance()
	if r.failed {
		return r
	}
	if current, e := freshPreparationIdentity(a.f, o.platform); e != nil || current != d.Identity {
		return a.freshRefusal("selected inputs changed before handoff", errPreparationAdmissionStale)
	}
	handoffOptions := o
	handoffOptions.operationCtx = ctx
	handoff, early := newAdvance(a.f, handoffOptions)
	if early != nil {
		return *early
	}
	handed := handoff.finish(nil, forkBuild{}, 0, nil, r.delivery.Reason)
	if handed.failed {
		return handed
	}
	r.delivery, r.good = handed.delivery, handed.good
	return r
}

// waitSharedDemandResult is the ONE requester lease/wait protocol. Source and
// ordinary-capture adapters supply their own authorization and handoff, not lifetimes.
func registerSharedDemand(ctx context.Context, d preparationDemand) (*sharedDemandRequest, error) {
	var err error
	dir := sharedDemandDir(d.Key, d.Runtime, d.Platform)
	if err = ensurePreparationRoot(dir); err != nil {
		return nil, fmt.Errorf("could not create requester lease: %w", err)
	}
	var path string
	var previousWorkerError []byte
	var lease *pidlock.Lock
	err = withDemandState(ctx, dir, true, func() error {
		previousWorkerError, _ = os.ReadFile(filepath.Join(dir, "worker-error.json"))
		f, e := os.CreateTemp(dir, "request-*.json")
		if e != nil {
			return e
		}
		path = f.Name()
		_ = f.Close()
		lease, e = pidlock.Acquire(path+".lease", pidlock.NoWait, nil)
		if e != nil {
			_ = os.Remove(path)
			return e
		}
		return writeDemandJSON(path, d)
	})
	r := &sharedDemandRequest{ctx: ctx, dir: dir, path: path, lease: lease, previousWorkerError: previousWorkerError}
	if err != nil {
		if lease != nil {
			r.release(false)
		}
		return nil, fmt.Errorf("could not attach preparation demand: %w", err)
	}
	if d.Fresh {
		_ = notifyPreparationScheduler()
	}
	// Launch processes never acquire the production/key/build lock. Registration can finish even
	// while transport, a build or another request's STARTED transaction is paused.
	if err = sharedPreparationSpawn(ctx, d); err != nil {
		r.release(false)
		return nil, fmt.Errorf("the owned preparation worker could not start: %w", err)
	}
	return r, nil
}

type sharedDemandRequest struct {
	ctx                 context.Context
	dir, path           string
	lease               *pidlock.Lock
	previousWorkerError []byte
}

// sharedDemandCleanupBound bounds a released request's cleanup (release): its wait for state.lock
// and its wait for a scheduler-stopped producer to drain. A var so a test that holds state.lock
// across the release need not wait it out.
var sharedDemandCleanupBound = time.Second

func (r *sharedDemandRequest) release(automatic bool) {

	r.lease.Release()
	// Remove only this detached request under the same transaction the worker reads.
	// Cleanup is bounded and never waits on an artifact/check/build lock.
	cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(r.ctx), sharedDemandCleanupBound)
	defer stop()
	_ = withDemandState(cleanupCtx, r.dir, true, func() error {
		_ = os.Remove(r.path)
		_ = os.Remove(r.path + ".result")
		_ = os.Remove(r.path + ".lease")
		return nil
	})
	if automatic && r.ctx.Err() != nil {
		// Scheduler stop drains a producer only when no other requester owns it.
		// A fresh launch's detach is never made to wait for that teardown.
		ticker := time.NewTicker(sharedDemandPoll)
		defer ticker.Stop()
		for {
			var live []livePreparationDemand
			err := withDemandState(cleanupCtx, r.dir, false, func() error { var e error; live, e = livePreparationDemands(r.dir); return e })
			if err == nil && len(live) > 0 {
				break
			}
			if !pidlock.Held(filepath.Join(r.dir, "worker.lock")) {
				break
			}
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}
func waitSharedDemandResult(ctx context.Context, d preparationDemand, automatic bool, say func(int)) (preparationDemandResult, error) {
	r, err := registerSharedDemand(ctx, d)
	if err != nil {
		return preparationDemandResult{}, err
	}
	defer r.release(automatic)
	if say != nil {
		say(pidlock.Holder(filepath.Join(r.dir, "worker.lock")))
	}
	return r.wait(ctx)
}
func (r *sharedDemandRequest) wait(ctx context.Context) (preparationDemandResult, error) {
	path, dir, previousWorkerError := r.path, r.dir, r.previousWorkerError
	ticker := time.NewTicker(sharedDemandPoll)
	defer ticker.Stop()
	waitBound := time.NewTimer(forkBuildWaitBound)
	defer waitBound.Stop()
	seenWorker := false
	started := time.Now()
	for {
		if ctx.Err() != nil {
			return preparationDemandResult{}, fmt.Errorf("the preparation requester was interrupted: %w", ctx.Err())
		}
		b, e := os.ReadFile(path + ".result")
		if e == nil {
			var result preparationDemandResult
			if e = json.Unmarshal(b, &result); e != nil {
				return preparationDemandResult{}, e
			}
			if ctx.Err() != nil {
				return preparationDemandResult{}, ctx.Err()
			}
			return result, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return preparationDemandResult{}, fmt.Errorf("could not read shared preparation outcome: %w", e)
		}
		workerError, readErr := os.ReadFile(filepath.Join(dir, "worker-error.json"))
		if readErr == nil && string(workerError) != string(previousWorkerError) {
			var failure struct{ Error string }
			if e = json.Unmarshal(workerError, &failure); e != nil {
				return preparationDemandResult{}, fmt.Errorf("the owned worker outcome is unreadable: %w", e)
			}
			return preparationDemandResult{}, fmt.Errorf("the owned preparation worker stopped: %w", errors.New(failure.Error))
		}
		held, probeErr := demandLeaseHeld(filepath.Join(dir, "worker.lock"))
		if errors.Is(probeErr, os.ErrNotExist) {
			held, probeErr = false, nil // no worker has ever started here
		}
		seenWorker = seenWorker || probeErr == nil && held
		if probeErr == nil && !held {
			// The worker may have answered and released between the read above and this probe.
			if _, e = os.Stat(path + ".result"); e == nil {
				continue
			}
			if seenWorker {
				return preparationDemandResult{}, fmt.Errorf("the owned preparation worker exited without a usable outcome: %w", errors.New("worker stopped"))
			}
			if time.Since(started) >= sharedWorkerStartGrace {
				return preparationDemandResult{}, fmt.Errorf("no preparation worker took this request within %s — see %s, then retry the launch", sharedWorkerStartGrace, backgroundAdvanceLog())
			}
		}
		select {
		case <-ctx.Done():
			return preparationDemandResult{}, fmt.Errorf("the preparation requester was interrupted: %w", ctx.Err())
		case <-waitBound.C:
			return preparationDemandResult{}, fmt.Errorf("waiting for owned preparation timed out: %w", pidlock.ErrTimedOut)
		case <-ticker.C:
		}
	}
}

// resolveDemandFork obtains executable authority only from the selected user declarations, not
// from a request, staged manifest, or persistent result. Contention is unknown authorization.
func resolveDemandFork(ctx context.Context, d preparationDemand) (packload.Fork, func(), error) {
	resolver, release := preparationScopedResolver(ctx)
	var contention error
	sel := selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		p, err := resolver(e)
		if errors.Is(err, packsrc.ErrLockHeld) {
			contention = err
		}
		return p, err
	}, config.UserScopeSelection())
	if contention != nil {
		return packload.Fork{}, release, contention
	}
	if problems := sel.problems(); len(problems) > 0 {
		return packload.Fork{}, release, fmt.Errorf("current user selection is incomplete — repair %s, then retry `yolo --update`", paths.UserConfigDisplayPath())
	}
	forks := append(packload.Forks(sel.packs), packload.PatchedTrees(sel.packs)...)
	for _, f := range forks {
		if f.Key() == d.Key {
			identity, err := freshPreparationIdentity(f, d.Platform)
			if err != nil {
				return f, release, err
			}
			platform := strings.SplitN(d.Platform, "/", 2)
			if len(platform) != 2 || !(packdecl.Install{Platforms: f.Platforms}).SupportsPlatform(platform[0], platform[1]) {
				return f, release, errPreparationAdmissionStale
			}
			if d.Identity != "" && identity != d.Identity {
				return f, release, errPreparationAdmissionStale
			}
			if d.SubscriptionGeneration != 0 {
				state, e := loadPreparationState()
				if e != nil {
					return f, release, e
				}
				idx := preparationRecordIndex(state, d.Key, d.Runtime, d.Platform)
				if idx < 0 || !state.Subscriptions[idx].Active || state.Subscriptions[idx].Generation != d.SubscriptionGeneration {
					return f, release, errPreparationAdmissionStale
				}
			}
			if !d.Fresh && d.Background && treeUpdateTiming(f) != timingNextLaunch {
				return f, release, errPreparationAdmissionStale
			}
			return f, release, nil
		}
	}
	return packload.Fork{}, release, errSharedPreparationForegroundOnly
}

// The worker reads yolo's own usable host-floor copy; no requester-supplied path or receipt
// can establish installed bytes. This preserves unchanged freshness when its store copy is gone.
func sharedInstalledCopy(ctx context.Context, d preparationDemand) *installedCopy {
	resolver, release := preparationScopedResolver(ctx)
	defer release()
	selected := selectHostPacks(resolver, config.UserScopeSelection())
	if len(selected.problems()) > 0 || ctx.Err() != nil {
		return nil
	}
	programs := floorPrograms(selected.packs)
	floor := newHostFloor(io.Discard, programs)
	for _, p := range programs {
		if p.Install.IsPatchedFork() && floorForkBuild(p, "").Fork.Key() == d.Key {
			status := floor.Status(p)
			if status.Disposition == hostfloor.Provisioned {
				return floorServingCopy(status.Record)
			}
		}
	}
	return nil
}

// Only positively authorized live requesters can admit. Unknown authorization keeps the
// child alive for a later read, but is never permission to publish its result.
func authorizedPreparationDemands(ctx context.Context, dir string, identity string) (map[string]bool, error) {
	var live []livePreparationDemand
	err := withDemandState(ctx, dir, false, func() error { var e error; live, e = livePreparationDemands(dir); return e })
	if err != nil {
		return nil, err
	}
	authorized := map[string]bool{}
	var unknown error
	for _, r := range live {
		if r.Identity == identity {
			_, release, e := resolvePreparationDemand(ctx, r.preparationDemand)
			release()
			if e == nil {
				authorized[r.path] = true
			} else if !errors.Is(e, errPreparationAdmissionStale) && !errors.Is(e, errSharedPreparationForegroundOnly) {
				unknown = e
			}
		}
	}
	if len(authorized) > 0 {
		return authorized, nil
	}
	if unknown != nil {
		return nil, unknown
	}
	return nil, errPreparationAdmissionStale
}

// A sleeping scheduler can own timer demand without a concurrent dispatch goroutine. Its
// recorded selection alone is insufficient: require the kernel-held worker lease and the
// matching worker identity, then resolve/revalidate the current policy and declaration.
func sharedTimerDemand(ctx context.Context, d preparationDemand) (*preparationRecord, error) {
	held, err := demandLeaseHeld(preparationWorkerLockPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, nil
	}
	state, err := loadPreparationState()
	if err != nil {
		return nil, err
	}
	idx := preparationRecordIndex(state, d.Key, d.Runtime, d.Platform)
	if idx < 0 || !state.Subscriptions[idx].Active {
		return nil, nil
	}
	record, live, probeErr := probePreparationWorker(preparationRoot())
	if live == preparationUnknown {
		return nil, probeErr
	}
	if live == preparationGone {
		return nil, nil
	}
	if record.Identity != state.Worker.Identity {
		return nil, errors.New("scheduler demand incarnation changed; retry status")
	}
	if record.StopRequested {
		return nil, nil
	}

	r := state.Subscriptions[idx]
	automatic := d
	if r.Kind == preparationOrdinaryProgramKind {
		if d.Kind != "" && d.Kind != r.Kind {
			return nil, nil
		}
		automatic.Kind = r.Kind
	}
	automatic.Fresh = false
	automatic.Background = true
	automatic.SubscriptionGeneration = r.Generation
	resolved, release, err := resolvePreparationDemand(ctx, automatic)
	defer release()
	if errors.Is(err, errPreparationAdmissionStale) || errors.Is(err, errSharedPreparationForegroundOnly) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	identity, err := preparationDemandInputIdentity(resolved, automatic)
	if err != nil {
		return nil, err
	}
	if identity != r.InputIdentity {
		return nil, nil
	}
	return &r, nil
}

// preparationAdmissionStateWait bounds a shared worker's wait for scheduler-state.lock: far longer
// than a scheduler.json update takes, far shorter than the check-start wait on the same path.
var preparationAdmissionStateWait = 2 * time.Second

// lockPreparationStateForAdmission takes scheduler-state.lock for a shared worker's subscription
// revalidation. That lock guards only scheduler.json's read-modify-write: every holder is a short
// in-memory update that takes no other lock, so a bounded wait cannot deadlock with the demand or
// check-record locks the admission already holds. A single try read the scheduler's own routine
// bookkeeping (each tick's record refresh, the update a finished task makes) as another process
// owning the key, and the timer retry that should have admitted went busy again. A hold that
// outlasts the bound still reads as contention: busy, retried on the next tick. The scheduler's
// own admission guard (preparationAdmissionGuardUnder) keeps its immediate try by design.
func lockPreparationStateForAdmission(ctx context.Context) (*pidlock.Lock, error) {
	lock, err := pidlock.Acquire(preparationStateLockPath(), pidlock.Mode{Wait: true, Bound: preparationAdmissionStateWait, Cancel: ctx.Done()}, nil)
	switch {
	case err == nil:
		return lock, nil
	case errors.Is(err, pidlock.ErrTimedOut), errors.Is(err, pidlock.ErrHeld):
		return nil, packsrc.ErrLockHeld
	case errors.Is(err, pidlock.ErrCanceled) && ctx.Err() != nil:
		return nil, ctx.Err()
	}
	return nil, err
}

// The caller already holds the artifact's check-record transaction. Do not reacquire that
// record to inspect a failure: the existing advance owns that check/replay/settle authority.
// Revalidate the independently owned subscription with a short, bounded metadata transaction.
func commitSubscribedPreparation(ctx context.Context, d preparationDemand, commit func() error) error {
	resolved, release, err := resolvePreparationDemand(ctx, d)
	defer release()
	if err != nil {
		return err
	}
	identity, err := preparationDemandInputIdentity(resolved, d)
	if err != nil {
		return err
	}
	lock, err := lockPreparationStateForAdmission(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	state, err := loadPreparationState()
	if err != nil {
		return err
	}
	idx := preparationRecordIndex(state, d.Key, d.Runtime, d.Platform)
	if idx < 0 {
		return errPreparationAdmissionStale
	}
	r := state.Subscriptions[idx]
	if !r.Active || r.Generation != d.SubscriptionGeneration || r.InputIdentity != identity {
		return errPreparationAdmissionStale
	}
	if state.Worker.StopRequested {
		return context.Canceled
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return commit()
}

// local serializes this guard with the same worker's demand monitor. The monitor polls state.lock
// throughout the work, and the guard takes it without waiting so that ANOTHER process's hold reads
// as contention (busy, retried); the worker's own poll is not another holder, and without this a
// poll that overlapped an admission refused it as "lock held" — a fresh plain fork's request
// failed outright, and a background advance went busy until the next tick.
func demandAdmissionGuard(ctx context.Context, dir string, d preparationDemand, local *sync.Mutex) func(func() error) error {
	return func(commit func() error) error {
		if sharedDemandAdmissionHook != nil {
			sharedDemandAdmissionHook(d)
		}
		local.Lock()
		defer local.Unlock()
		authorized, err := authorizedPreparationDemands(ctx, dir, d.Identity)
		timer, timerErr := sharedTimerDemand(ctx, d)
		if err != nil && timer == nil {
			return err
		}
		if timerErr != nil && len(authorized) == 0 {
			return timerErr
		}
		return withDemandState(ctx, dir, false, func() error {
			live, err := livePreparationDemands(dir)
			if err != nil {
				return err
			}
			for _, r := range live {
				if authorized[r.path] && r.Identity == d.Identity && r.SubscriptionGeneration == 0 {
					return commit()
				}
			}
			for _, r := range live {
				if authorized[r.path] && r.Identity == d.Identity {
					return commitSubscribedPreparation(ctx, r.preparationDemand, commit)
				}
			}
			if timer != nil {
				automatic := d
				automatic.Fresh = false
				automatic.Background = true
				automatic.SubscriptionGeneration = timer.Generation
				return commitSubscribedPreparation(ctx, automatic, commit)
			}
			return context.Canceled
		})
	}
}

func waitSharedDemandRetry(ctx context.Context, dir string, d preparationDemand) error {
	ticker := time.NewTicker(sharedDemandPoll)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var live []livePreparationDemand
		err := withDemandState(ctx, dir, false, func() error {
			var e error
			live, e = livePreparationDemands(dir)
			return e
		})
		if isSharedDemandStateLockHeld(err) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			return err
		}
		present := false
		for _, request := range live {
			if request.preparationDemand == d {
				present = true
				break
			}
		}
		if !present {
			return errPreparationAdmissionStale
		}
		_, release, err := resolvePreparationDemand(ctx, d)
		release()
		if err != nil {
			return err
		}
		if d.SubscriptionGeneration != 0 {
			if err = commitSubscribedPreparation(ctx, d, func() error { return nil }); err != nil {
				return err
			}
		}
		if d.Fresh {
			boundary, boundaryErr := patchedAdvanceStore(true).CheckBoundary(d.Key)
			if boundaryErr != nil {
				return boundaryErr
			}
			if boundary.Epoch != d.CheckBoundary.Epoch || boundary.Seq < d.CheckBoundary.Seq {
				return errPreparationAdmissionStale
			}
		}
		return nil
	}
}

func sharedFreshAdmissionRetry(ctx context.Context, dir string, d preparationDemand, f packload.Fork,
	opts advanceOptions) advanceResult {
	for {
		if err := ctx.Err(); err != nil {
			a := &advance{f: f, o: opts, pr: richtext.Printer{W: opts.out, Color: opts.color}, epr: richtext.Printer{W: opts.errw, Color: opts.color}}
			return a.freshRefusal("the preparation requester was interrupted", err)
		}
		result := advancePatchedFork(f, opts)
		if !isSharedDemandStateLockHeld(result.admissionErr) {
			return result
		}
		if err := waitSharedDemandRetry(ctx, dir, d); err != nil {
			a := &advance{f: f, o: opts, pr: richtext.Printer{W: opts.out, Color: opts.color}, epr: richtext.Printer{W: opts.errw, Color: opts.color}}
			return a.freshRefusal("shared preparation authority changed before retry", err)
		}
		opts.pool = newAdvancePool(d.Runtime)
	}
}

// sharedFreshPlainAdmissionRetry is sharedFreshAdmissionRetry for a plain fork's fresh request:
// another process holding the demand's state.lock at any admission is contention, so the worker
// waits for the lock (waitSharedDemandRetry, which also proves the request still live and its
// authority unchanged) and runs the preparation again, instead of failing the request outright.
// It stops on the same conditions as the patched retry, and each stop names the retry.
func sharedFreshPlainAdmissionRetry(ctx context.Context, dir string, d preparationDemand, f packload.Fork,
	guard func(func() error) error, w io.Writer) (advanceResult, error) {
	retry := "`yolo --update -- <command>`"
	if d.Host {
		retry = "`yolo host --update -- <command>`"
	}
	for {
		if err := ctx.Err(); err != nil {
			return advanceResult{}, fmt.Errorf("%s could not be prepared freshly (the preparation requester was interrupted: %s) — retry with %s",
				f.Label(), oneLineErr(err), retry)
		}
		res, err := runFreshPlainPreparation(ctx, f, d, guard, w)
		if !isSharedDemandStateLockHeld(err) {
			return res, err
		}
		if err = waitSharedDemandRetry(ctx, dir, d); err != nil {
			return advanceResult{}, fmt.Errorf("%s could not be prepared freshly (shared preparation authority changed before retry: %s) — retry with %s",
				f.Label(), oneLineErr(err), retry)
		}
	}
}

// runSharedPreparation uses the existing check/replay/build/admission pipeline in a process
// whose signal context is owned by the worker, never a launch. The short metadata lock is not
// held across resolution, transport, check/build locks, child teardown, or runtime inspection.
func runSharedPreparation(ctx context.Context, d preparationDemand, w io.Writer) (resultErr error) {
	dir := sharedDemandDir(d.Key, d.Runtime, d.Platform)
	if err := ensurePreparationRoot(dir); err != nil {
		return err
	}
	// A held worker.lock is NOT proof that another worker will answer this demand: every liveness
	// probe (demandLeaseHeld, pidlock.Held — the requester's own wait loop among them) takes the
	// lock for an instant, and an exiting worker holds it after its last read of the demand. Wait
	// for the lock instead of leaving; once it is ours, the loop below answers every live request
	// or finds none. The wait owns nothing, so cancelling it publishes no worker error.
	owner, err := pidlock.Acquire(filepath.Join(dir, "worker.lock"), pidlock.Mode{Wait: true, Cancel: ctx.Done()}, func(int) {
		if sharedWorkerLockContendedHook != nil {
			sharedWorkerLockContendedHook()
		}
	})
	if errors.Is(err, pidlock.ErrCanceled) {
		return nil
	}
	if err != nil {
		return err
	}
	defer owner.Release()
	// One worker's demand monitor and admission guard take state.lock in turn (demandAdmissionGuard).
	var stateMu sync.Mutex
	defer func() {
		if resultErr != nil {
			_ = writeDemandJSON(filepath.Join(dir, "worker-error.json"), struct {
				At    int64
				PID   int
				Error string
			}{time.Now().UnixNano(), os.Getpid(), resultErr.Error()})
		}
	}()
	for {
		var live []livePreparationDemand
		err = withDemandState(ctx, dir, true, func() error {
			var e error
			live, e = livePreparationDemands(dir)
			if e == nil && len(live) == 0 {
				owner.Release()
			}
			return e
		})
		if err != nil {
			return err
		}
		if len(live) == 0 {
			return nil
		}
		request := live[0].preparationDemand
		// Batch only identical declarations. A fresh request may join a check only if the STARTED
		// sequence is after its boundary. Requests arriving during transport get the next batch.
		for _, r := range live {
			if r.Identity == request.Identity && r.Fresh && (!request.Fresh || demandBoundaryAfter(r.preparationDemand, request)) {
				request = r.preparationDemand
			}
		}
		resolved, release, resolveErr := resolvePreparationDemand(ctx, request)
		f := resolved.Fork
		if errors.Is(resolveErr, packsrc.ErrLockHeld) {
			release()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sharedDemandPoll):
			}
			continue
		}
		var res advanceResult
		var program *npmCaptureRecord
		var unsupportedNPM bool
		var nodeFloorUnavailable *run.NodeFloorUnavailable
		if resolveErr != nil {
			res = advanceResult{failed: true, problem: resolveErr.Error(), delivery: entrypoint.ForkDelivery{Reason: resolveErr.Error()}}
		} else {
			workCtx, cancel := context.WithCancel(ctx)
			monitorDone := make(chan struct{})
			go func() {
				defer close(monitorDone)
				ticker := time.NewTicker(sharedDemandPoll)
				defer ticker.Stop()
				for {
					select {
					case <-workCtx.Done():
						return
					case <-ticker.C:
					}
					var pending []livePreparationDemand
					stateMu.Lock()
					e := withDemandState(workCtx, dir, false, func() error {
						if sharedDemandMonitorHook != nil {
							sharedDemandMonitorHook()
						}
						var err error
						pending, err = livePreparationDemands(dir)
						return err
					})
					stateMu.Unlock()
					if e != nil {
						continue
					} // lock contention/read failure is unknown, not zero demand
					keep := false
					for _, r := range pending {
						if r.Identity == request.Identity {
							keep = true
						}
					}
					timer, timerErr := sharedTimerDemand(workCtx, request)
					if !keep && timer == nil {
						if timerErr != nil {
							continue
						} // unknown observation is not zero demand
						cancel()
						return
					}
					stateMu.Lock()
					_, e = authorizedPreparationDemands(workCtx, dir, request.Identity)
					stateMu.Unlock()
					if timer == nil && errors.Is(e, errPreparationAdmissionStale) {
						cancel()
						return
					}
				}
			}()

			if resolved.NPM != nil {
				record, err := advanceNPMCapture(withOrdinaryCaptureRuntime(workCtx, request.Runtime), &capture.Store{Dir: paths.CapturesDir()}, *resolved.NPM, resolved.Fingerprint, request.Fresh, request.AfterSeq, w, demandAdmissionGuard(workCtx, dir, request, &stateMu))
				if err != nil {
					unsupportedNPM = errors.Is(err, errNPMCaptureUnsupported)
					if workCtx.Err() == nil {
						if errors.As(err, &nodeFloorUnavailable) && (!nodeFloorUnavailable.Valid() || nodeFloorUnavailable.Owner != resolved.NPM.Owner || nodeFloorUnavailable.Bin != resolved.NPM.Bin || nodeFloorUnavailable.Floor != resolved.NPM.NodeFloor) {
							nodeFloorUnavailable = nil
						}
					}
					res = advanceResult{failed: true, operationError: err.Error()}
				} else {
					program = &record
				}
			} else if !f.Patched() && !f.IsTree() && request.Fresh {
				var plainErr error
				res, plainErr = sharedFreshPlainAdmissionRetry(workCtx, dir, request, f, demandAdmissionGuard(workCtx, dir, request, &stateMu), w)
				if plainErr != nil {
					res = advanceResult{failed: true, operationError: plainErr.Error(), delivery: entrypoint.ForkDelivery{Reason: plainErr.Error()}}
					errors.As(plainErr, &res.pending)
				}
			} else {
				opts := advanceOptions{runtime: request.Runtime, platform: request.Platform, launch: true, host: request.Host, background: request.Background, freshCheck: request.Fresh, freshIdentity: request.Identity, freshBoundary: request.CheckBoundary, ctx: workCtx, out: w, errw: w, pool: newAdvancePool(request.Runtime), sharedOwned: true, admitGuard: demandAdmissionGuard(workCtx, dir, request, &stateMu)}
				if request.Host && !f.IsTree() {
					hostOnly := true
					for _, r := range live {
						if r.Identity == request.Identity && !r.Host {
							hostOnly = false
						}
					}
					if hostOnly {
						opts.installed = sharedInstalledCopy(workCtx, request)
					}
				}
				if request.Fresh {
					res = sharedFreshAdmissionRetry(workCtx, dir, request, f, opts)
				} else {
					res = advancePatchedFork(f, opts)
				}
			}
			cancel()
			<-monitorDone
		}
		release()
		err = withDemandState(ctx, dir, true, func() error {
			pending, e := livePreparationDemands(dir)
			if e != nil {
				return e
			}
			for _, r := range pending {
				if r.Identity != request.Identity || r.Fresh && (!request.Fresh || demandBoundaryAfter(r.preparationDemand, request)) {
					continue
				}
				if request.Kind != preparationOrdinaryProgramKind && request.Host && !f.IsTree() && !r.Host && r.Fresh && res.delivery.Key == "" && !res.failed && res.skipped == "" {
					continue
				} // an installed host copy cannot fulfill a jail artifact demand
				result := demandResult(res)
				result.Program = program
				result.UnsupportedNPM = unsupportedNPM
				result.NodeFloorUnavailable = nodeFloorUnavailable
				if e = writeDemandJSON(r.path+".result", result); e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
}

func runSharedPreparationCommand(a backgroundArgs, w io.Writer) int {
	if len(a.keys) != 1 {
		fmt.Fprintln(w, "shared preparation requires exactly one selected key — retry `yolo --update`")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), backgroundSignals...)
	defer stop()
	if err := runSharedPreparation(ctx, preparationDemand{Key: a.keys[0], Runtime: a.runtime, Platform: a.platform}, w); err != nil {
		fmt.Fprintf(w, "shared preparation stopped: %v — retry `yolo --update`\n", err)
		return 1
	}
	return 0
}
