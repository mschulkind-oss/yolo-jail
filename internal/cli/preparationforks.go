package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const (
	preparationTreeKind           = "tree"
	preparationPatchedProgramKind = "patched-program"
	preparationPlainProgramKind   = "plain-program"
)

func preparationKind(record preparationRecord) string {
	if record.Kind == "" { // task-1 records predate the kind field and are tree subscriptions.
		return preparationTreeKind
	}
	return record.Kind
}

func preparationForkKind(f packload.Fork) string {
	if f.IsTree() {
		return preparationTreeKind
	}
	if f.Patched() {
		return preparationPatchedProgramKind
	}
	return preparationPlainProgramKind
}

func preparationForkIdentity(f packload.Fork, commit, runtime, platform string) (string, error) {
	var series string
	if f.Patched() {
		loaded, err := f.DeclaredSeries()
		if err != nil {
			return "", err
		}
		series = loaded.Digest
	}
	identity := struct {
		Key, Pack, Base, Source, Follow, Build, Recipe, Series, Commit, Runtime, Platform string
		Produces, Platforms                                                               []string
	}{f.Key(), f.Pack, f.Base, f.Source, f.Follow, f.Build,
		packdecl.ForkSourceRecipe(f.Source, f.Build, f.Produces), series, commit, runtime, platform,
		f.Produces, f.Platforms}
	if f.Patched() {
		identity.Recipe = packdecl.ForkSourcePatchedRecipe(f.Source, f.Build, f.Produces, series)
	}
	b, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func preparationForkEligible(f packload.Fork, runtime, platform string) bool {
	return !f.IsTree() && forkUpdateTiming(f) == timingNextLaunch && (run.BackgroundBuildsOn(runtime) || f.Patched() && run.BackgroundChecksOn(runtime, platform))
}

// registerForkPreparation records only selected, already-pinned program forks. It never resolves a
// branch: a timer can fill a missing artifact for this recorded commit but only an explicit update
// may move the standing pin.
func registerForkPreparation(pins []packload.ForkPin, req run.ForkBuildRequest, w io.Writer, color bool, logs ...*perf.Log) (result error) {
	if len(pins) == 0 {
		return nil
	}
	selected := make(map[string]packload.ForkPin, len(pins))
	ordered := make([]packload.ForkPin, 0, len(pins))
	for _, pin := range pins {
		if pin.Fork.IsTree() || pin.Fork.IsPackTree() {
			// A pack-tree program has no upstream for a next-launch preparation to follow: its
			// revision is the launch's staged tree, which only a launch reads.
			continue
		}
		if _, exists := selected[pin.Fork.Key()]; exists {
			continue
		}
		selected[pin.Fork.Key()] = pin
		ordered = append(ordered, pin)
	}
	if len(ordered) == 0 {
		return nil
	}
	if len(logs) > 0 && !config.InJail() && run.BackgroundChecksOn(req.Runtime, req.Platform) {
		for _, pin := range ordered {
			if (packdecl.Install{Platforms: pin.Fork.Platforms}).UnpublishedReason("linux", strings.TrimPrefix(req.Platform, "linux/")) == "" {
				report := &buildReport{perf: logs[0]}
				span := report.preparationSpan("preparation.registration")
				defer func() {
					report.preparationOutcome("preparation.registration", "selected programs", result)
					span.End()
				}()
				break
			}
		}
	}
	spawn := false
	err := withPreparationState(func(state *preparationState) error {
		for _, pin := range ordered {
			f := pin.Fork
			key := f.Key()
			idx := preparationRecordIndex(*state, key, req.Runtime, req.Platform)
			if idx < 0 {
				state.Subscriptions = append(state.Subscriptions, preparationRecord{Key: key, Runtime: req.Runtime,
					Platform: req.Platform, Kind: preparationForkKind(f), Phase: "inactive"})
				idx = len(state.Subscriptions) - 1
			}
			r := &state.Subscriptions[idx]
			r.Kind = preparationForkKind(f)
			if pin.Commit == "" && !f.Patched() {
				// No identity exists yet. Preserve the ordinary launch/readiness path; do not invent
				// a timer pin or make automatic-off cold readiness look established.
				r.Active, r.Phase = false, "unresolved"
				r.Reason = "the selected fork has no recorded pin — a fresh launch establishes its initial pin; `yolo pack update` moves it later"
				r.UpdatedAt = preparationNow()
				continue
			}
			var identity string
			var identityErr error
			if f.Patched() {
				identity, identityErr = preparationForkIdentity(f, "", req.Runtime, req.Platform)
			} else {
				identity, identityErr = preparationForkIdentity(f, pin.Commit, req.Runtime, req.Platform)
			}
			if identityErr != nil {
				r.Active, r.Phase = false, "invalid"
				r.Reason = fmt.Sprintf("could not read the selected fork declaration (%v) — repair the pack, then run `yolo pack prepare start`", identityErr)
				r.UpdatedAt = preparationNow()
				continue
			}
			if identity != r.InputIdentity || pin.Commit != r.PinCommit {
				r.Generation++
				r.InputIdentity, r.PinCommit = identity, pin.Commit
				r.Phase, r.ServingEntry, r.At, r.RetryAt = "pending", "", time.Time{}, time.Time{}
			}
			r.Active = preparationForkEligible(f, req.Runtime, req.Platform)
			if r.Active {
				r.Phase, r.Reason = "pending", ""
				spawn = true
			} else if forkUpdateTiming(f) != timingNextLaunch {
				r.Phase, r.Reason = "held", "the current `agent_updates` policy holds this fork at launch-time updates"
			} else {
				r.Phase, r.Reason = "unsupported", runtimeName(req.Runtime)+" does not support background program preparation — use a fresh launch to prepare it"
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
			fmt.Fprintf(w, "⚠ next-launch program preparation was recorded, but its host worker could not start (%v) — run `yolo pack prepare start` to retry\n", err)
			return err
		}
		pr := richtext.Printer{W: w, Color: color}
		for _, pin := range ordered {
			if preparationForkEligible(pin.Fork, req.Runtime, req.Platform) {
				pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: next-launch preparation is recorded; the admitted artifact is for later launches, not this running jail — `yolo pack prepare status %s` shows progress (log %s)",
					pin.Fork.Label(), pin.Fork.Key(), backgroundAdvanceLog())))
			}
		}
	}
	return nil
}

