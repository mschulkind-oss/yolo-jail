package packsrc

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

// ReplaySnapshot binds replay and recording to the check authority observed before replay starts.
// Its exported fields are immutable by convention; constructors and consumers deep-copy pointers.
type ReplaySnapshot struct {
	Owner    string
	Seq      int64
	Inputs   CheckInputs
	Series   string
	Failure  *PatchFailure
	ApplyErr *ApplyError
}

// ReplaySnapshot captures the current check authority without Git or writes.
func (r *CheckRecord) ReplaySnapshot(inputs CheckInputs, series string) (ReplaySnapshot, error) {
	if r == nil {
		return ReplaySnapshot{}, errors.New("cannot snapshot a nil check record")
	}
	if r.Owner == "" {
		return ReplaySnapshot{}, errors.New("cannot snapshot a check record with no owner")
	}
	if r.Check == nil {
		return ReplaySnapshot{}, errors.New("cannot snapshot a check record with no finished check")
	}
	if r.Read != inputs {
		return ReplaySnapshot{}, errors.New("check record inputs do not match the replay inputs")
	}
	if r.Seq != r.Check.Seq {
		return ReplaySnapshot{}, errors.New("check record sequence does not match its finished check")
	}
	if series == "" {
		return ReplaySnapshot{}, errors.New("cannot snapshot an empty series digest")
	}
	return ReplaySnapshot{
		Owner: r.Owner, Seq: r.Seq, Inputs: inputs, Series: series,
		Failure: clonePatchFailure(r.PatchFailure), ApplyErr: cloneApplyError(r.ApplyErr),
	}, nil
}

func clonePatchFailure(f *PatchFailure) *PatchFailure {
	if f == nil {
		return nil
	}
	copy := *f
	copy.Paths = append([]string(nil), f.Paths...)
	return &copy
}

func cloneLegacyAttempt(a *LegacyReplayAttempt) *LegacyReplayAttempt {
	if a == nil {
		return nil
	}
	copy := *a
	return &copy
}

func cloneApplyError(e *ApplyError) *ApplyError {
	if e == nil {
		return nil
	}
	copy := *e
	copy.Legacy = cloneLegacyAttempt(e.Legacy)
	return &copy
}

func cloneReplaySnapshot(s *ReplaySnapshot) *ReplaySnapshot {
	if s == nil {
		return nil
	}
	copy := *s
	copy.Failure = clonePatchFailure(s.Failure)
	copy.ApplyErr = cloneApplyError(s.ApplyErr)
	return &copy
}

func replaySnapshotsEqual(a, b ReplaySnapshot) bool {
	return a.Owner == b.Owner && a.Seq == b.Seq && a.Inputs == b.Inputs && a.Series == b.Series &&
		reflect.DeepEqual(a.Failure, b.Failure) && reflect.DeepEqual(a.ApplyErr, b.ApplyErr)
}

// ErrUnboundReplay means recording was requested for a walk that did not retain its original check snapshot.
var ErrUnboundReplay = errors.New("walk has no original check snapshot")

// ErrStaleReplay means the check or failure authority changed while a replay was running.
var ErrStaleReplay = errors.New("replay snapshot is stale")

// RecordReplayResult separates classified operation evidence from persistence success.
type RecordReplayResult struct {
	Failure  *PatchFailure
	Recorded bool
	Err      error
}

// LegacyReplayOptions bounds the local-only probe used to reclassify one opaque legacy ApplyError.
type LegacyReplayOptions struct {
	Timeout time.Duration
	Now     time.Time
}

// LegacyReplayResult reports classification independently from persistence diagnostics.
type LegacyReplayResult struct {
	Failure    *PatchFailure
	State      string
	Diagnostic string
	Attempted  bool
	Recorded   bool
	Err        error
}

func invalidSnapshot(s ReplaySnapshot) error {
	if s.Owner == "" || s.Series == "" || s.Seq <= 0 {
		return errors.New("replay snapshot has incomplete identity")
	}
	if s.Inputs.Repo == "" {
		return errors.New("replay snapshot has incomplete inputs")
	}
	return nil
}

func snapshotForRecord(owner string, series string, seq int64, inputs CheckInputs, walk WalkResult) (ReplaySnapshot, error) {
	if walk.snapshot == nil {
		return ReplaySnapshot{}, ErrUnboundReplay
	}
	bound := *cloneReplaySnapshot(walk.snapshot)
	if owner != bound.Owner || series != bound.Series || seq != bound.Seq || inputs != bound.Inputs {
		return ReplaySnapshot{}, fmt.Errorf("recording arguments do not match the walk's original snapshot: %w", ErrStaleReplay)
	}
	return bound, nil
}
