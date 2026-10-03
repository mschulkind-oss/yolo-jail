package runtime

// podmanready.go is the READINESS GATE of docs/design/podman-reboot-readiness.md: the step
// of runtime selection that waits, within one fixed budget, for `podman info --format json`
// to answer, and whose successful answer is the launch's PODMAN FACTS (both terms are coined
// in that doc). The launch (internal/cli/run) and `yolo check` (internal/cli/check) both call
// WaitForPodman; neither keeps a probe of its own (PR-D6).
//
// # Why it waits instead of killing
//
// The first `podman info` after a boot does podman's own post-boot refresh while holding the
// `alive.lck` lock every other podman client blocks on (libpod/runtime.go makeRuntime, podman
// v6.1.2). Killing that probe either discards an answer that was seconds away (rootless: the
// re-executed child survives the kill and finishes) or aborts the refresh every later client
// is waiting for (rootful). So an attempt is NEVER killed (PR-D2): its only bound is what is
// left of the budget, and at the end of the budget the gate stops WAITING and names the pid it
// left running.
//
// # What it retries, and what it refuses at once (OQ-PR1)
//
// An attempt that exits early is classified from its stderr (ClassifyPodmanFailure), and one
// that could not start from its errno (ClassifyStartError). One that cannot clear on its own
// — a missing podman, a permission or configuration error, a missing helper, an explicit
// "run podman system migrate" — refuses at once and names the fix. Everything else is
// retried after a backoff, including every error the table does not recognize: the rule is
// tri-state, so "I do not know what this is" keeps waiting within the budget and never
// refuses early.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// PodmanReadyBudget is the gate's one budget, for every launch and every `yolo check`
// (PR-D11, PR-D12; OQ-PR1 ruled it applies always and is never keyed on a reboot). A
// constant with no config key and no YOLO_* dial: a budget that is too short would be a yolo
// bug, and an escape hatch is only for broken user config.
const PodmanReadyBudget = 60 * time.Second

// podmanReadyBackoff is the wait before each retry of an early exit: 1 s, 2 s, then 4 s,
// capped at 4 s (PR-D4). No wait ever runs past the budget.
var podmanReadyBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// backoffFor is the wait before retry number n (0-based).
func backoffFor(n int) time.Duration {
	if n < len(podmanReadyBackoff) {
		return podmanReadyBackoff[n]
	}
	return podmanReadyBackoff[len(podmanReadyBackoff)-1]
}

// retryFloorCap bounds the RETRY FLOOR (PR-D22 of podman-reboot-readiness.md; the term is
// coined there): the gate starts a retry only when what is left of the budget after the
// backoff is longer than the floor, which is how long podman took to give the early answer
// being retried, capped at this. An attempt started with less than that left is one that
// would most likely be abandoned mid-answer, leaving a podman running for nothing and a
// refusal that shows "still running" instead of podman's error. The cap keeps a slow error
// (one that waited behind a lock, and whose next attempt may answer at once) from costing
// more than a few seconds of the budget.
const retryFloorCap = 5 * time.Second

// retryFloor is the floor for a retry of a, the early answer just given.
func retryFloor(a Attempt) time.Duration {
	if a.Duration > retryFloorCap {
		return retryFloorCap
	}
	return a.Duration
}

// PodmanInfoArgv is the gate's probe (PR-D5): the JSON answer is parsed into the launch's
// Podman facts, so no later reader on the launch path asks podman again.
func PodmanInfoArgv(rt string) []string { return []string{rt, "info", "--format", "json"} }

// Attempt is what one run of the probe did.
type Attempt struct {
	// StartErr is set when the probe could not be started at all. ClassifyStartError reads
	// it: a binary that is missing or not executable fails the gate at once, since nothing
	// will make it start, while an error that can clear (the binary busy being replaced, a
	// process or file limit hit during a storm) is retried within the budget like any early
	// exit. A failure of yolo's own scratch files is a *ProbeScratchError.
	StartErr error
	// Exited is true when the process ended before the gate stopped waiting; RC, Stdout and
	// Stderr are then its exit code and output.
	Exited bool
	RC     int
	Stdout string
	Stderr string
	// Pid is the process's id. When Exited is false the process is STILL RUNNING and yolo
	// left it to finish (it may be the refresh every later podman client is waiting for).
	Pid int
	// Interrupted is true when the gate stopped waiting because the user interrupted it.
	Interrupted bool
	// Duration is how long the gate waited on this attempt.
	Duration time.Duration
}

