package testsupport

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TestUntilInterruptedRunsItsTrapOnEveryInterrupt: an interrupt sent to the stand-in's PID alone at
// any moment after it marks its start runs the trap. The 360 interrupts land in the first 3 ms
// after the mark, in steps of 0.1 ms, which is where the stand-ins this helper replaced dropped
// them; four workers share them, for speed. It is a race, so the test can only make a regression
// likely to show. Measured 2026-10-05 on a loaded Linux machine, with the replaced stand-ins' body
// put back: 9 runs in 10 failed under a bash 3.2.57 build, and 1 in 20 under bash 5.3. Measured
// 2026-10-30 on Linux: the helper's previous shape, one wait for a 30-second background loop,
// failed 3 runs in 3 under a stock bash 3.2.57 build, each on an interrupt sent at the mark
// (UntilInterrupted says why); with this shape, 3 runs each passed under that build, bash 5.3,
// dash 0.5.12 and busybox ash 1.37, and 1 under a bash 3.2.57 build whose gap before `wait` turns
// its interrupt on was widened by 1 ms, which failed the previous shape on its first run.
func TestUntilInterruptedRunsItsTrapOnEveryInterrupt(t *testing.T) {
	const workers, offsets, rounds = 4, 30, 3
	var wg sync.WaitGroup
	for w := range workers {
		dir := t.TempDir()
		wg.Go(func() {
			for n := range offsets * rounds {
				offset := time.Duration((n*workers+w)%offsets) * 100 * time.Microsecond
				if err := interruptAfterStart(dir, n, offset); err != nil {
					t.Errorf("interrupted %v after its start mark: %v", offset, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// interruptAfterStart runs stand-in n in dir, interrupts it offset after it marks its start, and
// says how the stand-in failed to act on the interrupt, if it did.
func interruptAfterStart(dir string, n int, offset time.Duration) error {
	started := filepath.Join(dir, fmt.Sprintf("%d-started", n))
	interrupted := filepath.Join(dir, fmt.Sprintf("%d-interrupted", n))
	cmd := exec.Command("sh", "-c", UntilInterrupted("touch "+shquote.Quote(interrupted), started))
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); !exists(started); {
		if time.Now().After(deadline) {
			return errors.New("it never marked its start")
		}
	}
	for at := time.Now(); time.Since(at) < offset; {
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("it was still running 2 seconds later (trap marker exists: %t)", exists(interrupted))
	}
	if code := cmd.ProcessState.ExitCode(); code != 130 || !exists(interrupted) {
		return fmt.Errorf("it ended %d, its trap marking %v", code, exists(interrupted))
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
