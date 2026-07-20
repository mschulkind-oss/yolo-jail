package builder

// The on-demand macOS Linux builder:
// VM lifecycle + reachability, and `yolo builder {setup,start,stop,status}`
// command bodies. macOS can't build the aarch64-linux image locally, so Nix offloads to
// a small Linux VM (nixpkgs#darwin.linux-builder); this brings it up on demand
// and lets a launchd idle-timer stop it.
// The PURE generators (ssh_config block, nix builders line, trusted-users
// merge, the single-sudo root script) live alongside in builder.go
// and are REUSED here, never re-ported. This adds the
// lifecycle orchestration (setup-state probing, ensure/poll/start/stop) and the
// command bodies. Every socket / subprocess / PID-file / platform probe is an
// injectable Deps seam so the orchestration logic is unit-testable on Linux;
// RealDeps wires production. The real VM bring-up stays a Mac-runbook step.

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Proc models a spawned VM process for poll-based liveness
// handle threaded through _poll_until_reachable). Poll returns (returncode, done)
// where done=false means still running (Python's proc.poll() is None).
type Proc interface {
	Poll() (int, bool)
}

// Deps are the injectable primitive seams. RealDeps wires production
// implementations; tests substitute fakes. These mirror the low-level helpers
// in builder.py (socket dial, is_file, nix.conf read, PID file, spawn, killpg)
// so the orchestration (builder_setup_state / ensure_builder / poll) is exact.
type Deps struct {
	// IsMacOS reports sys.platform == "darwin" (paths.IS_MACOS).
	IsMacOS func() bool
	// Reachable reports builder_reachable(): TCP accept on 127.0.0.1:BUILDER_PORT.
	Reachable func() bool
	// FileIsFile reports Path(p).is_file().
	FileIsFile func(path string) bool
	// ReadFileText reads a file best-effort (content, ok). Used by
	// _nix_conf_has_builder + builder_log_tail.
	ReadFileText func(path string) (string, bool)
	// NixCustomConfIncluded reports whether /etc/nix/nix.conf includes
	// nix.custom.conf (storage._nix_custom_conf_included); ok=false when unknown.
	NixCustomConfIncluded func() (bool, bool)
	// CurrentTrustedUsers returns the daemon's effective trusted-users
	// (nix config show), best-effort.
	CurrentTrustedUsers func() []string
	// DetectNixDaemonLabel returns the nix-daemon launchd label (or "", false).
	DetectNixDaemonLabel func() (string, bool)
	// HostUser is getpass.getuser().
	HostUser func() string
	// RunSetupScript pipes the root script to `sudo bash -s` (tty inherited for
	// the password prompt). Returns (returncode, ok) — ok=false on spawn error.
	RunSetupScript func(script string) (int, bool)
	// StartVMForeground runs `nix run nixpkgs#darwin.linux-builder` inheriting
	// the TTY (first_boot_interactive); returns an error (KeyboardInterrupt maps
	// to nil in the caller) or nil.
	StartVMForeground func() error
	// StartVMDetached spawns the VM detached and records the PID file; returns
	// (Proc, err). A nil Proc with nil err means "already running".
	StartVMDetached func() (Proc, error)
	// ReadBuilderPID reads the recorded VM PID (pid, ok).
	ReadBuilderPID func() (int, bool)
	// PIDIsLive reports os.kill(pid, 0) success.
	PIDIsLive func(pid int) bool
	// StopVM terminates the detached VM (killpg → kill fallback) and removes the
	// PID file. Returns (ok, errMsg).
	StopVM func() (bool, string)
	// Sleep/Now drive _poll_until_reachable (injected so tests avoid real waits).
	Sleep func(seconds float64)
	Now   func() float64
	// Confirm prompts (typer.confirm) for the setup step.
	Confirm func(prompt string) bool
	// Out receives the human output (rich markup stripped, parity on text).
	Out io.Writer
}

// Poll timing.
const (
	builderStartTimeoutS = 90.0
	builderPollIntervalS = 1.0
)

var richTagRe = regexp.MustCompile(`\[/?[a-zA-Z][^\]]*\]`)

type printer struct{ w io.Writer }

func (p printer) print(msg string)          { fmt.Fprintln(p.w, richTagRe.ReplaceAllString(msg, "")) }
func (p printer) printf(f string, a ...any) { p.print(fmt.Sprintf(f, a...)) }

type SetupState struct {
	SSHConfig  bool
	NixBuilder bool
	Key        bool
	Done       bool
}

type Status struct {
	SetupState
	Reachable   bool
	ConfPath    string
	DaemonLabel string // "" when none
}

// nix.conf.
func confPath(deps Deps) string {
	if inc, ok := deps.NixCustomConfIncluded(); ok && inc {
		return "/etc/nix/nix.custom.conf"
	}
	return "/etc/nix/nix.conf"
}

