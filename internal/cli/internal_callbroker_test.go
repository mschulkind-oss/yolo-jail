package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

func TestCallBrokerForwardIncludesNonTerminalDevNullStdin(t *testing.T) {
	serviceDir := filepath.Join(t.TempDir(), "services")
	if err := os.Mkdir(serviceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	endpointPath := filepath.Join(serviceDir, "tasks.endpoint")
	endpoint, err := svcendpoint.Listen(endpointPath, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = endpoint.Close() })

	type result struct {
		request map[string]json.RawMessage
		err     error
	}
	received := make(chan result, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := endpoint.Accept()
		if err != nil {
			received <- result{err: err}
			return
		}
		defer conn.Close()
		if _, err := svcendpoint.ReadPreamble(conn); err != nil {
			received <- result{err: err}
			return
		}
		body, err := frameproto.ReadRequestBytes(conn)
		if err != nil {
			received <- result{err: err}
			return
		}
		var request map[string]json.RawMessage
		if err := json.Unmarshal(body, &request); err != nil {
			received <- result{err: err}
			return
		}
		if _, err := frameproto.WriteExit(conn, 0); err != nil {
			received <- result{err: err}
			return
		}
		received <- result{request: request}
	}()
	t.Cleanup(func() {
		_ = endpoint.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			t.Error("fake call-broker endpoint did not stop")
		}
	})

	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if tty.IsTerminalFile(stdin) {
		t.Fatal("/dev/null unexpectedly classified as a terminal")
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdin.Close()
	})

	key := paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug("tasks") + paths.ServiceEnvVarSuffix
	t.Setenv(key, endpointPath)
	if rc := runInternal([]string{"call-broker-forward", "tasks", "--", "read"}); rc != 0 {
		t.Fatalf("call-broker-forward exit = %d, want 0", rc)
	}

	select {
	case got := <-received:
		if got.err != nil {
			t.Fatalf("fake call-broker endpoint: %v", got.err)
		}
		raw, ok := got.request["stdin"]
		if !ok {
			t.Fatalf("non-terminal /dev/null stdin was omitted from the forwarded request: keys=%v", got.request)
		}
		var stdinBase64 string
		if err := json.Unmarshal(raw, &stdinBase64); err != nil || stdinBase64 != "" {
			t.Fatalf("forwarded /dev/null stdin = %q, err=%v; want empty base64", stdinBase64, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call-broker-forward did not reach the fake endpoint")
	}
}
