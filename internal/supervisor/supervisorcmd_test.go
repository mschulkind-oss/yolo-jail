package supervisor

import (
	"bytes"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe to read while Main writes it from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// MAIN SAYS IT IS SUPERVISING, ON ITS OWN STDERR, BEFORE IT RUNS ANYTHING — the readiness line
// the macos-user launcher waits for before it prints "Started" (StartedLinePrefix). Driven
// through Main, so deleting the Fprintf from it fails this; and the SIGTERM that ends the run is
// sent only after the line is seen, which is the ordering the comment in Main promises.
func TestMainAnnouncesItIsSupervisingBeforeItRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate LogDir
	t.Setenv("YOLO_JAIL_DAEMONS", `[{"name":"a","cmd":["/bin/sh","-c","exit 0"],"restart":"no"},`+
		`{"name":"b","cmd":["/bin/sh","-c","exit 0"],"restart":"no"}]`)
	var buf syncBuffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })

	done := make(chan int, 1)
	go func() { done <- Main(nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), StartedLinePrefix) {
		if time.Now().After(deadline) {
			t.Fatalf("Main never wrote its readiness line; stderr so far: %q", buf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Main did not return after SIGTERM")
	}
	got := buf.String()
	if !strings.Contains(got, StartedLinePrefix+"a, b (pid ") {
		t.Errorf("the readiness line does not name the daemons in payload order: %q", got)
	}
}

// AN EMPTY OR INVALID PAYLOAD SAYS WHY THE SUPERVISOR IS EXITING, and never writes the readiness
// line — so a reader cannot mistake "nothing to supervise" for "supervising".
func TestMainSaysWhyItHasNothingToSupervise(t *testing.T) {
	for name, payload := range map[string]string{
		"unset":   "",
		"invalid": `[{"name":"x"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("YOLO_JAIL_DAEMONS", payload)
			var buf syncBuffer
			old := stderr
			stderr = &buf
			t.Cleanup(func() { stderr = old })
			if rc := Main(nil); rc != 0 {
				t.Errorf("rc = %d", rc)
			}
			got := buf.String()
			if !strings.Contains(got, "nothing to supervise") || strings.Contains(got, StartedLinePrefix) {
				t.Errorf("stderr = %q", got)
			}
		})
	}
}
