// Package supervisor is the in-jail daemon supervisor. It reads
// YOLO_JAIL_DAEMONS (a JSON list of {name, cmd, restart}) and supervises each
// entry as a subprocess.
//
// Frozen contracts: the YOLO_JAIL_DAEMONS JSON shape +
// skip-invalid-entry parsing, the restart policies (always | on-failure | no),
// per-daemon logs at ~/.local/state/yolo-jail-daemons/<name>.log rotated once
// at 5 MB (.log -> .log.1), the 1s→30s exponential backoff, and SIGTERM/SIGINT
// → terminate children (5s grace → kill).
package supervisor

import (
	"encoding/json"
	"errors"
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

// ParseEnv parses YOLO_JAIL_DAEMONS. Invalid JSON or a non-list → nil (Python
// returns []). A non-dict element within the list, or an invalid entry
// (missing name / non-list-or-empty cmd), is skipped individually — matching
// _parse_env, which iterates and drops bad entries rather than failing whole.
func ParseEnv(raw string) []Spec {
	// Decode into raw elements first so ONE bad element doesn't abort the whole
	// list (Python skips per-element).
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

// stringifyCmd mirrors Python str(x) for cmd elements (which are almost always
// strings; numbers/bools would be stringified). We accept string/number/bool.
func stringifyCmd(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		// str(int) vs str(float) — cmd tokens are strings in practice; a
		// JSON number here is malformed input. Return its compact form.
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

// waitAndMaybeRestart waits for the child and reports whether to restart per
// policy. Returns false if stop fired.
// including the pre-return backoff sleep + doubling.
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
		return false
	}
	if c.spec.Restart == "on-failure" && rc == 0 {
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

// superviseOne is the per-daemon loop: start (backoff-retry on spawn failure),
// wait, restart-or-return.
func (c *child) superviseOne(stop <-chan struct{}) {
	for !isStopped(stop) {
		if err := c.start(); err != nil {
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
