package cli

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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

const preparationSchedulerVerb = "preparation-scheduler"
const preparationTickInterval = time.Minute

var errPreparationWorkerFromTest = errors.New("a test binary does not self-exec the preparation scheduler")

// preparationSpawn is replaceable in in-process tests; it never self-execs the test binary.
var preparationSpawn = func() error {
	if flag.Lookup("test.v") != nil {
		return nil
	}
	return startPreparationScheduler()
}

func preparationInputIdentity(f packload.Fork, rt, platform string) (string, error) {
	series, err := f.DeclaredSeries()
	if err != nil {
		return "", err
	}
	identity := struct {
		Key, Pack, Owner, Follow, Recipe, Runtime, Platform string
		Platforms                                           []string
	}{f.Key(), f.Pack, f.Owner, f.Follow, run.PatchedRecipe(f, series.Digest), rt, platform, f.Platforms}
	b, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

type preparationPatchInspection struct {
	failure *packsrc.PatchFailure
	err     error
}

func preparationEligible(f packload.Fork, req run.TreeBuildRequest) bool {
	return req.Build && f.IsTree() && treeUpdateTiming(f) == timingNextLaunch && run.BackgroundChecksOn(req.Runtime, req.Platform)
}

// registerTreePreparation persists the selected tree set after pool registration, even when no
// subscription is due. It records no executable source paths: the worker resolves the user config.
func registerTreePreparation(trees []packload.Fork, req run.TreeBuildRequest, w io.Writer) error {
	return registerTreePreparationScoped(trees, req, w, true)
}

// registerHostTreePreparation adds only the host's selected owner-scoped keys. Unlike a jail launch,
// it cannot replace the scheduler's whole selected vector: host -- <bin> deliberately registers only
// the extensions loaded by that declared command owner.
func registerHostTreePreparation(trees []packload.Fork, w io.Writer) error {
	return registerTreePreparationScoped(trees, run.TreeBuildRequest{Build: true, Runtime: "podman", Platform: captureJailPlatform()}, w, false)
}

func registerTreePreparationScoped(trees []packload.Fork, req run.TreeBuildRequest, w io.Writer, replaceSelection bool) error {
	if !req.Build {
		return nil
	}
	inspections := make(map[string]preparationPatchInspection, len(trees))
	for _, f := range trees {
		if f.IsTree() {
			failure, inspectErr := preparationPatchFailure(context.Background(), f)
			inspections[f.Key()] = preparationPatchInspection{failure: failure, err: inspectErr}
		}
	}
	var spawn bool
	err := withPreparationState(func(state *preparationState) error {
		if replaceSelection {
			for i := range state.Subscriptions {
				r := &state.Subscriptions[i]
				if r.Runtime == req.Runtime && r.Platform == req.Platform && preparationKind(*r) == preparationTreeKind {
					if _, selected := inspections[r.Key]; selected {
						continue // selected keys are updated only after their check-record authority is known
					}
					r.Active, r.Reason = false, "not selected for next-launch preparation by the current user config"
				}
			}
		}
		for _, f := range trees {
			if !f.IsTree() {
				continue
			}
			key := f.Key()
			idx := preparationRecordIndex(*state, key, req.Runtime, req.Platform)
			if idx < 0 {
				state.Subscriptions = append(state.Subscriptions, preparationRecord{Key: key, Runtime: req.Runtime,
					Platform: req.Platform, Phase: "inactive"})
				idx = len(state.Subscriptions) - 1
			}
			r := &state.Subscriptions[idx]
			r.Kind = preparationTreeKind
			inspection := inspections[key]
			if inspection.err != nil {
				continue // contention, cancellation and unreadable authority are not a fresh result
			}
			if failure := inspection.failure; failure != nil {
				r.Active, r.Phase = false, "failed"
				r.Reason = failure.Error() + " — repair the patch failure before resuming `yolo pack prepare start`"
				r.UpdatedAt = preparationNow()
				continue
			}
			identity, identityErr := preparationInputIdentity(f, req.Runtime, req.Platform)
			if identityErr != nil {
				r.Active, r.Phase = false, "invalid"
				r.Reason = fmt.Sprintf("could not read the selected tree declaration (%v) — repair the pack, then run `yolo pack prepare start`", identityErr)
				continue
			}
			eligible := preparationEligible(f, req)
			if r.InputIdentity != identity {
				r.Generation++
				r.InputIdentity = identity
				r.Phase, r.ServingEntry, r.At, r.RetryAt = "pending", "", time.Time{}, time.Time{}
			}
			r.Active = eligible
			if eligible {
				r.Reason, r.Phase = "", "pending"
				spawn = true
			} else if treeUpdateTiming(f) != timingNextLaunch {
				r.Reason, r.Phase = "held", "inactive"
			} else {
				r.Reason, r.Phase = runtimeName(req.Runtime)+" does not support background tree preparation — use a fresh launch to prepare it", "unsupported"
			}
			r.UpdatedAt = preparationNow()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if spawn {
		if err := ensurePreparationScheduler(); err != nil {
			fmt.Fprintf(w, "⚠ next-launch tree preparation was recorded, but its host worker could not start (%v) — run `yolo pack prepare start` to retry\n", err)
			return err
		}
		fmt.Fprintf(w, "next-launch tree preparation is recorded; the worker prepares later generations without changing this environment — `yolo pack prepare status` shows progress (log %s)\n", backgroundAdvanceLog())
	}
	return nil
}

func preparationSchedulerArgv() []string { return []string{"internal", preparationSchedulerVerb} }

func startPreparationScheduler() error {
	if flag.Lookup("test.v") != nil {
		return errPreparationWorkerFromTest
	}
	if err := os.MkdirAll(filepath.Dir(backgroundAdvanceLog()), 0o700); err != nil {
		return err
	}
	f, err := notty.OpenDetachLog(backgroundAdvanceLog())
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := preparationSchedulerProcess()
	environment, err := loadPreparationEnvironment()
	if err != nil {
		return err
	}
	cmd.Env = environment
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, f, f
	return notty.StartDetached(cmd)
}

func preparationSchedulerProcess() *exec.Cmd {
	cmd := exec.Command(run.SelfExecPath(), preparationSchedulerArgv()...)
	return cmd
}

var preparationStartLock = filepath.Join(paths.BackgroundAdvanceDir(), "scheduler-start.lock")

func ensurePreparationScheduler() error {
	if err := ensurePreparationRoot(preparationRoot()); err != nil {
		return err
	}
	lock, err := pidlock.Acquire(preparationStartLock, pidlock.Wait, nil)
	if err != nil {
		return err
	}
	defer lock.Release()
	record, live, err := probePreparationWorker(preparationRoot())
	if live == preparationUnknown {
		return err
	}
	if live == preparationAlive {
		digest, err := preparationBinaryDigest(run.SelfExecPath())
		if err != nil {
			return err
		}
		if digest == record.BinaryDigest && record.Executable == run.SelfExecPath() {
			return controlPreparationWorker(context.Background(), preparationRoot(), record.Identity, "wake")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := controlPreparationWorker(ctx, preparationRoot(), record.Identity, "replace"); err != nil {
			return err
		}
		if err := waitPreparationGone(ctx, preparationRoot()); err != nil {
			return err
		}
	}
	return preparationSpawn()
}

func notifyPreparationScheduler() error {
	record, live, err := probePreparationWorker(preparationRoot())
	if live == preparationGone {
		return nil
	}
	if live == preparationUnknown {
		return err
	}
	return controlPreparationWorker(context.Background(), preparationRoot(), record.Identity, "wake")
}

func waitPreparationGone(ctx context.Context, root string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, live, _ := probePreparationWorker(root)
		if live == preparationGone {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("preparation teardown still owns its lease; retry `yolo pack prepare status` then start: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func runPreparationSchedulerCommand() int {
	if config.InJail() {
		fmt.Fprintln(os.Stderr, "yolo internal preparation-scheduler: runs on the host only — run `yolo pack prepare start` on the host")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), backgroundSignals...)
	defer cancel()
	exe := run.SelfExecPath()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	wake := make(chan struct{}, 1)
	drain := make(chan struct{})
	var drainOnce sync.Once
	wakeScheduler := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	err := runPreparationWorker(ctx, preparationRoot(), exe, ticks, func(ownerCtx context.Context) error {
		ticker := time.NewTicker(preparationTickInterval)
		defer ticker.Stop()
		wakeScheduler()
		return runPreparationScheduler(ownerCtx, ticker.C, os.Stdout, wake, drain)
	}, preparationWorkerControl{Wake: wakeScheduler, Finish: func(state *preparationState, err error) {
		for i := range state.Subscriptions {
			record := &state.Subscriptions[i]
			if err != nil && !errors.Is(err, context.Canceled) && record.Active {
				record.Phase = "binary-mismatch"
				record.Reason = err.Error()
				continue
			}
			if record.Phase != "checking" {
				continue
			}
			record.Phase = "interrupted"
			record.At = preparationNow()
			record.Reason = "the owned worker stopped before this act settled; admitted build records remain authoritative — run `yolo pack prepare start` to resume"
		}
	}, Drain: func() { drainOnce.Do(func() { close(drain) }) }})

	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type preparationTask struct {
	cancel     context.CancelFunc
	generation uint64
	baseline   uint64
}
type preparationTaskResult struct {
	key, runtime, platform string
	generation             uint64
	baseline               uint64
	err                    error
	program                *npmCaptureRecord
}

// preparationControlTicker paces the scheduler's stop and binary-identity checks; a test injects
// its own ticks. Nothing else reassigns it.
var preparationControlTicker = func() (<-chan time.Time, func()) {
	ticker := time.NewTicker(time.Second)
	return ticker.C, ticker.Stop
}

// runPreparationScheduler is the timer production loop. Tests inject ticks into this exact loop.
func runPreparationScheduler(ctx context.Context, ticks <-chan time.Time, w io.Writer, wakes ...<-chan struct{}) error {
	var wake, drain <-chan struct{}
	draining := false
	if len(wakes) > 0 {
		wake = wakes[0]
	}
	if len(wakes) > 1 {
		drain = wakes[1]
	}
	done := make(chan preparationTaskResult, 32)
	more := make(chan struct{}, 1)
	tickEvents := make(chan time.Time)
	pumpCtx, stopPump := context.WithCancel(ctx)
	defer stopPump()
	go func() {
		defer close(tickEvents)
		for {
			select {
			case tick, ok := <-ticks:
				if !ok {
					return
				}
				select {
				case tickEvents <- tick:
				case <-pumpCtx.Done():
					return
				}
			case <-more:
				select {
				case tickEvents <- time.Time{}:
				case <-pumpCtx.Done():
					return
				}
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	tasks := map[string]preparationTask{}
	var releaseSnapshot func()
	defer func() {
		if releaseSnapshot != nil {
			releaseSnapshot()
		}
	}()
	attempted := map[string]bool{}
	defer func() {
		for _, task := range tasks {
			task.cancel()
		}
		for range tasks {
			<-done
		}
	}()
	controlTicks, stopControl := preparationControlTicker()
	defer stopControl()
	var binaryCheck preparationBinaryCheck
	keyID := func(r preparationRecord) string { return r.Key + "\x00" + r.Runtime + "\x00" + r.Platform }
	for {
		if draining && len(tasks) == 0 {
			return nil
		}
		select {
		case <-drain:
			draining = true
			drain = nil
		case <-ctx.Done():
			return ctx.Err()
		case result := <-done:
			id := result.key + "\x00" + result.runtime + "\x00" + result.platform
			delete(tasks, id)
			reprocess := true
			_ = preparationUpdateRecord(result.key, result.runtime, result.platform, func(r *preparationRecord) {
				if r.Generation != result.generation || !r.Active {
					return
				}
				r.At = preparationNow()
				if r.Kind == preparationOrdinaryProgramKind {
					if errors.Is(result.err, packsrc.ErrLockHeld) {
						// Contention is a busy skip, as on the plain and tree paths: no failure
						// stamp and no retry throttle; the next timer tick tries again.
						r.Phase = "busy"
						r.Reason = "another yolo process holds a lock this preparation needs; the next timer tick retries without waiting for it"
						r.RetryAt = time.Time{}
					} else if result.err != nil || result.program == nil {
						r.Phase = "failed"
						r.Reason = fmt.Sprintf("%v — retry a fresh launch", result.err)
						r.RetryAt = preparationNow().Add(time.Hour)
					} else {
						r.Phase = "admitted"
						if r.ServingEntry == result.program.Entry {
							r.Phase = "unchanged"
						}
						r.ServingEntry, r.Reason = result.program.Entry, ""
						r.RetryAt = time.Time{}
					}
					return
				}
				if outcome, err := readBackgroundOutcome(backgroundOutcomePath(r.Key)); err == nil &&
					outcome.Generation > result.baseline && outcome.Key == r.Key && outcome.ProducerPID == os.Getpid() && outcome.SubscriptionGeneration == result.generation && outcome.Pid == 0 && outcome.State != bgRunning {
					phase := outcome.State
					switch phase {
					case bgMoved:
						phase = "admitted"
					case bgNothing:
						phase = "unchanged"
					case bgSkipped:
						phase = "busy"
					}
					r.Phase, r.ServingEntry = phase, outcome.To
					r.Log = outcome.Log
					r.Reason = outcome.Error
					if r.Reason == "" {
						r.Reason = outcome.Retained
					}
					r.RetryAt = time.Time{}
					if outcome.RetryAt > 0 {
						r.RetryAt = time.Unix(outcome.RetryAt, 0)
					}
				} else if result.err != nil {
					r.Phase, r.Reason = "failed", result.err.Error()
				} else {
					r.Phase, r.Reason = "busy", "another authorized advance owns this key; the next timer tick retries without changing its successful check stamp"
				}
			})
			preparationTaskSettled(result.key)
			if reprocess {
				select {
				case more <- struct{}{}:
				default:
				}
			}
		case <-wake:
			select {
			case more <- struct{}{}:
			default:
			}
		case <-controlTicks:
			// One read of the scheduler state serves both checks.
			state, err := loadPreparationState()
			if err != nil {
				return err
			}
			if state.Worker.StopRequested {
				return nil
			}
			if err := binaryCheck.check(state.Worker); err != nil {
				return err
			}

		case tick, ok := <-tickEvents:
			if draining {
				continue
			}
			if !ok {
				return ctx.Err()
			}
			if !tick.IsZero() {
				attempted = map[string]bool{}
			}
			state, stateErr := preparationSubscriptions()
			if stateErr != nil {
				return stateErr
			}
			if len(state) == 0 {
				return nil
			}
			if releaseSnapshot != nil {
				releaseSnapshot()
			}
			current, release, configErr := scopedPreparationTrees(ctx, state)
			releaseSnapshot = release
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(configErr, packsrc.ErrLockHeld) {
				continue
			}
			if configErr != nil {
				for _, r := range state {
					if r.Active {
						_ = preparationUpdateRecord(r.Key, r.Runtime, r.Platform, func(rec *preparationRecord) {
							rec.Active = false
							rec.Phase = "invalid-config"
							rec.Reason = fmt.Sprintf("%v — repair %s, then run `yolo pack prepare start`", configErr, paths.UserConfigDisplayPath())
						})
					}
				}
				return configErr
			}
			state = refreshPreparationSubscriptionsUnder(ctx, state, current)
			state = preparationRefreshOrdinary(ctx, state)
			if err := ctx.Err(); err != nil {
				return err
			}
			eligible := false
			for _, record := range state {
				eligible = eligible || record.Active
			}
			if !eligible && len(tasks) == 0 {
				return nil
			}
			for id, task := range tasks {
				var matching bool
				for _, r := range state {
					if keyID(r) == id && r.Active && r.Generation == task.generation {
						matching = true
						break
					}
				}
				if !matching {
					task.cancel()
				}
			}
			for _, r := range state {
				if !r.Active || !run.BackgroundChecksOn(r.Runtime, r.Platform) || len(tasks) > 0 {
					continue
				}
				if !r.RetryAt.IsZero() && preparationNow().Before(r.RetryAt) {
					continue
				}
				id := keyID(r)
				if _, busy := tasks[id]; busy || attempted[id] {
					continue
				}
				f, ok := current[id]
				ordinary := r.Kind == preparationOrdinaryProgramKind
				if !ok && !ordinary {
					continue
				}
				if ordinary {
					if !preparationOrdinaryDue(ctx, r) {
						continue
					}
				} else if preparationKind(r) == preparationPlainProgramKind {
					pin, pinErr := currentForkPin(f)
					if pinErr != nil || pin.Commit != r.PinCommit {
						if pinErr == nil {
							pinErr = fmt.Errorf("the standing pin changed from %s to %s", shortSHA(r.PinCommit), shortSHA(pin.Commit))
						}
						_ = preparationUpdateRecord(r.Key, r.Runtime, r.Platform, func(rec *preparationRecord) {
							if rec.Generation == r.Generation {
								rec.Active, rec.Phase = false, "unresolved"
								rec.Reason = pinErr.Error() + " — run `yolo pack install` to restore the selected pin"
							}
						})
						continue
					}
					b := forkBuild{Fork: f, Commit: pin.Commit, Platform: r.Platform}
					if entry, _, resolveErr := resolveForkBuild(&capture.Store{Dir: paths.CapturesDir()}, f.Bin, r.Platform, f.Source, pin.Commit, b.recipe()); resolveErr == nil {
						_ = preparationUpdateRecord(r.Key, r.Runtime, r.Platform, func(rec *preparationRecord) {
							if rec.Active && rec.Generation == r.Generation {
								rec.Phase, rec.ServingEntry, rec.Reason, rec.At = "unchanged", entry.Key, "", preparationNow()
							}
						})
						continue
					}
				} else {
					failure, inspectErr := preparationPatchFailure(ctx, f)
					if inspectErr != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						continue // unknown authority is retried; it must not revoke or refresh this subscription
					}
					if failure != nil {
						_ = preparationUpdateRecord(r.Key, r.Runtime, r.Platform, func(rec *preparationRecord) {
							rec.Active, rec.Phase = false, "failed"
							rec.Reason = failure.Error() + " — repair the patch failure before resuming `yolo pack prepare start`"
						})
						continue
					}
					advance, early := newAdvance(f, advanceOptions{platform: r.Platform, runtime: r.Runtime, background: true, launch: true})
					if early != nil {
						_ = preparationUpdateRecord(r.Key, r.Runtime, r.Platform, func(rec *preparationRecord) {
							rec.Phase = "unresolved"
							rec.Reason = early.problem + " — run `yolo pack status " + r.Key + "` and repair the selected declaration"
						})
						continue
					}
					if !advance.wantsBackground() {
						continue
					}
				}
				baseline := uint64(0)
				if old, err := readBackgroundOutcome(backgroundOutcomePath(r.Key)); err == nil {
					baseline = old.Generation
				}
				attempted[id] = true
				taskCtx, cancel := context.WithCancel(ctx)
				tasks[id] = preparationTask{cancel: cancel, generation: r.Generation, baseline: baseline}
				_ = preparationUpdateRecord(r.Key, r.Runtime, r.Platform, func(rec *preparationRecord) {
					rec.Phase, rec.Reason, rec.At, rec.RetryAt = "checking", "", preparationNow(), time.Time{}
					if preparationKind(r) == preparationPlainProgramKind {
						rec.Phase = "building"
					}
					rec.Log = backgroundAdvanceLog()
				})
				go func(r preparationRecord, f packload.Fork, baseline uint64) {
					var out strings.Builder
					var taskErr error
					var program *npmCaptureRecord
					if r.Kind == preparationOrdinaryProgramKind {
						program, taskErr = preparationRunOrdinary(taskCtx, r, &out)
					} else if preparationKind(r) == preparationPlainProgramKind {
						taskErr = runPlainForkPreparation(taskCtx, r, f, &out)
					} else {
						runBackgroundAdvanceUnder(taskCtx, backgroundArgs{runtime: r.Runtime, platform: r.Platform, keys: []string{r.Key}, schedulerGeneration: r.Generation}, &out)
					}
					if out.Len() > 0 {
						_, _ = io.WriteString(w, out.String())
					}
					cancel()
					done <- preparationTaskResult{key: r.Key, runtime: r.Runtime, platform: r.Platform, generation: r.Generation, baseline: baseline, err: taskErr, program: program}
				}(r, f, baseline)
			}

		}
	}
}

func currentPreparationTrees(records []preparationRecord) (map[string]packload.Fork, error) {
	// Compatibility for short-lived callers. Persistent readers use an explicitly owned scope.
	return preparationTreesWithResolver(records, resolveConfiguredPack)
}

func scopedPreparationTrees(ctx context.Context, records []preparationRecord) (map[string]packload.Fork, func(), error) {
	resolver, release := preparationScopedResolver(ctx)
	current, err := preparationTreesWithResolver(records, resolver)
	return current, release, err
}

func preparationTreesWithResolver(records []preparationRecord, resolver func(config.PackEntry) (*packload.Pack, error)) (map[string]packload.Fork, error) {
	var contention error
	sel := selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		p, err := resolver(e)
		if errors.Is(err, packsrc.ErrLockHeld) {
			contention = err
		}
		return p, err
	}, config.UserScopeSelection())
	if contention != nil {
		return nil, contention
	}
	if problems := sel.problems(); len(problems) > 0 {
		var lines []string
		for _, problem := range problems {
			lines = append(lines, problem.Name+": "+problem.Reason)
		}
		return nil, fmt.Errorf("the current user pack selection is incomplete (%s)", strings.Join(lines, "; "))
	}
	want := map[string]bool{}
	for _, record := range records {
		want[record.Key+"\x00"+record.Runtime+"\x00"+record.Platform] = true
	}
	out := map[string]packload.Fork{}
	forks := append(packload.PatchedTrees(sel.packs), packload.Forks(sel.packs)...)
	for _, f := range forks {
		for id := range want {
			parts := strings.SplitN(id, "\x00", 3)
			if len(parts) == 3 && parts[0] == f.Key() {
				out[id] = f
			}
		}
	}
	return out, nil
}

func refreshPreparationSubscriptions(records []preparationRecord, current map[string]packload.Fork) []preparationRecord {
	return refreshPreparationSubscriptionsUnder(context.Background(), records, current)
}

// The ordinary-program acts the timer drives, behind vars so an in-process test can stand in for
// one stage while the scheduler loop itself stays the production one. Nothing but a test
// reassigns them.
var (
	preparationRefreshOrdinary = refreshOrdinaryPreparationSubscriptions
	preparationOrdinaryDue     = ordinaryPreparationDue
	preparationRunOrdinary     = runOrdinaryScheduledPreparation
)

// preparationTaskSettled observes a finished task once its result is recorded in the
// subscription state, before the next dispatch pass; the default has no side effects.
var preparationTaskSettled = func(key string) {}

// preparationRefreshInspected observes completed authority inspections; the default has no side effects.
var preparationRefreshInspected = func(error) {}

func refreshPreparationSubscriptionsUnder(ctx context.Context, records []preparationRecord, current map[string]packload.Fork) []preparationRecord {
	for _, old := range records {
		if old.Kind == preparationOrdinaryProgramKind {
			continue
		}
		id := old.Key + "\x00" + old.Runtime + "\x00" + old.Platform
		f, ok := current[id]
		var failure *packsrc.PatchFailure
		var inspectErr error
		if ok && preparationKind(old) != preparationPlainProgramKind {
			failure, inspectErr = preparationPatchFailure(ctx, f)
			preparationRefreshInspected(inspectErr)
		}
		_ = preparationUpdateRecord(old.Key, old.Runtime, old.Platform, func(r *preparationRecord) {
			if inspectErr != nil {
				return // unknown check-record authority cannot overwrite prior failure, freshness or backoff
			}
			if !ok {
				r.Active = false
				r.Phase = "unresolved"
				r.Reason = "the user-configured pack selection no longer authorizes this key for background preparation; select its owner pack or run `yolo capture " + r.Key + "` for a manual build"
				return
			}
			if preparationKind(*r) != preparationForkKind(f) {
				r.Active, r.Phase = false, "unresolved"
				r.Reason = "the selected contribution changed from this recorded preparation kind — start a fresh launch to register its current artifact route"
				return
			}
			identity := ""
			var err error
			if preparationKind(*r) == preparationPlainProgramKind {
				pin, pinErr := currentForkPin(f)
				if pinErr != nil {
					r.Active, r.Phase = false, "unresolved"
					r.Reason = pinErr.Error() + " — run `yolo pack install` to establish the selected pin"
					return
				}
				identity, err = preparationForkIdentity(f, pin.Commit, r.Runtime, r.Platform)
				r.PinCommit = pin.Commit
			} else if preparationKind(*r) == preparationPatchedProgramKind {
				identity, err = preparationForkIdentity(f, "", r.Runtime, r.Platform)
			} else {
				identity, err = preparationInputIdentity(f, r.Runtime, r.Platform)
			}
			if err != nil {
				r.Active = false
				r.Phase = "invalid"
				r.Reason = fmt.Sprintf("%v — repair the selected pack, then run `yolo pack prepare start`", err)
				return
			}
			if failure != nil {
				r.Active = false
				r.Phase = "failed"
				r.Reason = failure.Error() + " — repair the patch failure before resuming `yolo pack prepare start`"
				return
			}
			if identity != r.InputIdentity {
				r.Generation++
				r.InputIdentity = identity
				r.Phase = "pending"
				r.At = time.Time{}
				r.ServingEntry = ""
				r.Reason = ""
				r.RetryAt = time.Time{}
			}
			eligible := false
			switch preparationKind(*r) {
			case preparationTreeKind:
				eligible = treeUpdateTiming(f) == timingNextLaunch
			case preparationPatchedProgramKind, preparationPlainProgramKind:
				eligible = forkUpdateTiming(f) == timingNextLaunch
			}
			if !eligible {
				r.Active = false
				r.Phase = "held"
				r.Reason = "the current `agent_updates` policy does not select next-launch preparation — change the user config to resume"
				return
			}
			if !run.BackgroundChecksOn(r.Runtime, r.Platform) || preparationKind(*r) == preparationPlainProgramKind && !run.BackgroundBuildsOn(r.Runtime) {
				r.Active = false
				r.Phase = "unsupported"
				r.Reason = runtimeName(r.Runtime) + " does not support background preparation — use a fresh launch to prepare it"
				return
			}
			if !r.Active {
				r.Reason = ""
			}
			r.Active = true
		})
	}
	updated, _ := preparationSubscriptions()
	return updated
}

func preparationStopRequested() bool {
	state, err := loadPreparationState()
	return err == nil && state.Worker.StopRequested
}

func preparationAdmissionGuard(key, rt, platform string, generation uint64) func(func() error) error {
	return preparationAdmissionGuardUnder(context.Background(), key, rt, platform, generation)
}

func preparationAdmissionGuardUnder(ctx context.Context, key, rt, platform string, generation uint64) func(func() error) error {
	return func(commit func() error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Nonwaiting resolution happens before acquiring the subscription guard.
		current, release, err := scopedPreparationTrees(ctx, []preparationRecord{{Key: key, Runtime: rt, Platform: platform}})
		defer release()
		if err != nil {
			return errPreparationAdmissionStale
		}
		id := key + "\x00" + rt + "\x00" + platform
		f, ok := current[id]
		if !ok {
			return errPreparationAdmissionStale
		}
		kind := preparationKind(preparationRecord{Kind: preparationForkKind(f)})
		eligible := false
		switch kind {
		case preparationTreeKind:
			eligible = treeUpdateTiming(f) == timingNextLaunch
		case preparationPatchedProgramKind, preparationPlainProgramKind:
			eligible = forkUpdateTiming(f) == timingNextLaunch
		}
		if !eligible || !run.BackgroundChecksOn(rt, platform) || kind == preparationPlainProgramKind && !run.BackgroundBuildsOn(rt) {
			return errPreparationAdmissionStale
		}
		if kind != preparationPlainProgramKind {
			failure, inspectErr := preparationPatchFailure(ctx, f)
			if inspectErr != nil {
				return inspectErr // held is retryable; unknown and cancellation are not revocation
			}
			if failure != nil {
				return errPreparationAdmissionStale
			}
		}
		identity := ""
		if kind == preparationPlainProgramKind {
			pin, pinErr := currentForkPin(f)
			if pinErr != nil {
				return errPreparationAdmissionStale
			}
			identity, err = preparationForkIdentity(f, pin.Commit, rt, platform)
		} else if kind == preparationPatchedProgramKind {
			identity, err = preparationForkIdentity(f, "", rt, platform)
		} else {
			identity, err = preparationInputIdentity(f, rt, platform)
		}
		if err != nil {
			return errPreparationAdmissionStale
		}
		lock, err := pidlock.Acquire(preparationStateLockPath(), pidlock.NoWait, nil)
		if err != nil {
			if errors.Is(err, pidlock.ErrHeld) {
				return packsrc.ErrLockHeld
			}
			return err
		}
		defer lock.Release()
		state, err := loadPreparationState()
		if err != nil {
			return err
		}
		i := preparationRecordIndex(state, key, rt, platform)
		if i < 0 {
			return errPreparationAdmissionStale
		}
		record := state.Subscriptions[i]
		if !record.Active || record.Generation != generation || preparationKind(record) != kind {
			return errPreparationAdmissionStale
		}
		if record.InputIdentity != identity {
			return errPreparationAdmissionStale
		}
		if preparationStopRequested() {
			return errPreparationAdmissionStale
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return commit()
	}
}

func preparationCheckBinaryIdentity() error {
	state, err := loadPreparationState()
	if err != nil {
		return err
	}
	return (&preparationBinaryCheck{}).check(state.Worker)
}

// preparationStatIdentity is the cheap stand-in for an executable's content: device, inode,
// size and modification time. It decides only whether the digest must be computed again; the
// digest stays the authority.
type preparationStatIdentity struct {
	dev, ino uint64
	size     int64
	mtime    int64
}

func preparationStatOf(path string) (preparationStatIdentity, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return preparationStatIdentity{}, false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return preparationStatIdentity{}, false, nil
	}
	return preparationStatIdentity{dev: uint64(st.Dev), ino: uint64(st.Ino), size: fi.Size(), mtime: fi.ModTime().UnixNano()}, true, nil
}

// preparationBinaryCheck remembers the stat identity of the last executable whose digest
// matched, so the scheduler's control tick hashes the binary only when that identity moved.
type preparationBinaryCheck struct {
	path, digest string
	stat         preparationStatIdentity
	known        bool
}

func (c *preparationBinaryCheck) check(worker preparationWorker) error {
	if worker.BinaryDigest == "" || worker.Executable == "" {
		return nil
	}
	changed := errors.New("yolo binary changed while the preparation worker was running — its work was cancelled; run `yolo pack prepare start` to restart with the selected binary")
	stat, statOK, err := preparationStatOf(worker.Executable)
	if err != nil {
		c.known = false
		return changed
	}
	if c.known && statOK && c.path == worker.Executable && c.digest == worker.BinaryDigest && c.stat == stat {
		return nil
	}
	c.known = false
	digest, digestErr := preparationBinaryDigest(worker.Executable)
	if digestErr != nil || digest != worker.BinaryDigest {
		return changed
	}
	c.path, c.digest, c.stat, c.known = worker.Executable, digest, stat, statOK
	return nil
}
