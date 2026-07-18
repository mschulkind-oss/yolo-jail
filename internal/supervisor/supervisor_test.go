package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseEnv(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []Spec
	}{
		{"empty", ``, nil},
		{"invalid json", `{not json`, nil},
		{"not a list", `{"name":"x"}`, nil},
		{
			"valid single",
			`[{"name":"broker","cmd":["python3","-m","x"],"restart":"always"}]`,
			[]Spec{{Name: "broker", Cmd: []string{"python3", "-m", "x"}, Restart: "always"}},
		},
		{
			"default restart on-failure",
			`[{"name":"a","cmd":["true"]}]`,
			[]Spec{{Name: "a", Cmd: []string{"true"}, Restart: "on-failure"}},
		},
		{
			"skip missing name / empty cmd / non-dict, keep valid",
			`[{"cmd":["x"]},{"name":"b","cmd":[]},"stringentry",{"name":"c","cmd":["ok"]}]`,
			[]Spec{{Name: "c", Cmd: []string{"ok"}, Restart: "on-failure"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseEnv(tc.raw)
			if !specsEqual(got, tc.want) {
				t.Errorf("ParseEnv(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func specsEqual(a, b []Spec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Restart != b[i].Restart ||
			strings.Join(a[i].Cmd, "\x00") != strings.Join(b[i].Cmd, "\x00") {
			return false
		}
	}
	return true
}

// TestRestartOnFailurePolicy: an "on-failure" daemon that exits 0 is NOT
// restarted; the supervise loop returns. Uses `true` (exits 0 immediately).
func TestRestartOnFailurePolicyExit0NoRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate LogDir
	c := &child{spec: Spec{Name: "t", Cmd: []string{"true"}, Restart: "on-failure"}, backoff: restartBackoffInitial}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { c.superviseOne(stop); close(done) }()
	select {
	case <-done: // returned without restarting — correct
	case <-time.After(5 * time.Second):
		close(stop)
		t.Fatal("on-failure daemon exiting 0 should not loop/restart")
	}
}

// TestRestartNoPolicy: a "no" daemon is never restarted even on failure.
func TestRestartNoPolicyNoRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &child{spec: Spec{Name: "t", Cmd: []string{"false"}, Restart: "no"}, backoff: restartBackoffInitial}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { c.superviseOne(stop); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(stop)
		t.Fatal(`"no" daemon should not restart`)
	}
}

// TestTerminateStopsAlwaysDaemon: an "always" daemon loops until stop; Run must
// terminate it and return.
func TestRunTerminatesAlwaysDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// `sleep 300` restart=always: runs until we terminate it.
	specs := []Spec{{Name: "sleeper", Cmd: []string{"sleep", "300"}, Restart: "always"}}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { Run(specs, stop); close(done) }()
	time.Sleep(300 * time.Millisecond) // let it start
	close(stop)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after stop (daemon not terminated)")
	}
}

// TestLogRotation: a >5MB log file is rotated to .log.1 on next openLog.
func TestLogRotation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logPath := filepath.Join(LogDir(), "d.log")
	if err := os.MkdirAll(LogDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a >5MB file.
	big := make([]byte, logMaxBytes+1)
	if err := os.WriteFile(logPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLog("d")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := os.Stat(filepath.Join(LogDir(), "d.log.1")); err != nil {
		t.Errorf("expected rotated d.log.1: %v", err)
	}
	// New d.log should be fresh (small).
	info, err := os.Stat(logPath)
	if err != nil || info.Size() != 0 {
		t.Errorf("new log not fresh: size=%d err=%v", info.Size(), err)
	}
}