func currentForkPin(f packload.Fork) (packload.ForkPin, error) {
	lock, err := packsrc.LoadForkLock(forkLockPath())
	if err != nil {
		return packload.ForkPin{}, err
	}
	pins := packload.ForkPins([]packload.Fork{f}, lock)
	if len(pins) != 1 || pins[0].Commit == "" || pins[0].Reason != "" {
		if len(pins) == 1 && pins[0].Reason != "" {
			return pins[0], fmt.Errorf("%s", pins[0].Reason)
		}
		return packload.ForkPin{}, fmt.Errorf("the selected fork has no usable recorded pin")
	}
	return pins[0], nil
}

func runPlainForkPreparation(ctx context.Context, record preparationRecord, f packload.Fork, w io.Writer) (taskErr error) {
	withBackgroundKey(record.Key, f.Label(), w, func(outcome *backgroundOutcome) {
		outcome.ProducerPID, outcome.SubscriptionGeneration = os.Getpid(), record.Generation
		pin, err := currentForkPin(f)
		if err != nil || pin.Commit != record.PinCommit {
			outcome.State, outcome.Error = bgSkipped, "the standing pin or selected declaration changed before the timer build — a fresh launch re-registers it"
			taskErr = errPreparationAdmissionStale
			return
		}
		b := forkBuild{Fork: f, Commit: pin.Commit, Platform: record.Platform}
		store := &capture.Store{Dir: paths.CapturesDir()}
		if entry, _, resolveErr := resolveForkBuild(store, f.Bin, record.Platform, f.Source, pin.Commit, b.recipe()); resolveErr == nil {
			outcome.State, outcome.To = bgNothing, entry.Key
			return
		}

		var buildErr error
		var entry *capture.Entry
		pool := newBuildPool(w, progress.Config{}, false, "", record.Runtime, run.SlotChecks, buildSlots(record.Runtime), nil)
		pool.add(f.Label(), func(it *poolItem) {
			release, ok := it.build()
			if !ok {
				buildErr = context.Canceled
				return
			}
			defer release()
			buildCtx := it.context()
			var bound bool
			entry, buildErr = buildFork(b, buildMode{
				lock:    pidlock.Mode{Wait: true, Bound: forkBuildWaitBound, Cancel: buildCtx.Done()},
				runJail: childJail(buildCtx, false, &bound), runtime: record.Runtime,
				sourceCheckoutCtx: buildCtx, sourceCheckoutNoWait: true, admitCtx: buildCtx,
				admitGuard: preparationAdmissionGuardUnder(buildCtx, record.Key, record.Runtime, record.Platform, record.Generation),
			}, it.stream(), it.stream(), false)
		})
		pool.runUnder(ctx)
		switch {
		case errors.Is(buildErr, errPreparationAdmissionStale):
			outcome.State, outcome.Error = bgSkipped, "the selected declaration or standing pin changed before admission — a fresh launch re-registers it"
			taskErr = errPreparationAdmissionStale
		case ctx.Err() != nil || errors.Is(buildErr, context.Canceled):
			outcome.State = bgInterrupted
			taskErr = ctx.Err()
		case errors.Is(buildErr, packsrc.ErrLockHeld):
			outcome.State, outcome.Error = bgSkipped, "the source mirror is busy; the next preparation attempt retries without waiting for its lock"
			taskErr = nil
		case buildErr != nil:
			outcome.State, outcome.Error = bgFailed, buildErr.Error()
			outcome.RetryAt = time.Now().Add(packsrc.BranchRefreshInterval).Unix()
			taskErr = buildErr
		case entry == nil:
			outcome.State, outcome.Error = bgFailed, "the timer build returned no admitted artifact"
			taskErr = errors.New(outcome.Error)
		default:
			outcome.State, outcome.To = bgMoved, entry.Key
		}
	})
	return taskErr
}
