package macosuser

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// This file holds the production ("real") backing for the Deps seams —
// subprocess / filesystem / platform probes. All are macOS-relevant at runtime
// but COMPILE on every GOOS (pure os/exec), so `GOOS=darwin go build ./...` and
// Linux CI both pass.

func isMacOSReal() bool { return runtime.GOOS == "darwin" }

// selfExeReal returns the running yolo binary path (os.Executable), staged for
// the sandbox to self-exec as the bootstrap. Falls back to "yolo" (resolved off
// PATH) only if os.Executable fails — the plan invariant flags an unstaged path.
func selfExeReal() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "yolo"
}

func whichReal(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// sandboxUserExistsReal `id <user>` returns 0
// (timeout 5s → False).
func sandboxUserExistsReal(user string) bool {
	cmd := exec.Command("id", user)
	return runWithTimeout(cmd, 5*time.Second) == 0
}

// gitConfigReal `git config --get <key>` (timeout 5s),
// stdout trimmed; "" + false when unset/empty/error.
func gitConfigReal(key string) (string, bool) {
	cmd := exec.Command("git", "config", "--get", key)
	out, rc := outputWithTimeout(cmd, 5*time.Second)
	if rc != 0 {
		return "", false
	}
	val := strings.TrimSpace(out)
	if val == "" {
		return "", false
	}
	return val, true
}

func hostUserReal() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return ""
}

// runReal runs argv inheriting stdio and returns the returncode. A start
// failure yields 1 (the call sites treat non-zero as failure).
//
// INHERITING IS THE POINT, and it stayed that way when the backend's own printing moved
// to the run pipeline's writers (orchestrator.go's launchWriter). These are CHILD
// PROCESSES, not lines this backend is disclosing: sudo prompts for a password on the
// real terminal, and the bootstrap and the login shell are interactive. Handing them a
// wrapped io.Writer would put a pipe between the child and the tty — changing what sudo
// asks and what every child resolves color against — and would copy an agent session's
// bytes into a 0644 launch.log, which is the one thing that log excludes by name
// (internal/cli/run/launchlog.go, *What it deliberately does not capture*).
//
// EACH CHILD IS STARTED AND NAMED TO THE FOREGROUND RUNNER while it runs (SetForegroundRunner), when a launch's
// signal arm has published one: a SIGTERM sent to yolo alone reaches no child, so the arm forwards
// it to the one running here — a sudo prompt, the bootstrap, a provisioning stage that can take
// minutes — and the launch ends at its next step instead of after it.
func runReal(argv []string) int { return runStdoutTo(os.Stdout, argv) }

// RunStdoutTo is a Deps.Run that is runReal with each command's stdout on w: stdin and stderr are
// still inherited, so sudo still asks on the real terminal. Its one caller is a capture whose output
// is another command's progress (the host floor's, on a Mac: internal/cli's captureAct.jailStdout),
// which hands this process's stderr — an *os.File, so the child inherits that descriptor itself and
// no pipe comes between it and the terminal.
func RunStdoutTo(w io.Writer) func(argv []string) int {
	return func(argv []string) int { return runStdoutTo(w, argv) }
}

