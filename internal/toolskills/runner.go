package toolskills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Defaults of a Runner whose fields are zero (MS-D2).
const (
	DefaultDeadline  = 15 * time.Second
	DefaultMaxStdout = 1 << 20 // 1 MiB; more is a failure, not a truncation
	maxStderr        = 64 << 10
)

// Outcome says how a listing ended.
type Outcome int

const (
	// OutcomeOK: mise exited 0 and printed a valid list. Skills and Warnings are set.
	OutcomeOK Outcome = iota
	// OutcomeUnsupported: this mise has no `skills` command (it is older than 2026.9.2).
	OutcomeUnsupported
	// OutcomeFailed: mise is absent, exited nonzero, printed malformed or oversized output, or
	// could not be started.
	OutcomeFailed
	// OutcomeTimedOut: the deadline passed; the process group was killed.
	OutcomeTimedOut
)

func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeUnsupported:
		return "unsupported"
	case OutcomeFailed:
		return "failed"
	case OutcomeTimedOut:
		return "timed out"
	}
	return fmt.Sprintf("Outcome(%d)", int(o))
}

// Result is what List learned. Reason is sanitized and fit to interpolate into a user line; it
// is empty for OutcomeOK.
type Result struct {
	Outcome  Outcome
	Skills   []Skill   // every entry mise listed, unfiltered (OutcomeOK only)
	Warnings []Warning // mise's missing-skill lines (set whenever stderr was read)
	Reason   string
}

// Runner runs `mise -C <dir> skills ls --json`. The zero value is ready to use.
type Runner struct {
	// Mise is the command to run; empty means "mise" found on PATH.
	Mise string
	// Env is the child's environment; nil inherits the caller's.
	Env []string
	// Deadline bounds the run; zero means DefaultDeadline.
	Deadline time.Duration
	// MaxStdout caps stdout in bytes; zero means DefaultMaxStdout. Exceeding it is a failure.
	MaxStdout int
	// After makes the deadline channel; nil means time.After. A test injects a channel it
	// closes on an event instead of waiting out a clock.
	After func(time.Duration) <-chan time.Time
}

// List runs the listing in dir. It leads its own process group and kills the whole group on
// the deadline, on ctx's cancellation and when stdout passes the cap, so no grandchild outlives
// it. It never returns an error: every failure is an Outcome.
func (r Runner) List(ctx context.Context, dir string) Result {
	mise := r.Mise
	if mise == "" {
		mise = "mise"
	}
	deadline := r.Deadline
	if deadline <= 0 {
		deadline = DefaultDeadline
	}
	max := r.MaxStdout
	if max <= 0 {
		max = DefaultMaxStdout
	}
	after := r.After
	if after == nil {
		after = time.After
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, mise, "-C", dir, "skills", "ls", "--json")
	cmd.Env = r.Env
	out := &capWriter{max: max, over: cancel}
	errBuf := &capWriter{max: maxStderr, silent: true}
	cmd.Stdout, cmd.Stderr = out, errBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// WaitDelay bounds a descendant that left the group and still holds the pipes.
	cmd.WaitDelay = time.Second

	if err := cmd.Start(); err != nil {
		return Result{Outcome: OutcomeFailed, Reason: sanitizeTo(fmt.Sprintf("could not start %s: %v", mise, err), 200)}
	}
	timedOut := make(chan struct{})
	done := make(chan struct{})
	timer := after(deadline)
	go func() {
		select {
		case <-timer:
			close(timedOut)
			cancel()
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)

	stderr := errBuf.String()
	res := Result{Warnings: ParseWarnings(stderr)}
	select {
	case <-timedOut:
		res.Outcome, res.Reason = OutcomeTimedOut, fmt.Sprintf("no answer within %s", deadline)
		return res
	default:
	}
	if out.exceeded() {
		res.Outcome, res.Reason = OutcomeFailed, fmt.Sprintf("output is larger than %d bytes", max)
		return res
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if lacksSkills(stderr) {
				res.Outcome, res.Reason = OutcomeUnsupported, "this mise has no skills command"
				return res
			}
			res.Outcome, res.Reason = OutcomeFailed, fmt.Sprintf("mise exited %d%s", ee.ExitCode(), firstLine(stderr))
			return res
		}
		res.Outcome, res.Reason = OutcomeFailed, sanitizeTo(err.Error(), 200)
		return res
	}
	skills, perr := ParseList(out.Bytes())
	if perr != nil {
		res.Outcome, res.Reason = OutcomeFailed, sanitizeTo(perr.Error(), 200)
		return res
	}
	res.Outcome, res.Skills = OutcomeOK, skills
	return res
}

// lacksSkills reports whether a nonzero exit's stderr says `skills` is not a command. mise
// before 2026.9.2 reads `skills` as a task name ("no tasks defined", measured on 2026.10.17
// with a made-up command), and a clap-style mise says "unrecognized subcommand". The test is a
// heuristic: a miss is reported as a plain failure, which keeps the previous snapshot too.
func lacksSkills(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, marker := range []string{
		"unrecognized subcommand", "unknown command", "no tasks defined",
		"task skills not found", "no task named skills",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// firstLine is stderr's first nonblank line, sanitized and prefixed for a Reason, or "".
func firstLine(stderr string) string {
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(ansiRE.ReplaceAllString(l, "")); l != "" {
			return ": " + sanitizeTo(l, 160)
		}
	}
	return ""
}

// capWriter keeps up to max bytes. Past it, the bytes are dropped; with over set, that cancels
// the run so the group is killed. silent writers drop the excess without cancelling.
type capWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	max    int
	over   func()
	silent bool
	full   bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); len(p) <= room {
		c.buf.Write(p)
		return len(p), nil
	} else if room > 0 {
		c.buf.Write(p[:room])
	}
	if !c.silent && !c.full {
		c.full = true
		if c.over != nil {
			c.over()
		}
	}
	return len(p), nil
}

func (c *capWriter) exceeded() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.full }
func (c *capWriter) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}
func (c *capWriter) String() string { c.mu.Lock(); defer c.mu.Unlock(); return c.buf.String() }
