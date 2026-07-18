package cgd

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestSocketRoundTrip drives the cgd request/response over a real Unix socket
// (the wire the daemon uses), proving the single-line-JSON protocol end to end
// against a fake cgroup tree. This complements cmd/yolo-cgd (which adds the
// SO_PEERCRED read + chmod that need a real nested jail to exercise).
func TestSocketRoundTrip(t *testing.T) {
	dir := t.TempDir()
	container := filepath.Join(dir, "container")
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(container, "cgroup.controllers"), []byte("cpu memory pids"), 0o644)
	_ = os.WriteFile(filepath.Join(container, "cgroup.procs"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(container, "cgroup.subtree_control"), []byte(""), 0o644)

	sock := filepath.Join(os.TempDir(), "cgd-test-"+filepath.Base(dir)+".sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	defer os.Remove(sock)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		if n := len(line); n > 0 && line[n-1] == '\n' {
			line = line[:n-1]
		}
		req, ok := ParseRequest(line)
		if !ok {
			return
		}
		resp := Handle(req, container, 999)
		s, _ := jsonx.DumpsCompact(resp)
		conn.Write([]byte(s + "\n"))
	}()

	c, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte(`{"op":"status"}` + "\n"))
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	decoded, err := jsonx.Decode(line[:len(line)-1])
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp := decoded.(*jsonx.OrderedMap)
	if v, _ := resp.Get("ok"); v != true {
		t.Errorf("status ok=%v", v)
	}
	if v, _ := resp.Get("cgroup"); v != container {
		t.Errorf("status cgroup=%v, want %s", v, container)
	}
	wg.Wait()
}
