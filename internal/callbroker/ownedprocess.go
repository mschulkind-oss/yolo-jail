package callbroker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

var startOwnedCommand = (*exec.Cmd).Start
var observeOwnedLeader = ownedLeaderExited
var signalOwnedGroup = syscall.Kill

// runOwnedGroup keeps the leader unreaped while it observes exit and terminates
// its process group. Cmd.Wait would release the PID/PGID before a successful
// wrapper's background children were stopped, allowing a reused PGID to be hit.
func runOwnedGroup(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	if err := startOwnedCommand(cmd); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			break
		}
		exited, err := observeOwnedLeader(pid)
		if err != nil {
			// Start owns the unreaped leader even when exit observation fails.
			// Stop its group before Wait can release the PGID.
			if signalErr := signalOwnedGroup(-pid, syscall.SIGKILL); signalErr != nil {
				return failedOwnedGroupSignal(cmd, signalErr)
			}
			waitErr := cmd.Wait()
			return fmt.Errorf("cannot observe owned call broker leader PID %d: %w (wait: %v); check the host process before retrying", pid, err, waitErr)
		}
		if exited {
			break
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	// The unreaped leader reserves pid as a PGID, including after normal exit.
	if err := signalOwnedGroup(-pid, syscall.SIGKILL); err != nil {
		// ESRCH after a known exited leader means there was no group left.
		if !errors.Is(err, syscall.ESRCH) || ctx.Err() != nil {
			return failedOwnedGroupSignal(cmd, err)
		}
	}
	return cmd.Wait()
}

func failedOwnedGroupSignal(cmd *exec.Cmd, signalErr error) error {
	pid := cmd.Process.Pid
	identity, identityErr := ownedLeaderIdentity(pid)
	deadline := time.Now().Add(100 * time.Millisecond)
	observation := "leader last observed live; process handle released without confirmed termination"
	for {
		exited, err := observeOwnedLeader(pid)
		if err != nil {
			observation = "leader exit could not be observed; process handle released"
			_ = cmd.Process.Release()
			break
		}
		if exited {
			observation = "leader exited and was reaped; descendants may remain"
			_ = cmd.Wait()
			break
		}
		if !time.Now().Before(deadline) {
			_ = cmd.Process.Release()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if identityErr != nil {
		identity = "unavailable"
	}
	return fmt.Errorf("call broker could not terminate owned process group PGID %d (leader birth %s): group signal failed (%v); %s; inspect this PID and birth identity on the host before retrying (do not signal by PGID alone after reap)", pid, identity, signalErr, observation)
}
