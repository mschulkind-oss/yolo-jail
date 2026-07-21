package containerbuilder

import (
	"fmt"
	"io"
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
	// Output runs argv and returns (stdout, rc) — used for `container ls` ADDR
	// discovery on Apple Container.
	Output func(argv []string) (string, int)
	// Reachable reports whether host:port accepts a TCP connection (the builder
	// sshd is up).
	Reachable func(host string, port int) bool
	// Sleep pauses for the given seconds (poll backoff). Injectable for tests.
	Sleep func(seconds float64)
	// Now returns a monotonic-ish wall clock in seconds (poll deadline).
	Now func() float64
	// Out receives human progress lines. nil => io.Discard.
	Out io.Writer
}

// Session drives one builder container's lifecycle for one build.
type Session struct {
	Runtime string // "podman" | "container"
	Pubkey  string // authorized_keys public half baked into the container
	Deps    Deps
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
	if s.Deps.Run(RunArgv(s.Runtime, s.Pubkey, "", "", 0)) != 0 {
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

	// Poll until sshd accepts a connection (or the deadline passes).
	deadline := s.Deps.Now() + reachableTimeout
	for s.Deps.Now() < deadline {
		if s.Deps.Reachable(host, port) {
			return host, port, true
		}
		s.Deps.Sleep(1.0)
	}
	s.Stop()
	fmt.Fprintln(out, "the Linux builder container did not become reachable in time")
	return "", 0, false
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
// address (convenience wrapper over the pure constructor).
func (s *Session) BuildersLine(host string, port, maxJobs int) string {
	return BuildersLine(host, port, maxJobs, "")
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
