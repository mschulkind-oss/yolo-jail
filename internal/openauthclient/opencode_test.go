package openauthclient

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// THE OPENCODE VIEW, through the command opencode's launcher runs (`yolo internal
// openai-auth-client token --opencode-auth <path>`): the `oauth` entry opencode's built-in ChatGPT
// support keys on, under opencode's own `openai` id, carrying the access token and the broker's
// generation marker and NEVER the canonical refresh token, merged beside every other provider's
// login the user stored. A response that smuggles a refresh token does not get it persisted.
func TestRunTokenMergesAnOpencodeView(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{"anthropic":{"type":"api","key":"keep-me"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		if _, present := s.Get("view"); present {
			s.Stderr("opencode must use the default access view\n")
			s.Exit(2)
			return
		}
		_ = s.JSON(map[string]any{
			"access_token": "access-oc", "expires_at": int64(4_102_444_800_000),
			"account_id": "acct-oc", "generation": int64(11),
			"refresh_token": "canonical-must-not-escape",
		})
		s.Exit(0)
	})
	var stdout, stderr bytes.Buffer
	rc := Run([]string{"token", "--opencode-auth", authPath}, func(name string) string {
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
	if strings.Contains(string(data), "canonical-must-not-escape") || strings.Contains(stdout.String(), "access-oc") {
		t.Fatalf("the opencode view exposed the broker response: file=%s stdout=%q", data, stdout.String())
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	oc := got["openai"]
	if oc["type"] != "oauth" || oc["access"] != "access-oc" || oc["refresh"] != "yolo-broker:11" ||
		oc["expires"] != float64(4_102_444_800_000) || oc["accountId"] != "acct-oc" {
		t.Fatalf("opencode credential = %#v", oc)
	}
	if got["anthropic"]["key"] != "keep-me" {
		t.Fatalf("the opencode writer lost an existing provider's login: %s", data)
	}
	if info, err := os.Stat(authPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("opencode auth mode = %v, %v; want 0600", info, err)
	}
	if strings.TrimSpace(stdout.String()) != `{"auth_path":"`+authPath+`"}` {
		t.Fatalf("stdout = %q, want the path written and nothing else", stdout.String())
	}
}

// An incomplete view writes nothing, so a broker that answered without a token cannot leave
// opencode an `oauth` entry with no access token, which opencode's own fetch would try to refresh.
func TestWriteOpencodeAuthRefusesAnIncompleteView(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	for _, response := range []string{
		`{"expires_at":4102444800000,"generation":2}`,
		`{"access_token":"a","generation":2}`,
		`{"access_token":"a","expires_at":4102444800000}`,
	} {
		if _, err := WriteOpencodeAuth(authPath, json.RawMessage(response)); err == nil {
			t.Errorf("WriteOpencodeAuth accepted %s", response)
		}
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("an incomplete view wrote %s: %v", authPath, err)
	}
}

// One view per call: the flags name three different files in three agents' formats.
func TestRunRefusesTwoViewFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := Run([]string{"token", "--pi-auth", "/tmp/a", "--opencode-auth", "/tmp/b"},
		func(string) string { return "" }, &stdout, &stderr)
	if rc != 2 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("rc=%d stderr=%q, want 2 and the refusal", rc, stderr.String())
	}
}

// A VIEW THAT REPLACES A LOGIN OF THE USER'S OWN SAYS SO. opencode files its own ChatGPT login and
// an OpenAI API key under the same `openai` key the view takes, so the write replaces either one.
// The launch then says what it replaced, where, and how to get an API key back: it never
// replaces one silently. Its own earlier view, and an empty store, replace nothing worth a line.
func TestRunTokenSaysWhenTheOpencodeViewReplacesALoginOfTheUsersOwn(t *testing.T) {
	endpoint := clientEndpoint(t, func(s *hostservice.Session) {
		_ = s.JSON(map[string]any{"access_token": "access-oc", "expires_at": int64(4_102_444_800_000), "generation": int64(3)})
		s.Exit(0)
	})
	for _, tc := range []struct {
		name, existing string
		want           []string
	}{
		{"no store", "", nil},
		{"yolo's earlier view", `{"openai":{"type":"oauth","refresh":"yolo-broker:2","access":"old","expires":1}}`, nil},
		{"another provider only", `{"anthropic":{"type":"api","key":"keep"}}`, nil},
		{"an API key", `{"openai":{"type":"api","key":"sk-users-own"}}`,
			[]string{"replaced an OpenAI API key", "/connect"}},
		{"opencode's own ChatGPT login", `{"openai":{"type":"oauth","refresh":"rt-users-own","access":"a","expires":1}}`,
			[]string{"replaced opencode's own ChatGPT login"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authPath := filepath.Join(t.TempDir(), "opencode", "auth.json")
			if tc.existing != "" {
				if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(authPath, []byte(tc.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			rc := Run([]string{"token", "--opencode-auth", authPath}, func(name string) string {
				if name == EndpointEnv {
					return endpoint
				}
				return ""
			}, &stdout, &stderr)
			if rc != 0 {
				t.Fatalf("rc=%d stderr=%q", rc, stderr.String())
			}
			if len(tc.want) == 0 {
				if stderr.Len() != 0 {
					t.Errorf("replacing nothing of the user's printed %q", stderr.String())
				}
				return
			}
			for _, want := range append(tc.want, authPath, "shared ChatGPT login") {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the replacement notice lacks %q:\n%s", want, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), "sk-users-own") || strings.Contains(stderr.String(), "rt-users-own") {
				t.Errorf("the notice printed the replaced secret: %s", stderr.String())
			}
		})
	}
}
