package mcpauthdaemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

type countingMCPTransport struct {
	Next  http.RoundTripper
	Calls *atomic.Int64
}

func (c countingMCPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.Calls.Add(1)
	return c.Next.RoundTrip(r)
}

func managedMCPSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(testtmp.ShortDir(), "mcp-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(dir, "service.sock")
}

func TestServeRefusesExpectedClientBeforeAnyHTTP(t *testing.T) {
	f := newOnceDaemonFixture(t)
	body, err := json.Marshal(f.b.share)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	wire["once"].([]any)[0].(map[string]any)["expected_client_id"] = "owner-client"
	body, _ = json.Marshal(wire)
	f.b.share, err = mcpauth.DecodeShare(body)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	previous := http.DefaultTransport
	http.DefaultTransport = countingMCPTransport{Next: previous, Calls: &calls}
	t.Cleanup(func() { http.DefaultTransport = previous })
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 1)
	restore := setOnceSeams(ticks, func() { passes <- struct{}{} })
	defer restore()
	stop, done := make(chan struct{}), make(chan error, 1)
	socket := managedMCPSocket(t)
	go func() { done <- f.b.serve(socket, stop) }()
	select {
	case <-passes:
	case <-time.After(30 * time.Second):
		close(stop)
		t.Fatal("poll pass did not finish")
	}
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("service did not stop")
	}
	if calls.Load() != 0 {
		t.Fatalf("client mismatch made %d HTTP calls", calls.Load())
	}
	store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.Read()
	if err != nil || len(file.Records) != 0 {
		t.Fatal("mismatched client was adopted")
	}
	restarted := &broker{share: f.b.share, canonical: f.b.canonical, scripts: f.b.scripts}
	reply := askBroker(t, restarted, map[string]any{"op": "status"})
	if calls.Load() != 0 {
		t.Fatal("restart status contacted a provider")
	}
	if reply.rc != 0 || !strings.Contains(reply.stdout, "refused-client-mismatch") {
		t.Fatalf("durable mismatch outcome absent: %+v", reply)
	}
}
