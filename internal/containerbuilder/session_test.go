package containerbuilder

import (
	"strings"
	"testing"
)

// fakeClock returns an incrementing wall clock so poll loops terminate.
type fakeClock struct{ t float64 }

func (c *fakeClock) now() float64 { c.t += 0.5; return c.t }

func TestSessionStartPodmanReachable(t *testing.T) {
	var ran [][]string
	clk := &fakeClock{}
	s := &Session{
		Runtime: "podman",
		Pubkey:  "ssh-ed25519 AAAA...",
		Deps: Deps{
			Run:       func(argv []string) int { ran = append(ran, argv); return 0 },
			Reachable: func(host string, port int) bool { return host == "127.0.0.1" && port == BuilderHostPort },
			Sleep:     func(float64) {},
			Now:       clk.now,
		},
	}
	host, port, ok := s.Start()
	if !ok || host != "127.0.0.1" || port != BuilderHostPort {
		t.Fatalf("Start = (%q, %d, %v), want (127.0.0.1, %d, true)", host, port, ok, BuilderHostPort)
	}
	// Pull then run were issued.
	if len(ran) < 2 || ran[0][0] != "podman" || ran[0][1] != "pull" {
		t.Errorf("expected pull first, got %v", ran)
	}
	if ran[1][1] != "run" {
		t.Errorf("expected run second, got %v", ran[1])
	}
	// The run argv publishes the sshd port.
	if !strings.Contains(strings.Join(ran[1], " "), "127.0.0.1:31022:22") {
		t.Errorf("podman run should publish the sshd port: %v", ran[1])
	}
}

func TestSessionStartPullFailsAborts(t *testing.T) {
	s := &Session{
		Runtime: "podman",
		Deps: Deps{
			Run:       func(argv []string) int { return 1 }, // pull fails
			Reachable: func(string, int) bool { return true },
			Sleep:     func(float64) {},
			Now:       (&fakeClock{}).now,
		},
	}
	if _, _, ok := s.Start(); ok {
		t.Error("Start should fail when pull fails")
	}
}

func TestSessionStartTimeoutStops(t *testing.T) {
	var stopped bool
	// A clock that jumps past the deadline immediately so the poll gives up.
	big := &fakeClock{t: 0}
	s := &Session{
		Runtime: "podman",
		Deps: Deps{
			Run: func(argv []string) int {
				if len(argv) > 1 && argv[1] == "stop" {
					stopped = true
				}
				return 0
			},
			Reachable: func(string, int) bool { return false }, // never reachable
			Sleep:     func(float64) { big.t += 100 },          // blow past the 60s deadline
			Now:       big.now,
		},
	}
	if _, _, ok := s.Start(); ok {
		t.Error("Start should time out when never reachable")
	}
	if !stopped {
		t.Error("Start should Stop the container on timeout")
	}
}

func TestSessionStartAppleContainerDiscoversIP(t *testing.T) {
	lsOut := "NAME               ADDR\n" +
		BuilderContainer + "   192.168.64.7/24\n"
	clk := &fakeClock{}
	s := &Session{
		Runtime: "container",
		Deps: Deps{
			Run:       func([]string) int { return 0 },
			Output:    func(argv []string) (string, int) { return lsOut, 0 },
			Reachable: func(host string, port int) bool { return host == "192.168.64.7" && port == BuilderGuestPort },
			Sleep:     func(float64) {},
			Now:       clk.now,
		},
	}
	host, port, ok := s.Start()
	if !ok || host != "192.168.64.7" || port != BuilderGuestPort {
		t.Fatalf("Apple Container Start = (%q, %d, %v)", host, port, ok)
	}
}

func TestStopArgv(t *testing.T) {
	if got := StopArgv("podman", ""); strings.Join(got, " ") != "podman stop "+BuilderContainer {
		t.Errorf("podman StopArgv = %v", got)
	}
	if got := StopArgv("container", ""); strings.Join(got, " ") != "container stop "+BuilderContainer {
		t.Errorf("container StopArgv = %v", got)
	}
}

func TestSessionBuildersLine(t *testing.T) {
	s := &Session{Runtime: "podman"}
	line := s.BuildersLine("127.0.0.1", BuilderHostPort, 4)
	if !strings.HasPrefix(line, "ssh-ng://root@127.0.0.1:31022 aarch64-linux ") {
		t.Errorf("BuildersLine = %q", line)
	}
	if !strings.HasSuffix(line, " 4") {
		t.Errorf("BuildersLine should end with maxjobs: %q", line)
	}
}
