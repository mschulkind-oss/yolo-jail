package gitremote

import (
	"errors"
	"fmt"
)

// GitGroupTerminationError reports a refused group kill without exposing Git's
// captured output, remote URL or config. The PID and birth identity are observations,
// not authority to signal a process after the unreaped-leader window ends.
type GitGroupTerminationError struct {
	PID             int
	BirthIdentity   string
	LastObservation string
	SignalErr       error
	OutcomeErr      error
	ReadErr         error
	ObservationErr  error
	ReapErr         error
}

func (e *GitGroupTerminationError) Error() string {
	identity := "birth identity unavailable"
	if e.BirthIdentity != "" {
		identity = "birth identity " + e.BirthIdentity
	}
	return fmt.Sprintf("Git preflight could not terminate process group PGID %d (last known leader PID %d, %s): group signal failed (%v); %s; descendants may remain even when the leader exited; inspect this PID/PGID and birth identity on the host before retrying (do not signal by PGID alone after the leader is reaped)", e.PID, e.PID, identity, e.SignalErr, e.LastObservation)
}

func (e *GitGroupTerminationError) Unwrap() error {
	return errors.Join(e.SignalErr, e.OutcomeErr, e.ReadErr, e.ObservationErr, e.ReapErr)
}

// Only failed termination crosses the generic refusal boundary: ordinary Git
// exit statuses, ref grammar and malformed config keep their existing advice.
func preflightGitFailure(err error, reason string, refuse func(string) (PushPreflight, error)) error {
	var termination *GitGroupTerminationError
	if errors.As(err, &termination) {
		return fmt.Errorf("git push preflight: %w", err)
	}
	_, generic := refuse(reason)
	return generic
}