// AttemptRunner runs argv once and waits for it until it exits, deadline passes, or
// interrupt is closed. It never kills the process: an attempt that is still running when it
// returns is reported with Exited false and its Pid (RunPodmanAttempt is the real one).
type AttemptRunner func(argv []string, deadline time.Time, interrupt <-chan struct{}) Attempt

// ReadySeams are the gate's injectable parts. A nil field takes the real one.
type ReadySeams struct {
	// Attempt runs one probe. nil => RunPodmanAttempt.
	Attempt AttemptRunner
	// Now is the gate's clock. nil => time.Now.
	Now func() time.Time
	// Sleep waits d, returning false if interrupt closed first. nil => a real timer.
	Sleep func(d time.Duration, interrupt <-chan struct{}) bool
	// Interrupt, when closed, makes the gate stop waiting (a Ctrl-C at the terminal).
	Interrupt <-chan struct{}
}

func (s ReadySeams) filled() ReadySeams {
	if s.Attempt == nil {
		s.Attempt = RunPodmanAttempt
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Sleep == nil {
		s.Sleep = realSleep
	}
	return s
}

func realSleep(d time.Duration, interrupt <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-interrupt:
		return false
	}
}

// ReadyHooks let the caller report while the gate runs. Every field may be nil.
type ReadyHooks struct {
	// OnAttempt is called once per attempt, as soon as the gate stops waiting on it,
	// success or not — the caller's `runtime.ready.attempt` note (PR-D10).
	OnAttempt func(n int, a Attempt, f Failure)
	// OnRetry is called before each backoff, with the attempt that exited early and the
	// wait that follows — the caller's "…; retrying in 2s" line (PR-D9).
	OnRetry func(n int, a Attempt, f Failure, wait time.Duration)
}

// ReadyOutcome is how the gate ended.
type ReadyOutcome int

const (
	// PodmanReady: an attempt exited 0 with JSON that parses. Result.Info is that JSON.
	PodmanReady ReadyOutcome = iota
	// PodmanNotReady: the budget ran out — the last attempt is still running, or no retry
	// fits in what is left (its backoff and its retry floor).
	PodmanNotReady
	// PodmanRefused: an attempt failed with an error that cannot clear on its own
	// (Result.Failure names it and its fix).
	PodmanRefused
	// PodmanNotStarted: the probe could not be started, for a reason that cannot clear on
	// its own (Result.Failure names it and its fix).
	PodmanNotStarted
	// PodmanInterrupted: the user interrupted the wait.
	PodmanInterrupted
)

// ReadyResult is the gate's answer.
type ReadyResult struct {
	Outcome ReadyOutcome
	// Info is the successful attempt's stdout: the Podman facts, `podman info --format
	// json`. "" on every other outcome.
	Info string
	// Attempts is every attempt, in order.
	Attempts []Attempt
	// Elapsed is the whole wait, retries and backoffs included.
	Elapsed time.Duration
	// Failure classifies the last attempt that ended with an error: one that exited early,
	// or one that could not start ({} when none did).
	Failure Failure
	// Running is the pid of a probe yolo stopped waiting on and left running, 0 when none.
	Running int
	// Machine is true for the result of WaitForPodmanMachine, macOS's patient one-shot
	// (podmanmachine.go), which asks once and retries nothing; its refusal says so.
	Machine bool
}

// StillRunning reports whether the wait ended on a probe that had not answered: the budget
// ran out with the last attempt still running (Running names it). A budget spent on early
// exits is podman's answer, not this.
func (r ReadyResult) StillRunning() bool {
	last := r.Last()
	return r.Outcome == PodmanNotReady && len(r.Attempts) > 0 && last.StartErr == nil && !last.Exited
}

