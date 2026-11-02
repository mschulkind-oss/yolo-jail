// Package brokerhook runs the user's on_request (boundary-broker.md §6.6, BB-D75): a
// command named in the user config, run once for each request a jail files, with the
// request-filed event's JSON on stdin and its fields in the environment, so a one-line
// notifier can be the whole approval front end. It is a REQUEST hook: nothing else fires
// it. A tool that wants every event reads `yolo approve --watch --json`.
//
// WHO RUNS IT. Each host broker daemon runs one Runner for its own broker lifetime: it
// watches the store it already holds and fires only for requests carrying its own start
// id. So every request has exactly one runner however many jails are up, and the hook runs
// whenever a request can exist, since a request is only ever filed by a live broker.
//
// IT ONLY NOTIFIES. The runner reads nothing back from the hook. A decision is the user's
// command running `yolo approve` itself (the maintainer's ruling: a button click does exactly
// what the CLI does, because it IS the CLI), so it gets the CLI's own checks, refusals and
// audit. A private directory holding exactly one entry, a link named `yolo` to the running
// yolo, leads the hook's PATH, so a plain `yolo approve` works wherever the broker was started
// (the maintainer's ruling, Q43); YOLO_BIN names the same binary absolutely.
//
// ITS FAILURES ARE VISIBLE (Q42). A hook that exits non-zero, cannot start, or runs into a
// Timeout set shorter than its request's life has its exit and the last of its output
// recorded on its request in the host store, which every listing of the request shows
// (`yolo approve`, `--json`, `--watch --json`); a `yolo approve` the hook ran that refused
// is part of that output. The broker's log gets one line naming
// the request and the exit, never the output: that log is shared by every jail.
//
// WHEN IT IS STOPPED. A hook runs until its request leaves `pending`, whichever way (decided
// with `yolo approve`, the hook's own call included, expired, or refused because its jail
// stopped), until the request's expiry, or until its broker stops. Waiting on a click until
// then is a notifier doing its job, so none of these is a failure, whichever the runner sees
// first: the store's expiry event or its own clock reaching the request's expiry. It is then
// stopped with its whole process group: SIGINT first, on which a notify-send waiting for a
// click closes its own notification (and a trap in a script can close another notifier's),
// then SIGKILL after StopGrace, so a notification waiting on a click does not outlive the
// question it asks.
package brokerhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// Runner runs one broker lifetime's hook.
type Runner struct {
	Store   *brokerstore.Store
	Start   string // the broker lifetime whose requests fire the hook
	Command string // run as `sh -c Command`
	// Bin is the absolute path of the yolo the hook calls to decide, handed over as
	// YOLO_BIN and linked as `yolo` in a private directory leading its PATH; empty hands
	// over neither.
	Bin string
	// Dir is the hook's working directory; empty means the user's home, never the
	// broker's own store directory, which is where the daemon itself runs.
	Dir string
	// Interval is how often the store is polled, and how long a failed first read waits
	// before it is tried again; zero means one second.
	Interval time.Duration
	// Timeout, when set shorter than the time its request has left, is a hard bound on one
	// run, and a hook that runs into it is recorded as failed: its request still waits, and
	// may never have been shown. Zero, the broker's setting, bounds a run by its request's
	// expiry alone, so a hook that waits on a notification can be answered until then and is
	// not failed for waiting.
	Timeout time.Duration
	// Log takes one line per hook that fails or is stopped. It names no workspace, repository or
	// command, and carries none of the hook's output, because a broker's log is shared by
	// every jail on the machine; the output goes on the request's record instead.
	Log io.Writer

	wg       sync.WaitGroup
	mu       sync.Mutex
	running  map[string]context.CancelCauseFunc // by request id
	lastWarn string
	// pathDir is the private directory holding the `yolo` link, once made (linkDir).
	pathDir string
}

var (
	errLeftPending = errors.New("its request is no longer pending (decided, expired, or refused because its jail stopped)")
	errExpired     = errors.New("its request expired")
	errTimedOut    = errors.New("it ran into its timeout")
)

// Run fires the hook for each request this lifetime files until ctx ends, then waits for
// the hooks still running, which ctx's end also stops. A store it cannot read yet is tried
// again every interval, so a passing fault at the broker's start does not switch the hook
// off for the broker's lifetime.
func (r *Runner) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = time.Second
	}
	opened := func(snap brokerstore.Snapshot) { r.opened(ctx, snap) }
	event := func(ev brokerstore.Event) { r.event(ctx, ev) }
	for {
		err := r.Store.Watch(ctx, interval, opened, event, r.warn)
		if err == nil || ctx.Err() != nil {
			break
		}
		r.warn(err)
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
		if ctx.Err() != nil {
			break
		}
	}
	r.wg.Wait()
	r.removeLinkDir()
}

