package ghbroker

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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// runner.go runs the host's gh the way docs/design/boundary-broker.md §4.1 requires
// (BB-D2): never as the argv arrived, but from the canonical argv the classifier rebuilt,
// in an empty broker-owned directory, with an environment built from nothing, a
// broker-owned config directory holding only a copy of the host's hosts.yml, and empty
// data, cache and state directories, so no alias, extension, browser, pager or editor the
// host's gh knows can resolve.
//
// > [!WARNING]
// > Never run the broker's gh in the workspace. The workspace is agent-writable and git
// > reads its config from there, so any gh command that shells out to git there runs
// > whatever the agent wrote, on the host, as the user (§9.5). The empty cwd and the
// > explicit repository are this boundary, not tidiness.

// Limits from the design (§3.5, §4.1).
const (
	CallTimeout = 300 * time.Second
	OutputCap   = 16 << 20
	StdinCap    = 1 << 20
)

// Exit codes the broker adds to gh's own, from sysexits.h (§3.2): none collides with gh's
// 0, 1, 2, 4 or 8.
const (
	ExitUsage       = 64 // EX_USAGE: refused, or out of scope
	ExitUnavailable = 69 // EX_UNAVAILABLE: no broker, no host gh, or no host login
	ExitTempFail    = 75 // EX_TEMPFAIL: pending a human (step 2)
	ExitNoPerm      = 77 // EX_NOPERM: denied, or a write this version cannot ask for
	ExitTimeout     = 124
	ghExitAuth      = 4 // gh's own "authentication required"
)

// testedMinor is the gh version range the measured grammar and the policy were built
// against (§5.1 rule 6): 2.101.x. Outside it no set applies.
const testedMinor = "2.101"

// ErrNoGH is a host with no gh on the broker's PATH.
var ErrNoGH = errors.New("no gh on the host's PATH")

// Runner runs one host gh for one broker.
type Runner struct {
	// GhPath is the host gh, resolved once at start to an absolute path; Version its
	// `gh --version` number; Tested whether that is inside testedMinor.
	GhPath  string
	Version string
	Tested  bool
	// HostsFile is the host hosts.yml copied into the broker's config dir, "" when the
	// host had none.
	HostsFile string
	// TokenRead says the broker holds the host token for redaction.
	TokenRead bool

	runDir  string
	token   string
	env     []string
	timeout time.Duration
	outCap  int64

	// running holds the process group of every gh a Run has started and not yet reaped, so
	// Shutdown can end them; closed says Shutdown ran, and no Run starts another.
	mu      sync.Mutex
	running map[int]bool
	closed  bool
}

// Shutdown ends every gh this runner is running, by process group, and lets no Run start
// another. The broker calls it when it is told to stop.
//
// It exists because each gh runs in a process group of its own (so a timeout reaches what
// gh spawned), and the launcher's SIGTERM to the broker's group therefore never reaches it.
// Without it a stop during a long read (`run watch`, `pr checks --watch`) waited for gh,
// outlasted the launcher's grace, and was SIGKILLed: its deferred cleanup never ran, the
// copy of the host's hosts.yml under run/ stayed, and gh ran on past its own timeout.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for pgid := range r.running {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// RunnerOptions are the facts a Runner takes from the broker's own process.
type RunnerOptions struct {
	// RunDir is an empty, broker-owned directory the runner lays its config, data, cache,
	// state and cwd out under.
	RunDir string
	// Getenv reads the broker's own environment, for PATH, HOME, the host gh's config dir
	// and the session bus the OS keyring is reached through. Nothing the jail sent reaches
	// here.
	Getenv func(string) string
	// LookPath resolves gh; nil means lookPathIn over Getenv("PATH").
	LookPath func(string) (string, error)
	// Refuse is asked about the resolved gh, once as found and once with its symlinks
	// resolved; a non-empty answer is why the broker will not run it, and NewRunner fails
	// with a *RefusedGHError carrying it. nil asks nothing.
	Refuse  func(gh string) string
	Timeout time.Duration
	OutCap  int64
}

// RefusedGHError is a host gh the broker found and will not run.
type RefusedGHError struct{ Path, Why string }

func (e *RefusedGHError) Error() string { return e.Why }

var versionRE = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

