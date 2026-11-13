package entrypoint

// packbootchecks.go runs the boot_check hook: a script a pack delivers, run once at boot, whose
// output is printed as a warning (docs/reference/mcp-configuration.md, OQ-MP9, ruled 2026-10-23:
// "if the pack discovers at boot for whatever it's trying to wrap … that it's not there, then it
// should print it … it is just a warning").
//
// CORE KNOWS NOTHING ABOUT WHAT A CHECK LOOKS FOR. The chrome-devtools pack's check finds a
// browser by the same search its wrapper runs at every server start, so the warning and the
// server can never disagree; core runs `/bin/sh <home>/<from> <flags...>` and prints what it
// says, with no switch on any pack's name.
//
// A CHECK NEVER STOPS A LAUNCH. What it looks for can arrive after boot (a nix install, a browser
// the user adds), and the pack's program picks it up then. So the step is a `run`, not a
// generator: a check's exit status chooses nothing but whether a silent failure gets a line, a
// check that does not finish within bootCheckTimeout is stopped and named, and a script the home
// does not hold is named. Each of those lines ends with the command that shows the check's own
// answer.
//
// A check that is satisfied prints nothing and so does this: a healthy launch gets no line, and
// the boot log records that each check ran.
//
// A CHECK RUNS IN A CONTAINER JAIL ONLY. There the whole boot is inside the jail, so a check has
// the jail's confinement and nothing more. A macos-user bootstrap is not reliably inside the
// agent's Seatbelt profile (the account mode's runs unconfined, MMP-D9), and its rule is that it
// runs only yolo's own code (P4, docs/reference/macos-user-provisioning.md); a check is a pack's
// script in an agent-writable home, so there it is skipped and the boot log says so. The pack's
// program says the same thing at its own start, which runs confined.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// bootCheckTimeout bounds one check. A var so a test can reach the timeout without waiting for
// it; five seconds is far beyond a lookup on disk, which is what a check is for, and short
// enough that a hung one costs a launch little.
var bootCheckTimeout = 5 * time.Second

// bootCheckMaxLines is how many lines of one check's output reach the terminal. A check is asked
// for one line; this bounds one that says more, and the rest is named with the command that
// shows it.
const bootCheckMaxLines = 4

// bootCheckMaxBytes caps what one check's output may hold in memory; past it the bytes are
// dropped, which bootCheckMaxLines would have hidden anyway.
const bootCheckMaxBytes = 64 << 10

// RunPackBootChecks runs each selected pack's boot_check hooks in pack order and prints what
// each says. It never fails the boot. On a macos-user boot it runs none and notes each skip to
// the boot log (the file comment says why).
func RunPackBootChecks(e *Env, target bootTarget, packs []*packload.Pack) {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, h := range p.Decl.HookContributions() {
			if h.Name != HookBootCheck {
				continue
			}
			if target != bootContainer {
				e.note(fmt.Sprintf("boot check %s (~/%s): skipped on macos-user, whose bootstrap "+
					"holds writes the agent's sandbox denies (it runs unsandboxed, or with the "+
					"home-content delivery lift) and so runs no pack script (P4)",
					p.Name, filepath.ToSlash(h.File)))
				continue
			}
			runPackBootCheck(e, p.Name, h)
		}
	}
}

// cappedBuffer keeps the first bootCheckMaxBytes written to it and drops the rest. Stdout and
// stderr write it from two goroutines, so it locks.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := bootCheckMaxBytes - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func runPackBootCheck(e *Env, pack string, h packdecl.Hook) {
	script := filepath.Join(e.Home, filepath.FromSlash(h.File))
	// What a user types in the jail to see the check's own answer: the home-relative spelling,
	// since that is the path an agent's shell knows it by.
	again := "/bin/sh ~/" + shquote.Quote(filepath.ToSlash(h.File))
	if len(h.Args) > 0 {
		again += " " + shquote.Join(h.Args)
	}
	prefix := "  ⚠ " + pack + ": "

	if st, err := os.Stat(script); err != nil || st.IsDir() {
		e.warn(fmt.Sprintf("%sits boot check ~/%s is not in this home, so it did not run; the "+
			"pack's files contribution delivers it — run `yolo check` on the host to see "+
			"what the pack declares", prefix, filepath.ToSlash(h.File)))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), bootCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{script}, h.Args...)...)
	cmd.Dir = e.Home
	env := envWith(os.Environ(), "HOME", e.Home)
	env = envWith(env, "PATH", agentPath(e))
	cmd.Env = envWith(env, "YOLO_BYPASS_SHIMS", "1")
	var out cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// The check leads its own process group and the timeout kills the group, so no grandchild
	// it started outlives it; WaitDelay bounds one that left the group and holds the pipe.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		e.warn(fmt.Sprintf("%sits boot check did not finish within %s and was stopped; the "+
			"launch goes on — run `%s` in the jail to see what it reports",
			prefix, bootCheckTimeout, again))
		return
	}
	lines := nonEmptyLines(out.String())
	for i, l := range lines {
		if i == bootCheckMaxLines {
			e.warn(fmt.Sprintf("%s(%d more lines — run `%s` to see them all)", prefix,
				len(lines)-bootCheckMaxLines, again))
			break
		}
		e.warn(prefix + l)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		e.note(fmt.Sprintf("boot check %s (%s): ran, %d line(s)", pack, again, len(lines)))
	case errors.As(err, &ee):
		if len(lines) == 0 {
			e.warn(fmt.Sprintf("%sits boot check exited %d without saying why; the launch goes "+
				"on — run `%s` in the jail to see it", prefix, ee.ExitCode(), again))
		} else {
			e.note(fmt.Sprintf("boot check %s (%s): exited %d", pack, again, ee.ExitCode()))
		}
	default:
		e.warn(fmt.Sprintf("%sits boot check could not run (%v); the launch goes on — run "+
			"`%s` in the jail to see why", prefix, err, again))
	}
}

// nonEmptyLines splits s into its lines, dropping blank ones and trailing whitespace.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}
