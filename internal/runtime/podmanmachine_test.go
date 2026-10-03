package runtime

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stoppedMachine is podman 5.8.4's answer for a Podman machine that is not running: the dial is
// refused, and cmd/podman/root.go prints the ConnectError banner above podman's own error.
func stoppedMachine() Attempt {
	return Attempt{Exited: true, RC: 125, Duration: 300 * time.Millisecond, Stderr: "Cannot connect to Podman. " +
		"Please verify your connection to the Linux system using `podman system connection list`, or try " +
		"`podman machine init` and `podman machine start` to manage a new Linux VM\n" +
		"Error: unable to connect to Podman socket: failed to connect: dial tcp 127.0.0.1:50655: connect: " +
		"connection refused\n"}
}

// The patient one-shot asks once, giving that one attempt the whole budget as its deadline,
// and an answer is the result.
func TestThePatientOneShotGivesItsOneAttemptTheWholeBudget(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	var deadlines []time.Duration
	seams := fakeSeams(c, func(argv []string, deadline time.Time, _ <-chan struct{}) Attempt {
		deadlines = append(deadlines, deadline.Sub(c.t))
		c.t = c.t.Add(15 * time.Second)
		return Attempt{Exited: true, RC: 0, Stdout: readyInfo, Duration: 15 * time.Second}
	})
	res := WaitForPodmanMachine(PodmanInfoArgv("podman"), PodmanReadyBudget, seams, ReadyHooks{})
	if res.Outcome != PodmanReady || res.Info != readyInfo || !res.Machine {
		t.Fatalf("outcome=%v info=%q machine=%v; want the answer", res.Outcome, res.Info, res.Machine)
	}
	if len(deadlines) != 1 || deadlines[0] != PodmanReadyBudget {
		t.Errorf("deadlines = %v; want one attempt given the whole %s", deadlines, PodmanReadyBudget)
	}
	if res.Elapsed != 15*time.Second {
		t.Errorf("elapsed = %s, want 15s", res.Elapsed)
	}
}

// An early exit is final: a stopped machine is asked once, never slept on, and refused with
// podman's own reason; and a probe that could not start is not retried either, even for an
// errno the Linux gate retries.
func TestThePatientOneShotRetriesNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		a       Attempt
		outcome ReadyOutcome
		want    string
	}{
		{"a stopped machine", stoppedMachine(), PodmanRefused,
			"podman info failed: exit 125: Error: unable to connect to Podman socket: failed to connect: dial tcp " +
				"127.0.0.1:50655: connect: connection refused."},
		{"a busy binary", Attempt{StartErr: &os.PathError{Op: "fork/exec", Path: "/opt/podman/bin/podman",
			Err: syscall.ETXTBSY}}, PodmanNotStarted, "podman info could not run: fork/exec /opt/podman/bin/podman: text file busy."},
		{"a recognized permanent error", Attempt{Exited: true, RC: 125, Stderr: "Error: open /x: permission denied\n"},
			PodmanRefused, "podman info failed: exit 125: Error: open /x: permission denied.\nFix: a permission error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeGateClock{t: time.Unix(1000, 0)}
			calls, sleeps := 0, 0
			seams := fakeSeams(c, scriptedAttempts(c, []Attempt{tc.a, answer()}, &calls))
			seams.Sleep = func(time.Duration, <-chan struct{}) bool { sleeps++; return true }
			res := WaitForPodmanMachine(PodmanInfoArgv("podman"), PodmanReadyBudget, seams, ReadyHooks{
				OnRetry: func(int, Attempt, Failure, time.Duration) { t.Error("the patient one-shot announced a retry") },
			})
			if res.Outcome != tc.outcome || calls != 1 || sleeps != 0 {
				t.Fatalf("outcome=%v attempts=%d sleeps=%d; want %v after one attempt and no wait",
					res.Outcome, calls, sleeps, tc.outcome)
			}
			got := res.Refusal("podman")
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("refusal = %q, want it to start %q", got, tc.want)
			}
			if strings.Contains(got, "does not clear on its own") {
				t.Errorf("a one-shot answer was called permanent: %q", got)
			}
		})
	}
}

