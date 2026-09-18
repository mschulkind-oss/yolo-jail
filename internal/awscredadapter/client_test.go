package awscredadapter

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// serveOnce answers one framed request on a net.Pipe and reports what the client
// sent. The whole host hop is exercised with no endpoint file, no TLS and no
// daemon, which is what lets these tests run anywhere.
func serveOnce(t *testing.T, reply func(w *bytes.Buffer) int) (net.Conn, <-chan []byte) {
	t.Helper()
	client, server := net.Pipe()
	got := make(chan []byte, 1)
	go func() {
		defer server.Close()
		request, err := frameproto.ReadRequestBytes(server)
		if err != nil {
			got <- nil
			return
		}
		got <- request
		var out bytes.Buffer
		rc := reply(&out)
		_, _ = server.Write(out.Bytes())
		_, _ = frameproto.WriteExit(server, rc)
	}()
	t.Cleanup(func() { _ = client.Close() })
	return client, got
}

func stdoutFrame(w *bytes.Buffer, body string) {
	_, _ = frameproto.WriteFrame(w, frameproto.StreamStdout, []byte(body))
}

// TestExchangeKeepsTheBodyOnANonzeroExit is the ONE behaviour this client has that
// internal/openauthclient's does not, and the reason the ~60 lines were copied
// rather than imported.
//
// The aws-auth daemon answers a lapsed session with the container-credentials 4xx
// body on STDOUT and exits 1 (awsauthdaemon's replyError). openauthclient turns
// that exit into *RemoteExitError and throws stdout away — which would delete the
// `aws sso login --profile X` sentence OQ-SSO6 exists to deliver, leaving the agent
// with a bare transport error. So the exit code is DATA here.
//
// Mutation check: making `rc != 0` return an error fails this test.
func TestExchangeKeepsTheBodyOnANonzeroExit(t *testing.T) {
	refusal := `{"Code":"ExpiredToken","Message":"on the HOST run: aws sso login --profile prod"}`
	conn, _ := serveOnce(t, func(w *bytes.Buffer) int {
		stdoutFrame(w, refusal)
		return 1
	})
	answer, err := exchange(conn, map[string]any{"action": "credentials"}, nil)
	if err != nil {
		t.Fatalf("exchange returned an error for a refusal the service DID answer: %v", err)
	}
	if answer.OK {
		t.Error("OK = true for an exit-1 reply; the adapter would serve a refusal as a 200")
	}
	if string(answer.Body) != refusal {
		t.Errorf("Body = %s, want the refusal verbatim %s", answer.Body, refusal)
	}
}

// TestExchangeReportsSuccessAndSendsTheRequest pins the other half of the exit
// code's meaning, and that the action the daemon dispatches on actually crosses.
func TestExchangeReportsSuccessAndSendsTheRequest(t *testing.T) {
	body := `{"AccessKeyId":"AKIA","SecretAccessKey":"s","Token":"t","Expiration":"2026-01-01T00:00:00Z"}`
	conn, sent := serveOnce(t, func(w *bytes.Buffer) int {
		stdoutFrame(w, body)
		return 0
	})
	answer, err := exchange(conn, map[string]any{"action": "credentials"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !answer.OK || string(answer.Body) != body {
		t.Errorf("answer = %+v, want OK with the body verbatim", answer)
	}
	select {
	case request := <-sent:
		var decoded map[string]any
		if err := json.Unmarshal(request, &decoded); err != nil {
			t.Fatalf("the service received unparseable bytes %q: %v", request, err)
		}
		if decoded["action"] != "credentials" {
			t.Errorf("request = %v, want action=credentials — the daemon dispatches on it "+
				"and defaults nowhere the adapter can rely on", decoded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the service never received a request")
	}
}

// TestExchangeForwardsStderr: the daemon writes its diagnosis on stderr beside the
// JSON. It reaches the adapter's own log, never the HTTP response — a credential
// service's diagnostics are not the agent's business.
func TestExchangeForwardsStderr(t *testing.T) {
	conn, _ := serveOnce(t, func(w *bytes.Buffer) int {
		_, _ = frameproto.WriteFrame(w, frameproto.StreamStderr, []byte("ExpiredToken: lapsed\n"))
		stdoutFrame(w, `{"Code":"ExpiredToken","Message":"m"}`)
		return 1
	})
	var log bytes.Buffer
	if _, err := exchange(conn, map[string]any{"action": "credentials"}, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "ExpiredToken: lapsed") {
		t.Errorf("stderr log = %q, want the daemon's diagnostic", log.String())
	}
}

// TestExchangeRefusesAnUnusableReply covers both directions of "nothing to
// forward": an empty body and one that is not JSON. Either would otherwise be
// handed to an SDK as a 200 or a 4xx with no Code and no Message, which is the one
// outcome with nothing in it for a human.
func TestExchangeRefusesAnUnusableReply(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		rc         int
	}{
		{"empty on success", "", 0},
		{"empty on failure", "", 1},
		{"not json", "aws: command not found", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, _ := serveOnce(t, func(w *bytes.Buffer) int {
				if tc.body != "" {
					stdoutFrame(w, tc.body)
				}
				return tc.rc
			})
			if _, err := exchange(conn, map[string]any{"action": "credentials"}, nil); err == nil {
				t.Error("exchange accepted a reply with no usable body")
			}
		})
	}
}

// TestRequestWithNoEndpointNamesTheVariable: the adapter runs in a jail where the
// loophole is not enabled and the variable is simply absent. The message has to say
// which one, because the in-jail symptom is otherwise a connection to nothing.
func TestRequestWithNoEndpointNamesTheVariable(t *testing.T) {
	_, err := Request("", map[string]any{"action": "credentials"}, nil)
	if err == nil || !strings.Contains(err.Error(), EndpointEnv) {
		t.Errorf("err = %v, want one naming %s", err, EndpointEnv)
	}
}
