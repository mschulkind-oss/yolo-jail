package runtime

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeGateClock is a clock the fake attempts and the fake sleep both advance, so a test can
// place every attempt at an exact second of the budget.
type fakeGateClock struct{ t time.Time }

func (c *fakeGateClock) now() time.Time { return c.t }

// scriptedAttempts runs one scripted attempt per call and advances the clock by each one's
// duration. Past the end of the script it repeats the last entry.
func scriptedAttempts(c *fakeGateClock, script []Attempt, calls *int) AttemptRunner {
	return func(argv []string, deadline time.Time, interrupt <-chan struct{}) Attempt {
		i := *calls
		*calls++
		if i >= len(script) {
			i = len(script) - 1
		}
		a := script[i]
		if !a.Exited && a.StartErr == nil && !a.Interrupted {
			// A scripted "still running" attempt waits until the deadline.
			a.Duration = deadline.Sub(c.t)
		}
		c.t = c.t.Add(a.Duration)
		return a
	}
}

func fakeSeams(c *fakeGateClock, run AttemptRunner) ReadySeams {
	return ReadySeams{
		Attempt: run,
		Now:     c.now,
		Sleep: func(d time.Duration, _ <-chan struct{}) bool {
			c.t = c.t.Add(d)
			return true
		},
	}
}

const readyInfo = `{"host":{"security":{"rootless":true}},"version":{"Version":"6.1.2"}}`

func busy() Attempt {
	return Attempt{Exited: true, RC: 125, Stderr: "Error: acquiring runtime init lock: resource temporarily unavailable\n"}
}

func answer() Attempt { return Attempt{Exited: true, RC: 0, Stdout: readyInfo} }

// A warm podman: one attempt, no wait, the JSON is the answer.
func TestTheGateTakesTheFirstAnswer(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{answer()}, &calls)), ReadyHooks{})
	if res.Outcome != PodmanReady || res.Info != readyInfo || calls != 1 || res.Elapsed != 0 {
		t.Fatalf("outcome=%v info=%q calls=%d elapsed=%s", res.Outcome, res.Info, calls, res.Elapsed)
	}
}

// Early exits are retried after 1, 2, then 4 s, capped at 4 s, and the answer that finally
// comes is taken — including one on the very last attempt the budget allows.
func TestTheGateRetriesAnEarlyExitUntilTheLastAttemptTheBudgetAllows(t *testing.T) {
	// Instant failures start at 0, 1, 3, 7, 11, …, 55, 59: the attempt at 59 s is the last
	// one whose backoff fits inside 60 s.
	var starts []time.Duration
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	origin := c.t
	calls := 0
	var retries []time.Duration
	run := func(argv []string, deadline time.Time, interrupt <-chan struct{}) Attempt {
		starts = append(starts, c.t.Sub(origin))
		calls++
		if c.t.Sub(origin) == 59*time.Second {
			return answer()
		}
		return busy()
	}
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget, fakeSeams(c, run),
		ReadyHooks{OnRetry: func(_ int, _ Attempt, _ Failure, wait time.Duration) {
			retries = append(retries, wait)
		}})
	if res.Outcome != PodmanReady {
		t.Fatalf("outcome = %v after starts %v, want ready on the attempt at 59s", res.Outcome, starts)
	}
	if got := starts[len(starts)-1]; got != 59*time.Second {
		t.Errorf("last attempt started at %s, want 59s (starts %v)", got, starts)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, w := range want {
		if retries[i] != w {
			t.Errorf("backoff %d = %s, want %s (all: %v)", i, retries[i], w, retries)
		}
	}
	// One more failure at 59 s would have ended it: no backoff fits.
	c2 := &fakeGateClock{t: time.Unix(1000, 0)}
	calls2 := 0
	res2 := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c2, scriptedAttempts(c2, []Attempt{busy()}, &calls2)), ReadyHooks{})
	if res2.Outcome != PodmanNotReady || calls2 != len(starts) {
		t.Errorf("always-busy: outcome=%v after %d attempts, want not-ready after %d", res2.Outcome, calls2, len(starts))
	}
	if res2.Failure.Class != FailureTransient || !strings.Contains(res2.Failure.Line, "resource temporarily unavailable") {
		t.Errorf("the last failure was not kept: %+v", res2.Failure)
	}
}

