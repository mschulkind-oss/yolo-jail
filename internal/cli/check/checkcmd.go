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
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/banner"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
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
	// AcceptConfigChanges pre-approves workspace config and writes the approval snapshot host-side (OQ-S2).
	AcceptConfigChanges bool
	// Now is the clock seam for time-dependent output: broker credential
	// freshness and the age of the cached update answer. nil => time.Now.
	Now func() time.Time
	// Version overrides the reported version string (the "Version: …" line).
	// "" => version.Get(repoRoot). Injected so goldens don't depend on the
	// test host's git describe.
	Version string
	// Getenv reads environment variables (YOLO_VERSION, YOLO_RUNTIME, …).
	// nil => os.Getenv.
	Getenv func(string) string
	// UpdateChannel, UpdateStatePath and UpdateCheckEnabled feed the Updates
	// section, which reads the cached update check and never the network. nil /
	// "" => selfupdate.Current, selfupdate.StatePath(), config.UpdateCheckEnabled.
	UpdateChannel      func() selfupdate.Channel
	UpdateStatePath    string
	UpdateCheckEnabled func() bool
	// LookPath resolves an executable on PATH. nil => real.
	LookPath func(string) (string, bool)
	// MiseNode returns the path to a mise-installed node binary (for the nix-ld
	// env-free tripwire), or "" if none is present. nil => a real glob of
	// /mise/installs/node/*/bin/node. Injected so the drift-detection branches
	// are unit-testable without a real mise install.
	MiseNode func() string
	// Exec runs a subprocess with a timeout in the given working directory (""
	// = inherit the current dir) and extra environment entries ("KEY=VALUE",
	// appended to the parent env). nil => real. Tests install a stub that
	// matches on argv and ignores dir/env.
	Exec func(argv []string, dir string, env []string, timeout time.Duration) ExecResult
	// Stdout is where the report is written. nil => os.Stdout.
	Stdout io.Writer
	// Stderr is where a DISCLOSURE is written — never the report. The report is a
	// formatted artifact a caller may redirect or ask for as JSON (Format), while a
	// disclosure is a fact about this machine that OQ-RO3 says no tier may gate, so the
	// two cannot share a stream. Today the base-home walk is the only writer.
	// nil => os.Stderr.
	Stderr io.Writer
	// Stdin is read for the orphan-jail cleanup prompt. nil => never prompt
	// (treated as "N").
	Stdin io.Reader
	// PodmanReadiness is the podman readiness gate's seams (podmanready.go): the attempt
	// runner, its clock and its sleep. A zero field takes the real one. check runs the SAME
	// gate as a launch (PR-D6 of docs/design/podman-reboot-readiness.md), so the two cannot
	// disagree about whether podman is up.
	PodmanReadiness yoloruntime.ReadySeams
	// IsTTYStderr reports whether Stderr is a terminal: the gate's progress line redraws in
	// place only there, and is written as lines anywhere else. nil => the ioctl probe on
	// os.Stderr.
	IsTTYStderr func() bool
	// storageErr is what EnsureGlobalStorage returned, so the Global Storage section can say why
	// a directory it should have created is missing. nil when it succeeded or did not run.
	storageErr error
	// podmanReady is the gate's result, asked once per check and read by every section that
	// needs a podman answer on Linux. nil until the first asks.
	podmanReady *yoloruntime.ReadyResult
	// Color enables ANSI styling. The ANSI-stripped output is identical to the
	// Color=false output (verified by test), so goldens pin Color=false. It is
	// only honored when IsTTYStdout() is also true (never leak ANSI to a pipe).
	Color bool
	// Format is the output format: "" / outfmt.Text (the human report,
	// unchanged) or outfmt.JSON. The CLI front door resolves the flag family.
	//
	// It changes only the RENDERING. Every section still runs, in the same order,
	// with the same side effects — including the nix image build under --build —
	// because `check --format json` is asked the same question as `check`, and a
	// JSON run that quietly probed less would be a different command wearing the
	// same name. See jsonreport.go.
	Format string
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
	// Geteuid and PathIsDir are two of the macos-user launch's precondition probes
	// (macosuser.LaunchProbes) that no other section needs. nil => os.Geteuid and an
	// os.Stat that reports a directory.
	Geteuid   func() int
	PathIsDir func(string) bool
	// Machine is platform.machine() (x86_64 / aarch64). "" => derived.
	Machine string
	// Workspace is the directory whose yolo-jail.jsonc is validated. "" => cwd.
	Workspace string
	// RepoRoot resolves the yolo-jail repo root. nil => default resolver.
	// Returns (resolution, ok); ok=false means the repo could not be located.
	// The resolution carries what SELECTED the root, reported alongside the
	// flake.nix line so check answers "which flake, and why that one".
	RepoRoot func() (reporoot.Resolution, bool)
	// PathExists tests filesystem presence (device nodes, /nix, CDI specs,
	// creds file, flake.nix). nil => os.Stat.
	PathExists func(string) bool
	// ioSysRoot is the root the disk resolver reads the mount table and sysfs under
	// (internal/ioprio.Resolve, for sectionIOPriority). "" => the real root; tests point it
	// at a fake tree.
	ioSysRoot string
	// nixHostRoot is the root the Nix sections read the host's nix config (/etc/nix) and launchd
	// plists (/Library/LaunchDaemons) under, to name the file and the restart a hint gives; the
	// hint still prints the real paths. "" => the real root; tests point it at a fake tree.
	nixHostRoot string
	// cdiHostRoot is the root the AMD section reads CDI spec dirs and containers.conf under
	// (run.FindAMDCDISpec). "" => the real root; tests point it at a fake tree.
	cdiHostRoot string
	// nixVersion is `nix --version`'s answer, asked once per check and read by the Nix
	// section's version row and by every hint that depends on which Nix this is. nil until the
	// first asks.
	nixVersion *ExecResult
	// currentUser is the account this check runs as, which the untrusted-user hint names: on the
	// macos-user backend that is the sandbox's own account, not the human who pastes the fix in
	// their own terminal. nil => os/user's current user, $USER when that has no name.
	currentUser func() string

	// BuildImage runs the real `nix build .#ociImage`
	// and returns (storePath, stderrTail). storePath is "" on failure. nil =>
	// real implementation.
	BuildImage func(repoRoot string, extraPackages []any) (string, []string)
	// Writable reports whether this process may write path — access(2) W_OK, which answers
	// EROFS for a read-only bind and is consulted by the macOS sandbox. Read for the in-jail
	// workspace-config lock line. nil => real syscall.
	Writable func(string) bool
	// AccessRW tests read+write access for device-node checks (KVM /
	// ROCm). nil => real syscall (Linux). Injected so device sections golden.
	AccessRW func(string) bool
	// NodeGID returns the owning GID of a device node and its group name,
	// plus ok=false when the node can't be stat'd. nil => real.
	NodeGID func(string) (gid int, groupName string, ok bool)
	// InUserGroups reports whether gid is in the process's supplementary groups. nil => real.
	InUserGroups func(gid int) bool
	// HostFloor builds the host agent floor the "Host agent floor" section reads, for the
	// selection's programs. The CLI wires the launch's own construction (so `yolo check` and
	// `yolo host --` read one floor); nil => the prefix under this home with the user-scope
	// `host_floor`, which is all a disposition reads.
	HostFloor func(progs []hostfloor.Program) *hostfloor.Floor

	// selectedPacks is the SELECTED pack set sectionPacks resolved, handed forward to the
	// host-wrappers section so it can ask "which programs do these packs install that have
	// no wrapper?" without loading the packs a second time — and without a second loader
	// free to disagree with the one that just validated them. Not a seam: sections write
	// it, never a caller.
	//
	// selectedPacksKnown distinguishes "resolved, and empty" from "sectionPacks never ran"
	// (a test driving one section directly), so the wrappers section only states a
	// completeness fact when it has one.
	selectedPacks      []*packload.Pack
	selectedPacksKnown bool
}