// linkDirName is the directory, inside the store's own directory, that holds this lifetime's
// `yolo` link. The lifetime id makes it one per broker, as several brokers share a store.
func (r *Runner) linkDirName() string { return "on-request-path-" + r.Start }

// linkDir makes, once, the private directory holding exactly one entry, a link named `yolo`
// to Bin, and returns it; "" when there is no Bin or it cannot be made, and the hook then
// has YOLO_BIN alone. It is made through an os.Root on the store's directory, so no link
// already in its place is followed.
func (r *Runner) linkDir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pathDir != "" || r.Bin == "" || r.Store == nil || r.Store.Dir == "" {
		return r.pathDir
	}
	if err := os.MkdirAll(r.Store.Dir, 0o700); err != nil {
		r.logf("could not make the directory for plain `yolo` (%v); hooks get YOLO_BIN alone", err)
		return ""
	}
	root, err := os.OpenRoot(r.Store.Dir)
	if err != nil {
		r.logf("could not make the directory for plain `yolo` (%v); hooks get YOLO_BIN alone", err)
		return ""
	}
	defer root.Close()
	name := r.linkDirName()
	_ = root.RemoveAll(name) // a broker killed outright left its own; the id is this lifetime's
	if err := root.Mkdir(name, 0o700); err != nil {
		r.logf("could not make the directory for plain `yolo` (%v); hooks get YOLO_BIN alone", err)
		return ""
	}
	if err := root.Symlink(r.Bin, filepath.Join(name, "yolo")); err != nil {
		_ = root.RemoveAll(name)
		r.logf("could not link plain `yolo` (%v); hooks get YOLO_BIN alone", err)
		return ""
	}
	r.pathDir = filepath.Join(r.Store.Dir, name)
	return r.pathDir
}

// removeLinkDir removes the `yolo` link's directory once every hook has stopped.
func (r *Runner) removeLinkDir() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pathDir == "" {
		return
	}
	if root, err := os.OpenRoot(r.Store.Dir); err == nil {
		_ = root.RemoveAll(r.linkDirName())
		root.Close()
	}
	r.pathDir = ""
}

// hookEnv is base with dir first on PATH, then the request's fields.
func hookEnv(base []string, dir string, fields []string) []string {
	env := make([]string, 0, len(base)+len(fields)+1)
	path := ""
	for _, kv := range base {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
			continue
		}
		env = append(env, kv)
	}
	if dir != "" {
		if path == "" {
			path = dir
		} else {
			path = dir + string(os.PathListSeparator) + path
		}
	}
	if path != "" {
		env = append(env, "PATH="+path)
	}
	return append(env, fields...)
}

// opened fires for this lifetime's requests already pending when the watch opens: those
// filed while the store could not be read.
func (r *Runner) opened(ctx context.Context, snap brokerstore.Snapshot) {
	r.clearWarn()
	for _, q := range snap.Requests {
		if q.Start == r.Start && q.State == "pending" {
			r.launch(ctx, q)
		}
	}
}

func (r *Runner) event(ctx context.Context, ev brokerstore.Event) {
	r.clearWarn()
	q := ev.Request
	if q == nil || q.Start != r.Start {
		return
	}
	switch ev.Kind {
	case brokerstore.EventRequestFiled:
		// Filed and decided between two polls: there is no question left to ask.
		if ev.Current != "" && ev.Current != "pending" {
			return
		}
		r.launch(ctx, *q)
	case brokerstore.EventRequestDecided, brokerstore.EventRequestExpired:
		r.mu.Lock()
		stop := r.running[q.ID]
		r.mu.Unlock()
		if stop != nil {
			stop(errLeftPending)
		}
	}
}

// warn logs a failed read once, not once per tick, until a read succeeds again.
func (r *Runner) warn(err error) {
	line := fmt.Sprintf("reading the store failed (%v); retrying", err)
	r.mu.Lock()
	repeat := line == r.lastWarn
	r.lastWarn = line
	r.mu.Unlock()
	if !repeat {
		r.logf("%s", line)
	}
}

