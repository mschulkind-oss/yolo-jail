package supervisor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jailtrace"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func traceEvents(t *testing.T, b []byte) []string {
	t.Helper()
	var out []string
	for _, s := range strings.Split(string(b), "\n") {
		if s == "" {
			continue
		}
		var l struct {
			K    string `json:"k"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(s), &l); err != nil {
			t.Fatalf("trace line %q is not JSON: %v", s, err)
		}
		out = append(out, l.K+" "+l.Name)
	}
	return out
}

// EACH SPAWN IS A SPAN OF THE LAUNCH'S TRACE (docs/design/startup-tracing.md §5.6): a spawn that
// worked writes a start and an end; one that failed writes a failed mark beside its end.
func TestEachSpawnIsASpan(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate LogDir
	var buf bytes.Buffer
	trace := jailtrace.NewWriter(&buf, 1, time.Now)

	ok := &child{spec: Spec{Name: "svc", Cmd: []string{"true"}, Restart: "no"},
		backoff: RestartBackoffInitial, trace: trace}
	if err := ok.start(); err != nil {
		t.Fatal(err)
	}
	_ = ok.waitAndMaybeRestart(make(chan struct{}))

	bad := &child{spec: Spec{Name: "typo", Cmd: []string{filepath.Join(t.TempDir(), "absent")}, Restart: "no"},
		backoff: RestartBackoffInitial, trace: trace}
	if err := bad.start(); err == nil {
		t.Fatal("a spawn of a missing binary succeeded")
	}

	got := strings.Join(traceEvents(t, buf.Bytes()), ",")
	want := "start daemon.svc.spawn,end daemon.svc.spawn," +
		"start daemon.typo.spawn,end daemon.typo.spawn,mark daemon.typo.spawn.failed"
	if got != want {
		t.Errorf("trace = %s\nwant    %s", got, want)
	}
}

// Run takes its writer from the YOLO_TRACEPARENT in its environment, under the jail home, so a
// supervisor started with a context appends to the trace file the entrypoint created, and one
// started without a context writes none. Fails if Run stops opening the writer.
func TestRunWritesIntoTheTraceItsEnvironmentNames(t *testing.T) {
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JAIL_HOME", home)
	path := filepath.Join(home, jailtrace.FileName("0af7651916cd43dd8448eb211c80319c"))

	// The daemon says it ran on the readiness pipe (fd 3), which is the event the test waits on;
	// stop is closed only after it, so the spawn happened, and Run returns only once every
	// supervise loop has, so the spawn's end line is written by the time it does.
	run := func() {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		defer write.Close()
		t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(write.Fd())))
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			Run([]Spec{{Name: "once", Cmd: []string{"/bin/sh", "-c", "echo ran >&3"}, Restart: "no"}}, stop)
			close(done)
		}()
		if _, err := bufio.NewReader(read).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		close(stop)
		<-done
	}

	t.Setenv(jailtrace.Env, "")
	run()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a supervisor with no context wrote a trace file (%v)", err)
	}

	t.Setenv(jailtrace.Env, parent)
	run()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(traceEvents(t, b), ","); got != "start daemon.once.spawn,end daemon.once.spawn" {
		t.Errorf("trace = %s", got)
	}
}
