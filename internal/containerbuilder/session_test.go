package containerbuilder

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

// fakeClock returns an incrementing wall clock so poll loops terminate.
type fakeClock struct{ t float64 }

func (c *fakeClock) now() float64 { c.t += 0.5; return c.t }

// scannedKeyBody is the key half of a real `ssh-keyscan -t ed25519` line.
const scannedKeyBody = "AAAAC3NzaC1lZDI1NTE5AAAAIMMliNkaI/Rxhwgydhf+nbofjaDXF95s2hLG15wEktA3"

// keyscanOutput is what a live builder's sshd answers with, banner comment included.
func keyscanOutput(host string, port int) string {
	_ = port
	return "# " + host + " SSH-2.0-OpenSSH_10.4\n" +
		host + " ssh-ed25519 " + scannedKeyBody + " root@builder\n"
}

// okOutput answers both argv shapes a Session issues through Deps.Output: the Apple
// Container `container ls` address discovery, and the ssh-keyscan readiness probe.
func okOutput(lsOut string) func([]string) (string, int) {
	return func(argv []string) (string, int) {
		if len(argv) > 0 && argv[0] == "ssh-keyscan" {
			return keyscanOutput("127.0.0.1", BuilderHostPort), 0
		}
		return lsOut, 0
	}
}