// An attempt still running at the end of the budget is left running and named, never killed
// (the runner is the only thing that could kill it, and the gate never asks it to).
func TestTheGateLeavesAnAttemptThatOutlivesTheBudgetRunning(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	var deadline time.Time
	run := func(argv []string, d time.Time, interrupt <-chan struct{}) Attempt {
		calls++
		deadline = d
		c.t = d
		return Attempt{Pid: 4242, Duration: 60 * time.Second}
	}
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget, fakeSeams(c, run), ReadyHooks{})
	if res.Outcome != PodmanNotReady || res.Running != 4242 || calls != 1 {
		t.Fatalf("outcome=%v running=%d calls=%d", res.Outcome, res.Running, calls)
	}
	if want := time.Unix(1060, 0); !deadline.Equal(want) {
		t.Errorf("the attempt's deadline was %s, want the whole budget (%s)", deadline, want)
	}
}

// A later attempt gets only what is left of the budget.
func TestAnAttemptsDeadlineIsWhatIsLeftOfTheBudget(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	var deadlines []time.Time
	calls := 0
	run := func(argv []string, d time.Time, interrupt <-chan struct{}) Attempt {
		deadlines = append(deadlines, d)
		calls++
		if calls == 1 {
			c.t = c.t.Add(10 * time.Second)
			return busy()
		}
		c.t = d
		return Attempt{Pid: 7}
	}
	WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget, fakeSeams(c, run), ReadyHooks{})
	for i, d := range deadlines {
		if want := time.Unix(1060, 0); !d.Equal(want) {
			t.Errorf("attempt %d deadline = %s, want %s", i+1, d, want)
		}
	}
}

// A permanent error refuses at once: one attempt, no backoff, the fix named.
func TestAPermanentErrorRefusesAtOnce(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	retried := false
	a := Attempt{Exited: true, RC: 125, Stderr: "time=\"2026-09-29T14:33:50Z\" level=warning msg=\"x\"\n" +
		"Error: fatal error, invalid internal status, unable to create a new pause process: " +
		"cannot re-exec process. Try running \"podman system migrate\" and if that doesn't work reboot to recover\n"}
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{a}, &calls)),
		ReadyHooks{OnRetry: func(int, Attempt, Failure, time.Duration) { retried = true }})
	if res.Outcome != PodmanRefused || calls != 1 || retried || res.Elapsed != 0 {
		t.Fatalf("outcome=%v calls=%d retried=%v elapsed=%s", res.Outcome, calls, retried, res.Elapsed)
	}
	if res.Failure.Fix != "run `podman system migrate`" || !strings.HasPrefix(res.Failure.Line, "Error: fatal error") {
		t.Errorf("failure = %+v", res.Failure)
	}
}

// An error the table does not know keeps being retried until the budget: it never refuses
// early (the tri-state rule).
func TestAnUnrecognizedErrorIsRetriedWithinTheBudget(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	a := Attempt{Exited: true, RC: 125, Stderr: "Error: something podman has never said before\n"}
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{a}, &calls)), ReadyHooks{})
	if res.Outcome != PodmanNotReady || calls < 10 {
		t.Fatalf("outcome=%v after %d attempts; an unknown error must be retried to the budget", res.Outcome, calls)
	}
	if res.Failure.Class != FailureUnknown || res.Failure.Line != "Error: something podman has never said before" {
		t.Errorf("failure = %+v", res.Failure)
	}
}

// Exit 0 with output that is not JSON is an early exit, retried, never facts.
func TestExitZeroWithoutJSONIsRetried(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{{Exited: true, RC: 0, Stdout: "host: {}"}, answer()}, &calls)),
		ReadyHooks{})
	if res.Outcome != PodmanReady || calls != 2 {
		t.Fatalf("outcome=%v calls=%d", res.Outcome, calls)
	}
}

// A binary that cannot start fails at once.
func TestAMissingBinaryFailsAtOnce(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{{StartErr: errors.New("exec: \"podman\": executable file not found in $PATH")}}, &calls)),
		ReadyHooks{})
	if res.Outcome != PodmanNotStarted || calls != 1 {
		t.Fatalf("outcome=%v calls=%d", res.Outcome, calls)
	}
}