// Last is the last attempt, or the zero Attempt when there was none.
func (r ReadyResult) Last() Attempt {
	if len(r.Attempts) == 0 {
		return Attempt{}
	}
	return r.Attempts[len(r.Attempts)-1]
}

// WaitForPodman is the readiness gate. It runs argv until one attempt answers, an answer
// cannot clear on its own, the budget ends (no retry fits in what is left, or the last
// attempt is still running), or the user interrupts; see the file comment for the rules and
// podman-reboot-readiness.md for why each one is there.
//
// Elapsed time is the larger of the clock's reading and the sum of the backoffs the gate
// slept, so a clock that does not move (a test's frozen seam) still ends the loop.
func WaitForPodman(argv []string, budget time.Duration, seams ReadySeams, hooks ReadyHooks) ReadyResult {
	s := seams.filled()
	start := s.Now()
	var slept time.Duration
	elapsed := func() time.Duration {
		if e := s.Now().Sub(start); e > slept {
			return e
		}
		return slept
	}
	var res ReadyResult
	for n := 0; ; n++ {
		// The attempt may run for whatever is left of the budget, and no longer.
		deadline := s.Now().Add(budget - elapsed())
		a := s.Attempt(argv, deadline, s.Interrupt)
		res.Attempts = append(res.Attempts, a)
		f := classifyAttempt(a)
		if hooks.OnAttempt != nil {
			hooks.OnAttempt(n+1, a, f)
		}
		switch {
		case a.StartErr != nil && f.Class == FailurePermanent:
			// Nothing will make it start. A start error that can clear is retried below, as
			// an early exit is.
			res.Outcome, res.Failure = PodmanNotStarted, f
			res.Elapsed = elapsed()
			return res
		case a.Interrupted:
			res.Outcome, res.Running = PodmanInterrupted, a.Pid
			res.Elapsed = elapsed()
			return res
		case a.StartErr == nil && !a.Exited:
			// Still running at the end of the budget. Left to finish, and named.
			res.Outcome, res.Running = PodmanNotReady, a.Pid
			res.Elapsed = elapsed()
			return res
		case a.RC == 0 && isJSONObject(a.Stdout):
			res.Outcome, res.Info = PodmanReady, a.Stdout
			res.Elapsed = elapsed()
			return res
		}
		res.Failure = f
		if f.Class == FailurePermanent {
			res.Outcome = PodmanRefused
			res.Elapsed = elapsed()
			return res
		}
		// A retry must fit its backoff AND its floor: an attempt started with less left than
		// podman just took to answer would most likely be abandoned mid-answer (PR-D22).
		wait := backoffFor(n)
		if elapsed()+wait+retryFloor(a) >= budget {
			res.Outcome = PodmanNotReady
			res.Elapsed = elapsed()
			return res
		}
		if hooks.OnRetry != nil {
			hooks.OnRetry(n+1, a, f, wait)
		}
		if !s.Sleep(wait, s.Interrupt) {
			res.Outcome = PodmanInterrupted
			res.Elapsed = elapsed()
			return res
		}
		slept += wait
	}
}

// classifyAttempt is the gate's reading of one attempt: a start error by its errno, an exit by
// podman's stderr, an exit 0 whose output is not JSON as unknown, and {} for an answer or an
// attempt that has not ended.
func classifyAttempt(a Attempt) Failure {
	switch {
	case a.StartErr != nil:
		return ClassifyStartError(a.StartErr)
	case a.Exited && a.RC == 0 && isJSONObject(a.Stdout):
		return Failure{}
	case a.Exited && a.RC == 0:
		return Failure{Class: FailureUnknown, Line: "the output is not JSON"}
	case a.Exited:
		return ClassifyPodmanFailure(a.Stderr)
	}
	return Failure{}
}

// isJSONObject is the gate's success test beside exit 0: the output parses as one JSON
// object. Anything else is an early exit to retry, never facts to act on.
func isJSONObject(s string) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal([]byte(s), &m) == nil && m != nil
}

// FailureClass is what ClassifyPodmanFailure makes of one failed attempt.
type FailureClass int