func TestSessionStartPodmanReachable(t *testing.T) {
	var ran [][]string
	clk := &fakeClock{}
	s := &Session{
		Runtime: "podman",
		Pubkey:  "ssh-ed25519 AAAA...",
		Deps: Deps{
			Run:       func(argv []string) int { ran = append(ran, argv); return 0 },
			Output:    okOutput(""),
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
			Output:    okOutput(""),
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
			Output:    okOutput(""),
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
			Run: func([]string) int { return 0 },
			Output: func(argv []string) (string, int) {
				if len(argv) > 0 && argv[0] == "ssh-keyscan" {
					return keyscanOutput("192.168.64.7", BuilderGuestPort), 0
				}
				return lsOut, 0
			},
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
	// Read as nix reads it, because where the key sits depends on this process's home
	// (BuildersLine moves a key path nix would split into the URI). System derived from the
	// host arch, not frozen — see TestBuilderURIAndLine.
	m := parseBuildersAsNix(t, line)
	if m.base != "ssh-ng://root@127.0.0.1:31022" || m.system != BuilderSystem() || m.key != BuilderKey() {
		t.Errorf("BuildersLine = %q: nix reads %q, system %q, key %q", line, m.base, m.system, m.key)
	}
	if len(m.fields) != 4 || m.fields[3] != "4" {
		t.Errorf("BuildersLine should end with maxjobs: %q", line)
	}
}

// A TCP ACCEPT IS NOT A BUILDER. This is the exact shape podman machine hands us: the
// port is forwarded and connectable the instant `podman run -d` returns, because
// gvproxy's proxy accepts before it dials the VM, and sshd inside the container has
// not bound yet — or never will. Start must refuse that, because what it returns is
// handed to nix as a machine nix will then fail to reach, two layers from the cause.
//
// This is also the test that fails if the readiness call site is deleted: drop the
// scanHostKey call from Start's poll and it returns ok=true here.
func TestSessionStartRefusesAPortWithNothingServingSSH(t *testing.T) {
	var stopped bool
	clk := &fakeClock{}
	s := &Session{
		Runtime: "podman",
		Deps: Deps{
			Run: func(argv []string) int {
				if len(argv) > 1 && argv[1] == "stop" {
					stopped = true
				}
				return 0
			},
			// Connectable, always — the accept-then-dial forwarder.
			Reachable: func(string, int) bool { return true },
			// …and nothing behind it: ssh-keyscan prints nothing at all.
			Output: func(argv []string) (string, int) { return "", 0 },
			Sleep:  func(float64) { clk.t += 100 },
			Now:    clk.now,
		},
	}
	if host, port, ok := s.Start(); ok {
		t.Errorf("Start = (%q, %d, true) against a port with no sshd behind it; a TCP "+
			"accept must not count as a ready builder", host, port)
	}
	if !stopped {
		t.Error("Start should Stop the container when the builder never serves SSH")
	}
}

// The whole point of scanning: the key reaches the --builders line. Without the eighth
// field the nix-DAEMON's ssh (which is the one that runs, and which never sees the
// caller's NIX_SSHOPTS) meets an unknown host key and dies as
// "failed to start SSH connection" — the macOS nightly's failure, reproduced and fixed
// 2026-09-13. Delete the s.hostKey argument in Session.BuildersLine and this fails.
func TestSessionStartPinsTheScannedHostKeyIntoTheBuildersLine(t *testing.T) {
	clk := &fakeClock{}
	s := &Session{
		Runtime: "podman",
		Deps: Deps{
			Run:       func([]string) int { return 0 },
			Output:    okOutput(""),
			Reachable: func(string, int) bool { return true },
			Sleep:     func(float64) {},
			Now:       clk.now,
		},
	}
	host, port, ok := s.Start()
	if !ok {
		t.Fatal("Start should succeed when ssh-keyscan returns a key")
	}
	line := s.BuildersLine(host, port, 4)
	want := base64.StdEncoding.EncodeToString([]byte("ssh-ed25519 " + scannedKeyBody))
	if !strings.HasSuffix(line, " "+want) {
		t.Errorf("builders line does not end with the scanned host key:\n got %q\n want suffix %q",
			line, " "+want)
	}
	if got := len(parseBuildersAsNix(t, line).fields); got != 8 {
		t.Errorf("builders line has %d fields, want 8 so the key lands in nix's "+
			"publicHostKey slot: %q", got, line)
	}
}

// A Session whose Output seam is nil can still pull and run a container, and used to
// hand nix an unpinned line on the strength of a TCP connect. It must not: an offload
// that cannot observe the host key cannot authenticate to the builder at all on the
// configuration this exists for.
func TestSessionStartWithoutAnOutputSeamDoesNotStart(t *testing.T) {
	clk := &fakeClock{}
	s := &Session{
		Runtime: "podman",
		Deps: Deps{
			Run:       func([]string) int { return 0 },
			Reachable: func(string, int) bool { return true },
			Sleep:     func(float64) { clk.t += 100 },
			Now:       clk.now,
		},
	}
	if _, _, ok := s.Start(); ok {
		t.Error("Start should fail when no Output seam can read the builder's host key")
	}
}

// TestSessionStartPicksTheNestedNetworkFromTheProbe drives Start — the call site — so it
// fails if Start stops consulting Deps.InContainer or stops passing its answer to the run
// argv. Nested podman cannot always make the bridge's veth pair (NestedNetwork's comment
// has the measurement); not-nested must stay byte-identical to RunArgv, which is what
// every macOS launch runs.
func TestSessionStartPicksTheNestedNetworkFromTheProbe(t *testing.T) {
	for _, tc := range []struct {
		runtime string
		nested  bool
		want    []string
	}{
		{"podman", false, RunArgv("podman", "PUB", "", "", 0)},
		{"podman", true, []string{
			"podman", "run", "-d", "--rm", "--name", BuilderContainer,
			"-e", "YOLO_BUILDER_PUBKEY=PUB",
			"--network=slirp4netns",
			"-p", "127.0.0.1:31022:22", BuilderImage,
		}},
		// Apple Container takes no network selector, nested or not.
		{"container", true, RunArgv("container", "PUB", "", "", 0)},
	} {
		var ran [][]string
		nested := tc.nested
		s := &Session{
			Runtime: tc.runtime,
			Pubkey:  "PUB",
			Deps: Deps{
				Run:         func(argv []string) int { ran = append(ran, argv); return 0 },
				Output:      okOutput("NAME STATE ADDR\n" + BuilderContainer + " running 192.168.64.2/24\n"),
				Reachable:   func(string, int) bool { return true },
				Sleep:       func(float64) {},
				Now:         (&fakeClock{}).now,
				InContainer: func() bool { return nested },
			},
		}
		if _, _, ok := s.Start(); !ok {
			t.Fatalf("%s nested=%v: Start failed", tc.runtime, tc.nested)
		}
		if len(ran) < 2 || !reflect.DeepEqual(ran[1], tc.want) {
			t.Errorf("%s nested=%v: run argv\n got %v\n want %v", tc.runtime, tc.nested, ran, tc.want)
		}
	}
}
