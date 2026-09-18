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

// TestRestartNoPolicy: a "no" daemon is never restarted even on failure, and
// says in <name>.log that it has been abandoned — `false` writes nothing of its
// own, so without that line an abandoned daemon and a running one look identical.
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
	if body := readDaemonLog(t, "t"); !strings.Contains(body, "not restarting") {
		t.Errorf("abandoning a %q daemon was silent; <name>.log = %q", c.spec.Restart, body)
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

// readDaemonLog returns the whole body of ~/.local/state/yolo-jail-daemons/<name>.log.
// A missing file and an empty one are the same answer here on purpose: both are
// the "empty log, no process" symptom these tests exist to assert is gone.
func readDaemonLog(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(LogDir(), name+".log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading %s.log: %v", name, err)
	}
	return string(b)
}

// TestSpawnFailureRestartNoGivesUp: a daemon whose cmd cannot be spawned AT ALL
// is governed by the restart policy exactly as an exiting one is. Under "no"
// the loop must give up instead of backing off forever (1s→30s for the life of
// the jail), and the error value must reach <name>.log — an empty log and no
// process was the entire observable symptom of any spawn fault.
func TestSpawnFailureRestartNoGivesUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &child{
		spec:    Spec{Name: "t", Cmd: []string{"/nonexistent/yolo-no-such-binary"}, Restart: "no"},
		backoff: restartBackoffInitial,
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { c.superviseOne(stop); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(stop)
		t.Fatal(`a "no" daemon that fails to SPAWN must not be retried`)
	}
	body := readDaemonLog(t, "t")
	if body == "" {
		t.Fatal("spawn failure left <name>.log empty — the error value was discarded")
	}
	if !strings.Contains(body, "yolo-no-such-binary") {
		t.Errorf("<name>.log does not name the cmd that failed to spawn: %q", body)
	}
	// Nothing ever started, so c.cmd is nil: Run terminates every child
	// unconditionally, and this early return must keep that nil-safe.
	c.terminate(time.Millisecond)
}

// TestSpawnFailureRestartAlwaysRetries is the other half of the same ruling:
// "always" means always, spawn failures included, so the loop must NOT give up
// — but the error is reported now rather than swallowed.
func TestSpawnFailureRestartAlwaysRetries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &child{
		spec:    Spec{Name: "t", Cmd: []string{"/nonexistent/yolo-no-such-binary"}, Restart: "always"},
		backoff: restartBackoffInitial,
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { c.superviseOne(stop); close(done) }()
	select {
	case <-done:
		t.Fatal(`an "always" daemon must keep retrying a spawn failure until stop`)
	case <-time.After(300 * time.Millisecond): // inside the first 1s backoff
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("superviseOne did not return after stop")
	}
	if body := readDaemonLog(t, "t"); !strings.Contains(body, "spawn failed") {
		t.Errorf("spawn failure not reported in <name>.log: %q", body)
	}
}
