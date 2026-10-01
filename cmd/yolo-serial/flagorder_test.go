package main

// flagorder_test.go pins where yolo-serial reads its flags: anywhere, as its usage text writes
// them, after the device and the data as well as before. Go's flag package stops at the first
// argument that is not a flag, so `yolo-serial read /dev/ttyUSB0 --baud 9600` read at the default
// 115200 and dropped both words silently, and an `--endpoint` written after the device was never
// read at all.

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// recordingFront is a bridge behind a real front that records each request's fields and ends the
// session with exit 0. It returns the endpoint file and the recorded requests.
func recordingFront(t *testing.T) (string, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var seen []map[string]any
	endpoint := frontFor(t, func(s *hostservice.Session) {
		got := map[string]any{}
		for _, k := range []string{"mode", "device", "data", "baud", "append_newline", "timeout_ms", "max_bytes"} {
			if v, ok := s.Get(k); ok {
				got[k] = v
			}
		}
		mu.Lock()
		seen = append(seen, got)
		mu.Unlock()
	})
	return endpoint, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), seen...)
	}
}

func TestFlagsAfterTheDeviceAreRead(t *testing.T) {
	endpoint, requests := recordingFront(t)
	cases := []struct {
		name string
		args []string
		want map[string]any
	}{
		{"read", []string{"read", "/dev/ttyUSB0", "--baud", "9600", "--endpoint", endpoint, "--max-bytes", "7"},
			map[string]any{"mode": "read", "device": "/dev/ttyUSB0", "baud": 9600, "max_bytes": 7}},
		{"write", []string{"write", "/dev/ttyUSB0", "hello", "--no-newline", "--baud=9600", "--endpoint", endpoint},
			map[string]any{"mode": "write", "device": "/dev/ttyUSB0", "data": "hello", "baud": 9600,
				"append_newline": false}},
		{"write, data after --", []string{"write", "--endpoint", endpoint, "/dev/ttyUSB0", "--", "-starts-with-a-dash"},
			map[string]any{"mode": "write", "data": "-starts-with-a-dash", "append_newline": true}},
		{"monitor", []string{"monitor", "/dev/ttyUSB0", "--baud", "9600", "--endpoint", endpoint},
			map[string]any{"mode": "monitor", "device": "/dev/ttyUSB0", "baud": 9600}},
		{"flags first, as before", []string{"read", "--baud", "57600", "--endpoint", endpoint, "/dev/ttyACM0"},
			map[string]any{"mode": "read", "device": "/dev/ttyACM0", "baud": 57600}},
	}
	if runtime.GOOS == "linux" {
		cases = append(cases, struct {
			name string
			args []string
			want map[string]any
		}{"pty", []string{"pty", "/dev/ttyUSB0", "--baud", "9600", "--endpoint", endpoint},
			map[string]any{"mode": "monitor", "device": "/dev/ttyUSB0", "baud": 9600}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(requests())
			var rc int
			_, stderr := captureStdio(t, func() { rc = run(tc.args) })
			if rc != 0 {
				t.Fatalf("rc = %d, want 0; stderr = %q", rc, stderr)
			}
			all := requests()
			if len(all) != before+1 {
				t.Fatalf("the bridge got %d request(s), want 1; stderr = %q", len(all)-before, stderr)
			}
			got := all[len(all)-1]
			for k, want := range tc.want {
				// Compared as printed: the bridge's request decoder keeps numbers as json.Number.
				if fmt.Sprint(got[k]) != fmt.Sprint(want) {
					t.Errorf("request %s = %v, want %v (whole request %v)", k, got[k], want, got)
				}
			}
		})
	}
}

// TestAnUnknownFlagAfterTheDeviceIsRefused: a flag the subcommand does not have is refused
// wherever it is written, rather than read as the data or dropped.
func TestAnUnknownFlagAfterTheDeviceIsRefused(t *testing.T) {
	endpoint, requests := recordingFront(t)
	var rc int
	_, stderr := captureStdio(t, func() {
		rc = run([]string{"read", "/dev/ttyUSB0", "--bogus", "--endpoint", endpoint})
	})
	if rc != 2 || !strings.Contains(stderr, "-bogus") {
		t.Errorf("rc = %d, stderr = %q; want 2 naming the unknown flag", rc, stderr)
	}
	if n := len(requests()); n != 0 {
		t.Errorf("the bridge got %d request(s) for a refused command line", n)
	}
}