const (
	// FailureUnknown: nothing in the table matched. Retried within the budget — an
	// unrecognized answer never refuses early (the tri-state rule).
	FailureUnknown FailureClass = iota
	// FailureTransient: recognized as able to clear on its own (podman busy, a lock held).
	// Retried within the budget.
	FailureTransient
	// FailurePermanent: recognized as unable to clear on its own. Refused at once, naming
	// Fix.
	FailurePermanent
)

// Failure is one classified failure.
type Failure struct {
	Class FailureClass
	// Line is the stderr line the class was read from (podman's own words), or the first
	// meaningful stderr line when nothing matched.
	Line string
	// Fix is the next step to name when Class is FailurePermanent, and for every failure of
	// yolo's own scratch files whatever its class: podman never ran then, so the step is the
	// temporary directory's, never podman's (scratchFix).
	Fix string
}

// Describe renders an attempt the way the gate's retry line and refusal print it:
// "exit 125: Error: …" — the exit code and podman's own reason.
func Describe(a Attempt, f Failure) string {
	switch {
	case a.StartErr != nil:
		return "could not run: " + a.StartErr.Error()
	case a.Interrupted:
		return fmt.Sprintf("interrupted after %.1fs", a.Duration.Seconds())
	case !a.Exited:
		return fmt.Sprintf("still running after %.1fs", a.Duration.Seconds())
	}
	s := fmt.Sprintf("exit %d", a.RC)
	if f.Line != "" {
		s += ": " + f.Line
	}
	return s
}

// podmanFailurePattern is one row of the classification table: a lower-cased fragment of a
// message podman prints, the class it means, and the fix to name.
type podmanFailurePattern struct {
	fragment string
	class    FailureClass
	fix      string
}

// podmanFailurePatterns is OQ-PR1's classification, read from podman v6.1.2's own error
// texts (each group's comment names the source file it comes from; the errno and timeout
// rows are the kernel's and Go's words, which podman wraps in its own). TRANSIENT ROWS WIN: a
// line that matches one is retried whatever else it says, because the cost of a wrong
// "transient" is at most the budget and the cost of a wrong "permanent" is a refused launch
// that would have come up.
//
// The fixes name the next step and no more: podman's own line is printed beside them.
var podmanFailurePatterns = []podmanFailurePattern{
	// --- Can clear on its own: retried. ---
	// libpod/sqlite_state.go opens SQLite with a busy timeout, then reports "database is
	// locked" — another podman holds the database.
	{"database is locked", FailureTransient, ""},
	// EAGAIN / EWOULDBLOCK / EBUSY / EINTR / ETXTBSY from any lock or file podman takes.
	{"resource temporarily unavailable", FailureTransient, ""},
	{"device or resource busy", FailureTransient, ""},
	{"interrupted system call", FailureTransient, ""},
	{"text file busy", FailureTransient, ""},
	{"try again", FailureTransient, ""},
	{"timed out", FailureTransient, ""},
	{"timeout", FailureTransient, ""},

	// --- Cannot clear on its own: refused at once. ---
	// pkg/rootless/rootless_linux.c reexec_in_user_namespace: the kernel (or an AppArmor
	// policy restricting unprivileged user namespaces) refused the user namespace.
	{"cannot clone:", FailurePermanent, "podman cannot create its user namespace: allow unprivileged " +
		"user namespaces for podman (on AppArmor hosts, the profile your distribution ships for it)"},
	{"user namespaces are not enabled", FailurePermanent, "enable user namespaces " +
		"(sysctl user.max_user_namespaces)"},
	// pkg/rootless/rootless_linux.go tryMappingTool: newuidmap/newgidmap failed, or is missing.
	{"cannot set up namespace using", FailurePermanent, "check that newuidmap and newgidmap are " +
		"setuid and that /etc/subuid and /etc/subgid list this user"},
	{"command required for rootless mode with multiple ids", FailurePermanent, "install newuidmap " +
		"and newgidmap (the uidmap or shadow-utils package)"},
	{"invalid configuration: the specified mapping", FailurePermanent, "fix /etc/subuid and /etc/subgid"},
	// libpod/runtime.go getDBState: BoltDB is gone in podman 6.
	{"the boltdb database backend was removed", FailurePermanent, "remove the database_backend " +
		"line from containers.conf"},
	{"a boltdb database exists but is no longer being used", FailurePermanent, "migrate it with " +
		"`podman system migrate --migrate-db` once no other podman is running, as podman's message says"},
	// pkg/domain/infra/abi/system_linux.go SetupRootless ("invalid internal status … Try
	// running podman system migrate"), and every other message that asks for it by name.
	{"system migrate", FailurePermanent, "run `podman system migrate`"},
	// libpod/sqlite_state.go validateDBAgainstConfig (define.ErrDBBadConfig).
	{"database configuration mismatch", FailurePermanent, "podman's database was created with " +
		"different storage settings; restore them, as podman's message says"},
	// go.podman.io/common/pkg/config and go.podman.io/storage/types: a configuration file
	// that does not parse or validate.
	{"parsing containers.conf", FailurePermanent, "fix containers.conf"},
	{"validating containers config", FailurePermanent, "fix containers.conf"},
	{"validating engine configs", FailurePermanent, "fix containers.conf"},
	{"validating network configs", FailurePermanent, "fix containers.conf"},
	{"unrecognized database backend", FailurePermanent, "fix database_backend in containers.conf"},
	{"runroot must be set", FailurePermanent, "fix storage.conf"},
	{"graphroot must be set", FailurePermanent, "fix storage.conf"},
	// libpod/runtime.go makeRuntime: a runtime component podman needs is not installed.
	{"no oci runtime has been configured", FailurePermanent, "install an OCI runtime (crun) or " +
		"configure one in containers.conf"},
	{"default oci runtime", FailurePermanent, "install the OCI runtime containers.conf names (crun), " +
		"or change it"},
	{"could not find a working conmon binary", FailurePermanent, "install conmon"},
	// Any permission error: the path podman names is not this user's to use.
	{"permission denied", FailurePermanent, "a permission error: fix the ownership of the path " +
		"podman names"},
	{"operation not permitted", FailurePermanent, "a permission error: podman may not do what it " +
		"names"},
	// A `podman` that does not know the probe's own flags is not a podman yolo can use.
	{"unknown flag", FailurePermanent, "install a current podman"},
	{"unknown shorthand flag", FailurePermanent, "install a current podman"},
}

