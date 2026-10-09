package hostservice

import (
	"net"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// sessionFrames runs fn against a Session on a pipe and returns every frame it wrote,
// in order, up to and including the exit frame.
func sessionFrames(t *testing.T, fn func(*Session)) []frameproto.Frame {
	t.Helper()
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan []frameproto.Frame)
	go func() {
		var frames []frameproto.Frame
		for {
			f, err := frameproto.ReadFrame(client)
			if err != nil {
				done <- frames
				return
			}
			frames = append(frames, f)
			if f.StreamID == frameproto.StreamExit {
				done <- frames
				return
			}
		}
	}()
	s := &Session{conn: server}
	fn(s)
	s.Exit(0) // what handleOne does when the handler returns without exiting
	frames := <-done
	_ = server.Close()
	return frames
}

// TestSessionJSONEncodeFailureIsNotASuccess pins that a reply jsonx cannot encode ends the
// session with exit 1 and a stderr line, and writes nothing on stdout — whether or not the
// handler looks at the returned error. Every handler but one discards it (`_ = s.JSON(...)`),
// so without this an unencodable reply read as rc=0 with an empty body.
func TestSessionJSONEncodeFailureIsNotASuccess(t *testing.T) {
	type notJSON struct{ X int }
	var returned error
	frames := sessionFrames(t, func(s *Session) {
		returned = s.JSON(map[string]any{"devices": []notJSON{{1}}})
	})
	if returned == nil {
		t.Fatal("Session.JSON returned nil for an unencodable value")
	}
	var stderr strings.Builder
	exit := -1
	for _, f := range frames {
		switch f.StreamID {
		case frameproto.StreamStdout:
			t.Errorf("stdout frame %q written for an unencodable reply", f.Payload)
		case frameproto.StreamStderr:
			stderr.Write(f.Payload)
		case frameproto.StreamExit:
			exit, _ = frameproto.ExitCode(f.Payload)
		}
	}
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr.String(), "unsupported type") || !strings.Contains(stderr.String(), ".devices") {
		t.Errorf("stderr = %q, want the encode error naming the type and its path", stderr.String())
	}
}

// TestSessionJSONWritesOneLine pins the success path's bytes: one compact line, then the
// handler's own exit.
func TestSessionJSONWritesOneLine(t *testing.T) {
	frames := sessionFrames(t, func(s *Session) {
		if err := s.JSON(map[string]any{"pong": true, "pid": int64(7)}); err != nil {
			t.Errorf("JSON: %v", err)
		}
	})
	if len(frames) != 2 || frames[0].StreamID != frameproto.StreamStdout ||
		string(frames[0].Payload) != "{\"pid\": 7, \"pong\": true}\n" {
		t.Fatalf("frames = %+v", frames)
	}
	if code, _ := frameproto.ExitCode(frames[1].Payload); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}
