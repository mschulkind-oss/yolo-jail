package run

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

func TestMCPAuthShareSessionThroughProducerMainAndAuthenticatedFront(t *testing.T) {
	packHome(t)
	t.Setenv(hostservice.PublicationPathEnv, "/inherited/attacker.endpoint")
	parent, err := os.MkdirTemp(testtmp.ShortDir(), "yj-session-binding-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	frontDir := filepath.Join(parent, "this-launch")
	if err := os.Mkdir(frontDir, 0700); err != nil {
		t.Fatal(err)
	}
	input := onceLaunchInput(t, `{"fixture-server":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once"}}`, `{"fixture-server":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once"}}`)
	data, err := mcpShareFromPacks(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	share, err := mcpauth.DecodeShare(data)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(state, "replicas"), 0700); err != nil {
		t.Fatal(err)
	}
	sharePath := filepath.Join(state, "replicas", share.Jail+".json")
	if err := os.WriteFile(sharePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := mcpauth.NewOnceStore(filepath.Join(state, "once"))
	if err != nil {
		t.Fatal(err)
	}
	const token = "inert-session-fixture"
	if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
		file.Records = []mcpauth.Record{{Server: "fixture-server", Identity: share.Once[0].Identity, URL: share.Once[0].URL, Generation: 1, State: "live", Entry: json.RawMessage(`{}`), AccessToken: token, ExpiresAtMS: time.Now().Add(24 * time.Hour).UnixMilli()}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	command := []string{"yolo", "internal", "daemon", "mcp-auth-broker", "--socket", "{socket}", "--state-dir", state, "--share", sharePath}
	body, _ := json.Marshal(map[string]any{"command": command, "env": map[string]string{hostservice.PublicationPathEnv: "/manifest/attacker.endpoint"}})
	decoded, err := jsonx.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: input.Workspace, keeperJailName: share.Jail, Stderr: io.Discard, Stdout: io.Discard}
	fillDefaults(o)
	o.helperPin = testPin()
	h, ok := o.startExternalService("mcp-auth-broker", decoded.(*jsonx.OrderedMap), frontDir, loopholes.TransportLoopbackTLS, "127.0.0.1", &loopholes.HostDaemon{Publishes: loopholes.PublishesSocket, Preamble: true})
	if !ok {
		t.Fatal("real Main did not start behind producer's authenticated front")
	}
	defer h.stop()
	endpoint := filepath.Join(frontDir, "mcp-auth-broker.endpoint")
	for _, op := range []string{"status", "header"} {
		reply := sessionBindingRequest(t, endpoint, op, share.Jail)
		if reply.code != 0 {
			t.Fatalf("trusted producer mapping refused %s: rc=%d", op, reply.code)
		}
		if op == "status" && (strings.Contains(reply.output, token) || !strings.Contains(reply.output, "live")) {
			t.Fatal("status leaked token or omitted synthetic record")
		}
		if op == "header" && strings.TrimSpace(reply.output) != "Bearer "+token {
			t.Fatal("bound helper did not return privately compared fixture header")
		}
	}
	// Another valid authenticated publication reaches the SAME real daemon but
	// must not inherit its share. Spoofing the expected cname in the request does
	// not change the authenticated frontend principal.
	for _, name := range []string{"other-launch", "recreated-launch"} {
		dir := filepath.Join(parent, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(dir, "mcp-auth-broker.endpoint")
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			_ = svcendpoint.ServeFront(foreign, "127.0.0.1", frontSocketFile(frontShortHash(frontDir), "mcp-auth-broker"), stop)
			close(done)
		}()
		statusProducerWaitPath(t, foreign)
		for _, op := range []string{"status", "header"} {
			if reply := sessionBindingRequest(t, foreign, op, share.Jail); reply.code != 2 || reply.output != "" {
				close(stop)
				<-done
				t.Fatalf("foreign authenticated %s session borrowed share: rc=%d", op, reply.code)
			}
		}
		close(stop)
		<-done
	}
	// A TLS/cert-correct endpoint with a different token cannot authenticate.
	published, err := os.ReadFile(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	wrongToken, err := svcendpoint.Parse(string(published))
	if err != nil {
		t.Fatal("private fixture publication could not be parsed")
	}
	wrongToken.Token = strings.Repeat("0", 64)
	unauthenticated := filepath.Join(parent, "wrong-token.endpoint")
	if err := os.WriteFile(unauthenticated, []byte(wrongToken.Format()), 0600); err != nil {
		t.Fatal(err)
	}
	if conn, err := svcendpoint.DialLocal(unauthenticated, 20*time.Second); !errors.Is(err, svcendpoint.ErrAuthRejected) {
		if conn != nil {
			conn.Close()
		}
		t.Fatal("unauthenticated session was not rejected")
	}
	// Reaching the host-private socket without a trusted front preamble cannot
	// substitute a self-claimed cname for authentication.
	raw, err := net.Dial("unix", frontSocketFile(frontShortHash(frontDir), "mcp-auth-broker"))
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.SetDeadline(time.Now().Add(30 * time.Second))
	_ = frameproto.WriteRequest(raw, []byte(`{"op":"status","jail_id":"jail-synthetic"}`))
	if _, err := frameproto.ReadFrame(raw); err == nil {
		raw.Close()
		t.Fatal("nonfronted request borrowed a share")
	}
	raw.Close()
	// The frozen descriptor is read before serving: changing its on-disk cname
	// cannot change the running daemon's binding or its share.
	share.Jail = "wrong-share"
	wrong, _ := json.Marshal(share)
	if err := os.WriteFile(sharePath, wrong, 0600); err != nil {
		t.Fatal(err)
	}
	if reply := sessionBindingRequest(t, endpoint, "status", "wrong-share"); reply.code != 0 || strings.Contains(reply.output, "wrong-share") {
		t.Fatal("incoming or replaced descriptor renamed the frozen share")
	}
	// A different valid private share filename cannot borrow the producer's
	// trusted container binding, even behind its own authenticated front.
	wrongPath := filepath.Join(state, "replicas", share.Jail+".json")
	if err := os.WriteFile(wrongPath, wrong, 0600); err != nil {
		t.Fatal(err)
	}
	command[len(command)-1] = wrongPath
	body, _ = json.Marshal(map[string]any{"command": command})
	decoded, _ = jsonx.Decode(body)
	wrongHandle, ok := o.startExternalService("mcp-auth-wrong-share", decoded.(*jsonx.OrderedMap), frontDir, loopholes.TransportLoopbackTLS, "127.0.0.1", &loopholes.HostDaemon{Publishes: loopholes.PublishesSocket, Preamble: true})
	if !ok {
		t.Fatal("wrong-share native daemon failed before executable request assertion")
	}
	defer wrongHandle.stop()
	for _, op := range []string{"status", "header"} {
		if reply := sessionBindingRequest(t, wrongHandle.hostPath, op, share.Jail); reply.code != 2 || reply.output != "" {
			t.Fatalf("wrong share borrowed trusted container: rc=%d", reply.code)
		}
	}
}

type sessionBindingReply struct {
	code   int
	output string
}

func sessionBindingRequest(t *testing.T, endpoint, op, spoof string) sessionBindingReply {
	t.Helper()
	conn, err := svcendpoint.DialLocal(endpoint, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	body, _ := json.Marshal(map[string]any{"op": op, "server": "fixture-server", "jail_id": spoof})
	if err := frameproto.WriteRequest(conn, body); err != nil {
		t.Fatal(err)
	}
	var reply sessionBindingReply
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		if frame.StreamID == frameproto.StreamStdout {
			reply.output += string(frame.Payload)
		}
		if frame.StreamID == frameproto.StreamExit {
			reply.code, err = frameproto.ExitCode(frame.Payload)
			if err != nil {
				t.Fatal(err)
			}
			return reply
		}
	}
}