func fillDefaults(o *Options) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.UpdateChannel == nil {
		o.UpdateChannel = selfupdate.Current
	}
	if o.UpdateStatePath == "" {
		o.UpdateStatePath = selfupdate.StatePath()
	}
	if o.UpdateCheckEnabled == nil {
		o.UpdateCheckEnabled = config.UpdateCheckEnabled
	}
	if o.LookPath == nil {
		o.LookPath = func(name string) (string, bool) {
			p, err := exec.LookPath(name)
			return p, err == nil
		}
	}
	if o.MiseNode == nil {
		o.MiseNode = realFirstMiseNode
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
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return tty.IsTerminalFile(os.Stdout) }
	}
	if o.IsTTYStderr == nil {
		o.IsTTYStderr = func() bool { return tty.IsTerminalFile(os.Stderr) }
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
	if o.Geteuid == nil {
		o.Geteuid = os.Geteuid
	}
	if o.PathIsDir == nil {
		o.PathIsDir = func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && fi.IsDir()
		}
	}
	if o.RepoRoot == nil {
		o.RepoRoot = func() (reporoot.Resolution, bool) { return resolveRepoRoot(o.Getenv) }
	}
	if o.BuildImage == nil {
		o.BuildImage = buildImageReal
	}
	if o.AccessRW == nil {
		o.AccessRW = accessRW
	}
	if o.Writable == nil {
		o.Writable = writableReal
	}
	if o.NodeGID == nil {
		o.NodeGID = nodeGIDReal
	}
	if o.InUserGroups == nil {
		o.InUserGroups = inUserGroupsReal
	}
	if o.currentUser == nil {
		o.currentUser = func() string {
			if u, err := user.Current(); err == nil && u.Username != "" {
				return u.Username
			}
			return o.Getenv("USER")
		}
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

// machineForPlatform maps Go's GOARCH to the uname machine spelling for the
// given GOOS — amd64→x86_64 everywhere; arm64→aarch64 ONLY off macOS, since on
// macOS/Apple Silicon the machine name is "arm64" (audit 2026-07-18 §C: an
// unconditional arm64→aarch64 map reported "aarch64" on darwin, diverging from
// the run banner).
//
// It used to be a hand-copied twin of the banner's own mapping, under a comment
// saying "mirrors internal/cli/run.platformMachine" — a mirror is not a
// mechanism, and the divergence that audit found is what a mirror costs. It
// delegates now; the name stays because internal/darwinpkg names it.
func machineForPlatform(goos, goarch string) string {
	return banner.Machine(goos, goarch)
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
	// A nix (`nix --version`, the dry-run, `nix config show`) starts through the tracked set
	// (internal/nixchildren), so a signal sent to the check alone stops it; every other probe
	// starts bare. Its release is this function's once Wait returned.
	release, err := nixchildren.StartIfNix(cmd)
	if err != nil {
		return ExecResult{Ran: false}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		release()
		return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Ran: true, Timeout: true}
	case err := <-done:
		release()
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