// ClassifyPodmanFailure reads a failed attempt's stderr. Only podman's FATAL lines are read
// — "Error: …", and the bare lines its rootless C code prints before exiting — never its
// logrus lines (WARN[…], ERRO[…], time="…" level=…): a refresh logs "ERRO … permission
// denied" for one container it could not refresh and still succeeds, so a warning is not
// podman's answer.
func ClassifyPodmanFailure(stderr string) Failure {
	fatal := fatalLines(stderr)
	for _, class := range []FailureClass{FailureTransient, FailurePermanent} {
		for _, line := range fatal {
			low := strings.ToLower(line)
			for _, p := range podmanFailurePatterns {
				if p.class == class && strings.Contains(low, p.fragment) {
					return Failure{Class: class, Line: line, Fix: p.fix}
				}
			}
		}
	}
	f := Failure{Class: FailureUnknown}
	if len(fatal) > 0 {
		f.Line = fatal[len(fatal)-1]
	} else if first := firstStderrLine(stderr); first != "" {
		f.Line = first
	}
	return f
}

// ClassifyStartError reads an attempt that could not be started (Attempt.StartErr) by the
// errno under it, never its words, with ClassifyPodmanFailure's tri-state: only what cannot
// clear on its own refuses at once (OQ-PR1, PR-D23).
//
//   - PERMANENT: no binary at the path or on PATH (ENOENT, ENOTDIR, exec's not-found and
//     relative-path refusals), one this user may not execute (EACCES, EPERM), a file that is
//     not a program (ENOEXEC, EISDIR), and an empty argv, which is a yolo bug.
//   - RETRIED within the budget, like any early exit: everything else. ETXTBSY is a podman
//     binary being replaced (a package upgrade), EAGAIN a fork refused at a process limit,
//     ENOMEM, EMFILE and ENFILE the kind of limit a restore storm hits; each clears once the
//     moment passes, and an errno this list does not name keeps retrying, never refusing
//     early.
//
// A failure of yolo's own scratch files (*ProbeScratchError) says so, with its own fix
// (scratchFix): permanent when the temporary directory cannot be used at all (missing, not a
// directory, not writable, read-only), retried otherwise (a full disk may be freed, a
// descriptor limit may lift).
func ClassifyStartError(err error) Failure {
	line := err.Error()
	var scratch *ProbeScratchError
	if errors.As(err, &scratch) {
		class := FailureUnknown
		if unusableTempDir(err) {
			class = FailurePermanent
		}
		return Failure{Class: class, Line: line, Fix: scratchFix(err)}
	}
	switch {
	case errors.Is(err, errEmptyArgv):
		return Failure{Class: FailurePermanent, Line: line, Fix: "this is a yolo bug; please report it"}
	case errors.Is(err, exec.ErrDot):
		return Failure{Class: FailurePermanent, Line: line,
			Fix: "put podman's directory on PATH by its absolute path, not as a relative one"}
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return Failure{Class: FailurePermanent, Line: line, Fix: "install podman, or put it on PATH"}
	case errors.Is(err, fs.ErrPermission):
		return Failure{Class: FailurePermanent, Line: line,
			Fix: "a permission error: make the podman on PATH executable by this user"}
	case errors.Is(err, syscall.ENOEXEC), errors.Is(err, syscall.EISDIR):
		return Failure{Class: FailurePermanent, Line: line,
			Fix: "the podman on PATH is not a program this host can run: reinstall podman"}
	case errors.Is(err, syscall.ETXTBSY), errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EINTR),
		errors.Is(err, syscall.ENOMEM), errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return Failure{Class: FailureTransient, Line: line}
	}
	return Failure{Class: FailureUnknown, Line: line}
}

