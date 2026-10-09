package containerbuilder

import (
	"fmt"
	"io"
	"net"
	"os"
	goruntime "runtime"
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// session.go is the on-demand builder lifecycle (J3): when a macOS `packages:`
// build isn't cached, nix must offload to Linux. Instead of a second
// hypervisor, this starts a tiny nix+sshd builder as a CONTAINER on the runtime
// already up (podman / Apple Container) and hands nix a `--builders` line
// pointing at it over ssh-ng.
//
// The pure argv/URI/builders-line constructors live in containerbuilder.go; this
// file adds the pull → run → wait-reachable → stop orchestration with injectable
// seams so it is unit-testable without a real runtime. The subprocess bodies
// (RunReal etc.) are wired by the caller; the behavioral end-to-end is the
// mac-ac-container-builder runbook (Track M — PASSED on real HW 2026-07-17).

// Deps are the injectable subprocess/clock seams for a builder Session.
type Deps struct {
	// Run runs argv (inherit stdio) and returns the return code.
	Run func(argv []string) int
	// Output runs argv and returns (stdout, rc) — `container ls` ADDR discovery on
	// Apple Container, and the `ssh-keyscan` readiness probe on BOTH runtimes.
	Output func(argv []string) (string, int)
	// Reachable reports whether host:port accepts a TCP connection. A cheap negative
	// filter ONLY: on podman machine an accepted connection says nothing about
	// whether sshd exists behind it (see Start).
	Reachable func(host string, port int) bool
	// Sleep pauses for the given seconds (poll backoff). Injectable for tests.
	Sleep func(seconds float64)
	// Now returns a monotonic-ish wall clock in seconds (poll deadline).
	Now func() float64
	// Out receives human progress lines. nil => io.Discard.
	Out io.Writer
	// InContainer reports whether the runtime's podman runs inside a container (a
	// yolo jail), which picks NestedNetwork for the builder. nil => the real probe,
	// insideContainer, so every caller gets the nested network without wiring it.
	InContainer func() bool
}

// insideContainer is the real nested probe: the run assembler's own predicate
// (paths.InsideContainer), and never on macOS, where it is never true either way.
func insideContainer() bool {
	return goruntime.GOOS == "linux" && paths.InsideContainer(func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// nested is Deps.InContainer, defaulted.
func (d Deps) nested() bool {
	if d.InContainer == nil {
		return insideContainer()
	}
	return d.InContainer()
}

// Session drives one builder container's lifecycle for one build.
type Session struct {
	Runtime string // "podman" | "container"
	Pubkey  string // authorized_keys public half baked into the container
	Deps    Deps

	// hostKey is the builder's SSH host key, base64'd for the eighth field of the
	// --builders line (see BuildersLine for why that field is load-bearing). Start
	// sets it; BuildersLine reads it. Unexported because it is an observation Start
	// makes, not a knob: a caller that supplied one would be pinning a key it has
	// no way to have seen, since the container mints a fresh one every boot.
	hostKey string
}

// reachableTimeout is how long Start polls for the builder sshd before giving up.
const reachableTimeout = 60.0

// Start pulls the builder image and runs the container detached, then polls
// until its sshd is reachable. Returns (host, port, ok). On podman the sshd is
// published to 127.0.0.1:BuilderHostPort; on Apple Container (no -p) the VM IP
// is discovered from `container ls`. ok=false means the builder never came up
// (the caller falls back to the plain build + failure diagnosis).
func (s *Session) Start() (host string, port int, ok bool) {
	out := s.Deps.Out
	if out == nil {
		out = io.Discard
	}

	if s.Deps.Run(PullArgv(s.Runtime, "")) != 0 {
		fmt.Fprintln(out, "could not pull the Linux builder image")
		return "", 0, false
	}
	if s.Deps.Run(runArgv(s.Runtime, s.Pubkey, "", "", 0, s.Deps.nested())) != 0 {
		fmt.Fprintln(out, "could not start the Linux builder container")
		return "", 0, false
	}

	// Resolve the address the builder is reachable at.
	host, port = s.reachableAddress()
	if host == "" {
		s.Stop()
		fmt.Fprintln(out, "could not resolve the builder container address")
		return "", 0, false
	}

	// Poll until an SSH SERVER answers at that address — not merely until something
	// accepts a TCP connection there.
	//
	// ⚠ THE TCP CHECK ALONE IS A GUARANTEED FALSE POSITIVE ON podman machine, which is
	// the only configuration this offload exists for. A published port is forwarded by
	// gvproxy, whose proxy listens on the Mac, ACCEPTS, and only then dials the VM
	// (inetaf/tcpproxy: Accept() precedes dialContext()) — and podman registers that
	// forward as the FIRST statement of configureNetNS, before the netns is built and
	// long before sshd could bind. So `podman run -d` returns, a TCP dial succeeds
	// immediately, and the far end is nothing. The dial is kept as a cheap negative
	// filter (it costs one syscall where the scan costs a process), but it can never be
	// the readiness answer.
	//
	// Reading the host key IS the readiness answer, and it is the same observation the
	// builders line needs anyway: a key comes back only once sshd has completed a
	// version exchange, so one probe settles "is it up" and "what do I pin".
	deadline := s.Deps.Now() + reachableTimeout
	for s.Deps.Now() < deadline {
		if s.Deps.Reachable(host, port) {
			if key := s.scanHostKey(host, port); key != "" {
				s.hostKey = key
				return host, port, true
			}
		}
		s.Deps.Sleep(1.0)
	}
	s.Stop()
	fmt.Fprintln(out, "the Linux builder container's sshd never answered at "+
		net.JoinHostPort(host, strconv.Itoa(port))+
		" — the container started, so the address is reachable but nothing is serving SSH behind it")
	return "", 0, false
}

// scanHostKey returns the builder's host key, base64'd for the builders line, or ""
// when nothing answered. A nil Output seam (a caller that wired only Run) yields "",
// which the poll reads as "not ready" — so a half-wired Deps fails loudly at the
// deadline instead of handing nix a builder it cannot authenticate.
func (s *Session) scanHostKey(host string, port int) string {
	if s.Deps.Output == nil {
		return ""
	}
	stdout, rc := s.Deps.Output(HostKeyScanArgv(host, port))
	if rc != 0 {
		return ""
	}
	return EncodeHostKey(stdout)
}

// reachableAddress returns the (host, port) the builder sshd listens on:
// 127.0.0.1:BuilderHostPort for podman (published), or the VM IP from
// `container ls` for Apple Container (no host port-publish).
func (s *Session) reachableAddress() (string, int) {
	if s.Runtime == "container" {
		stdout, rc := s.Deps.Output([]string{"container", "ls"})
		if rc != 0 {
			return "", 0
		}
		host, port, ok := ReachableAddressFromContainerLs(stdout, BuilderContainer)
		if !ok {
			return "", 0
		}
		return host, port
	}
	return "127.0.0.1", BuilderHostPort
}

// BuildersLine returns the nix --builders spec for this session's resolved
// address, carrying the host key Start observed (convenience wrapper over the pure
// constructor). Called before Start, it yields the unpinned four-field line — which
// is the line that cannot authenticate against a daemon nix, so do not.
func (s *Session) BuildersLine(host string, port, maxJobs int) string {
	return BuildersLine(host, port, maxJobs, "", s.hostKey)
}

// Stop tears the builder container down (best-effort; --rm means a stopped
// container is auto-removed). Safe to call even if Start failed partway.
func (s *Session) Stop() {
	_ = s.Deps.Run(StopArgv(s.Runtime, ""))
}

// StopArgv returns the argv to stop the builder container. Empty name falls back
// to the frozen default.
func StopArgv(runtime, name string) []string {
	if name == "" {
		name = BuilderContainer
	}
	if runtime == "container" {
		return []string{"container", "stop", name}
	}
	return []string{runtime, "stop", name}
}
