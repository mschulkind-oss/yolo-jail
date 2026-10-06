package integration

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TestMacosUserRelaysPortRemaps is the port remaps on the hardware (internal/cli/run's
// macosuserportrelay.go; docs/design/declaration-parity.md §5.1.1, corrected 2026-10-05). A
// macos-user sandbox shares the Mac's network stack, so a same-port entry needs nothing, and a
// REMAP is carried by a TCP relay the launch opens outside the sandbox, as the host user, for the
// command's lifetime:
//
//   - `forward_host_ports` "J:H": a host service on 127.0.0.1:H answers at the sandbox's
//     127.0.0.1:J;
//   - `ports` "127.0.0.1:PH:PJ": a service the sandbox binds on 127.0.0.1:PJ answers at the Mac's
//     127.0.0.1:PH. A loopback listen address, so the test publishes nothing beyond this Mac.
//
// WHAT ONLY THIS TEST CAN SEE, none of it executed before: that a process the sandbox account runs
// under Seatbelt connects to a listener the host user opened, and the reverse; that the launch
// says it is relaying; and that neither listener survives the session. The unit tests run the
// same relays on Linux with a stand-in for the sandbox (macosuserportrelay_test.go).
func TestMacosUserRelaysPortRemaps(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{}`)

	// The forward's far side: an HTTP service on the Mac's loopback.
	hostSvc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("HOSTSVC"))
	}), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(hostSvc) }()
	t.Cleanup(func() { _ = srv.Close() })
	h := hostSvc.Addr().(*net.TCPAddr).Port
	j, ph, pj := relayFreePort(t), relayFreePort(t), relayFreePort(t)
	fwdEntry := fmt.Sprintf("%d:%d", j, h)
	pubEntry := fmt.Sprintf("127.0.0.1:%d:%d", ph, pj)
	ws := macosUserWorkspace(t, fmt.Sprintf(`{"network": {"forward_host_ports": [%q], "ports": [%q]}}`,
		fwdEntry, pubEntry))

	// The publish half's host side: dial the Mac's PH until the sandbox's service answers through
	// the relay, or the session ends. Before the sandbox listens, the relay accepts and closes.
	published := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if line, err := relayReadLine(net.JoinHostPort("127.0.0.1", strconv.Itoa(ph))); err == nil && line != "" {
				published <- line
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()

	listener := `import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", ` + strconv.Itoa(pj) + `))
s.listen(1)
s.settimeout(60)
try:
    c, _ = s.accept()
    c.sendall(b"SANDBOXSVC\n")
    c.close()
    print("SERVED=yes")
except socket.timeout:
    print("SERVED=timeout")
`
	r := macosUserRunProbe(t, "port-relay", ws, strings.Join([]string{
		`echo "=== RELAY ==="`,
		`echo "FWD=$(curl -s --max-time 10 http://127.0.0.1:` + strconv.Itoa(j) + `/ || echo FAILED)"`,
		`python3 -c ` + shquote.Quote(listener) + ` 2>&1`,
		`echo "=== END ==="`,
	}, "\n"))
	close(done)
	probe := section(r.stdout, "=== RELAY ===", "=== END ===")
	diag := "\n--- probe:\n" + probe + "\n--- launch stderr:\n" + r.stderr

	for _, want := range []string{
		fmt.Sprintf("Relaying 127.0.0.1:%d -> 127.0.0.1:%d for `network.forward_host_ports` entry %s", j, h, fwdEntry),
		fmt.Sprintf("Relaying 127.0.0.1:%d -> 127.0.0.1:%d for `network.ports` entry %s", ph, pj, pubEntry),
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the launch did not say %q%s", want, diag)
		}
	}
	if !strings.Contains(probe, "FWD=HOSTSVC") {
		t.Errorf("the sandbox's 127.0.0.1:%d did not reach the host's service on %d through the "+
			"forward relay%s", j, h, diag)
	}
	select {
	case line := <-published:
		if line != "SANDBOXSVC" {
			t.Errorf("the Mac's 127.0.0.1:%d answered %q, want the sandbox's service%s", ph, line, diag)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("the Mac's 127.0.0.1:%d never reached the sandbox's service on %d through the "+
			"publish relay (the probe's SERVED= line says whether anything connected)%s", ph, pj, diag)
	}
	for _, port := range []int{j, ph} {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second); err == nil {
			c.Close()
			t.Errorf("127.0.0.1:%d still listens after the session ended: the relay outlived its command%s",
				port, diag)
		}
	}
}

// relayFreePort is a TCP port nothing on the Mac's loopback listens on at the moment of the call.
func relayFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// relayReadLine dials addr and returns the first line it is sent.
func relayReadLine(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}
