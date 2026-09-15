package openauthclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func clientEndpoint(t *testing.T, handler hostservice.Handler) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "broker.sock")
	endpoint := filepath.Join(dir, "broker.endpoint")
	daemonStop := make(chan struct{})
	frontStop := make(chan struct{})
	t.Cleanup(func() { close(frontStop); close(daemonStop) })
	go func() { _ = hostservice.ServeFrontedUnix(handler, socket, daemonStop) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("broker socket was not published")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", socket, frontStop) }()
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("broker endpoint was not published")
		}
		time.Sleep(time.Millisecond)
	}
	return endpoint
}

func clientUnixSocket(t *testing.T, handler hostservice.Handler) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "broker.sock")
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = hostservice.ServeUnix(handler, socket, stop) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			return socket
		}
		if time.Now().After(deadline) {
			t.Fatal("host broker socket was not published")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRequestUsesAuthenticatedFramingAndForwardsStderr(t *testing.T) {
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		action, _ := s.Get("action")
		if action != "token" {
			s.Stderr("wrong action\n")
			s.Exit(2)
			return
		}
		s.Stderr("refreshing cached generation\n")
		_ = s.JSON(map[string]any{"access_token": "access-1", "expires_at": int64(4_102_444_800_000)})
		s.Exit(0)
	})
	var stderr bytes.Buffer
	response, err := Request(endpoint, map[string]any{"action": "token"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "refreshing cached generation\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatal(err)
	}
	if got["access_token"] != "access-1" {
		t.Fatalf("response = %s", response)
	}
}

func TestRequestReturnsRemoteExitWithoutSwallowingDiagnostics(t *testing.T) {
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		s.Stderr("login required\n")
		s.Exit(7)
	})
	var stderr bytes.Buffer
	_, err := Request(endpoint, map[string]any{"action": "token"}, &stderr)
	var exitErr *RemoteExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("error = %v, want remote exit 7", err)
	}
	if stderr.String() != "login required\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRequestUnixUsesDirectHostFraming(t *testing.T) {
	socket := clientUnixSocket(t, func(s *hostservice.Session) {
		action, _ := s.Get("action")
		if action != "status" {
			s.Exit(2)
			return
		}
		s.Stderr("host status\n")
		_ = s.JSON(map[string]any{"logged_in": true})
		s.Exit(0)
	})
	var stderr bytes.Buffer
	response, err := RequestUnix(socket, map[string]any{"action": "status"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	if err := json.Unmarshal(response, &view); err != nil || view["logged_in"] != true || stderr.String() != "host status\n" {
		t.Fatalf("response=%s stderr=%q", response, stderr.String())
	}
}

func TestRunFallsBackToManagedHostSocket(t *testing.T) {
	socket := clientUnixSocket(t, func(s *hostservice.Session) {
		_ = s.JSON(map[string]any{"logged_in": true})
		s.Exit(0)
	})
	var stdout, stderr bytes.Buffer
	rc := Run([]string{"status"}, func(name string) string {
		if name == HostSocketEnv {
			return socket
		}
		return ""
	}, &stdout, &stderr)
	if rc != 0 || strings.TrimSpace(stdout.String()) != `{"logged_in":true}` {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, stdout.String(), stderr.String())
	}
}

func TestRunTokenWritesSafeJSON(t *testing.T) {
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		_ = s.JSON(map[string]any{
			"access_token": "access-1", "expires_at": int64(4_102_444_800_000), "account_id": "acct-1",
		})
		s.Exit(0)
	})
	var stdout, stderr bytes.Buffer
	rc := Run([]string{"token"}, func(name string) string {
		if name == EndpointEnv {
			return endpoint
		}
		return ""
	}, &stdout, &stderr)
	if rc != 0 || stderr.Len() != 0 {
		t.Fatalf("rc=%d stderr=%q", rc, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != `{"access_token":"access-1","account_id":"acct-1","expires_at":4102444800000}` {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunMapsOperationalCommandsToBrokerActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "login", args: []string{"login"}},
		{name: "status", args: []string{"status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := clientEndpoint(t, func(s *hostservice.Session) {
				action, _ := s.Get("action")
				if action != tc.name {
					s.Stderr("wrong action\n")
					s.Exit(2)
					return
				}
				if _, present := s.Get("path"); present {
					s.Stderr("client must not select the host import path\n")
					s.Exit(2)
					return
				}
				_ = s.JSON(map[string]any{"ok": true})
				s.Exit(0)
			})
			var stdout, stderr bytes.Buffer
			rc := Run(tc.args, func(name string) string {
				if name == EndpointEnv {
					return endpoint
				}
				return ""
			}, &stdout, &stderr)
			if rc != 0 || strings.TrimSpace(stdout.String()) != `{"ok":true}` {
				t.Fatalf("rc=%d stdout=%q stderr=%q", rc, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRefusesMachineWideMutations(t *testing.T) {
	for _, action := range []string{"import", "logout"} {
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{action}, func(string) string { return "" }, &stdout, &stderr); rc != 2 {
			t.Errorf("%s rc = %d, want usage refusal 2", action, rc)
		}
	}
}

func TestRunTokenWritesCodexViewAtomically(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, ".codex", "auth.json")
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		view, _ := s.Get("view")
		if view != "codex" {
			s.Exit(2)
			return
		}
		_ = s.JSON(map[string]any{
			"access_token": "access-1", "id_token": "id-1", "refresh_token": "yolo-broker:1",
			"expires_at": int64(4_102_444_800_000), "account_id": "acct-1",
		})
		s.Exit(0)
	})
	var stdout, stderr bytes.Buffer
	rc := Run([]string{"token", "--codex-auth", authPath}, func(name string) string {
		if name == EndpointEnv {
			return endpoint
		}
		return ""
	}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%q", rc, stderr.String())
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "yolo-broker:1") {
		t.Fatalf("stdout exposed canonical refresh token: %q", stdout.String())
	}
	var auth struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			AccessToken  string `json:"access_token"`
			IDToken      string `json:"id_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatal(err)
	}
	if auth.AuthMode != "chatgpt" || auth.Tokens.AccessToken != "access-1" ||
		auth.Tokens.IDToken != "id-1" || auth.Tokens.RefreshToken != "yolo-broker:1" || auth.Tokens.AccountID != "acct-1" {
		t.Fatalf("Codex auth view = %s", data)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Codex auth mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteCodexAuthRejectsCanonicalRefreshSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "auth.json")
	response := json.RawMessage(`{"access_token":"access","id_token":"id","refresh_token":"canonical-secret","expires_at":4102444800000}`)
	if err := WriteCodexAuth(path, response); err == nil || !strings.Contains(err.Error(), "non-broker") {
		t.Fatalf("WriteCodexAuth error = %v, want non-broker credential refusal", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused credential wrote %s: %v", path, err)
	}
}