// An interrupt during a backoff ends the wait at once.
func TestAnInterruptDuringTheBackoffEndsTheWait(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	s := fakeSeams(c, scriptedAttempts(c, []Attempt{busy()}, &calls))
	s.Sleep = func(time.Duration, <-chan struct{}) bool { return false }
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget, s, ReadyHooks{})
	if res.Outcome != PodmanInterrupted || calls != 1 {
		t.Fatalf("outcome=%v calls=%d", res.Outcome, calls)
	}
}

// A clock that never moves (a frozen test seam) cannot make the gate loop forever: the
// backoffs it slept count as elapsed time.
func TestAFrozenClockStillEndsTheGate(t *testing.T) {
	calls := 0
	frozen := time.Unix(0, 0)
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget, ReadySeams{
		Attempt: func([]string, time.Time, <-chan struct{}) Attempt { calls++; return busy() },
		Now:     func() time.Time { return frozen },
		Sleep:   func(time.Duration, <-chan struct{}) bool { return true },
	}, ReadyHooks{})
	if res.Outcome != PodmanNotReady || calls > 20 {
		t.Fatalf("outcome=%v after %d attempts", res.Outcome, calls)
	}
}

// OQ-PR1's classification, row by row, against podman's own texts.
func TestClassifyPodmanFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		class  FailureClass
		fix    string
	}{
		{"sqlite busy", "Error: database is locked", FailureTransient, ""},
		{"lock EAGAIN", "Error: acquiring lock 3 for container x: resource temporarily unavailable", FailureTransient, ""},
		{"userns refused", "cannot clone: Operation not permitted\nError: cannot re-exec process", FailurePermanent, "podman cannot create its user namespace"},
		{"userns disabled", "user namespaces are not enabled in /proc/sys/user/max_user_namespaces\nError: cannot re-exec process", FailurePermanent, "user.max_user_namespaces"},
		{"newuidmap", `Error: cannot set up namespace using "/usr/bin/newuidmap": should have setuid or have filecaps setuid: exit status 1`, FailurePermanent, "newuidmap"},
		{"no newuidmap", `Error: command required for rootless mode with multiple IDs: exec: "newuidmap": executable file not found in $PATH`, FailurePermanent, "install newuidmap"},
		{"boltdb gone", "Error: the BoltDB database backend was removed in Podman 6.0 - please comment out the `database_backend` line in containers.conf", FailurePermanent, "database_backend"},
		{"boltdb left", "Error: a BoltDB database exists but is no longer being used. BoltDB support was removed", FailurePermanent, "--migrate-db"},
		{"migrate", `Error: invalid internal status, try resetting the pause process with "podman system migrate": could not find any running process: no such process`, FailurePermanent, "run `podman system migrate`"},
		{"db mismatch", `Error: database static dir "/a" does not match our static dir "/b": database configuration mismatch`, FailurePermanent, "storage settings"},
		{"bad conf", "Error: parsing containers.conf: decode configuration /etc/containers/containers.conf: toml: line 3", FailurePermanent, "fix containers.conf"},
		{"no runtime", `Error: default OCI runtime "crun" not found: invalid argument`, FailurePermanent, "crun"},
		{"no conmon", "Error: could not find a working conmon binary (configured options: []: invalid argument)", FailurePermanent, "install conmon"},
		{"permission", "Error: creating runtime static files directory \"/x\": mkdir /x: permission denied", FailurePermanent, "permission"},
		{"unknown", "Error: the moon is in the wrong phase", FailureUnknown, ""},
		{"no output", "", FailureUnknown, ""},
		// A refresh's ERRO line about one container is not podman's answer: the fatal line is.
		{"logrus ignored", "ERRO[0003] Refreshing container abc: permission denied\nError: database is locked", FailureTransient, ""},
		{"logrus only", "time=\"2026-09-29T14:34:00Z\" level=error msg=\"Refreshing volume v: permission denied\"", FailureUnknown, ""},
		// Transient wins over permanent: a wrong "permanent" refuses a launch that would come up.
		{"transient wins", "Error: open /x: permission denied\nError: resource temporarily unavailable", FailureTransient, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := ClassifyPodmanFailure(tc.stderr)
			if f.Class != tc.class {
				t.Fatalf("class = %v, want %v (%+v)", f.Class, tc.class, f)
			}
			if tc.fix != "" && !strings.Contains(f.Fix, tc.fix) {
				t.Errorf("fix = %q, want it to mention %q", f.Fix, tc.fix)
			}
			if tc.class == FailurePermanent && f.Line == "" {
				t.Error("a permanent failure must carry podman's own line")
			}
		})
	}
}

