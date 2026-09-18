// Package supervisor is the in-jail daemon supervisor. It reads
// YOLO_JAIL_DAEMONS (a JSON list of {name, cmd, restart}) and supervises each
// entry as a subprocess. That payload carries pack `service` daemons as well
// as loophole daemons — one frozen contract with one writer, internal/loopholes'
// RuntimeArgsForWithJailDaemons — so everything in this package governs both
// populations, and a change to spawn or restart behaviour changes both.
//
// Frozen contracts: the YOLO_JAIL_DAEMONS JSON shape +
// skip-invalid-entry parsing, the restart policies (always | on-failure | no),
// which govern a failure to SPAWN exactly as they govern an exit,
// per-daemon logs at ~/.local/state/yolo-jail-daemons/<name>.log rotated once
// at 5 MB (.log -> .log.1) and carrying the supervisor's own spawn-failure and
// giving-up lines beside the child's output, the 1s→30s exponential backoff,
// and SIGTERM/SIGINT → terminate children (5s grace → kill).
package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	logMaxBytes           = 5 * 1024 * 1024 // 5 MB
	restartBackoffInitial = 1.0
	restartBackoffMax     = 30.0
)

// LogDir returns ~/.local/state/yolo-jail-daemons.
func LogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".local", "state", "yolo-jail-daemons")
}

// Spec is one daemon entry. restart defaults to "on-failure".
type Spec struct {
	Name    string
	Cmd     []string
	Restart string
}

// jsonEntry mirrors the raw JSON object; fields are validated in ParseEnv.
type jsonEntry struct {
	Name    string  `json:"name"`
	Cmd     []any   `json:"cmd"`
	Restart *string `json:"restart"`
}

// ParseEnv parses YOLO_JAIL_DAEMONS. Invalid JSON or a non-list → nil. A
// non-dict element within the list, or an invalid entry (missing name /
// non-list-or-empty cmd), is skipped individually: one bad entry drops itself
// and the rest of the payload is still supervised. Skip-invalid-entry parsing
// is a frozen contract, so widen what is accepted rather than what is rejected.
func ParseEnv(raw string) []Spec {
	// Decode into raw elements first so ONE bad element doesn't abort the whole
	// list — the skip is per-element.
	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &elems); err != nil {
		return nil // invalid JSON, or valid JSON that isn't a list
	}
	var out []Spec
	for _, raw := range elems {
		var e jsonEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue // non-dict entry — skip
		}
		if e.Name == "" || len(e.Cmd) == 0 {
			continue
		}
		cmd := make([]string, 0, len(e.Cmd))
		ok := true
		for _, c := range e.Cmd {
			s, isStr := stringifyCmd(c)
			if !isStr {
				ok = false
				break
			}
			cmd = append(cmd, s)
		}
		if !ok {
			continue
		}
		restart := "on-failure"
		if e.Restart != nil {
			restart = *e.Restart
		}
		out = append(out, Spec{Name: e.Name, Cmd: cmd, Restart: restart})
	}
	return out
}

// stringifyCmd coerces a cmd element to a token. Elements are strings in every
// real payload; the number and bool cases only exist because the wire shape
// cannot forbid them, and their spellings ("True"/"False", JSON-compact
// numbers) are inherited from the retired Python writer, whose output some
// older jail could still hold. Accept string/number/bool, reject the rest.
func stringifyCmd(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		// A JSON number here is malformed input; return its compact form
		// rather than guessing at an int/float spelling.
		b, _ := json.Marshal(t)
		return string(b), true
	case bool:
		if t {
			return "True", true
		}
		return "False", true
	default:
		return "", false
	}
}