// runStdoutTo runs argv with stdin and stderr inherited and its stdout on w, starting it through the
// published foreground runner and returning its returncode; a start failure yields 1.
func runStdoutTo(w io.Writer, argv []string) int {
	if len(argv) == 0 {
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, w, os.Stderr
	done, err := startForegroundProcess(func() (*os.Process, error) {
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd.Process, nil
	})
	if err != nil {
		return exitCodeOf(err)
	}
	err = cmd.Wait()
	done()
	return exitCodeOf(err)
}

// ForegroundRunner starts a child and, when appropriate, publishes it as foreground before signals
// can be handled. The runner must call start exactly once.
type ForegroundRunner func(start func() (*os.Process, error)) (done func(), err error)

// foregroundRunner is the published runner runReal uses while a launch's signal arm is installed.
var (
	foregroundRunnerMu sync.Mutex
	foregroundRunner   ForegroundRunner
)

// SetForegroundRunner publishes runner for children runReal starts, and returns the undo, never nil.
// The macos-user launch's signal arm starts and publishes each child under one lock so a signal
// cannot land between cmd.Start and foreground publication.
func SetForegroundRunner(runner ForegroundRunner) (undo func()) {
	foregroundRunnerMu.Lock()
	defer foregroundRunnerMu.Unlock()
	prev := foregroundRunner
	foregroundRunner = runner
	return func() {
		foregroundRunnerMu.Lock()
		defer foregroundRunnerMu.Unlock()
		foregroundRunner = prev
	}
}

// startForegroundProcess runs start under the published runner, if any, and returns a no-op done
// when no launch has published one.
func startForegroundProcess(start func() (*os.Process, error)) (done func(), err error) {
	foregroundRunnerMu.Lock()
	runner := foregroundRunner
	foregroundRunnerMu.Unlock()
	if runner == nil {
		_, err := start()
		return func() {}, err
	}
	done, err = runner(start)
	if done == nil {
		done = func() {}
	}
	return done, err
}

// runBashReal runs `bash -c <script>` inheriting stdio; returns the returncode.
func runBashReal(script string) int {
	return runReal([]string{"bash", "-c", script})
}

// tee <path> (content on stdin, stdout to /dev/null), sudo chmod <mode> <path>.
// Any failure → false.
func installRootFileReal(path, content, mode string) bool {
	parent := filepath.Dir(path)
	if runReal([]string{"sudo", mkdirBin, "-p", parent}) != 0 {
		return false
	}
	tee := exec.Command("sudo", teeBin, path)
	tee.Stdin = strings.NewReader(content)
	tee.Stdout = nil // discard tee's stdout echo
	tee.Stderr = os.Stderr
	if err := tee.Run(); err != nil {
		return false
	}
	return runReal([]string{"sudo", chmodBin, mode, path}) == 0
}

// GIDs (Groups/PrimaryGroupID) via dscl, timeout 10s each. Best-effort.
func takenIDsReal() map[int]struct{} {
	ids := map[int]struct{}{}
	for _, kv := range [][2]string{{"Users", "UniqueID"}, {"Groups", "PrimaryGroupID"}} {
		cmd := exec.Command("dscl", ".", "-list", "/"+kv[0], kv[1])
		out, rc := outputWithTimeout(cmd, 10*time.Second)
		if rc != 0 {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}
			last := parts[len(parts)-1]
			if isDigits(strings.TrimLeft(last, "-")) {
				if n, err := strconv.Atoi(last); err == nil {
					ids[n] = struct{}{}
				}
			}
		}
	}
	return ids
}

// setRandomPasswordReal generates a random password and applies it via
// `sudo /bin/sh -c 'read -r pw; dscl . -passwd /Users/<u> "$pw"'`, feeding the
// password on STDIN (never argv, so it can't show in `ps`; never env, which
// sudo's env_reset strips — see the finding-6 fix below).
func setRandomPasswordReal(user string) bool {
	rand := exec.Command("openssl", "rand", "-base64", "32")
	pwOut, rc := outputWithTimeout(rand, 5*time.Second)
	if rc != 0 {
		return false
	}
	pw := strings.TrimSpace(pwOut)
	// Finding 6: pass the password via STDIN to the root shell, never via
	// cmd.Env — sudo's env_reset strips arbitrary env vars, so the previous
	// YOLO_SBPW-on-env approach set an EMPTY password. `read -r pw` on stdin is
	// the same pattern installRootFileReal uses (strings.NewReader stdin), and
	// keeps the secret off argv (which would leak in `ps`). NOTE: the actual
	// password-apply + dscl empty-string semantics are M1-verified on hardware.
	cmd := exec.Command("sudo", "/bin/sh", "-c",
		"read -r pw; dscl . -passwd /Users/"+user+" \"$pw\"")
	cmd.Stdin = strings.NewReader(pw + "\n")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run() == nil
}

