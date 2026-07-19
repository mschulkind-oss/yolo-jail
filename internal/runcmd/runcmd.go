// Package runcmd is the Go port of the `yolo run` command body
// (src/cli/run_cmd.py:run + its lifecycle helpers) — the container-startup
// command and the heaviest, most side-effect-laden module in the CLI. It builds
// the full podman / Apple-Container argv (mounts, env, network, devices, GPU,
// kvm, loopholes), prestarts the host-side service plumbing, and either execs
// into an existing container or launches a fresh one.
//
// The pure engines it orchestrates are already ported and byte-verified and are
// REUSED here, never reimplemented: internal/runmount (mount-arg builders),
// internal/network (port-forward argv), internal/runtime (naming, tracking,
// liveness tri-state), internal/image (image load/build), internal/agentsmd
// (briefings + skills), internal/storage (global storage, host probes),
// internal/loopholes (discover + runtime args), internal/config (load, merge,
// validate, snapshot), internal/agents (specs + yolo-flag injection),
// internal/shquote (shell quoting), internal/ttyproxy (the TTY proxy).
//
// This package supplies ONLY the orchestration and the ordered-argv assembly.
// The plan's Stage 16 exit criteria pin the ORDERED container argv byte-for-byte
// (flags-before-image, `-e` at index(image), mount order), so the argv assembly
// is factored into a golden-able builder (assemble.go) driven off the injected
// seams.
//
// `run` is the default subcommand (bare `yolo -- cmd` → run).
package runcmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ExecResult is the outcome of a short subprocess probe (git/jj identity,
// lsusb, runtime lookups). Ran is false when the binary was absent or the
// process could not be started (the Python FileNotFoundError branch); Timeout
// is true when the call exceeded its deadline. Both degrade gracefully — the
// Python `except` clauses swallow them.
type ExecResult struct {
	Stdout  string
	Stderr  string
	RC      int
	Ran     bool
	Timeout bool
}

// Options configures a run(). The CLI flags map to the leading fields; every
// side-effecting seam below is injectable so the probe + argv-assembly paths
// are deterministically unit-testable and golden-able. nil/zero fields are
// filled with real implementations by fillDefaults.
type Options struct {
	// --- CLI surface (typer options + ctx.args) ---
	// Network mirrors --network (default "bridge").
	Network string
	// New mirrors --new (force a fresh container).
	New bool
	// Profile mirrors --profile (startup timing report).
	Profile bool
	// DryRun mirrors --dry-run (macos-user only; a hard error elsewhere).
	DryRun bool
	// Args is ctx.args — the command after `--` (empty → interactive bash).
	Args []string

	// --- seams ---
	// Now is the clock seam. nil => time.Now.
	Now func() time.Time
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
	// IsMacOS overrides the compile-time platform.
	IsMacOS bool
	// Workspace is Path.cwd() — the directory whose jail is launched. "" => cwd.
	Workspace string
	// RepoRoot resolves the yolo-jail repo root for nix builds (with the
	// installed-wheel staging invariant). Returns (path, ok); ok=false is the
	// Python SystemExit(1) branch. nil => default resolver.
	RepoRoot func() (string, bool)
	// PathExists tests filesystem presence. nil => os.Stat.
	PathExists func(string) bool
	// Getpid returns the current PID (owner-PID file, out-link name). nil =>
	// os.Getpid.
	Getpid func() int
	// IsTTYStdout / IsTTYStdin report tty-ness (the -t flag, the approval
	// prompt, the tty-proxy fallback). nil => real isatty.
	IsTTYStdout func() bool
	IsTTYStdin  func() bool
	// MacosUserRun handles the runtime==macos-user native branch (Stage 16b —
	// replaces the seam #8 Python delegation). It receives the resolved config,
	// workspace, selected agents, the post-`--` argv, the repo root, and the
	// dry-run flag, returning the process exit code. nil => the branch prints an
	// actionable "not wired" error (keeps runcmd free of the macosuser +
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
		o.RepoRoot = func() (string, bool) { return resolveRepoRoot(o.Getenv, o.Stderr, o.Color) }
	}
	if o.Getpid == nil {
		o.Getpid = os.Getpid
	}
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return isTTY(os.Stdout) }
	}
	if o.IsTTYStdin == nil {
		o.IsTTYStdin = func() bool { return isTTY(os.Stdin) }
	}
}

// inJail mirrors `os.environ.get("YOLO_VERSION") is not None`. The host always
// sets YOLO_VERSION to a real (non-empty) version string inside a jail, so a
// non-empty read is the faithful, test-injectable signal.
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
	// timeout <= 0 means "no deadline" (mirrors the Python subprocess.run calls
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

// isTTY reports whether f is a real terminal, mirroring Python's os.isatty /
// file.isatty() (a TCGETS ioctl), NOT a character-device mode check —
// /dev/null is a char device but not a tty, and a mode check would wrongly add
// the container `-t` flag (observed divergence). See isattyFD (platform split).
func isTTY(f *os.File) bool {
	return isattyFD(int(f.Fd()))
}

// NewDefaultOptions returns Options with the real platform predicate — the
// shape the CLI front door passes (then overrides the flags).
func NewDefaultOptions() Options {
	return Options{Network: "bridge", IsMacOS: paths.IsMacOS}
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
