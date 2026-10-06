package notty

// detach_test.go covers `yolo internal no-terminal --detach --log=FILE -- <command>`, the start a
// launcher's BACKGROUND pre-launch refresh takes (docs/design/program-delivery.md OQ-PD31): the
// command runs on after the verb returns, in a session and process group of its own, reading
// nothing and writing only to the log. Platform-neutral, so macOS's check runs it too; the
// terminal half is in notty_linux_test.go, which has a pty to take away.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitFor polls for path until the deadline, failing the test with what the log holds if it never
// appears.
func waitFor(t *testing.T, path, log string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			got, _ := os.ReadFile(log)
			t.Fatalf("%s never appeared; the log holds:\n%s", filepath.Base(path), got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDetachReturnsWhileTheCommandRuns is the verb's whole point: it starts the command and returns
// 0 without waiting for it, and the command goes on to finish, its stdout and stderr in the log and
// nothing on its stdin. The command blocks until the test releases it, so a verb that waited would
// still be waiting when the release is written — no timing is needed to tell the two apart.
func TestDetachReturnsWhileTheCommandRuns(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "job.log")
	script := `: > "$1/started"
for _ in $(seq 1 400); do [ -e "$1/release" ] && break; sleep 0.05; done
if IFS= read -r line; then echo "STDIN:$line"; else echo NO_STDIN; fi
echo OUT; echo ERR >&2
echo "PGID:$(ps -o pgid= -p $$ | tr -d ' ') PID:$$"
: > "$1/finished"`
	if rc := Main("no-terminal", []string{"--detach", "--log=" + log, "--", "sh", "-c", script, "sh", dir}); rc != 0 {
		t.Fatalf("the detach returned %d, want 0", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "finished")); err == nil {
		t.Fatal("the command had already finished when the verb returned: it waited for it")
	}
	waitFor(t, filepath.Join(dir, "started"), log)
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, filepath.Join(dir, "finished"), log)
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NO_STDIN\n", "OUT\n", "ERR\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the log lacks %q: the command's stdin must be empty and its output the log's:\n%s", want, got)
		}
	}
	// A process group of its own (it leads a session of its own): a signal a terminal sends the
	// launcher's foreground group — a Ctrl-C, a hangup — cannot reach it.
	var pgid, pid string
	for _, f := range strings.Fields(string(got)) {
		if v, ok := strings.CutPrefix(f, "PGID:"); ok {
			pgid = v
		}
		if v, ok := strings.CutPrefix(f, "PID:"); ok {
			pid = v
		}
	}
	if pgid == "" || pgid != pid {
		t.Errorf("the detached command must lead its own process group (pgid %q, pid %q):\n%s", pgid, pid, got)
	}
}

// TestDetachAppendsAndRotatesItsLog: a second job appends to the first's log, so one that finds
// the work already done does not erase the record of one that failed; and a log past
// detachLogLimit is moved aside to <log>.prev first, so a refresh run hourly for months does not
// grow one file without bound.
func TestDetachAppendsAndRotatesItsLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "job.log")
	if err := os.WriteFile(log, []byte("FIRST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(word string) {
		t.Helper()
		done := filepath.Join(dir, word+".done")
		if rc := Main("no-terminal", []string{"--detach", "--log=" + log, "--",
			"sh", "-c", `echo "$1"; : > "$2"`, "sh", word, done}); rc != 0 {
			t.Fatalf("the detach returned %d", rc)
		}
		waitFor(t, done, log)
	}
	run("SECOND")
	if got, _ := os.ReadFile(log); string(got) != "FIRST\nSECOND\n" {
		t.Errorf("a second job must append to the log, got %q", got)
	}
	big := strings.Repeat("x", detachLogLimit+1)
	if err := os.WriteFile(log, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	run("THIRD")
	if got, _ := os.ReadFile(log); string(got) != "THIRD\n" {
		t.Errorf("a log past the limit must be started afresh, got %d bytes", len(got))
	}
	if prev, _ := os.ReadFile(log + ".prev"); len(prev) != len(big) {
		t.Errorf("the full log must be kept as %s.prev (%d bytes there)", filepath.Base(log), len(prev))
	}
}

// TestDetachMisuse: a detach needs a log, takes no bound (the command it starts bounds its own
// act, as a launcher's job does through _bounded), and a log without a detach is a mistake. Each is
// status 2 and runs nothing; a log that cannot be opened is status 1 and runs nothing either, so a
// launcher can tell "this yolo cannot" from "the job started".
func TestDetachMisuse(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	cmd := []string{"--", "sh", "-c", `: > "$1"`, "sh", ran}
	log := "--log=" + filepath.Join(dir, "job.log")
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"no log", append([]string{"--detach"}, cmd...), 2},
		{"a bound", append([]string{"--detach", log, "--timeout=5"}, cmd...), 2},
		{"a kill-after", append([]string{"--detach", log, "--kill-after=5"}, cmd...), 2},
		{"a log without a detach", append([]string{log}, cmd...), 2},
		{"an unopenable log", append([]string{"--detach", "--log=" + filepath.Join(dir, "absent", "job.log")}, cmd...), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Main("no-terminal", tc.args); got != tc.want {
				t.Errorf("Main(%v) = %d, want %d", tc.args, got, tc.want)
			}
			time.Sleep(50 * time.Millisecond)
			if _, err := os.Stat(ran); err == nil {
				t.Errorf("Main(%v) ran the command", tc.args)
			}
		})
	}
}

// TestDetachOfAMissingCommandIs127: the shell's "could not start" status, and nothing left behind.
func TestDetachOfAMissingCommandIs127(t *testing.T) {
	dir := t.TempDir()
	if got := Main("no-terminal", []string{"--detach", "--log=" + filepath.Join(dir, "job.log"), "--",
		filepath.Join(dir, "absent")}); got != 127 {
		t.Errorf("a command that cannot start = %d, want 127", got)
	}
}
