package darwinpkg

// materialize.go is the IMPURE half of darwin_packages.py — the actual nix
// invocations (the skip-list eval + the streaming buildEnv build). It was the
// last piece left in Python when the pure builders (darwinpkg.go) landed; the
// macos-user run wiring needs it, so it is ported here now.
import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MaterializeError nix missing or the build
// failed. The caller aborts with an actionable message rather than launching a
// half-provisioned sandbox.
type MaterializeError struct{ msg string }

func (e *MaterializeError) Error() string { return e.msg }

// Materialize realizes the darwin buildEnv profile natively via nix and returns
// its PATH prefix + env + skip list. IMPURE (runs nix; macOS-only in practice).
// It streams the build's stderr (`--print-build-logs` progress) straight to the
// process stderr so a from-source darwin build is VISIBLE, while capturing
// stdout (the store out-path) and a 30-line stderr tail for the error message.
//
// repoRoot is the nix build cwd (the repo ROOT — parent of src). system ""
// defaults to DarwinSystem. errStderr defaults to os.Stderr (injectable for
// tests). The returned error is always a *MaterializeError on failure.
func Materialize(repoRoot string, packages []any, system string, errStderr io.Writer) (*DarwinPackages, error) {
	if system == "" {
		system = DarwinSystem
	}
	if errStderr == nil {
		errStderr = os.Stderr
	}
	baseEnv, err := BuildEnv(os.Environ(), packages)
	if err != nil {
		return nil, &MaterializeError{msg: fmt.Sprintf("could not build nix env: %v", err)}
	}

	skipped := skippedNames(repoRoot, baseEnv, system)

	// Stream stderr live while capturing stdout (the store out-path) and a
	// bounded stderr tail for the error message.
	cmd := exec.Command(BuildProfileArgv(system)[0], BuildProfileArgv(system)[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = baseEnv
	var stdout strings.Builder
	cmd.Stdout = &stdout

	tailLines, exitCode, err := streamStderrTail(cmd, errStderr, 30)
	if err != nil {
		// FileNotFoundError → "nix command not found on PATH"; other start
		// failures → "nix build failed to run: …" (matches the Python split).
		if errors.Is(err, exec.ErrNotFound) {
			return nil, &MaterializeError{msg: "nix command not found on PATH"}
		}
		return nil, &MaterializeError{msg: fmt.Sprintf("nix build failed to run: %v", err)}
	}

	if exitCode != 0 {
		msg := strings.TrimSpace(strings.Join(tailLines, "\n"))
		if msg == "" {
			msg = "nix build of darwin packages failed"
		}
		return nil, &MaterializeError{msg: msg}
	}

	pkgs := ProfilePathsFromStdout(stdout.String(), skipped, nil)
	if pkgs == nil {
		return nil, &MaterializeError{msg: "nix build produced no store path"}
	}
	return pkgs, nil
}

// ProfilePathsFromStdout is the PURE tail of materialize: pick the last
// non-blank line of `--print-out-paths` stdout (the profile) and derive the
// PATH prefix + env, attaching the skip list. Returns nil when stdout has no
// store path (the DarwinPackagesError("no store path") branch). checkPkgConfig
// is forwarded to ProfilePaths (nil → real filesystem).
func ProfilePathsFromStdout(stdout string, skipped []string, checkPkgConfig func(string) bool) *DarwinPackages {
	var outLines []string
	for _, ln := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(ln) != "" {
			outLines = append(outLines, ln)
		}
	}
	if len(outLines) == 0 {
		return nil
	}
	pathPrefix, extra := ProfilePaths(outLines[len(outLines)-1], checkPkgConfig)
	return &DarwinPackages{PathPrefix: pathPrefix, Env: extra, Skipped: skipped}
}

// skippedNames is the best-effort read of the no-darwin-build skip list (a nix
// eval with a 120s timeout). Non-fatal on any failure.
func skippedNames(repoRoot string, env []string, system string) []string {
	argv := UnavailableEvalArgv(system)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = env
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(120 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return nil
	case err := <-done:
		if err != nil {
			return nil
		}
	}
	return ParseSkippedNames(stdout.String())
}

// streamStderrTail starts cmd, streams its stderr live to errStderr (so a
// from-source build stays VISIBLE), and returns the last `max` non-blank lines
// plus the child's exit code. The stderr pipe is drained SYNCHRONOUSLY before
// cmd.Wait() — the idiom os/exec documents for StderrPipe — so the tail is
// always complete and there is no concurrent access to the buffer. (The prior
// code called cmd.Wait() while a pump goroutine was still draining, which could
// truncate the tail and raced on the buffer after a 5s join timeout.)
//
// cmd.Stdout must already be set by the caller; err is non-nil only when the
// process could not be started (exec.ErrNotFound / other start failure). A
// non-zero exit is reported via the returned code, not err.
func streamStderrTail(cmd *exec.Cmd, errStderr io.Writer, max int) (tail []string, exitCode int, err error) {
	stderrPipe, perr := cmd.StderrPipe()
	if perr != nil {
		return nil, 0, perr
	}
	if serr := cmd.Start(); serr != nil {
		return nil, 0, serr
	}
	ring := newStderrTail(max)
	sc := bufio.NewScanner(stderrPipe)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(errStderr, line)
		if clean := strings.TrimRight(line, " \t\r\n"); clean != "" {
			ring.push(clean)
		}
	}
	waitErr := cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else if waitErr != nil {
		code = 1
	}
	return ring.lines(), code, nil
}

// stderrTail is a bounded ring of the last N non-blank stderr lines (the Python
// stderr_tail list capped at 30).
type stderrTail struct {
	max int
	buf []string
}

func newStderrTail(max int) *stderrTail { return &stderrTail{max: max} }

func (s *stderrTail) push(line string) {
	s.buf = append(s.buf, line)
	if len(s.buf) > s.max {
		s.buf = s.buf[len(s.buf)-s.max:]
	}
}

func (s *stderrTail) lines() []string { return s.buf }
