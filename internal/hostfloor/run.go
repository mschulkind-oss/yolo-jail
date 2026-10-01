package hostfloor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// run.go runs the floor's installers: bounded, in a SET environment, with their output shown.
//
// # The set environment
//
// An install's result must not depend on who launched yolo — the governing ruling of
// host-agent-environment.md (OQ-HE0) — so installerEnv builds the child's environment rather than
// inheriting it whole. PATH is the floor's Node first and then a fixed baseline of system
// directories: never the ambient PATH, whose `npm` might be a mise shim or another prefix's.
// Every variable that would REDIRECT an npm install (NPM_CONFIG_* and npm_config_*, the way nvm
// and a user's rc steer npm) or change what Node loads at start (NODE_OPTIONS, NODE_PATH) is
// dropped, and the floor sets the ones it means. What stays is what the network and the locale
// need — proxies, CA bundles, LANG — which describe the machine rather than choose the install.
//
// # Bounded, and killed whole
//
// A first install is bounded at 600 s (§4); on timeout the whole PROCESS GROUP is killed, because
// npm's work is done by children that would otherwise outlive it, and the caller removes the
// half-written install directory. Stdin is /dev/null, so an installer can neither prompt nor
// hang on one.

// BaselinePath is the fixed system PATH an installer runs with after the floor's Node — and what a
// host launch hands its child when the caller passed no PATH at all (internal/cli hostChildPath): the
// directories a login shell on Linux or macOS always has, and NixOS's fixed profile directories
// when present. Filtered by existence at call time.
func BaselinePath() []string {
	dirs := []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
		"/run/wrappers/bin", "/run/current-system/sw/bin"}
	if u, err := user.Current(); err == nil && u.Username != "" {
		dirs = append(dirs, "/etc/profiles/per-user/"+u.Username+"/bin")
	}
	var out []string
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// droppedEnv reports whether an ambient variable is left out of an installer's environment.
func droppedEnv(key string) bool {
	upper := strings.ToUpper(key)
	switch {
	case key == "PATH", key == "HOME":
		return true // set explicitly below
	case strings.HasPrefix(upper, "NPM_CONFIG_"):
		return true
	case upper == "NODE_OPTIONS", upper == "NODE_PATH":
		return true
	}
	return false
}

// installerEnv is the environment one installer runs with: the ambient environment minus
// droppedEnv, PATH = <first>:<baseline>, HOME, and the caller's own settings last.
func (f *Floor) installerEnv(first string, set ...string) []string {
	base := f.Environ
	if base == nil {
		base = os.Environ()
	}
	var out []string
	for _, kv := range base {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || droppedEnv(k) {
			continue
		}
		out = append(out, kv)
	}
	path := BaselinePath()
	if first != "" {
		path = append([]string{first}, path...)
	}
	out = append(out, "PATH="+strings.Join(path, string(os.PathListSeparator)))
	home := f.Home
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home != "" {
		out = append(out, "HOME="+home)
	}
	return append(out, set...)
}

// tailLines is how much of an installer's output a failure message carries.
const tailLines = 12

// runResult is one installer run: its captured stdout (for a poll that reads an answer) and the
// last lines of everything it printed (for the failure message).
type runResult struct {
	stdout bytes.Buffer
	tail   *tailWriter
}

// runBounded runs argv in env and dir, bounded by timeout, streaming both its streams to the
// floor's output under an indent unless capture is set, in which case stdout is kept and only
// stderr is shown.
func (f *Floor) runBounded(ctx context.Context, timeout time.Duration, dir string, env, argv []string,
	capture bool) (*runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// The whole group: npm's work is done by children.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	res := &runResult{tail: &tailWriter{max: tailLines}}
	shown := &indentWriter{w: f.out(), indent: "    "}
	errSink := io.MultiWriter(shown, res.tail)
	cmd.Stderr = errSink
	if capture {
		cmd.Stdout = io.MultiWriter(&res.stdout, res.tail)
	} else {
		cmd.Stdout = errSink
	}
	err := cmd.Run()
	shown.flush()
	if ctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("%s did not finish within %s and was stopped", filepath.Base(argv[0]), timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return res, fmt.Errorf("%s exited %d", filepath.Base(argv[0]), ee.ExitCode())
		}
		return res, err
	}
	return res, nil
}

// failure renders an installer's error with the last lines it printed.
func failure(what string, err error, res *runResult) error {
	if res == nil || res.tail == nil || len(res.tail.lines) == 0 {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %w; its last lines:\n      %s", what, err,
		strings.Join(res.tail.lines, "\n      "))
}

// tailWriter keeps the last max lines written to it.
type tailWriter struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial = append(t.partial, p...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(t.partial[:i]), "\r"); strings.TrimSpace(line) != "" {
			t.lines = append(t.lines, line)
			if len(t.lines) > t.max {
				t.lines = t.lines[len(t.lines)-t.max:]
			}
		}
		t.partial = t.partial[i+1:]
	}
	return len(p), nil
}

// indentWriter prefixes every line with indent, so an installer's output reads as nested under
// the progress line that started it.
type indentWriter struct {
	mu     sync.Mutex
	w      io.Writer
	indent string
	buf    []byte
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	iw.mu.Lock()
	defer iw.mu.Unlock()
	iw.buf = append(iw.buf, p...)
	for {
		i := bytes.IndexByte(iw.buf, '\n')
		if i < 0 {
			break
		}
		fmt.Fprintf(iw.w, "%s%s\n", iw.indent, strings.TrimRight(string(iw.buf[:i]), "\r"))
		iw.buf = iw.buf[i+1:]
	}
	return len(p), nil
}

func (iw *indentWriter) flush() {
	iw.mu.Lock()
	defer iw.mu.Unlock()
	if len(iw.buf) > 0 {
		fmt.Fprintf(iw.w, "%s%s\n", iw.indent, string(iw.buf))
		iw.buf = nil
	}
}