func (r *Runner) clearWarn() {
	r.mu.Lock()
	r.lastWarn = ""
	r.mu.Unlock()
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, "on_request: "+format+"\n", args...)
	}
}

// launch starts the hook for q, once per request; parent ending stops it.
func (r *Runner) launch(parent context.Context, q brokerstore.Request) {
	limit, cause := r.limit(q)
	ctx, cancel := context.WithCancelCause(parent)
	r.mu.Lock()
	if r.running == nil {
		r.running = map[string]context.CancelCauseFunc{}
	}
	if r.running[q.ID] != nil {
		r.mu.Unlock()
		cancel(nil)
		return
	}
	r.running[q.ID] = cancel
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.running, q.ID)
			r.mu.Unlock()
			cancel(nil)
		}()
		timer := time.AfterFunc(limit, func() { cancel(cause) })
		defer timer.Stop()
		r.fire(ctx, q)
	}()
}

// Env is the request's fields as the hook's environment: what a notifier that takes its
// text as arguments, such as notify-send, needs, and YOLO_BIN, the yolo the hook calls to
// decide. Every request value is safe to show on a terminal.
func Env(s *brokerstore.Store, q brokerstore.Request, bin string) []string {
	env := []string{"YOLO_EVENT=" + brokerstore.EventRequestFiled, "YOLO_SOURCE=" + s.SourceName()}
	if bin != "" {
		env = append(env, "YOLO_BIN="+bin)
	}
	add := func(k, v string) { env = append(env, k+"="+termsafe.Visible(v)) }
	add("YOLO_REQUEST_ID", q.ID)
	add("YOLO_REQUEST_SUMMARY", s.RequestSummary(q))
	add("YOLO_REQUEST_EXPIRES", q.Expires().Format(time.RFC3339))
	// YOLO_REQUEST_*, never yolo's own names: YOLO_WORKSPACE, for one, is read by the
	// `yolo approve` the hook runs.
	add("YOLO_REQUEST_WORKSPACE", q.Workspace)
	add("YOLO_REQUEST_JAIL", q.Jail)
	add("YOLO_REQUEST_SET", q.Set)
	add("YOLO_REQUEST_REPO", q.Repo)
	return env
}

// fire runs the hook for one request. The hook decides nothing itself: a decision is the
// user's command running `yolo approve`, with the CLI's own checks and audit.
func (r *Runner) fire(ctx context.Context, q brokerstore.Request) {
	q.State, q.Decided = "pending", time.Time{}
	body, err := json.Marshal(r.Store.EventView(brokerstore.Event{Kind: brokerstore.EventRequestFiled, Request: &q}, time.Now()))
	if err != nil {
		r.logf("%s: %v", q.ID, err)
		return
	}
	cmd := exec.Command("/bin/sh", "-c", r.Command)
	cmd.Stdin = bytes.NewReader(append(body, '\n'))
	cmd.Env = hookEnv(os.Environ(), r.linkDir(), Env(r.Store, q, r.Bin))
	cmd.Dir = r.Dir
	if cmd.Dir == "" {
		cmd.Dir, _ = os.UserHomeDir()
	}
	// Its output is kept, the last OutputMax bytes, for the request's record should it fail.
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// A child that outlives the shell can hold the output open; stop waiting after KillWait.
	cmd.WaitDelay = KillWait
	cmd.SysProcAttr = hookProcAttr()
	// On Linux the hook dies with the broker (hookProcAttr's Pdeathsig), which the kernel
	// ties to the THREAD that started it, so this goroutine keeps its thread until the hook
	// is reaped.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if ctx.Err() != nil {
		return // its request stopped waiting before the hook could start
	}
	if err := cmd.Start(); err != nil {
		r.logf("the hook for %s could not start (%v)", q.ID, err)
		r.noteFailure(q.ID, "hook could not start: "+err.Error(), "")
		return
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	// The process group is the hook's own, so signalling it reaches whatever it started too.
	// It is stopped when its request leaves pending or expires, its run times out or the
	// broker stops, whether or not the shell has already exited: a notifier it backgrounded
	// is still asking a question that has been answered. SIGINT comes first: notify-send
	// (libnotify 0.8.0 and newer) waiting on -A or -w closes its notification on it, where
	// SIGTERM would kill it and leave the notification on screen. Whatever is left after
	// StopGrace, such as a child a non-interactive shell backgrounded with SIGINT ignored, is
	// SIGKILLed.
	pgid := cmd.Process.Pid
	stop := func() {
		why := context.Cause(ctx)
		if errors.Is(why, context.Canceled) {
			why = errors.New("the broker stopped")
		}
		if syscall.Kill(-pgid, syscall.SIGINT) != nil {
			return // the group is already gone
		}
		r.logf("the hook for %s was stopped because %v", q.ID, why)
		grace := time.NewTimer(StopGrace)
		defer grace.Stop()
		tick := time.NewTicker(stopPoll)
		defer tick.Stop()
		for syscall.Kill(-pgid, 0) == nil {
			select {
			case <-grace.C:
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				return
			case <-tick.C:
			}
		}
	}
	select {
	case err = <-waited:
	case <-ctx.Done():
		stop()
		<-waited
		// Stopped because its request left pending or expired, or the broker stopped, is no
		// failure; running into a Timeout shorter than its request's life is: the request
		// still waits and may never have been seen.
		if errors.Is(context.Cause(ctx), errTimedOut) {
			r.noteFailure(q.ID, failureLine(fmt.Sprintf("hook timed out after %s", r.Timeout), out.String()), out.String())
		}
		return
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // the shell succeeded; a child it left running held its output open
	}
	if err != nil {
		what := "hook failed: " + err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code := exit.ExitCode(); code >= 0 {
				what = fmt.Sprintf("hook exited %d", code)
			} else {
				what = "hook was killed: " + exit.String()
			}
		}
		r.logf("the hook for %s failed (%s); `yolo approve` shows its output", q.ID, what)
		r.noteFailure(q.ID, failureLine(what, out.String()), out.String())
	}
	// The shell is reaped. While anything it started still holds its group, keep that
	// group to kill; once the group is empty its id is free for reuse, so stop watching.
	tick := time.NewTicker(groupPoll)
	defer tick.Stop()
	for syscall.Kill(-pgid, 0) == nil {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-tick.C:
		}
	}
}