// A probe still running when the budget ends is what was seen, said as that: podman did not
// answer in the budget, the machine may be busy or still starting, and the probe left running
// is named. Never "timed out", never "not started".
func TestThePatientOneShotSaysWhatItSawWhenNoAnswerCame(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	res := WaitForPodmanMachine(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{{Pid: 4242}}, &calls)), ReadyHooks{})
	if res.Outcome != PodmanNotReady || res.Running != 4242 || calls != 1 {
		t.Fatalf("outcome=%v running=%d attempts=%d", res.Outcome, res.Running, calls)
	}
	want := "podman info did not answer within 60s (1 attempt, 60.0s); the Podman machine may be busy or still starting.\n" +
		"podman (pid 4242) is still running; yolo left it to finish."
	if got := res.Refusal("podman"); got != want {
		t.Errorf("refusal =\n%s\nwant\n%s", got, want)
	}
	if res.DoneText() != "failed" {
		t.Errorf("done text = %q", res.DoneText())
	}
}

// An interrupted wait is the interrupt, and a frozen clock still reports how long the attempt
// was waited on.
func TestThePatientOneShotReportsAnInterruptAndAFrozenClock(t *testing.T) {
	frozen := time.Unix(1000, 0)
	seams := ReadySeams{
		Attempt: func([]string, time.Time, <-chan struct{}) Attempt {
			return Attempt{Interrupted: true, Pid: 7, Duration: 3 * time.Second}
		},
		Now: func() time.Time { return frozen },
	}
	res := WaitForPodmanMachine(PodmanInfoArgv("podman"), PodmanReadyBudget, seams, ReadyHooks{})
	if res.Outcome != PodmanInterrupted || res.Running != 7 || res.Elapsed != 3*time.Second {
		t.Fatalf("outcome=%v running=%d elapsed=%s", res.Outcome, res.Running, res.Elapsed)
	}
	if got := res.Refusal("podman"); !strings.HasPrefix(got, "Interrupted while waiting for podman info to answer (1 attempt, 3.0s).") {
		t.Errorf("refusal = %q", got)
	}
}

// The showing wrapper runs the patient one-shot, not the gate: a stopped machine is asked once
// under a line, and each attempt is noted.
func TestThePatientOneShotShowingAsksOnceAndNotesTheAttempt(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	var notes []string
	res := WaitForPodmanMachineShowing(nil, "podman",
		fakeSeams(c, scriptedAttempts(c, []Attempt{stoppedMachine()}, &calls)),
		func(d string) { notes = append(notes, d) })
	if res.Outcome != PodmanRefused || calls != 1 || len(notes) != 1 || !strings.HasPrefix(notes[0], "n=1 ") {
		t.Errorf("outcome=%v attempts=%d notes=%q", res.Outcome, calls, notes)
	}
}

// The next step after no answer looks at the machine and waits; it names a restart for a
// machine that never settles (a hung VM, which waiting never clears, so a hint that ended at
// "wait" sent its user round the same refusal for good), saying what a restart stops, and
// starting only for the case the list shows stopped.
func TestPodmanMachineBusyHintNamesTheListAndTheWait(t *testing.T) {
	got := PodmanMachineBusyHint("launch again")
	for _, want := range []string{
		"`podman machine list`",
		"wait for it to settle and launch again.",
		"If it still does not answer, restart it, which stops every container in it: " +
			"`podman machine stop`, then `podman machine start`.",
		"If it is stopped, start it with `podman machine start`.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hint lacks %q: %q", want, got)
		}
	}
}

// StillRunning is a wait that ended on a probe with no answer, and only that: not a budget
// spent on early exits, not a start error retried to the end, not an answer.
func TestStillRunningIsAWaitThatEndedOnAProbeWithNoAnswer(t *testing.T) {
	txtbsy := Attempt{StartErr: &os.PathError{Op: "fork/exec", Path: "/usr/bin/podman", Err: syscall.ETXTBSY},
		Duration: time.Second}
	for _, tc := range []struct {
		name   string
		script []Attempt
		want   bool
	}{
		{"still running at the budget", []Attempt{{Pid: 9}}, true},
		{"early exits to the end of the budget", []Attempt{busy()}, false},
		{"a start error retried to the end", []Attempt{txtbsy}, false},
		{"an answer", []Attempt{answer()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeGateClock{t: time.Unix(1000, 0)}
			calls := 0
			res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
				fakeSeams(c, scriptedAttempts(c, tc.script, &calls)), ReadyHooks{})
			if got := res.StillRunning(); got != tc.want {
				t.Errorf("StillRunning() = %v, want %v (outcome %v after %d attempts)", got, tc.want, res.Outcome, calls)
			}
		})
	}
}
