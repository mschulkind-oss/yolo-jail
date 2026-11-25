package toolskills

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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

func TestListHangIsKilledOnTheInjectedDeadlineWithItsGrandchild(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "ready")
	held := filepath.Join(dir, "held")
	for _, f := range []string{fifo, held} {
		if err := syscall.Mkfifo(f, 0o600); err != nil {
			t.Skipf("no fifo: %v", err)
		}
	}
	// The script starts a grandchild that holds the write end of the "held" fifo (fd 9, which
	// survives its exec of sleep), tells the test its pid through the "ready" fifo (which blocks
	// until the test reads), then hangs. The kernel closes fd 9 only when the grandchild has
	// actually exited, so EOF on "held" is the event "the grandchild is dead".
	m := fakeMise(t, "sleep 1000 9>"+shquote.Quote(held)+" &\necho $! > "+shquote.Quote(fifo)+"\nwait")
	fire := make(chan time.Time)
	released := make(chan struct{})
	heldCh := make(chan *os.File, 1)
	pidCh := make(chan int, 1)
	go func() {
		defer close(released)
		defer close(fire)       // the event: the script is up and hanging; now the deadline "passes"
		f, err := os.Open(held) // returns once the grandchild has opened its end
		if err != nil {
			return
		}
		heldCh <- f
		b, err := os.ReadFile(fifo)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				pidCh <- pid
			}
		}
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
	var f *os.File
	var pid int
	select {
	case f = <-heldCh:
		defer f.Close()
	default:
		t.Fatal("grandchild never opened its held fifo")
	}
	select {
	case pid = <-pidCh:
	default:
		t.Fatal("no grandchild pid reported")
	}
	// SIGKILL to the group is queued before List returns, but a process dies only when the
	// scheduler runs it, which on a loaded machine can be after List returned. So wait for the
	// event itself: EOF on the held fifo. The timer is only a failure guard against a hang.
	eof := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, f); close(eof) }()
	select {
	case <-eof:
	case <-time.After(60 * time.Second):
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("grandchild %d outlived the deadline", pid)
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
	res := Runner{Mise: fakeMise(t, "echo up > "+shquote.Quote(fifo)+"\nsleep 1000")}.List(ctx, t.TempDir())
	if res.Outcome != OutcomeFailed || ctx.Err() == nil {
		t.Fatalf("%+v", res)
	}
}