// uncommented `builders` line naming both aarch64-linux and the ssh host alias.
func nixConfHasBuilder(deps Deps) bool {
	conf := confPath(deps)
	if !deps.FileIsFile(conf) {
		return false
	}
	text, ok := deps.ReadFileText(conf)
	if !ok {
		return false
	}
	for _, raw := range strings.Split(text, "\n") {
		s := strings.TrimSpace(raw)
		if strings.HasPrefix(s, "#") || !strings.HasPrefix(s, "builders") {
			continue
		}
		if strings.Contains(s, "aarch64-linux") && strings.Contains(s, BuilderSSHHost) {
			return true
		}
	}
	return false
}

// BuilderSetupState probes setup state without touching the VM. Mirrors
// builder_setup_state.
func BuilderSetupState(deps Deps) SetupState {
	sshOK := deps.FileIsFile(SSHConfigPath())
	keyOK := deps.FileIsFile(BuilderKeyPath)
	nixOK := nixConfHasBuilder(deps)
	return SetupState{
		SSHConfig:  sshOK,
		NixBuilder: nixOK,
		Key:        keyOK,
		Done:       sshOK && nixOK,
	}
}

// BuilderStatus is the full read-only snapshot.
func BuilderStatus(deps Deps) Status {
	label, _ := deps.DetectNixDaemonLabel()
	return Status{
		SetupState:  BuilderSetupState(deps),
		Reachable:   deps.Reachable(),
		ConfPath:    confPath(deps),
		DaemonLabel: label,
	}
}

// RunSetup does the whole privileged wiring in ONE sudo.
// builds the root script (reusing internal/builder + the resolved daemon label)
// and pipes it to `sudo bash -s`. Returns (ok, errMsg).
func RunSetup(deps Deps, maxJobs int, me string) (bool, string) {
	label, _ := deps.DetectNixDaemonLabel()
	script := SetupRootScript(maxJobs, me, deps.CurrentTrustedUsers(), confPath(deps), label)
	rc, ok := deps.RunSetupScript(script)
	if !ok {
		return false, "privileged setup failed to run"
	}
	if rc != 0 {
		return false, fmt.Sprintf("privileged setup exited %d", rc)
	}
	return true, ""
}

// FirstBootInteractive runs the VM's one-time first boot in the foreground.
// installed (or reachability). Returns (ok, errMsg).
func FirstBootInteractive(deps Deps) (bool, string) {
	if err := deps.StartVMForeground(); err != nil {
		return false, err.Error()
	}
	if deps.FileIsFile(BuilderKeyPath) || deps.Reachable() {
		return true, ""
	}
	return false, "ssh key still not installed after first boot"
}

// EnsureBuilder makes the builder ready to accept a build, starting it if
// needed.
// onProgress is optional (nil = silent).
func EnsureBuilder(deps Deps, onProgress func(string)) (bool, string) {
	if !deps.IsMacOS() {
		return false, "not macOS"
	}
	if deps.Reachable() {
		return true, ""
	}
	state := BuilderSetupState(deps)
	if !state.Done {
		return false, "not set up"
	}
	if !state.Key {
		return false, "needs first-boot"
	}
	if onProgress != nil {
		onProgress("starting Linux builder VM (first boot downloads it)…")
	}
	proc, err := deps.StartVMDetached()
	if err != nil {
		return false, "could not start builder: " + err.Error()
	}
	ok, reason := pollUntilReachable(deps, proc, onProgress)
	if ok {
		return true, ""
	}
	return false, reason
}

// pollUntilReachable polls the builder's SSH port until it answers, the child
// dies, or timeout.
// short-circuit with a log-tail-derived reason, and the final re-check).
func pollUntilReachable(deps Deps, proc Proc, onProgress func(string)) (bool, string) {
	deadline := deps.Now() + builderStartTimeoutS
	waited := 0
	for deps.Now() < deadline {
		if deps.Reachable() {
			return true, ""
		}
		if proc != nil {
			if _, done := proc.Poll(); done {
				last := lastLogLine(deps)
				if last == "" {
					last = "no output"
				}
				return false, "builder process exited early (" + last + ")"
			}
		}
		if onProgress != nil {
			onProgress(fmt.Sprintf("waiting for Linux builder VM to boot… (%ds)", waited))
		}
		deps.Sleep(builderPollIntervalS)
		// Python: waited += int(interval_s) or 1
		inc := int(builderPollIntervalS)
		if inc == 0 {
			inc = 1
		}
		waited += inc
	}
	if deps.Reachable() {
		return true, ""
	}
	hint := ""
	if last := lastLogLine(deps); last != "" {
		hint = "; last log line: " + last
	}
	return false, fmt.Sprintf("not reachable within %ds%s", int(builderStartTimeoutS), hint)
}

// lastLogLine returns the last non-empty line of the builder log tail (matching
// builder_log_tail(...).splitlines()[-1] usage). "" when the log is empty.
func lastLogLine(deps Deps) string {
	text, ok := deps.ReadFileText(BuilderLogFilePath())
	if !ok {
		return ""
	}
	// builder_log_tail: last 12 lines joined + strip; callers take [-1]. The net
	// effect for the reason string is the last non-empty line of the file.
	lines := strings.Split(text, "\n")
	tail := lines
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	joined := strings.TrimSpace(strings.Join(tail, "\n"))
	if joined == "" {
		return ""
	}
	parts := strings.Split(joined, "\n")
	return parts[len(parts)-1]
}
