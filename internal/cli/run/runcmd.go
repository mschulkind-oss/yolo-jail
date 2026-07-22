// Package run implements the `yolo run` command — the container-startup
// command and the heaviest module in the CLI. It builds the full podman /
// Apple-Container argv (mounts, env, network, devices, GPU, kvm, loopholes),
// prestarts the host-side service plumbing, and either execs into an existing
// container or launches a fresh one.
// `run` is the default subcommand (bare `yolo -- cmd` → run).
package run

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// ExecResult is the outcome of a short subprocess probe (git/jj identity,
// lsusb, runtime lookups). Ran is false when the binary was absent or the
// process could not be started; Timeout is true when the call exceeded its
// deadline. Both degrade gracefully — callers swallow them.
type ExecResult struct {
	Stdout  string
	Stderr  string
	RC      int
	Ran     bool
	Timeout bool
}

// Options configures a run(). The CLI flags map to the leading fields; every
// side-effecting seam below is injectable so the probe + argv-assembly paths
// are deterministically unit-testable. nil/zero fields are
// filled with real implementations by fillDefaults.
type Options struct {
	// --- CLI surface (typer options + ctx.args) ---
	Network string
	New     bool
	// Profile is --profile (startup timing report).
	Profile bool
	// DryRun is --dry-run (macos-user only; a hard error elsewhere).
	DryRun bool
	// Args is ctx.args — the command after `--` (empty → interactive bash).
	Args []string

	// --- seams ---
	// Now is the clock seam. nil => time.Now.
	Now func() time.Time
	// RelayKillGrace is the SIGTERM→SIGKILL drain window in relayKill. 0 =>
	// relayKillGraceDefault (3s). Injectable ONLY to shrink it in tests (the
	// drain always waits the real wall clock, so 3s of real sleep otherwise
	// dominates the unit suite); production always uses the default.
	RelayKillGrace time.Duration
	// Getenv reads environment variables. nil => os.Getenv.
	Getenv func(string) string
	// LookPath resolves an executable on PATH (shutil.which). nil => real.
	LookPath func(string) (string, bool)
	// Exec runs a short subprocess probe with a timeout in dir ("" = inherit)
	// with extra env entries ("KEY=VALUE", appended to the parent env). nil =>
	// real. Used for git/jj identity, lsusb, runtime version/liveness probes.
	Exec func(argv []string, dir string, env []string, timeout time.Duration) ExecResult
	// Stdout/Stderr receive the human output (console.print goes to stderr in
	// rich by default for status; run() uses console (stdout) for most lines).
	// nil => os.Stdout / os.Stderr.
	Stdout io.Writer
	Stderr io.Writer
	// Stdin is read for the config-change approval prompt. nil => os.Stdin.
	Stdin io.Reader
	// Color enables ANSI styling in the human output.
	Color bool
	// IsMacOS / IsLinux override the compile-time platform. Both exist because
	// the argv assembly asks two different questions ("is this a mac host?" for
	// the mise named-volume, "is this a Linux host?" for podman's
	// --read-only-tmpfs) and a golden test must be able to pin each one
	// independently of the host it runs on. Reading paths.IsLinux /
	// paths.IsMacOS directly from assembly code bypasses the seam and makes the
	// golden argv host-dependent — that is exactly how
	// TestAssembleRunCmdPodmanLinuxGolden started failing on the macOS runner.
	IsMacOS bool
	IsLinux bool
	// Workspace is Path.cwd() — the directory whose jail is launched. "" => cwd.
	Workspace string
	// RepoRoot resolves the yolo-jail repo root for nix builds (with the
	// installed-wheel staging invariant). Returns (path, ok); ok=false is the
	// exit(1) branch. nil => default resolver.
	RepoRoot func() (string, bool)
	// PathExists tests filesystem presence. nil => os.Stat.
	PathExists func(string) bool
	// Getpid returns the current PID (owner-PID file, out-link name). nil =>
	// os.Getpid.
	Getpid func() int
	// PIDAlive probes whether a recorded PID is still running — the gate in
	// front of every kill/reap decision. nil => pidAlive. Injectable because a
	// test that hands real PIDs to relayKill is signalling real processes: a
	// PID reaped moments earlier can already have been RECYCLED (macOS wraps at
	// PID_MAX 99999, four orders of magnitude below Linux's default 4194304),
	// and the drain loop then SIGTERMs — and 3s later SIGKILLs — some unrelated
	// process, quite possibly a sibling `go test` binary.
	PIDAlive func(int) bool
	// IsTTYStdout / IsTTYStdin report tty-ness (the -t flag, the approval
	// prompt, the tty-proxy fallback). nil => real isatty.
	IsTTYStdout func() bool
	IsTTYStdin  func() bool
	// MacosUserRun handles the runtime==macos-user native branch. It receives the resolved config,
	// workspace, selected agents, the post-`--` argv, the repo root, and the
	// dry-run flag, returning the process exit code. nil => the branch prints an
	// actionable "not wired" error (keeps run free of the macosuser +
	// darwinpkg deps; the front door injects the real handler).
	MacosUserRun func(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string, repoRoot string, dryRun bool) int
}

