// Package check implements the `yolo check` command. It orchestrates every
// preflight probe the doctor cares about — container runtime, nix, macOS
// plumbing, global storage, config validation, entrypoint dry-run, GPU, KVM,
// image build, container image presence, running jails, loopholes (+ broker
// creds freshness / per-jail service liveness), disk usage, and inline
// loopholes — and prints a PASS/WARN/FAIL report ending in a pass/warn/fail
// summary (exit 0 = no failures, 1 = any fail).
//
// The pure diagnostic engines it leans on live elsewhere:
// internal/nixdiag (nix-build classifier, dry-run parser, builder-config
// parser, duration formatter, self-check splitter), internal/config (load,
// merge, validate), internal/loopholes (discovery + validation + resolver),
// internal/storage, internal/runtime, internal/image, internal/version. This
// package supplies ONLY the orchestration and the side-effecting subprocess /
// filesystem probes that feed those engines.
//
// Output contract: the report reproduces the SECTION ORDERING, the
// PASS/WARN/FAIL badge semantics + counts, the exit code, and the control flow
// (parse errors exit before merged validation before dry-run). The exact ANSI
// bytes are pinned by an ANSI-stripped golden. Diagnostic STRINGS that carry
// meaning (nix remedy, config validation errors, creds-freshness messages) come
// from the diagnostic engines and are byte-exact.
package check

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// ExecResult is the outcome of a subprocess probe. Ran is false when the
// binary was absent or the process could not be started; Timeout is true when
// the call exceeded its deadline. Both degrade gracefully — callers treat a
// non-Ran/Timeout result as a soft failure, never a crash.
type ExecResult struct {
	Stdout  string
	Stderr  string
	RC      int
	Ran     bool
	Timeout bool
}

// Options configures a Check run. Every side-effecting seam is injectable so
// the whole section sequence is deterministically ; nil/zero fields
// are filled with real implementations by fillDefaults.
type Options struct {
	// Build mirrors the --build/--no-build flag (default true when wired).
	Build bool
	// Now is the clock seam for the broker creds-freshness check (the only
	// time-dependent output). nil => time.Now.
	Now func() time.Time
	// Version overrides the reported version string (the "Version: …" line).
	// "" => version.Get(repoRoot). Injected so goldens don't depend on the
	// test host's git describe.
	Version string
	// Getenv reads environment variables (YOLO_VERSION, YOLO_RUNTIME, …).
	// nil => os.Getenv.
	Getenv func(string) string
	// LookPath resolves an executable on PATH. nil => real.
	LookPath func(string) (string, bool)
	// Exec runs a subprocess with a timeout in the given working directory (""
	// = inherit the current dir) and extra environment entries ("KEY=VALUE",
	// appended to the parent env). nil => real. Tests install a stub that
	// matches on argv and ignores dir/env.
	Exec func(argv []string, dir string, env []string, timeout time.Duration) ExecResult
	// Stdout is where the report is written. nil => os.Stdout.
	Stdout io.Writer
	// Stdin is read for the orphan-jail cleanup prompt. nil => never prompt
	// (treated as "N").
	Stdin io.Reader
	// Color enables ANSI styling. The ANSI-stripped output is identical to the
	// Color=false output (verified by test), so goldens pin Color=false. It is
	// only honored when IsTTYStdout() is also true (never leak ANSI to a pipe).
	Color bool
	// IsTTYStdout reports whether Stdout is a real terminal — the color gate, so
	// ANSI reaches only a terminal. nil => the shared internal/tty ioctl probe on
	// os.Stdout. Tests inject a constant to force color on/off over a buffer.
	IsTTYStdout func() bool
	// SkipEnsureStorage suppresses the ensure_global_storage() side effect (dir
	// creation). Production leaves it false; tests set it so repeated runs over
	// a shared HOME see a stable Global Storage section.
	SkipEnsureStorage bool
	// IsMacOS overrides the compile-time platform (macOS-stubbed fixtures).
	IsMacOS bool
	// Machine is platform.machine() (x86_64 / aarch64). "" => derived.
	Machine string
	// Workspace is the directory whose yolo-jail.jsonc is validated. "" => cwd.
	Workspace string
	// RepoRoot resolves the yolo-jail repo root. nil => default resolver.
	// Returns (path, ok); ok=false means the repo could not be located.
	RepoRoot func() (string, bool)
	// PathExists tests filesystem presence (device nodes, /nix, CDI specs,
	// creds file, flake.nix). nil => os.Stat.
	PathExists func(string) bool

	// BuilderSetupDone reports whether `yolo builder setup` has wired the Nix
	// daemon to the on-demand Linux builder (macOS only). nil => real probe.
	BuilderSetupDone func() bool
	// BuilderKeyInstalled reports whether the builder VM's one-time ssh key is
	// present (installed on its first boot). nil => real probe.
	BuilderKeyInstalled func() bool
	// EnsureBuilder starts the on-demand Linux builder if needed (macOS only),
	// returning (started, err). err is "" on success, or a short reason
	// ("needs first-boot", …). nil => real
	// implementation. onProgress receives boot status messages.
	EnsureBuilder func(onProgress func(string)) (bool, string)
	// BuildImage runs the real `nix build .#ociImage`
	// and returns (storePath, stderrTail). storePath is "" on failure. nil =>
	// real implementation.
	BuildImage func(repoRoot string, extraPackages []any) (string, []string)
	// AccessRW tests read+write access for device-node checks (KVM /
	// ROCm). nil => real syscall (Linux). Injected so device sections golden.
	AccessRW func(string) bool
	// NodeGID returns the owning GID of a device node and its group name,
	// plus ok=false when the node can't be stat'd. nil => real.
	NodeGID func(string) (gid int, groupName string, ok bool)
	// InUserGroups reports whether gid is in the process's supplementary groups. nil => real.
	InUserGroups func(gid int) bool
}

