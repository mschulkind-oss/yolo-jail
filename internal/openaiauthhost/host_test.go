package openaiauthhost

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codexViewFixture() json.RawMessage {
	return json.RawMessage(`{"access_token":"access","id_token":"id","refresh_token":"yolo-broker:7","expires_at":4102444800000,"account_id":"acct"}`)
}

func TestPreparePiUsesHostSocketWithoutStartingAdapter(t *testing.T) {
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			return json.RawMessage(`{"logged_in":true}`), nil
		},
	}
	launch, err := prepare(d, "pi", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if launch.vars["YOLO_OPENAI_AUTH_HOST_SOCKET"] != "/tmp/broker.host" || launch.listener != nil {
		t.Fatalf("Pi launch = %#v", launch)
	}
}

func TestManagedCodexHomeLeavesOrdinaryAuthUntouchedAndAdapterFollowsAgent(t *testing.T) {
	root := t.TempDir()
	ordinary := filepath.Join(root, "home", ".codex")
	managedStore := filepath.Join(root, "store")
	if err := os.MkdirAll(filepath.Join(ordinary, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ordinary, "config.toml"), []byte("model = 'host'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinaryAuth := []byte(`{"refresh_token":"ordinary-secret"}`)
	if err := os.WriteFile(filepath.Join(ordinary, "auth.json"), ordinaryAuth, 0o600); err != nil {
		t.Fatal(err)
	}
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			action := request.(map[string]any)["action"]
			if action == "status" {
				return json.RawMessage(`{"logged_in":true}`), nil
			}
			return codexViewFixture(), nil
		},
		listen:  net.Listen,
		home:    func() string { return filepath.Join(root, "home") },
		storage: func() string { return managedStore },
	}
	launch, err := prepare(d, "codex", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(managedStore, "host-agents", "codex")
	if launch.vars["CODEX_HOME"] != managed {
		t.Fatalf("CODEX_HOME = %q", launch.vars["CODEX_HOME"])
	}
	if target, err := os.Readlink(filepath.Join(managed, "config.toml")); err != nil || target != filepath.Join(ordinary, "config.toml") {
		t.Fatalf("managed config link = %q, %v", target, err)
	}
	if got, _ := os.ReadFile(filepath.Join(ordinary, "auth.json")); !bytes.Equal(got, ordinaryAuth) {
		t.Fatalf("ordinary auth changed: %s", got)
	}
	managedAuth, err := os.ReadFile(filepath.Join(managed, "auth.json"))
	if err != nil || !strings.Contains(string(managedAuth), "yolo-broker:7") || strings.Contains(string(managedAuth), "ordinary-secret") {
		t.Fatalf("managed auth = %s, %v", managedAuth, err)
	}
	address := launch.listener.Addr().String()
	rc, handled := launch.Run("/bin/sh", []string{"sh", "-c", "test -n \"$CODEX_HOME\""}, launch.Environ(os.Environ()), nil, io.Discard, io.Discard)
	if !handled || rc != 0 {
		t.Fatalf("Run = %d, %v", rc, handled)
	}
	if conn, err := net.Dial("tcp", address); err == nil {
		conn.Close()
		t.Fatal("Codex adapter still accepts connections after agent exit")
	}
}

func TestPrepareStartsBrowserLoginOnlyWhenStatusRequiresIt(t *testing.T) {
	var actions []string
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			action := request.(map[string]any)["action"].(string)
			actions = append(actions, action)
			if action == "status" {
				return json.RawMessage(`{"logged_in":false}`), nil
			}
			return json.RawMessage(`{"ok":true}`), nil
		},
	}
	if _, err := prepare(d, "pi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "status,login" {
		t.Fatalf("actions = %v", actions)
	}
}

func TestManagedEnvironmentOverridesHaveStableOrder(t *testing.T) {
	launch := &Launch{vars: map[string]string{"Z_LAST": "z", "A_FIRST": "a"}}
	want := []string{"KEEP=1", "A_FIRST=a", "Z_LAST=z"}
	for i := 0; i < 20; i++ {
		got := launch.Environ([]string{"KEEP=1", "Z_LAST=old"})
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Environ = %v, want stable %v", got, want)
		}
	}
}

func TestHostSocketWaitRetriesDuringDaemonStartup(t *testing.T) {
	attempts := 0
	err := waitHostSocket("/tmp/broker.host", time.Second,
		func(string, any, io.Writer) (json.RawMessage, error) {
			attempts++
			if attempts < 3 {
				return nil, os.ErrNotExist
			}
			return json.RawMessage(`{"pong":true}`), nil
		})
	if err != nil || attempts != 3 {
		t.Fatalf("waitHostSocket = %v after %d attempts, want success after retry", err, attempts)
	}
}