func fillDefaults(o *Options) {
	if o.Network == "" {
		o.Network = "bridge"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RelayKillGrace == 0 {
		o.RelayKillGrace = relayKillGraceDefault
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.LookPath == nil {
		o.LookPath = func(name string) (string, bool) {
			p, err := exec.LookPath(name)
			return p, err == nil
		}
	}
	if o.Exec == nil {
		o.Exec = realExec
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			o.Workspace = wd
		} else {
			o.Workspace = "."
		}
	}
	if o.PathExists == nil {
		o.PathExists = func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		}
	}
	if o.RepoRoot == nil {
		// stderr is nil: repo-root resolution is no longer fatal (D2 degraded
		// launch), so the resolver must not print its "Cannot find repo root" fix
		// hint. Run() emits a softer degraded notice when repoRoot comes back "".
		o.RepoRoot = func() (string, bool) { return resolveRepoRoot(o.Getenv, nil, o.Color) }
	}
	if o.Getpid == nil {
		o.Getpid = os.Getpid
	}
	if o.PIDAlive == nil {
		o.PIDAlive = pidAlive
	}
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return isTTY(os.Stdout) }
	}
	if o.IsTTYStdin == nil {
		o.IsTTYStdin = func() bool { return isTTY(os.Stdin) }
	}
}

// inJail reports whether YOLO_VERSION is set. The host always sets
// YOLO_VERSION to a real (non-empty) version string inside a jail, so a
// non-empty read is the test-injectable signal.
func (o *Options) inJail() bool {
	return o.Getenv("YOLO_VERSION") != ""
}

// realExec runs argv with a timeout, capturing stdout/stderr as text. A missing
// binary or start failure yields Ran=false; a deadline overrun yields
// Timeout=true. dir sets the working directory (""=inherit); env entries are
// appended to os.Environ().
func realExec(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
	if len(argv) == 0 {
		return ExecResult{}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return ExecResult{Ran: false}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// timeout <= 0 means "no deadline" (matches the subprocess.run calls
	// that pass no timeout, e.g. find_running_container / find_existing_container).
	var timer <-chan time.Time
	if timeout > 0 {
		timer = time.After(timeout)
	}
	select {
	case <-timer:
		_ = cmd.Process.Kill()
		<-done
		return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Ran: true, Timeout: true}
	case err := <-done:
		rc := 0
		if cmd.ProcessState != nil {
			rc = cmd.ProcessState.ExitCode()
		}
		_ = err
		return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), RC: rc, Ran: true}
	}
}

// isTTY reports whether f is a real terminal via a TCGETS ioctl, NOT a
// character-device mode check — /dev/null is a char device but not a tty, and a
// mode check would wrongly add the container `-t` flag (observed divergence).
// The ioctl lives in the shared internal/tty package (platform split there).
func isTTY(f *os.File) bool {
	return tty.IsTerminalFile(f)
}

// NewDefaultOptions returns Options with the real platform predicate — the
// shape the CLI front door passes (then overrides the flags).
func NewDefaultOptions() Options {
	return Options{Network: "bridge", IsMacOS: paths.IsMacOS, IsLinux: paths.IsLinux}
}

// RunWithProxy launches argv under the platform-appropriate TTY proxy (Linux:
// internal/ttyproxy; other: a plain foreground exec) and returns the child exit
// code, or 1 on a launch error. It is the run-proxy seam the front door injects
// into macosuser (whose RunWithProxy field is `func([]string) int`), so the
// macos-user path never imports the Linux-only ttyproxy package directly (which
// would break the GOOS=darwin build).
func RunWithProxy(argv []string) int {
	rc, err := runWithProxy(argv, nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch failed: %v\n", err)
		return 1
	}
	return rc
}