// noteFailure records a failed hook on its request, where every listing shows it.
func (r *Runner) noteFailure(id, failure, output string) {
	if err := r.Store.NoteHookFailure(id, failure, termsafe.VisibleLines(output)); err != nil {
		r.logf("could not record the failed hook on %s: %s", id, termsafe.Visible(err.Error()))
	}
}

// limit is how long q's hook may run, and why it is stopped then. Its request's expiry, read
// on the store's clock, bounds every run as a stop that is no failure (errExpired): a hook
// still waiting on a click then was doing its job, and the store's own expiry event, which
// may arrive first, stops it the same way, so the race between the two decides nothing. A
// Timeout shorter than that is a hard bound, and running into it a failure (errTimedOut).
// The expiry bound is capped at PendingLife, so a clock set back cannot stretch a run.
func (r *Runner) limit(q brokerstore.Request) (time.Duration, error) {
	now := time.Now()
	if r.Store != nil && r.Store.Now != nil {
		now = r.Store.Now()
	}
	left := min(max(q.Expires().Sub(now), 0), brokerstore.PendingLife)
	if r.Timeout > 0 && r.Timeout < left {
		return r.Timeout, errTimedOut
	}
	return left, errExpired
}

// failureLine is what, then the hook's last non-empty output line, cut to FailureLineMax
// bytes: "hook exited 127: sh: notify-send: not found".
func failureLine(what, output string) string {
	last := ""
	for _, l := range strings.Split(output, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			last = l
		}
	}
	line := what
	if last != "" {
		line += ": " + last
	}
	line = termsafe.Visible(line)
	if len(line) > FailureLineMax {
		cut := FailureLineMax - len("…")
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	return line
}

// OutputMax bounds the hook output kept for a failed hook's record; FailureLineMax bounds
// its one-line failure.
const (
	OutputMax      = 4096
	FailureLineMax = 240
)

// tailBuffer keeps the last OutputMax bytes written to it.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if over := len(t.b) - OutputMax; over > 0 {
		t.b = append(t.b[:0:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// groupPoll is how often an exited hook's process group is checked for members left behind.
const groupPoll = 100 * time.Millisecond

// stopPoll is how often a stopped hook's process group is checked for members during
// StopGrace.
const stopPoll = 20 * time.Millisecond

// KillWait bounds the wait on a hook's output once it exits or is killed. A broker
// stopping waits at least StopGrace plus this long for its runner.
const KillWait = 5 * time.Second

// StopGrace is how long a stopped hook has, after SIGINT to its process group, to close
// what it showed (a notification still on screen) before the group is SIGKILLed.
const StopGrace = 2 * time.Second
