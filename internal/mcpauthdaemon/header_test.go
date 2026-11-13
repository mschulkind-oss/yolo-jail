package mcpauthdaemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// headerReply is one framed answer: stdout, stderr and the exit status.
type headerReply struct {
	stdout, stderr string
	rc             int
}

// askBroker serves b's request handler (the one serve hands ServeFrontedUnix) behind a jail
// front named for b's jail, sends one request and reads the whole answer.
func askBroker(t *testing.T, b *broker, request map[string]any) headerReply {
	t.Helper()
	parent, err := os.MkdirTemp(testtmp.ShortDir(), "yj-mcp-header-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	frontDir := filepath.Join(parent, b.share.Jail)
	if err := os.Mkdir(frontDir, 0o700); err != nil {
		t.Fatal(err)
	}
	upstream := filepath.Join(parent, "upstream.sock")
	endpoint := filepath.Join(frontDir, "front.endpoint")
	daemonStop, frontStop := make(chan struct{}), make(chan struct{})
	daemonDone, frontDone := make(chan struct{}), make(chan struct{})
	go func() { _ = hostservice.ServeFrontedUnix(b.handleStatus, upstream, daemonStop); close(daemonDone) }()
	waitForMCPStatusPath(t, upstream)
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", upstream, frontStop); close(frontDone) }()
	waitForMCPStatusPath(t, endpoint)
	defer func() { close(frontStop); <-frontDone; close(daemonStop); <-daemonDone }()
	conn, err := svcendpoint.DialLocal(endpoint, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	body, _ := json.Marshal(request)
	if err := frameproto.WriteRequest(conn, body); err != nil {
		t.Fatal(err)
	}
	var reply headerReply
	var stdout, stderr strings.Builder
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			stdout.Write(frame.Payload)
		case frameproto.StreamStderr:
			stderr.Write(frame.Payload)
		case frameproto.StreamExit:
			reply.rc, _ = frameproto.ExitCode(frame.Payload)
			reply.stdout, reply.stderr = stdout.String(), stderr.String()
			return reply
		}
	}
}

// The broker's request handler answers the header op: before any sign-in it refuses naming the
// sign-in; after the adoption it hands over the whole `Bearer <token>` header value and audits
// it; a server this launch did not compose is refused with exit 2. Deleting the header dispatch
// in handleStatus fails every case here.
func TestTheBrokerAnswersTheHeaderOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOnceDaemonFixture(t)
	if reply := askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"}); reply.rc != 1 || reply.stdout != "" ||
		!strings.Contains(reply.stderr, "not signed in") || !strings.Contains(reply.stderr, "run /mcp in claude and authenticate slack") {
		t.Fatalf("before the sign-in: %+v", reply)
	}
	once, err := f.b.newOnceBroker()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.b.passAll(context.Background(), once, f.b.onceSources()); err != nil {
		t.Fatal(err)
	}
	<-f.refreshs
	reply := askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"})
	if reply.rc != 0 || reply.stdout != "Bearer at-2\n" || reply.stderr != "" {
		t.Fatalf("after the sign-in: %+v", reply)
	}
	if strings.Contains(reply.stdout, "rt-2") {
		t.Fatal("the header route handed over the refresh token")
	}
	reply = askBroker(t, f.b, map[string]any{"op": "header", "server": "linear"})
	if reply.rc != 2 || reply.stdout != "" || !strings.Contains(reply.stderr, "yolo config set mcp_servers.linear.sign_in once --user") {
		t.Fatalf("an uncomposed server: %+v", reply)
	}
}

// The `header` audit event is written for each token handed over, never with the token.
func TestTheHeaderOpAuditsEachTokenItHandsOver(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOnceDaemonFixture(t)
	b, err := launcherBroker(f.b.canonical.Dir, f.b.share, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := BoundaryPass(context.Background(), f.b.canonical.Dir, f.b.share, nil); err != nil {
		t.Fatal(err)
	}
	<-f.refreshs
	if reply := askBroker(t, b, map[string]any{"op": "header", "server": "slack"}); reply.rc != 0 {
		t.Fatalf("header: %+v", reply)
	}
	audit, _ := os.ReadFile(filepath.Join(paths.BrokerDir(), "audit.jsonl"))
	if !strings.Contains(string(audit), `"header"`) || strings.Contains(string(audit), "at-2") {
		t.Fatalf("audit = %s", audit)
	}
}

// HostHeader is the route at `yolo host`, where no daemon runs: the same canonical store, so a
// sign-in a jail's broker adopted is served, and a share with no source refreshes nothing back.
func TestHostHeaderServesTheCanonicalSignIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOnceDaemonFixture(t)
	if err := BoundaryPass(context.Background(), f.b.canonical.Dir, f.b.share, nil); err != nil {
		t.Fatal(err)
	}
	<-f.refreshs
	headerOnly := f.b.share
	headerOnly.Jail, headerOnly.Sources = "host", nil
	token, err := HostHeader(context.Background(), f.b.canonical.Dir, headerOnly, "slack", nil)
	if err != nil || token != "at-2" {
		t.Fatalf("HostHeader = %q, %v", token, err)
	}
	if _, err := HostHeader(context.Background(), f.b.canonical.Dir, headerOnly, "linear", nil); err == nil {
		t.Fatal("served a server the composition does not name")
	}
}