// openLog opens (rotating once at 5 MB) the per-daemon log file.
// Rename <name>.log -> <name>.log.1 when over the limit.
func openLog(name string) (*os.File, error) {
	if err := os.MkdirAll(LogDir(), 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(LogDir(), name+".log")
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Size() > logMaxBytes {
		_ = os.Rename(p, filepath.Join(LogDir(), name+".log.1"))
	}
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
}

// child supervises one daemon.
type child struct {
	spec    Spec
	backoff float64
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // closed when the current cmd's Wait() returns
}

func (c *child) start() error {
	lf, err := openLog(c.spec.Name)
	if err != nil {
		return err
	}
	cmd := exec.Command(c.spec.Cmd[0], c.spec.Cmd[1:]...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	if err := cmd.Start(); err != nil {
		lf.Close()
		return err
	}
	c.mu.Lock()
	c.cmd = cmd
	c.done = make(chan struct{})
	c.mu.Unlock()
	// Close our copy of the fd; the child holds its own.
	_ = lf.Close()
	return nil
}

// logf appends one supervisor-authored line to the daemon's own <name>.log,
// beside the child's stdout/stderr. That file is the one a human opens when a
// daemon misbehaves, and it is the only channel that reaches them: the
// supervisor's own stderr is /dev/null in a real jail, because
// entrypoint.startJailDaemonSupervisor starts it detached with Stderr unset.
// So an error reported anywhere else is an error reported nowhere. The prefix
// distinguishes these lines from the child's output in a shared file.
//
// Best-effort by necessity — a supervisor cannot report that it could not
// report. When openLog is itself what failed, the caller's error is lost, and
// that is the one remaining silent path.
func (c *child) logf(format string, args ...any) {
	lf, err := openLog(c.spec.Name)
	if err != nil {
		return
	}
	defer lf.Close()
	_, _ = fmt.Fprintf(lf, "[yolo-jaild %s] %s\n",
		time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// waitAndMaybeRestart waits for a child that DID spawn and reports whether to
// restart it per policy, including the pre-return backoff sleep + doubling.
// Returns false if stop fired. Every give-up is announced in <name>.log: an
// abandoned daemon that says nothing is indistinguishable from a running one.
func (c *child) waitAndMaybeRestart(stop <-chan struct{}) bool {
	c.mu.Lock()
	cmd := c.cmd
	done := c.done
	c.mu.Unlock()
	err := cmd.Wait()
	close(done) // signal terminate() that this process has reaped
	if isStopped(stop) {
		return false
	}
	rc := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc = ee.ExitCode()
		} else {
			rc = 1
		}
	}
	if c.spec.Restart == "no" {
		c.logf("exited with status %d — restart policy \"no\": not restarting", rc)
		return false
	}
	if c.spec.Restart == "on-failure" && rc == 0 {
		c.logf("exited with status 0 — restart policy \"on-failure\": not restarting")
		return false
	}
	sleepInterruptible(stop, time.Duration(c.backoff*float64(time.Second)))
	c.backoff = minFloat(c.backoff*2, restartBackoffMax)
	return !isStopped(stop)
}

// terminate SIGTERMs the child, then SIGKILLs after the grace period. It only
// SIGNALS the process — the single cmd.Wait() lives in the supervise
// goroutine (Go forbids a second Wait), so we poll exit via exited() rather
// than Wait()ing here.
func (c *child) terminate(timeout time.Duration) {
	c.mu.Lock()
	cmd := c.cmd
	done := c.done
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil || done == nil {
		return
	}
	select {
	case <-done:
		return // already reaped
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
	}
}

// superviseOne is the per-daemon loop: start, wait, restart-or-give-up.
//
// A failure to SPAWN is a failure like any other, so the restart policy decides
// what happens next — "no" gives up, "on-failure" and "always" back off and
// retry, and "always" retries forever because that is what the word means.
// Only a successful spawn reaches waitAndMaybeRestart, so until 2026-09-17 the
// policy had no say in this branch at all: every spawn failure was retried
// 1s→30s for the life of the jail, and c.start()'s error was read as a
// nil-test and the VALUE thrown away. A typo'd cmd therefore presented as an
// empty <name>.log and no process, openLog having already created the file.
//
// A spawn that never succeeded leaves c.cmd nil, and Run terminates every child
// unconditionally — so a give-up here depends on terminate() staying nil-safe.
func (c *child) superviseOne(stop <-chan struct{}) {
	for !isStopped(stop) {
		if err := c.start(); err != nil {
			if c.spec.Restart == "no" {
				c.logf("spawn failed: %v — restart policy %q: giving up, this daemon will not run", err, c.spec.Restart)
				return
			}
			c.logf("spawn failed: %v — restart policy %q: retrying in %.0fs", err, c.spec.Restart, c.backoff)
			sleepInterruptible(stop, time.Duration(c.backoff*float64(time.Second)))
			c.backoff = minFloat(c.backoff*2, restartBackoffMax)
			continue
		}
		if !c.waitAndMaybeRestart(stop) {
			return
		}
	}
}

// Run supervises specs until stop is closed. Returns when all daemons have
// settled (or stop fired + children terminated).
// parsing.
func Run(specs []Spec, stop <-chan struct{}) {
	children := make([]*child, len(specs))
	for i, s := range specs {
		children[i] = &child{spec: s, backoff: restartBackoffInitial}
	}
	var wg sync.WaitGroup
	for _, c := range children {
		wg.Add(1)
		go func(c *child) { defer wg.Done(); c.superviseOne(stop) }(c)
	}
	<-stop
	// Terminate all children (5s grace each), matching the shutdown handler.
	var tw sync.WaitGroup
	for _, c := range children {
		tw.Add(1)
		go func(c *child) { defer tw.Done(); c.terminate(5 * time.Second) }(c)
	}
	tw.Wait()
	waitTimeout(&wg, 10*time.Second)
}

// --- small helpers ---
func isStopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func sleepInterruptible(stop <-chan struct{}, d time.Duration) {
	select {
	case <-stop:
	case <-time.After(d):
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}