// NewRunner resolves the host gh, lays out the broker's directories, copies the host's
// hosts.yml, reads gh's version and reads the host token once for redaction.
func NewRunner(o RunnerOptions) (*Runner, error) {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Timeout == 0 {
		o.Timeout = CallTimeout
	}
	if o.OutCap == 0 {
		o.OutCap = OutputCap
	}
	look := o.LookPath
	if look == nil {
		look = func(name string) (string, error) { return lookPathIn(o.Getenv("PATH"), name) }
	}
	gh, err := look("gh")
	if err != nil || gh == "" {
		return nil, ErrNoGH
	}
	if abs, aerr := filepath.Abs(gh); aerr == nil {
		gh = abs
	}
	if o.Refuse != nil {
		candidates := []string{gh}
		if real, rerr := filepath.EvalSymlinks(gh); rerr == nil && real != gh {
			candidates = append(candidates, real)
		}
		for _, c := range candidates {
			if why := o.Refuse(c); why != "" {
				return nil, &RefusedGHError{Path: gh, Why: why}
			}
		}
	}
	r := &Runner{GhPath: gh, runDir: o.RunDir, timeout: o.Timeout, outCap: o.OutCap}
	for _, d := range []string{"config", "xdg-config", "data", "cache", "state", "cwd", "tmp"} {
		if err := os.MkdirAll(filepath.Join(r.runDir, d), 0o700); err != nil {
			return nil, err
		}
	}
	r.HostsFile = copyHostsFile(o.Getenv, filepath.Join(r.runDir, "config", "hosts.yml"))
	r.env = r.buildEnv(o.Getenv)

	out, _, _ := r.capture([]string{"--version"})
	if m := versionRE.FindStringSubmatch(out); m != nil {
		r.Version = m[1] + "." + m[2] + "." + m[3]
		r.Tested = m[1]+"."+m[2] == testedMinor
	}
	// The token, once, host-side, only to redact it (BB-D16). It never leaves this process.
	if tok, _, rc := r.capture([]string{"auth", "token", "--hostname", "github.com"}); rc == 0 {
		if tok = strings.TrimSpace(tok); len(tok) >= 8 && !strings.ContainsAny(tok, " \n") {
			r.token, r.TokenRead = tok, true
		}
	}
	return r, nil
}

// buildEnv is the gh environment, built from nothing (§4.1). In particular it carries no
// GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, GH_REPO, BROWSER, GH_BROWSER, EDITOR, VISUAL,
// GH_EDITOR, PAGER or GIT_* — GH_BROWSER and GH_PAGER are exec paths gh runs (MEASURED).
func (r *Runner) buildEnv(getenv func(string) string) []string {
	env := []string{
		"PATH=" + filepath.Dir(r.GhPath) + ":/usr/bin:/bin",
		"HOME=" + getenv("HOME"),
		"GH_CONFIG_DIR=" + filepath.Join(r.runDir, "config"),
		"XDG_CONFIG_HOME=" + filepath.Join(r.runDir, "xdg-config"),
		"XDG_DATA_HOME=" + filepath.Join(r.runDir, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(r.runDir, "cache"),
		"XDG_STATE_HOME=" + filepath.Join(r.runDir, "state"),
		"TMPDIR=" + filepath.Join(r.runDir, "tmp"),
		"GH_HOST=github.com",
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_NO_EXTENSION_UPDATE_NOTIFIER=1",
		"GH_SPINNER_DISABLED=1",
		"GH_PAGER=cat",
		"NO_COLOR=1",
		"GH_TELEMETRY=0",
		"DO_NOT_TRACK=1",
	}
	// The OS keyring, where gh keeps the token by default since 2.26.0, is reached on
	// Linux over the user's session bus (BB-D42). The address names the user's own bus and
	// nothing the jail can set.
	if v := getenv("DBUS_SESSION_BUS_ADDRESS"); v != "" {
		env = append(env, "DBUS_SESSION_BUS_ADDRESS="+v)
	}
	return env
}

// hostGHConfigDir is where the host's gh keeps its config, by gh's own precedence.
func hostGHConfigDir(getenv func(string) string) string {
	if v := getenv("GH_CONFIG_DIR"); v != "" {
		return v
	}
	if v := getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "gh")
	}
	return filepath.Join(getenv("HOME"), ".config", "gh")
}

