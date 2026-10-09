package serialdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	var doc map[string]any
	if err := json.Unmarshal(stdout, &doc); err != nil {
		t.Errorf("`list --json` is not JSON: %v\n%s", err, stdout)
	}
}

// listJSONOver runs `list --json` against a bridge allowing allowed and returns its stdout.
func listJSONOver(t *testing.T, allowed []string) []byte {
	t.Helper()
	ep, stop := startTestDaemon(t, Settings{AllowedDevices: allowed, DefaultBaud: DefaultBaudRate})
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
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatalf("the stream ended before the exit frame: %v (stdout so far %q)", err, stdout)
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			stdout = append(stdout, f.Payload...)
		case frameproto.StreamExit:
			if rc, err := frameproto.ExitCode(f.Payload); err != nil || rc != 0 {
				t.Errorf("list exit = %d (%v), want 0", rc, err)
			}
			return stdout
		}
	}
}

// TestListJSONIsJSONForEveryDeviceSet: `yolo-serial list --json` printed `{"devices": }` on the
// macos-user CI runner, and does for ANY device set — Session.JSON is jsonx, which wrote the
// []DeviceEntry it has no case for as nothing. An allowed device that exists is listed with its
// access, and a pattern matching nothing gives an empty list rather than null.
func TestListJSONIsJSONForEveryDeviceSet(t *testing.T) {
	type entry struct {
		Path       string  `json:"path"`
		Accessible bool    `json:"accessible"`
		Error      *string `json:"error"`
	}
	type doc struct {
		Devices *[]entry `json:"devices"`
	}
	decode := func(t *testing.T, out []byte) []entry {
		t.Helper()
		var d doc
		if err := json.Unmarshal(out, &d); err != nil {
			t.Fatalf("`list --json` is not JSON: %v\n%s", err, out)
		}
		if d.Devices == nil {
			t.Fatalf("`list --json` has no devices array:\n%s", out)
		}
		return *d.Devices
	}

	t.Run("none match", func(t *testing.T) {
		out := listJSONOver(t, []string{"/dev/yolo-serial-test-none-*"})
		if got := decode(t, out); len(got) != 0 {
			t.Errorf("devices = %+v, want none", got)
		}
		if want := `{"devices": []}` + "\n"; string(out) != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	})

	t.Run("an allowed device exists", func(t *testing.T) {
		// /dev/null stands for the allowed device: it exists and opens read-write everywhere,
		// as the CI runner's pty slave did.
		out := listJSONOver(t, []string{"/dev/null", "/dev/yolo-serial-test-none-*"})
		got := decode(t, out)
		if len(got) != 1 || got[0].Path != "/dev/null" || !got[0].Accessible || got[0].Error != nil {
			t.Errorf("devices = %+v, want /dev/null alone, accessible, with no error\n%s", got, out)
		}
	})

	t.Run("an inaccessible device carries its error", func(t *testing.T) {
		got := listJSON([]DeviceEntry{{Path: "/dev/ttyUSB0", Error: "permission denied"}})
		out, err := jsonx.DumpsCompact(got)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"devices": [{"path": "/dev/ttyUSB0", "accessible": false, "error": "permission denied"}]}`; out != want {
			t.Errorf("listJSON = %s, want %s", out, want)
		}
	})
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
