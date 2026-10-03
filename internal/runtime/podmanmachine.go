package runtime

// podmanmachine.go is macOS's podman probe, the PATIENT ONE-SHOT (a term coined here, and
// ledgered as PR-D24 of docs/design/podman-reboot-readiness.md): ONE `podman info --format
// json`, run by the readiness gate's own attempt runner, never killed, waited for up to the
// gate's budget (PodmanReadyBudget), and never retried. Its result is a ReadyResult, so the
// launch and `yolo check` render it with the gate's own view (podmanreadyview.go) and say the
// same thing (PR-D6).
//
// # Why one attempt (PR-D7, which this keeps)
//
// On macOS podman is a client of a VM, the Podman machine. A STOPPED machine answers at once:
// nothing listens on the machine's forwarded port, so the client's dial is refused, and podman
// prints "Cannot connect to Podman" and exits 125 (podman v5.8.4: pkg/bindings/connection.go
// wraps the dial error in a ConnectError, and cmd/podman/root.go prints that banner for one).
// Retrying that answer would charge every stopped-machine user the budget, so an early exit is
// final.
//
// # Why patient (PR-D24, which amends PR-D7)
//
// An attempt that has NOT answered has got past that dial: the machine is up or coming up, and
// busy. The probe used to kill it at 10 s and report the machine "installed but not started",
// naming `podman machine start`. Measured on the Intel macOS nightly of 2026-10-03 (run
// 37118791671, shard 10, a 2-CPU machine): a launch begun half a second after the suite killed
// its warm-up launch, whose jail-keeper had just started, was refused that way after 10.0 s, and
// the next tests on the same machine launched. Waiting costs a stopped machine nothing, because
// it never gets here, so the wait is the gate's one budget; a probe still running at the end of
// it is reported as what was seen (PodmanNotReady, its pid named), never as a machine that is
// not started.

import (
	"fmt"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// WaitForPodmanMachine runs the patient one-shot for argv: one attempt, given the whole budget.
// The result reads as the gate's does, with Machine set:
//
//   - PodmanReady: it exited 0 with a JSON object (Info).
//   - PodmanNotReady: it was still running when the budget ended; Running names it, left to
//     finish.
//   - PodmanRefused: it exited with an error. On macOS that is almost always a stopped machine.
//   - PodmanNotStarted: it could not be started, whatever the errno, since nothing is retried
//     for a start error to clear in.
//   - PodmanInterrupted: the user interrupted the wait.
//
// Elapsed is the larger of the clock's reading and the attempt's own Duration, so a clock that
// does not move (a test's frozen seam) still reports the wait.
func WaitForPodmanMachine(argv []string, budget time.Duration, seams ReadySeams, hooks ReadyHooks) ReadyResult {
	s := seams.filled()
	start := s.Now()
	a := s.Attempt(argv, start.Add(budget), s.Interrupt)
	f := classifyAttempt(a)
	if hooks.OnAttempt != nil {
		hooks.OnAttempt(1, a, f)
	}
	res := ReadyResult{Machine: true, Attempts: []Attempt{a}, Elapsed: s.Now().Sub(start)}
	if a.Duration > res.Elapsed {
		res.Elapsed = a.Duration
	}
	switch {
	case a.StartErr != nil:
		res.Outcome, res.Failure = PodmanNotStarted, f
	case a.Interrupted:
		res.Outcome, res.Running = PodmanInterrupted, a.Pid
	case !a.Exited:
		res.Outcome, res.Running = PodmanNotReady, a.Pid
	case a.RC == 0 && isJSONObject(a.Stdout):
		res.Outcome, res.Info = PodmanReady, a.Stdout
	default:
		res.Outcome, res.Failure = PodmanRefused, f
	}
	return res
}

// WaitForPodmanMachineShowing is WaitForPodmanShowing for the patient one-shot: the line's
// detail says how much of the budget has gone and what the wait is for, and note receives the
// attempt's `runtime.ready.attempt` detail (nil records nothing). The caller closes the line
// with the result's DoneText.
func WaitForPodmanMachineShowing(line *progress.Line, rt string, seams ReadySeams, note func(detail string)) ReadyResult {
	stop := showWait(line, PodmanReadyBudget, "waiting for the Podman machine to answer (it may be busy or still starting)")
	res := WaitForPodmanMachine(PodmanInfoArgv(rt), PodmanReadyBudget, seams, ReadyHooks{
		OnAttempt: func(n int, a Attempt, f Failure) {
			if note != nil {
				note(AttemptNote(n, a, f))
			}
		},
	})
	stop()
	line.Set("")
	return res
}

// PodmanMachineBusyHint is the next step after a patient one-shot podman did not answer: the
// machine is up or coming up, so starting it is not the fix, and looking at it is. again names
// what to do once it is up ("launch again", "run `yolo check` again"). A machine can also be
// hung, which waiting never clears (the macOS nightly has seen `podman machine start` run 14
// minutes and never return), so the hint ends in a restart for one that still does not answer,
// and says what a restart stops: every container in the machine, other jails included.
func PodmanMachineBusyHint(again string) string {
	return fmt.Sprintf("Run `podman machine list` to see the machine's state. If it is running or "+
		"starting, wait for it to settle and %s. If it still does not answer, restart it, which stops "+
		"every container in it: `podman machine stop`, then `podman machine start`. If it is stopped, "+
		"start it with `podman machine start`.", again)
}