// copyHostsFile copies the host's hosts.yml, and nothing else — no config.yml, so no
// aliases and no browser, pager, editor or http_unix_socket keys — into the broker's
// config dir, 0600. It returns the source, or "" when the host has none.
//
// A symlinked hosts.yml is followed: the host's gh config is the user's own, never
// agent-writable, and dotfile managers link it. (The no-symlink rule is for the
// workspace's git config, which the agent writes.)
func copyHostsFile(getenv func(string) string, dst string) string {
	src := filepath.Join(hostGHConfigDir(getenv), "hosts.yml")
	fi, err := os.Stat(src)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return ""
	}
	if os.WriteFile(dst, b, 0o600) != nil {
		return ""
	}
	return src
}

// capture runs gh once with the broker's environment and returns its stdout, stderr and
// exit code. It is for the broker's own startup reads, never for a jail's command.
func (r *Runner) capture(args []string) (string, string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.GhPath, args...)
	cmd.Env, cmd.Dir = r.env, filepath.Join(r.runDir, "cwd")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), exitCode(err)
}

// Account is the host gh's active github.com login, as `gh auth status --json hosts`
// reports it. Login is "" when the host gh has none.
type Account struct {
	Login, State, GitProtocol string
}

// ActiveAccount asks the host gh which github.com account it would use, and whether that
// login passed gh's own check (State "success"), for the broker's answer to a jail's
// `gh auth status` (BB-D65). The argv is the broker's alone, and gh's JSON form drops the
// token; nothing it prints crosses but the login and the state. It runs through Run, so
// Shutdown ends it like any call.
func (r *Runner) ActiveAccount() (Account, error) {
	var out, errOut bytes.Buffer
	res := r.Run([]string{"auth", "status", "--hostname", "github.com", "--active", "--json", "hosts"}, nil,
		func(b []byte) { out.Write(b) }, func(b []byte) { errOut.Write(b) })
	if res.Exit != 0 {
		return Account{}, fmt.Errorf("gh auth status exited %d", res.Exit)
	}
	var doc struct {
		Hosts map[string][]struct {
			State       string `json:"state"`
			Active      bool   `json:"active"`
			Login       string `json:"login"`
			GitProtocol string `json:"gitProtocol"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		return Account{}, fmt.Errorf("gh auth status printed no JSON: %w", err)
	}
	for _, e := range doc.Hosts["github.com"] {
		if e.Active {
			return Account{Login: e.Login, State: e.State, GitProtocol: e.GitProtocol}, nil
		}
	}
	return Account{}, nil
}

// RunResult is what one run did.
type RunResult struct {
	Exit       int
	BytesOut   int64
	Redactions int
	TimedOut   bool
	Truncated  bool
}

// Run runs gh with the canonical argv, feeding stdin, and streams its stdout and stderr to
// the two sinks through the redactor. Output past the cap is cut, with a last stderr line
// saying so; a call past the timeout is killed with exit 124.
func (r *Runner) Run(argv []string, stdin []byte, stdout, stderr func([]byte)) RunResult {
	cmd := exec.Command(r.GhPath, argv...)
	cmd.Env, cmd.Dir = r.env, filepath.Join(r.runDir, "cwd")
	cmd.SysProcAttr = childProcAttr()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	outPipe, _ := cmd.StdoutPipe()
	errPipe, _ := cmd.StderrPipe()
	// On Linux the child dies with the broker (childProcAttr's Pdeathsig), which the kernel
	// ties to the THREAD that started it, so this goroutine keeps its thread until gh is
	// reaped.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		stderr([]byte("github-broker: the broker is stopping, so nothing ran\n"))
		return RunResult{Exit: ExitUnavailable}
	}
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		stderr([]byte("github-broker: could not run the host gh: " + err.Error() + "\n"))
		return RunResult{Exit: ExitUnavailable}
	}
	pgid := cmd.Process.Pid
	if r.running == nil {
		r.running = map[int]bool{}
	}
	r.running[pgid] = true
	r.mu.Unlock()
	// Forgotten only once gh is reaped (the deferred call runs after cmd.Wait below), so
	// Shutdown never signals a process group id the system has handed to someone else.
	defer func() {
		r.mu.Lock()
		delete(r.running, pgid)
		r.mu.Unlock()
	}()

	var (
		mu        sync.Mutex
		total     int64
		truncated bool
	)
	kill := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// counted gates one stream's redacted output on the shared cap. Past the cap the rest
	// is read and dropped rather than the process killed, so gh's own exit code still
	// crosses; the timeout bounds how long that can take.
	counted := func(sink func([]byte)) func([]byte) {
		return func(b []byte) {
			mu.Lock()
			defer mu.Unlock()
			if truncated {
				return
			}
			if total+int64(len(b)) > r.outCap {
				b = b[:r.outCap-total]
				truncated = true
			}
			total += int64(len(b))
			if len(b) > 0 {
				sink(b)
			}
		}
	}
	outRed := newRedactor(r.token, counted(stdout))
	errRed := newRedactor(r.token, counted(stderr))
	var wg sync.WaitGroup
	pump := func(rd io.Reader, red *redactor) {
		defer wg.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := rd.Read(buf)
			if n > 0 {
				red.write(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				red.flush()
				return
			}
		}
	}
	wg.Add(2)
	go pump(outPipe, outRed)
	go pump(errPipe, errRed)

	timedOut := false
	timer := time.AfterFunc(r.timeout, func() {
		mu.Lock()
		timedOut = true
		mu.Unlock()
		kill()
	})
	wg.Wait()
	timer.Stop()
	err := cmd.Wait()

	res := RunResult{Exit: exitCode(err), BytesOut: total, Redactions: outRed.count + errRed.count,
		Truncated: truncated}
	mu.Lock()
	res.TimedOut = timedOut
	mu.Unlock()
	if res.Truncated {
		stderr([]byte(fmt.Sprintf("github-broker: output cut at %d bytes\n", r.outCap)))
	}
	if res.TimedOut {
		res.Exit = ExitTimeout
		stderr([]byte(fmt.Sprintf("github-broker: gh ran past %s and was stopped\n", r.timeout)))
	}
	return res
}

// Close removes the broker's directories, the hosts.yml copy with them.
func (r *Runner) Close() { _ = os.RemoveAll(r.runDir) }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// lookPathIn is exec.LookPath over an explicit PATH, so the broker resolves gh on the
// PATH its launch handed it rather than on whatever this process inherited later.
//
// A RELATIVE PATH ENTRY IS SKIPPED, never resolved. The launch spawns the broker from the
// workspace, so `./bin`, `node_modules/.bin` or `.` on the user's PATH names a directory
// the agent writes, and a `gh` planted there would run on the host as the user before any
// call arrived (`gh --version`, then `gh auth token`). exec.LookPath refuses a relative
// result for the same reason (exec.ErrDot); the broker never runs one at all.
func lookPathIn(pathList, name string) (string, error) {
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// Summary is the line the broker logs at start: Describe without the host paths, since the
// daemon's log is shared by every jail on the machine (newBroker).
func (r *Runner) Summary() string {
	tested := "inside the tested range " + testedMinor + ".x"
	if !r.Tested {
		tested = "OUTSIDE the tested range " + testedMinor + ".x"
	}
	v := r.Version
	if v == "" {
		v = "unknown"
	}
	return "host gh version " + v + ", " + tested + "; hosts.yml copied: " +
		strconv.FormatBool(r.HostsFile != "") + "; token held for redaction: " + strconv.FormatBool(r.TokenRead)
}

// Describe is the line `yolo check` prints, on the terminal that ran it.
func (r *Runner) Describe() string {
	tested := "inside the tested range " + testedMinor + ".x"
	if !r.Tested {
		tested = "OUTSIDE the tested range " + testedMinor + ".x, so no set applies and every command " +
			"needs its own approval"
	}
	v := r.Version
	if v == "" {
		v = "unknown"
	}
	hosts := "no hosts.yml (no login?)"
	if r.HostsFile != "" {
		hosts = "hosts.yml from " + r.HostsFile
	}
	return "host gh " + r.GhPath + " version " + v + ", " + tested + "; " + hosts +
		"; token held for redaction: " + strconv.FormatBool(r.TokenRead)
}
