package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func registerOrdinaryPreparations(ctx context.Context, vector []npmCaptureInput, req run.ProgramCaptureRequest, w io.Writer) error {
	if config.InJail() || !run.BackgroundBuildsOn(req.Runtime) {
		return nil
	}
	authorized := map[string]preparationDemand{}
	for _, in := range vector {
		if !entrypoint.PackPolicyAllows(config.AgentUpdatesWire(), in.Owner) {
			continue
		}
		d := preparationDemand{Kind: preparationOrdinaryProgramKind, Key: ordinaryProgramKey(in), Runtime: req.Runtime, Platform: req.Platform, Identity: ordinaryProgramIdentity(in, req.Fingerprint, req.Runtime, req.Platform)}
		_, _, release, err := resolveOrdinaryPreparation(ctx, d)
		release()
		if err == nil {
			authorized[d.Key] = d
		} else if !errors.Is(err, errSharedPreparationForegroundOnly) && !errors.Is(err, errPreparationAdmissionStale) {
			return err
		}
	}
	// A launch adds or refreshes only its own authorized keys. An existing subscription it does
	// not carry belongs to the USER selection, not to this launch's merged vector (another
	// workspace may differ), so it is re-resolved through the user resolver and deactivated only
	// when that resolver no longer authorizes it: removed from the user selection, or its owner's
	// agent_updates policy off.
	revoked := map[string]string{}
	existing, err := preparationSubscriptions()
	if err != nil {
		return err
	}
	for _, r := range existing {
		if r.Kind != preparationOrdinaryProgramKind || !r.Active || r.Runtime != req.Runtime || r.Platform != req.Platform {
			continue
		}
		if _, ok := authorized[r.Key]; ok {
			continue
		}
		_, _, release, err := resolveOrdinaryPreparation(ctx, preparationDemand{Kind: r.Kind, Key: r.Key, Runtime: r.Runtime, Platform: r.Platform, Background: true})
		release()
		var off *ordinaryAutomaticOff
		switch {
		case errors.As(err, &off):
			revoked[r.Key] = off.Error()
		case errors.Is(err, errSharedPreparationForegroundOnly) || errors.Is(err, errPreparationAdmissionStale):
			revoked[r.Key] = "no longer authorized by the user selection — use a fresh launch to register its owner"
		}
		// Any other outcome (authorized, lock contention, an unresolvable user config) is the
		// timer's to judge on its next refresh, never this launch's.
	}
	spawn := false
	err = withPreparationState(func(state *preparationState) error {
		for i := range state.Subscriptions {
			r := &state.Subscriptions[i]
			if r.Kind == preparationOrdinaryProgramKind && r.Runtime == req.Runtime && r.Platform == req.Platform && revoked[r.Key] != "" {
				if _, ok := authorized[r.Key]; !ok {
					r.Active = false
					r.Phase = "inactive"
					r.Reason = revoked[r.Key]
				}
			}
		}
		for key, d := range authorized {
			idx := preparationRecordIndex(*state, key, d.Runtime, d.Platform)
			if idx < 0 {
				state.Subscriptions = append(state.Subscriptions, preparationRecord{Key: key, Runtime: d.Runtime, Platform: d.Platform, Kind: preparationOrdinaryProgramKind})
				idx = len(state.Subscriptions) - 1
			}
			r := &state.Subscriptions[idx]
			if r.InputIdentity != d.Identity {
				r.Generation++
				r.InputIdentity = d.Identity
				r.Phase = "pending"
				r.At = time.Time{}
				r.RetryAt = time.Time{}
				r.ServingEntry = ""
			}
			// A revoked record carries a terminal label from its revocation; re-registered, it is
			// due work again (its admitted entry stays served) — as a reactivated tree is.
			if !r.Active || r.Phase == "inactive" || r.Phase == "unresolved" {
				r.Phase = "pending"
			}
			r.Active = true
			r.Reason = ""
			r.UpdatedAt = preparationNow()
			spawn = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if spawn {
		return ensurePreparationScheduler()
	}
	return nil
}

func refreshOrdinaryPreparationSubscriptions(ctx context.Context, records []preparationRecord) []preparationRecord {
	for _, old := range records {
		if old.Kind != preparationOrdinaryProgramKind || !old.Active {
			continue
		}
		d := preparationDemand{Kind: old.Kind, Key: old.Key, Runtime: old.Runtime, Platform: old.Platform, Background: true}
		in, fingerprint, release, err := resolveOrdinaryPreparation(ctx, d)
		release()
		_ = preparationUpdateRecord(old.Key, old.Runtime, old.Platform, func(r *preparationRecord) {
			if errors.Is(err, packsrc.ErrLockHeld) || ctx.Err() != nil {
				return
			}
			var off *ordinaryAutomaticOff
			if errors.As(err, &off) {
				r.Active = false
				r.Phase = "inactive"
				r.Reason = off.Error()
				return
			}
			if err != nil {
				r.Active = false
				r.Phase = "unresolved"
				r.Reason = fmt.Sprintf("%v — use a fresh launch to register current ordinary inputs", err)
				return
			}
			identity := ordinaryProgramIdentity(*in, fingerprint, old.Runtime, old.Platform)
			if identity != r.InputIdentity {
				r.Generation++
				r.InputIdentity = identity
				r.At = time.Time{}
				r.RetryAt = time.Time{}
				r.Phase = "pending"
			}
			r.Active = true
			r.Reason = ""
		})
	}
	updated, err := preparationSubscriptions()
	if err != nil {
		return records
	}
	return updated
}

func ordinaryPreparationDue(ctx context.Context, r preparationRecord) bool {
	d := preparationDemand{Kind: r.Kind, Key: r.Key, Runtime: r.Runtime, Platform: r.Platform, Identity: r.InputIdentity, Background: true, SubscriptionGeneration: r.Generation}
	in, _, release, err := resolveOrdinaryPreparation(ctx, d)
	release()
	if err != nil {
		return false
	}
	record, err := readNPMCaptureRecord(&capture.Store{Dir: paths.CapturesDir()}, *in)
	if err != nil {
		return false
	}
	if record.Resolution == nil {
		return true
	}
	// Fixed selectors are still checked by manual --update, never advanced to latest.
	_, selector := packdecl.SplitNpmSpec(in.Package)
	if packdecl.NpmSpecIsPinned(selector) {
		return false
	}
	if record.Failure != "" && !r.At.IsZero() && preparationNow().Sub(r.At) < time.Hour {
		return false
	}
	return record.CheckedAt.IsZero() || preparationNow().Sub(record.CheckedAt) >= time.Hour
}

func runOrdinaryScheduledPreparation(ctx context.Context, r preparationRecord, w io.Writer) (*npmCaptureRecord, error) {
	d := preparationDemand{Kind: r.Kind, Key: r.Key, Runtime: r.Runtime, Platform: r.Platform, Identity: r.InputIdentity, Background: true, Fresh: true, SubscriptionGeneration: r.Generation}
	in, _, release, err := resolveOrdinaryPreparation(ctx, d)
	release()
	if err != nil {
		return nil, err
	}
	d.AfterSeq, err = registerNPMCaptureRequest(ctx, &capture.Store{Dir: paths.CapturesDir()}, *in)
	if err != nil {
		return nil, err
	}
	result, err := waitSharedDemandResult(ctx, d, true, func(pid int) {
		fmt.Fprintf(w, "%s: timer joined ordinary shared producer (%s)\n", in.Bin, sharedProducerPhrase(pid))
	})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if failure := result.ordinaryFloorFailure(*in); failure != nil {
		return nil, failure
	}
	if result.UnsupportedNPM {
		return nil, fmt.Errorf("%w: %s", errNPMCaptureUnsupported, result.OperationError)
	}
	if result.Failed || result.Program == nil {
		return nil, fmt.Errorf("ordinary timer preparation failed: %s — retry a fresh launch", result.OperationError)
	}
	return result.Program, nil
}
