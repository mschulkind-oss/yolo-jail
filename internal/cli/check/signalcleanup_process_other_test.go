package check

import (
	"os/exec"
	"time"
)

type signalCleanupCmdWait struct {
	done chan struct{}
	err  error
}

func startSignalCleanupCmdWait(cmd *exec.Cmd) *signalCleanupCmdWait {
	waited := &signalCleanupCmdWait{done: make(chan struct{})}
	go func() {
		waited.err = cmd.Wait()
		close(waited.done)
	}()
	return waited
}

func (w *signalCleanupCmdWait) wait(guard time.Duration) (error, bool) {
	timer := time.NewTimer(guard)
	defer timer.Stop()
	select {
	case <-w.done:
		return w.err, true
	case <-timer.C:
		return nil, false
	}
}

func (w *signalCleanupCmdWait) isDone() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

type signalCleanupTeardown struct {
	waitErr     error
	childJoined bool
	nixStarted  bool
	nixExited   bool
}

func (r signalCleanupTeardown) safeToReadOutput() bool {
	return r.childJoined && (!r.nixStarted || r.nixExited)
}
