// Package banner renders the one-line startup banner that every `yolo`
// subcommand writes to stderr before it does anything, plus the pieces the run
// pipeline's launch line reuses.
//
// # Why a package of its own
//
// Two callers on opposite sides of the CLI have to spell the same line:
// internal/cli's dispatch (which prints it for every registered subcommand) and
// internal/cli/run (whose launch line continues it with the runtime and the
// container name). A third, internal/cli/check, needs the same uname machine
// spelling for its report and used to carry a hand-copied twin of it under a
// comment saying "mirrors internal/cli/run.platformMachine" — a mirror is not a
// mechanism, so both now call Machine here.
//
// # What the banner is for
//
// A pasted bug report has to say which yolo it came from. Before this the
// version reached stderr only from a SUCCESSFUL `yolo run`, emitted after the
// image build and argv assembly — so every report about a launch that died in
// config load, in the nix build, or at the source-skew refusal arrived with no
// version at all, and every report from any other subcommand arrived with none
// either.
//
// Three properties follow from that purpose and are not negotiable:
//
//   - STDERR, never stdout. `yolo config dump`, `yolo config drift`, `yolo
//     describe --json` and `yolo config-ref` all have machine-readable stdout
//     that a banner would corrupt.
//   - No isatty gate. A bug report is `yolo check 2>&1 | pbcopy`; the piped case
//     is precisely the one that gets pasted, so gating on a terminal would
//     suppress the banner exactly where it is wanted.
//   - An escape hatch, SuppressEnv, for the caller that has to have a quiet
//     stderr.
package banner

import (
	"runtime"
	"strings"
)

// SuppressEnv turns the startup banner off when set to any non-empty value.
// Named for the repo's existing YOLO_NO_* hatches (YOLO_NO_TMUX,
// YOLO_NO_HOST_LOOPBACK) and, like them, off by default.
//
// It exists for the caller whose stderr is a contract rather than a log: a
// wrapper that greps `yolo host` output, a test harness diffing stderr, an
// editor integration that surfaces stderr as an error. It does NOT gate on a
// terminal, because the piped case is the one bug reports are pasted from.
const SuppressEnv = "YOLO_NO_BANNER"

// Suppressed reports whether SuppressEnv is set to a non-empty value. getenv is
// injected (os.Getenv at the call sites) so the decision is testable without
// touching the process environment.
func Suppressed(getenv func(string) string) bool {
	return getenv(SuppressEnv) != ""
}

// jailEnv is the discriminator for "this process is running inside a jail". The
// host always sets YOLO_VERSION to a non-empty version string in the container
// env (internal/cli/run/assemble.go), which is why config.InJail,
// check.insideJail, prune, storage and loopholes all read the same variable.
const jailEnv = "YOLO_VERSION"

// Side returns "in-jail" when this process is running inside a yolo jail and
// "host" when it is not.
//
// It is on the banner because it is the first question a bug report raises, and
// the one the version string cannot answer: in a jail YOLO_VERSION wins inside
// version.Get, so the in-jail banner reports the HOST's version verbatim and is
// otherwise indistinguishable from a host banner.
func Side(getenv func(string) string) string {
	if getenv(jailEnv) != "" {
		return "in-jail"
	}
	return "host"
}

// Platform returns "<goos>/<machine>" (e.g. "linux/x86_64"), using the running
// GOOS/GOARCH.
func Platform() string {
	return runtime.GOOS + "/" + Machine(runtime.GOOS, runtime.GOARCH)
}

// Machine maps Go's GOARCH to the uname machine spelling for the given GOOS. It
// is a pure function of (goos, goarch) so every OS/arch combo is unit-testable,
// not just the one the tests happen to run on. NOT Go's amd64/arm64:
// amd64→x86_64 everywhere; arm64→aarch64 ONLY on Linux — on macOS/Apple Silicon
// the machine name is "arm64" (audit 2026-07-18 §C: the unconditional
// arm64→aarch64 map was wrong on macOS and a test locked the bug). Any other
// GOARCH passes through unchanged.
func Machine(goos, goarch string) string {
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

// Startup renders the startup banner line — no trailing newline, so the caller
// decides the terminator:
//
//	yolo-jail 0.8.0+881.ga6f61864 | linux/x86_64 | host
//	yolo-jail 0.8.0+881.ga6f61864 | linux/x86_64 | in-jail
//
// version is version.Get's answer (already "unknown" when nothing resolved), so
// this function never has to describe the absence of one.
func Startup(version string, getenv func(string) string) string {
	return strings.Join([]string{
		"yolo-jail " + version,
		Platform(),
		Side(getenv),
	}, " | ")
}
