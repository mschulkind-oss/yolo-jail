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
var ownedGroupHasNoLiveMember = ownedGroupHasNoLiveMemberOS

// ownedGroupDrainGuard bounds how long runOwnedGroup waits, after the group
// signal, for the group to have no live member. It is a failure guard, not a
// budget: a SIGKILLed member normally exits within milliseconds, and the wait
// ends on that event. Only a member still live this long fails the call.
var ownedGroupDrainGuard = 10 * time.Second

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
		// darwin answers EPERM instead when no member could be signalled
		// because every one is a zombie (the unreaped leader included), so
		// EPERM counts as gone only once the group is listed with no live
		// member: a live member that refused the signal still fails closed.
		if ctx.Err() != nil || !(errors.Is(err, syscall.ESRCH) || (errors.Is(err, syscall.EPERM) && ownedGroupConfirmedEmpty(pid))) {
			return failedOwnedGroupSignal(cmd, err)
		}
	}
	// A member SIGKILLed above holds its copy of the output pipes until it has
	// actually exited, which on a loaded machine can take longer than
	// WaitDelay. Wait for that event before Wait relies on the pipes reaching
	// EOF, so WaitDelay bounds only a pipe held from outside the group.
	if err := awaitOwnedGroupDrained(pid); err != nil {
		return failedOwnedGroupSignal(cmd, err)
	}
	return cmd.Wait()
}

// awaitOwnedGroupDrained waits, with the leader still unreaped so pgid cannot
// be reused, until group pgid has no live member. It signals the group again
// on each look, so a member forked while the first signal was being delivered
// is stopped too. An error means a member was still live after
// ownedGroupDrainGuard, or the signal itself failed: the caller fails closed.
// A listing error confirms nothing either way, so it leaves Wait's WaitDelay
// as the backstop, as it was before this wait existed.
func awaitOwnedGroupDrained(pgid int) error {
	deadline := time.Now().Add(ownedGroupDrainGuard)
	for {
		empty, err := ownedGroupHasNoLiveMember(pgid)
		if err != nil || empty {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("a group member was still live %s after SIGKILL", ownedGroupDrainGuard)
		}
		// ESRCH, and darwin's EPERM for a group of zombies, leave nothing to
		// stop; the next listing tells a live member that refused it apart.
		if err := signalOwnedGroup(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ownedGroupConfirmedEmpty reports whether process group pgid was listed and
// held no live member. A listing error is not a confirmation.
func ownedGroupConfirmedEmpty(pgid int) bool {
	empty, err := ownedGroupHasNoLiveMember(pgid)
	return err == nil && empty
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
