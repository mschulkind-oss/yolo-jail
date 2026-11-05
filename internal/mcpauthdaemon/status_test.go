package mcpauthdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestMCPAuthStatusRequestNeverRepairsCanonicalPermissions(t *testing.T) {
	canonical, err := mcpauth.NewCanonicalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.WithLock(func(*mcpauth.CanonicalFile) error { return nil }); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(canonical.Dir, "codex.json")
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(canonical.Dir, "codex.lock")
	beforeLock, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	b := broker{canonical: canonical, share: mcpauth.Share{Jail: "jail-status", Replicas: []mcpauth.Replica{{Pack: "codex"}}}}
	parent, err := os.MkdirTemp("/tmp", "yj-mcp-status-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	frontDir := filepath.Join(parent, "jail-status")
	if err := os.Mkdir(frontDir, 0o700); err != nil {
		t.Fatal(err)
	}
	upstream := filepath.Join(parent, "upstream.sock")
	endpoint := filepath.Join(frontDir, "front.endpoint")
	daemonStop, frontStop := make(chan struct{}), make(chan struct{})
	daemonDone, frontDone := make(chan struct{}), make(chan struct{})
	go func() { _ = hostservice.ServeFrontedUnix(b.handleStatus, upstream, daemonStop); close(daemonDone) }()
	waitForMCPStatusPath(t, upstream)
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", upstream, frontStop); close(frontDone) }()
	waitForMCPStatusPath(t, endpoint)
	defer func() { close(frontStop); <-frontDone; close(daemonStop); <-daemonDone }()
	conn, err := svcendpoint.DialLocal(endpoint, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := frameproto.WriteRequest(conn, []byte(`{"op":"status"}`)); err != nil {
		t.Fatal(err)
	}
	var unavailable bool
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(frame.Payload), "canonical state unavailable") {
			unavailable = true
		}
		if frame.StreamID == frameproto.StreamExit {
			break
		}
	}
	if !unavailable {
		t.Fatal("status request did not refuse unsafe canonical mode")
	}
	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != 0o644 {
		t.Fatalf("status changed canonical mode to %04o", after.Mode().Perm())
	}
	afterLock, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	if !afterLock.ModTime().Equal(beforeLock.ModTime()) || afterLock.Size() != beforeLock.Size() {
		t.Fatal("status mutated lock file")
	}
}

func waitForMCPStatusPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status socket did not bind: %s", path)
}
