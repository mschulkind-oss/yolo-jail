package integration

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

// Host port-forwarding test. It runs a real HTTP server on the host side and
// proves forward_host_ports carries TCP
// data end-to-end into the jail (in-jail curl reads back the server's marker).
//
// No t.Parallel(): the integration package runs serially by default, giving this
// test the same isolation the Python suite's serial CI job provided. The orphaned
// sock_dir conftest fixture is not ported — its consumers died with test_runtime.py.

// TestHostPortForwardingData confirms forward_host_ports actually forwards TCP
// data end-to-end: a host HTTP server returns a known payload and the same
// payload is read from inside the jail via curl on the forwarded port.
func TestHostPortForwardingData(t *testing.T) {
	requireJail(t)

	// Bind a free port on the host loopback and keep the listener for http.Serve.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding host listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	marker := fmt.Sprintf("YOLO_PORT_TEST_%d", port)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(marker))
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	dir := writeProject(t, fmt.Sprintf(
		`{"network": {"mode": "bridge", "forward_host_ports": [%d]}}`, port))

	r := runYolo(t, dir, fmt.Sprintf("curl -s --max-time 5 http://127.0.0.1:%d/", port))
	if !strings.Contains(r.stdout, marker) {
		t.Fatalf("expected %q in stdout, got: %q\nstderr: %q", marker, r.stdout, r.stderr)
	}
}