// deadlineAttempts runs every attempt as fail: it takes fail.Duration to exit, unless the
// deadline comes first, in which case it is still running there (pid 110), exactly as the real
// runner reports an attempt it stopped waiting on. It records when each attempt started.
func deadlineAttempts(c *fakeGateClock, fail Attempt, starts *[]time.Duration) AttemptRunner {
	origin := c.t
	return func(argv []string, deadline time.Time, interrupt <-chan struct{}) Attempt {
		*starts = append(*starts, c.t.Sub(origin))
		if end := c.t.Add(fail.Duration); end.After(deadline) {
			a := Attempt{Pid: 110, Duration: deadline.Sub(c.t)}
			c.t = deadline
			return a
		}
		c.t = c.t.Add(fail.Duration)
		return fail
	}
}

// THE RETRY FLOOR (PR-D22): a podman that fails the same unrecognized way every time, taking
// 3 s to say so. The attempt ending at 54 s is followed by a 4 s backoff (58 s), which leaves
// 2 s: less than podman takes to answer, so a tenth attempt would only be abandoned
// mid-answer. The gate stops instead, every attempt it started got to answer, and the refusal
// carries podman's own error rather than a pid it left running.
func TestTheGateStartsNoRetryItWouldHaveToAbandon(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	fail := Attempt{Exited: true, RC: 125, Duration: 3 * time.Second, Pid: 7,
		Stderr: "Error: unable to connect to Podman socket: dial unix /run/user/1000/podman/podman.sock: connect: connection refused\n"}
	var starts []time.Duration
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, deadlineAttempts(c, fail, &starts)), ReadyHooks{})
	if res.Outcome != PodmanNotReady || res.Running != 0 {
		t.Fatalf("outcome=%v running=%d after attempts starting at %v: the gate started an attempt "+
			"it could only abandon", res.Outcome, res.Running, starts)
	}
	for i, a := range res.Attempts {
		if !a.Exited {
			t.Errorf("attempt %d (started at %s) did not get to answer: %+v", i+1, starts[i], a)
		}
	}
	if last := starts[len(starts)-1]; last != 51*time.Second || len(starts) != 9 {
		t.Errorf("attempts started at %v, want nine, the last at 51s", starts)
	}
	refusal := res.Refusal("podman")
	if !strings.Contains(refusal, "the last attempt: exit 125: Error: unable to connect to Podman socket") ||
		strings.Contains(refusal, "still running") {
		t.Errorf("the refusal does not carry podman's error, or names a podman it left running:\n%s", refusal)
	}

	// The floor is capped: an error that took 20 s (one that waited behind a lock) still
	// leaves a retry that has more than the cap left to run.
	c2 := &fakeGateClock{t: time.Unix(1000, 0)}
	slow := fail
	slow.Duration = 20 * time.Second
	var starts2 []time.Duration
	WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget, fakeSeams(c2, deadlineAttempts(c2, slow, &starts2)), ReadyHooks{})
	if len(starts2) != 3 || starts2[2] != 43*time.Second {
		t.Errorf("a slow error's retries started at %v, want 0s, 21s and 43s: the floor is capped at %s",
			starts2, retryFloorCap)
	}
}

// When the last attempt was still running at the end of the budget, the refusal still gives
// the last error podman DID give, beside the pid it left running.
func TestARefusalBehindARunningAttemptCarriesTheLastErrorPodmanGave(t *testing.T) {
	c := &fakeGateClock{t: time.Unix(1000, 0)}
	calls := 0
	failed := Attempt{Exited: true, RC: 125, Duration: time.Second,
		Stderr: "Error: something podman has never said before\n"}
	res := WaitForPodman(PodmanInfoArgv("podman"), PodmanReadyBudget,
		fakeSeams(c, scriptedAttempts(c, []Attempt{failed, {Pid: 4242}}, &calls)), ReadyHooks{})
	if res.Outcome != PodmanNotReady || res.Running != 4242 || calls != 2 {
		t.Fatalf("outcome=%v running=%d calls=%d", res.Outcome, res.Running, calls)
	}
	refusal := res.Refusal("podman")
	for _, want := range []string{
		"the last error it gave: exit 125: Error: something podman has never said before",
		"podman (pid 4242) is still running; yolo left it to finish.",
	} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal lacks %q:\n%s", want, refusal)
		}
	}
}
