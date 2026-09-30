package check

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// podmanreadyfixture_test.go is how a test in this package says what podman answers at the
// readiness gate (podmanready.go). TestMain makes the real attempt runner refuse, so a test
// that reaches the gate without one of these fails loudly instead of running the machine's
// podman.

// refusingPodmanAttempt is TestMain's default attempt runner. Its start error wraps
// exec.ErrNotFound so the gate refuses it at once (runtime.ClassifyStartError): an error the
// gate retried would make the offending test wait out the real budget before failing.
func refusingPodmanAttempt([]string, time.Time, <-chan struct{}) runtime.Attempt {
	return runtime.Attempt{StartErr: fmt.Errorf("test guard: this test reached the podman "+
		"readiness gate without a fake; set Options.PodmanReadiness (answeringPodman): %w", exec.ErrNotFound)}
}

// gateRecorder counts the gate's attempts.
type gateRecorder struct {
	mu    sync.Mutex
	argvs [][]string
}

func (g *gateRecorder) record(argv []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.argvs = append(g.argvs, append([]string(nil), argv...))
}

func (g *gateRecorder) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.argvs)
}

// scriptedPodman makes the gate's attempts follow script (the last entry repeating), on a
// clock the attempts and sleeps advance, and returns the record of its runs.
func scriptedPodman(o *Options, script ...runtime.Attempt) *gateRecorder {
	rec := &gateRecorder{}
	now := time.Unix(1_000_000, 0)
	o.PodmanReadiness = runtime.ReadySeams{
		Attempt: func(argv []string, deadline time.Time, _ <-chan struct{}) runtime.Attempt {
			i := rec.count()
			rec.record(argv)
			if i >= len(script) {
				i = len(script) - 1
			}
			a := script[i]
			if !a.Exited && a.StartErr == nil && !a.Interrupted {
				a.Duration = deadline.Sub(now)
			}
			now = now.Add(a.Duration)
			return a
		},
		Now: func() time.Time { return now },
		Sleep: func(d time.Duration, _ <-chan struct{}) bool {
			now = now.Add(d)
			return true
		},
		Interrupt: make(chan struct{}),
	}
	return rec
}

// answeringPodman makes every attempt exit 0 with info.
func answeringPodman(o *Options, info string) *gateRecorder {
	return scriptedPodman(o, runtime.Attempt{Exited: true, RC: 0, Stdout: info, Pid: 1})
}

// podmanAnswers is scriptedPodman for a fixture that states podman's answer the way this
// package's Exec fakes always have: did not run (the binary would not start), timed out (the
// attempt is still running at the end of the budget), or an exit code with its output.
func podmanAnswers(o *Options, res ExecResult) *gateRecorder {
	switch {
	case !res.Ran:
		return scriptedPodman(o, runtime.Attempt{StartErr: &exec.Error{Name: "podman", Err: exec.ErrNotFound}})
	case res.Timeout:
		return scriptedPodman(o, runtime.Attempt{Pid: 4242})
	}
	return scriptedPodman(o, runtime.Attempt{Exited: true, RC: res.RC, Stdout: res.Stdout, Stderr: res.Stderr, Pid: 1})
}
