package serialdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestIsDeviceAllowed(t *testing.T) {
	allowed := []string{"/dev/ttyUSB*", "/dev/ttyACM*"}

	cases := []struct {
		device string
		want   bool
	}{
		{"/dev/ttyUSB0", true},
		{"/dev/ttyUSB1", true},
		{"/dev/ttyACM0", true},
		{"/dev/ttyS0", false},
		{"/dev/sda", false},
		{"/etc/shadow", false},
		{"/dev/../etc/shadow", false},
		{"relative/dev/ttyUSB0", false},
	}

	for _, c := range cases {
		got := isDeviceAllowed(c.device, allowed)
		if got != c.want {
			t.Errorf("isDeviceAllowed(%q) = %v, want %v", c.device, got, c.want)
		}
	}
}

func startTestDaemon(t *testing.T, cfg Settings) (string, func()) {
	t.Helper()
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	dir, err := os.MkdirTemp("/tmp", "yj-ser-test-")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(dir, "serial.endpoint")
	stopCh := make(chan struct{})
	done := make(chan struct{})

	handler := BuildHandler(cfg)
	go func() {
		defer close(done)
		_ = hostservice.ServeEndpoint(handler, endpoint, stopCh)
	}()

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(endpoint); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop := func() {
		close(stopCh)
		<-done
		_ = os.RemoveAll(dir)
	}
	return endpoint, stop
}

func TestSerialDaemonListMode(t *testing.T) {
	cfg := DefaultSettings()
	ep, stop := startTestDaemon(t, cfg)
	defer stop()

	conn, err := svcendpoint.Dial(ep, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req, _ := json.Marshal(map[string]any{"mode": "list", "format": "json"})
	if err := frameproto.WriteRequest(conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	var stdout []byte
	exitCode := -1
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			break
		}
		if f.StreamID == frameproto.StreamStdout {
			stdout = append(stdout, f.Payload...)
		}
		if f.StreamID == frameproto.StreamExit {
			if rc, err := frameproto.ExitCode(f.Payload); err == nil {
				exitCode = rc
			}
			break
		}
	}

	if exitCode != 0 {
		t.Errorf("list exit code = %d, want 0", exitCode)
	}
	if len(stdout) == 0 {
		t.Error("list stdout is empty")
	}
}

func TestSerialDaemonUnauthorizedDevice(t *testing.T) {
	cfg := DefaultSettings()
	ep, stop := startTestDaemon(t, cfg)
	defer stop()

	conn, err := svcendpoint.Dial(ep, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req, _ := json.Marshal(map[string]any{"mode": "read", "device": "/dev/sda"})
	if err := frameproto.WriteRequest(conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	exitCode := -1
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			break
		}
		if f.StreamID == frameproto.StreamExit {
			if rc, err := frameproto.ExitCode(f.Payload); err == nil {
				exitCode = rc
			}
			break
		}
	}

	if exitCode != 2 {
		t.Errorf("read unauthorized device exit code = %d, want 2", exitCode)
	}
}
