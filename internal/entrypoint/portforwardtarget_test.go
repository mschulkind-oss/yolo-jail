package entrypoint

// The container-side end of `network.forward_host_ports`, and the one place the
// two backends must NOT be allowed to disagree.
//
// A forward has two ports and they differ whenever the entry remaps:
// "5432:3306" means LISTEN on 5432 inside the jail, CONNECT to 3306 on the host.
// The Unix-socket path (Linux podman, Apple Container) never sees the host port
// — the host-side socat already applied the remap and named its socket after the
// LOCAL port. The TCP-gateway path (macOS podman) has no host-side socat at all,
// so it is the only path that has to carry the host port itself, and it used the
// local port for both: on macOS "5432:3306" silently forwarded to host 5432.
//
// containerForwardTarget owns both ports so a caller cannot pick the wrong one.

import (
	"strings"
	"testing"
)

func TestContainerForwardTargetGatewayDialsTheHOSTPort(t *testing.T) {
	// The regression: a remapping entry on the TCP-gateway path.
	target, sock := containerForwardTarget("host.containers.internal", "/tmp/yolo-fwd", 5432, 3306)
	if want := "TCP:host.containers.internal:3306"; target != want {
		t.Errorf("target = %q, want %q — the gateway must dial the HOST port", target, want)
	}
	if sock != "" {
		t.Errorf("gateway mode needs no socket, got %q", sock)
	}
}

func TestContainerForwardTargetUnixSocketIsNamedByTheLOCALPort(t *testing.T) {
	// The host-side socat created port-<local>.sock and already points it at the
	// host port, so the jail must ask for the LOCAL name and must not re-apply
	// the remap.
	target, sock := containerForwardTarget("", "/tmp/yolo-fwd", 5432, 3306)
	if want := "/tmp/yolo-fwd/port-5432.sock"; sock != want {
		t.Errorf("sock = %q, want %q", sock, want)
	}
	if want := "UNIX-CONNECT:" + sock; target != want {
		t.Errorf("target = %q, want %q", target, want)
	}
	if strings.Contains(target, "3306") {
		t.Error("the Unix-socket path must not re-apply the remap")
	}
}

func TestContainerForwardTargetNonRemappedIsIdenticalOnBothPaths(t *testing.T) {
	// The common case — a plain integer entry — where local == host. This is why
	// the bug above stayed invisible: it only shows up when the two differ.
	gw, _ := containerForwardTarget("gw", "/tmp/yolo-fwd", 6379, 6379)
	if want := "TCP:gw:6379"; gw != want {
		t.Errorf("gateway target = %q, want %q", gw, want)
	}
	_, sock := containerForwardTarget("", "/tmp/yolo-fwd", 6379, 6379)
	if want := "/tmp/yolo-fwd/port-6379.sock"; sock != want {
		t.Errorf("sock = %q, want %q", sock, want)
	}
}

// TestForwardEntryPortsSplitsLocalFromHost pins the ORDER, which is the whole
// reason this is confusing: a forward_host_ports string is "<local>:<host>" —
// JAIL side first. `network.ports` is the opposite ("<host>:<container>", podman's
// -p), and nothing but the docs stops someone transposing one into the other.
func TestForwardEntryPortsSplitsLocalFromHost(t *testing.T) {
	cases := []struct {
		raw        string // JSON array with one element
		wantLocal  int
		wantHost   int
		wantOK     bool
		wantPanics bool
	}{
		{raw: `[8080]`, wantLocal: 8080, wantHost: 8080, wantOK: true},
		{raw: `["8080"]`, wantLocal: 8080, wantHost: 8080, wantOK: true},
		// The order under test: jail 8080 → host 80, never the reverse.
		{raw: `["8080:80"]`, wantLocal: 8080, wantHost: 80, wantOK: true},
		{raw: `["5432:3306"]`, wantLocal: 5432, wantHost: 3306, wantOK: true},
		// A publish-style "ip:host:container" borrowed from network.ports. Config
		// validation rejects it; reaching here means it bypassed `yolo check`, and
		// aborting boot beats starting a shell with a broken forward.
		{raw: `["127.0.0.1:5000:5000"]`, wantOK: true, wantPanics: true},
		{raw: `["nope"]`, wantOK: true, wantPanics: true},
		{raw: `[3.5]`, wantOK: false},
		{raw: `[true]`, wantOK: false},
		{raw: `[null]`, wantOK: false},
	}
	for _, c := range cases {
		entry := decodeFirst(t, c.raw)
		if c.wantPanics {
			assertPanics(t, func() { forwardEntryPorts(entry) }, c.raw)
			continue
		}
		local, host, ok := forwardEntryPorts(entry)
		if ok != c.wantOK || local != c.wantLocal || host != c.wantHost {
			t.Errorf("forwardEntryPorts(%s) = (%d, %d, %v), want (%d, %d, %v)",
				c.raw, local, host, ok, c.wantLocal, c.wantHost, c.wantOK)
		}
	}
}