func pathIsDirReal(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func pathExistsReal(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// readFileReal reads a file, reporting whether the content is KNOWN.
//
// ⚠ AN ABSENT FILE IS KNOWLEDGE, not a failure, and the distinction is the whole point
// for the one caller (runProvisionStage): "the stage wrote no log" is the observation it
// classifies on, while "I could not read the log" is the one it must refuse to classify.
// os.ReadFile reports both as an error, so they are separated here.
func readFileReal(p string) (string, bool) {
	b, err := os.ReadFile(p)
	switch {
	case err == nil:
		return string(b), true
	case os.IsNotExist(err):
		return "", true
	default:
		return "", false
	}
}

// removeFileReal removes a file and reports whether it is GONE afterwards — an
// already-absent file is a success, because the caller asks "is this path clear?" rather
// than "did I delete something?".
func removeFileReal(p string) bool {
	err := os.Remove(p)
	return err == nil || os.IsNotExist(err)
}

// --- small subprocess helpers ---------------------------------------------

func runWithTimeout(cmd *exec.Cmd, d time.Duration) int {
	if err := cmd.Start(); err != nil {
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return 1
	case err := <-done:
		return exitCodeOf(err)
	}
}

func outputWithTimeout(cmd *exec.Cmd, d time.Duration) (string, int) {
	var buf strings.Builder
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return "", 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return buf.String(), 1
	case err := <-done:
		return buf.String(), exitCodeOf(err)
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// startBackgroundReal starts argv as the jail-daemon supervisor's launcher: no terminal
// (stdin /dev/null; stdout and stderr one bounded in-memory capture, never the terminal, since
// this runs beside the session, which holds the terminal), in a PROCESS GROUP OF ITS OWN, so the stop can signal
// the whole group, matching the container's teardown (macos-user-nix-and-features.md JD-6).
//
// The capture holds only what is written BEFORE the guest takes stdout and stderr over — sudo's
// own refusal, sandbox-exec's — because the supervisor's argv sends everything after that to
// supervisor.log (supervisorLogWrapper, JD-8). Background.Output reads it; Background.Exited is
// closed when the process has been reaped.
//
// The stop is SIGTERM to the group, then up to jailDaemonStopGrace for it to exit, then
// SIGKILL. argv[0] is sudo, which this uid may signal (its real uid is ours) and which RELAYS
// a SIGTERM to the command it runs; the supervisor's own teardown then gives each daemon
// SIGTERM and 5 s before SIGKILL, which is why the grace here is longer than that. A SIGKILL
// is not relayed — it is the last resort for a sudo that did not exit, and what it would leave
// behind is the one thing only a Mac can measure (the plan's verification split).
func startBackgroundReal(argv []string) (Background, error) {
	if len(argv) == 0 {
		return Background{}, errors.New("empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	captured := &cappedBuffer{max: backgroundOutputCap}
	// ONE writer for both, so exec.Cmd makes one pipe and the two streams keep their order.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, captured, captured
	// A writer that is not an *os.File makes Wait also wait for the pipe to close. Nothing in
	// the guest keeps it open (the log wrapper points both streams elsewhere, so sudo holds the
	// last copy), but if something did, Exited would never close and the stop would take its
	// full grace. This bounds that to two seconds after the process exits.
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return Background{}, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	pgid := cmd.Process.Pid
	var once sync.Once
	stop := func() {
		once.Do(func() {
			select {
			case <-done:
				return
			default:
			}
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(jailDaemonStopGrace):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				select {
				case <-done:
				case <-time.After(2 * time.Second):
				}
			}
		})
	}
	return Background{Stop: stop, Exited: done, Output: captured.String}, nil
}

// backgroundOutputCap bounds startBackgroundReal's capture: a sudo or sandbox-exec refusal is a
// line or two, and nothing else should reach it.
const backgroundOutputCap = 8 << 10

// cappedBuffer keeps the first max bytes written to it and discards the rest, safe for the
// exec.Cmd copier to write while the launch reads.
type cappedBuffer struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - len(c.b); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		c.b = append(c.b, p[:room]...)
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.b)
}

// jailDaemonStopGrace is how long the stop waits for the supervisor's own SIGTERM→5 s→SIGKILL
// teardown (internal/supervisor) before killing what is left.
const jailDaemonStopGrace = 10 * time.Second

// SessionRecordsDir is where the production liveness records live (sessionfiles.go), beside the
// per-workspace launch locks: <global storage>/locks/macos-user-sessions, a record per session
// named <session key>.lock.
func SessionRecordsDir() string {
	return filepath.Join(paths.GlobalStorage(), "locks", sessionRecordLeaf)
}

// readSystemKeychainReal exports every certificate in the System keychain as PEM, as the invoking
// user (systemKeychainExportArgv): the keychain file is world-readable, so this needs no sudo, and
// it reads, never writes. A failure names the command, so the launch's warning gives the user
// the one line that reproduces it.
func readSystemKeychainReal() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	argv := systemKeychainExportArgv()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("`" + shquote.JoinDisplay(argv) + "`: " + msg)
	}
	return stdout.String(), nil
}

// verifyCAReal asks macOS whether it trusts one CA for TLS (verifyCAArgv, whose comment says what
// each flag is for). The PEM goes through a private temporary file, since the tool takes a path.
func verifyCAReal(pemBlock string) bool {
	f, err := os.CreateTemp("", "yolo-ca-*.pem")
	if err != nil {
		return false
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(pemBlock); err != nil {
		_ = f.Close()
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}
	argv := verifyCAArgv(f.Name())
	return runWithTimeout(exec.Command(argv[0], argv[1:]...), 15*time.Second) == 0
}