func fillDefaults(o *Options) {
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
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return tty.IsTerminalFile(os.Stdout) }
	}
	if o.Machine == "" {
		o.Machine = pythonMachine()
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
		o.RepoRoot = func() (string, bool) { return resolveRepoRoot(o.Getenv) }
	}
	if o.BuilderSetupDone == nil {
		o.BuilderSetupDone = builderSetupDone
	}
	if o.BuilderKeyInstalled == nil {
		o.BuilderKeyInstalled = builderKeyInstalled
	}
	if o.EnsureBuilder == nil {
		o.EnsureBuilder = ensureBuilderReal
	}
	if o.BuildImage == nil {
		o.BuildImage = buildImageReal
	}
	if o.AccessRW == nil {
		o.AccessRW = accessRW
	}
	if o.NodeGID == nil {
		o.NodeGID = nodeGIDReal
	}
	if o.InUserGroups == nil {
		o.InUserGroups = inUserGroupsReal
	}
}

// inJail reports whether we are running inside a jail. The host always sets
// YOLO_VERSION to a real (non-empty) version string inside a jail, so a
// non-empty read is the reliable, test-injectable signal — the theoretical
// empty-but-set case never occurs in real operation.
func (o *Options) inJail() bool {
	return o.Getenv("YOLO_VERSION") != ""
}

// pythonMachine returns the uname machine spelling for the running platform
// (x86_64 / aarch64 / arm64), NOT Go's amd64/arm64.
func pythonMachine() string {
	return machineForPlatform(runtime.GOOS, runtime.GOARCH)
}

// machineForPlatform maps Go's GOARCH to the uname machine spelling
// for the given GOOS. Pure so every OS/arch combo is unit-testable, not just the
// host's: amd64→x86_64 everywhere; arm64→aarch64 ONLY off macOS — on macOS/Apple
// Silicon the machine name is "arm64" (audit 2026-07-18 §C: an unconditional
// arm64→aarch64 map reported "aarch64" on darwin, diverging from the run banner).
// Mirrors internal/cli/run.platformMachine. Any other GOARCH passes through.
func machineForPlatform(goos, goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		if goos != "darwin" {
			return "aarch64" // Linux uname; macOS keeps arm64
		}
		return "arm64"
	default:
		return goarch
	}
}

// realExec runs argv with a timeout, capturing stdout/stderr as text. A missing
// binary or start failure yields Ran=false; a deadline overrun yields
// Timeout=true — the two graceful-degradation branches probe callers rely on.
// dir sets the working directory (""=inherit); env entries are appended to
// os.Environ().
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
	select {
	case <-time.After(timeout):
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

// NewDefaultOptions returns Options with the real platform predicate and build
// enabled — the shape the CLI front door passes (then overrides Build from the
// flag).
func NewDefaultOptions() Options {
	return Options{Build: true, IsMacOS: paths.IsMacOS}
}