// unusableTempDir reports a scratch-file error that only a change to the temporary directory
// clears: it is missing, not a directory, not writable, or read-only.
func unusableTempDir(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.ENOTDIR)
}

// scratchFix is the next step for a scratch file yolo could not create, read from its errno.
// Podman never ran, so the step is never podman's: the temporary directory's, or for a
// descriptor limit, that limit's.
func scratchFix(err error) string {
	switch {
	case unusableTempDir(err):
		return "make the temporary directory usable: TMPDIR, or /tmp when TMPDIR is unset, must be a writable directory"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return "free space in the temporary directory (TMPDIR, or /tmp when TMPDIR is unset), " +
			"or point TMPDIR at a writable directory that has room"
	case errors.Is(err, syscall.EMFILE):
		return "yolo reached its limit on open files (`ulimit -Hn` in the shell that runs yolo shows it): " +
			"raise it, or report a yolo bug if it is already high"
	case errors.Is(err, syscall.ENFILE):
		return "the system's open-files table is full: close programs that hold many files open"
	}
	return "check the temporary directory (TMPDIR, or /tmp when TMPDIR is unset), " +
		"or point TMPDIR at another writable directory"
}

// fatalLines is stderr without podman's logrus lines and blanks.
func fatalLines(stderr string) []string {
	var out []string
	for _, raw := range strings.Split(stderr, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || isLogrusLine(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// isLogrusLine recognizes podman's two logrus renderings: the terminal one ("WARN[0000]
// msg") and the text one a pipe gets (`time="…" level=warning msg="…"`).
func isLogrusLine(line string) bool {
	if strings.HasPrefix(line, "time=\"") || strings.HasPrefix(line, "level=") {
		return true
	}
	for _, lvl := range []string{"TRAC[", "DEBU[", "INFO[", "WARN[", "ERRO[", "FATA[", "PANI["} {
		if strings.HasPrefix(line, lvl) {
			return true
		}
	}
	return false
}

// firstStderrLine is the first non-blank stderr line, for a failure nothing classified.
func firstStderrLine(stderr string) string {
	for _, raw := range strings.Split(stderr, "\n") {
		if line := strings.TrimSpace(raw); line != "" {
			return line
		}
	}
	return ""
}
