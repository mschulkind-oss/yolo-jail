package hostservice

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// This first consumer regression exercises the authenticated endpoint and the
// actual handleOne reader. It uses a moderate oversized frame and sends no body,
// so the unfixed reader can safely demonstrate that it waits for the declared
// payload instead of refusing from the prefix.
func TestHTTPHostIngressRejectsDeclaredLengthBeforeAllocation(t *testing.T) {
	advertiseLoopback(t)
	dir, err := os.MkdirTemp("/tmp", "yj-http-ingress-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	endpoint := filepath.Join(dir, "http.endpoint")
	var handlerInvocations atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = ServeHTTPBrokerEndpoint(func(*Session) { handlerInvocations.Add(1) }, endpoint, stop)
		close(done)
	}()
	waitForEndpoint(t, endpoint)
	t.Cleanup(func() { close(stop); <-done })

	for _, declared := range []uint32{4_456_449, ^uint32(0)} {
		conn := dialEndpoint(t, endpoint)
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], declared)
		if _, err := conn.Write(header[:]); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		var one [1]byte
		_, err := conn.Read(one[:])
		_ = conn.Close()
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatalf("authenticated HTTP ingress waited for declared length %d: %v", declared, err)
		}
		if err == nil {
			t.Fatalf("oversized HTTP frame %d unexpectedly produced a response", declared)
		}
	}
	if n := handlerInvocations.Load(); n != 0 {
		t.Fatalf("handler invocations=%d, want zero", n)
	}
}
