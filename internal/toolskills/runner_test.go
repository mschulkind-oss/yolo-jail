package toolskills

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeMise writes an executable shell script and returns its path. The script is the stand-in
// for mise; its argv is checked by the scripts that care.
func fakeMise(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mise")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func run(t *testing.T, body string) Result {
	t.Helper()
	return Runner{Mise: fakeMise(t, body)}.List(context.Background(), t.TempDir())
}

func TestListOK(t *testing.T) {
	dir := t.TempDir()
	sample := filepath.Join("testdata", "ls-nonempty.json")
	abs, _ := filepath.Abs(sample)
	stderr, _ := filepath.Abs(filepath.Join("testdata", "ls-missing.stderr"))
	m := fakeMise(t, `[ "$1 $3 $4 $5" = "-C skills ls --json" ] || { echo "argv: $*" >&2; exit 9; }
[ "$2" = "`+dir+`" ] || exit 8
cat `+stderr+` >&2
cat `+abs)
	res := Runner{Mise: m}.List(context.Background(), dir)
	if res.Outcome != OutcomeOK || len(res.Skills) != 2 || len(res.Warnings) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestListEmptyArrayIsOK(t *testing.T) {
	if res := run(t, "echo '[]'"); res.Outcome != OutcomeOK || len(res.Skills) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestListNonzeroIsFailedWithFirstStderrLine(t *testing.T) {
	res := run(t, "echo 'boom: bad config' >&2; exit 3")
	if res.Outcome != OutcomeFailed || !strings.Contains(res.Reason, "exited 3") || !strings.Contains(res.Reason, "boom") {
		t.Fatalf("%+v", res)
	}
}

func TestListMalformedIsFailed(t *testing.T) {
	for _, body := range []string{"echo not json", "echo '{}'", "echo '[{\"name\":\"a\",\"tool\":\"t\",\"path\":\"rel\"}]'", "true"} {
		if res := run(t, body); res.Outcome != OutcomeFailed || res.Reason == "" {
			t.Errorf("%s: %+v", body, res)
		}
	}
}

func TestListUnsupportedMiseWithoutSkillsCommand(t *testing.T) {
	for _, msg := range []string{
		"mise ERROR no tasks defined in /w. Are you in a project directory?",
		"error: unrecognized subcommand 'skills'",
	} {
		if res := run(t, "echo '"+msg+"' >&2; exit 1"); res.Outcome != OutcomeUnsupported {
			t.Errorf("%q: %+v", msg, res)
		}
	}
}

func TestListMiseAbsentIsFailed(t *testing.T) {
	res := Runner{Mise: filepath.Join(t.TempDir(), "no-such-mise")}.List(context.Background(), t.TempDir())
	if res.Outcome != OutcomeFailed || res.Reason == "" {
		t.Fatalf("%+v", res)
	}
}

func TestListOversizedStdoutIsFailedAndTheGroupIsKilled(t *testing.T) {
	// The script would write forever; only the cap stopping it lets this return.
	res := Runner{Mise: fakeMise(t, "yes '[' "), MaxStdout: 4096}.List(context.Background(), t.TempDir())
	if res.Outcome != OutcomeFailed || !strings.Contains(res.Reason, "larger than 4096") {
		t.Fatalf("%+v", res)
	}
}

func TestListStdoutExactlyAtTheCapIsAccepted(t *testing.T) {
	out := "[]"
	res := Runner{Mise: fakeMise(t, "printf '[]'"), MaxStdout: len(out)}.List(context.Background(), t.TempDir())
	if res.Outcome != OutcomeOK {
		t.Fatalf("%+v", res)
	}
}

// alive reports whether pid is a live process (a zombie is not alive).
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return syscall.Kill(pid, 0) == nil
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || len(s) < i+3 || s[i+2] != 'Z'
}

func TestListHangIsKilledOnTheInjectedDeadlineWithItsGrandchild(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "ready")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no fifo: %v", err)
	}
	// The script starts a grandchild, tells the test its pid through the fifo (which blocks
	// until the test reads), then hangs.
	m := fakeMise(t, "sleep 1000 &\necho $! > "+fifo+"\nwait")
	fire := make(chan time.Time)
	released := make(chan struct{})
	pidCh := make(chan int, 1)
	go func() {
		defer close(released)
		b, err := os.ReadFile(fifo)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				pidCh <- pid
			}
		}
		close(fire) // the event: the script is up and hanging; now the deadline "passes"
	}()
	var gotDeadline time.Duration
	r := Runner{Mise: m, Deadline: 7 * time.Second, After: func(d time.Duration) <-chan time.Time {
		gotDeadline = d
		return fire
	}}
	res := r.List(context.Background(), t.TempDir())
	<-released
	if res.Outcome != OutcomeTimedOut || gotDeadline != 7*time.Second {
		t.Fatalf("%+v deadline %v", res, gotDeadline)
	}
	select {
	case pid := <-pidCh:
		// SIGKILL to the group is delivered before List returns; the zombie check covers a
		// grandchild its parent never reaped.
		if alive(pid) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grandchild %d outlived the deadline", pid)
		}
	default:
		t.Fatal("no grandchild pid reported")
	}
}

func TestListDefaultsMatchTheDesign(t *testing.T) {
	if DefaultDeadline != 15*time.Second || DefaultMaxStdout != 1<<20 {
		t.Fatal("MS-D2 bounds changed")
	}
}

func TestListCallerCancellationStopsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fifo := filepath.Join(t.TempDir(), "ready")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no fifo: %v", err)
	}
	go func() {
		_, _ = os.ReadFile(fifo)
		cancel()
	}()
	res := Runner{Mise: fakeMise(t, "echo up > "+fifo+"\nsleep 1000")}.List(ctx, t.TempDir())
	if res.Outcome != OutcomeFailed || ctx.Err() == nil {
		t.Fatalf("%+v", res)
	}
}
