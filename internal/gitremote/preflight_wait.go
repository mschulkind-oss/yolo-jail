package gitremote

import (
	"context"
	"time"
)

// waitUnreapedGit observes exit without consuming the leader's PID/PGID.
// Cmd.Wait alone would reap first, allowing a simultaneous deadline to
// signal an unrelated process group after the PID is reused.
func waitUnreapedGit(ctx context.Context, pid int) error {
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		exited, err := gitExitedUnreaped(pid)
		if err != nil || exited {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
