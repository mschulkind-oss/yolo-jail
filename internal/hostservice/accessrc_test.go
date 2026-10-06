package hostservice

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// serveOneLogged runs handleOne over a pipe for one request and returns the access line it
// logged. The client half reads every frame up to the exit frame, so the handler's reply is
// whole before the line is read.
func serveOneLogged(t *testing.T, handler Handler) string {
	t.Helper()
	logs := captureLog(t)
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleOne(handler, server, false)
		close(done)
	}()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if err := frameproto.WriteRequest(client, []byte(`{"jail_id":"j","mode":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	for {
		f, err := frameproto.ReadFrame(client)
		if err != nil {
			t.Fatalf("reading the reply: %v", err)
		}
		if f.StreamID == frameproto.StreamExit {
			break
		}
	}
	_ = client.Close()
	<-done
	return logs.String()
}

// THE ACCESS LINE RECORDS THE CODE THE HANDLER SENT. It used to log rc=0 for every reply,
// because Session.Exit kept no code and handleOne logged the default it would have sent had the
// handler not exited — so a host daemon that refused a request (exit 2) left an access log
// saying it had succeeded, which hid a launch-check failure from the one reader who looks there.
func TestTheAccessLineRecordsTheHandlersExitCode(t *testing.T) {
	line := serveOneLogged(t, func(s *Session) {
		s.Stderr("refused\n")
		s.Exit(2)
	})
	if !strings.Contains(line, "rc=2 ") {
		t.Errorf("a handler that exited 2 was logged as:\n%s", line)
	}
	if strings.Contains(line, "rc=0") {
		t.Errorf("the access line still claims success for a refused request:\n%s", line)
	}
}

// The default exit stays 0, and a handler that exits first keeps its own code over the
// recover path's: the line names the code the client received.
func TestTheAccessLineKeepsTheDefaultAndTheFirstExit(t *testing.T) {
	if line := serveOneLogged(t, func(s *Session) { s.Stdout("pong\n") }); !strings.Contains(line, "rc=0 ") {
		t.Errorf("a handler that did not exit was logged as:\n%s", line)
	}
	line := serveOneLogged(t, func(s *Session) {
		s.Exit(3)
		panic("after the reply")
	})
	if !strings.Contains(line, "rc=3 ") {
		t.Errorf("a handler that exited 3 and then panicked was logged as:\n%s", line)
	}
}
