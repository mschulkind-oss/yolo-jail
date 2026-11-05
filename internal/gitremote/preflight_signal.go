package gitremote

import (
	"errors"
	"io"
	"os/exec"
	"time"
)

// failedGitGroupSignal must not block on Wait when the OS refused termination.
// The leader still reserves its PGID during the bounded observation and report.
func failedGitGroupSignal(c *exec.Cmd, signalErr, outcomeErr, readErr error) error {
	pid := c.Process.Pid
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	result := &GitGroupTerminationError{PID: pid, SignalErr: signalErr, OutcomeErr: outcomeErr, ReadErr: readErr}
	// Capture identity while still owned, including the exited-but-unreaped case.
	result.BirthIdentity, result.ObservationErr = gitLeaderIdentity(pid)
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		exited, err := gitExitedUnreaped(pid)
		if err != nil {
			result.LastObservation = "leader exit could not be observed; handle released without a confirmed reap"
			result.ObservationErr = errors.Join(result.ObservationErr, err)
			result.ReapErr = c.Process.Release()
			return result
		}
		if exited {
			result.LastObservation = "leader exited and was reaped; group emptiness is unconfirmed"
			result.ReapErr = c.Wait()
			return result
		}
		if !time.Now().Before(deadline) {
			result.LastObservation = "leader was last observed live; handle released without a kill or reap and may later become an unreaped zombie"
			result.ReapErr = c.Process.Release()
			return result
		}
		time.Sleep(5 * time.Millisecond)
	}
}
