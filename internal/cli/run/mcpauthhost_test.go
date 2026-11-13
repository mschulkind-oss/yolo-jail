package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ID-21: at `yolo host` on Linux the session adopts a sign_in = "once" server's sign-in from the
// real home's ~/.claude/.credentials.json, writes the fresh access token back there without the
// refresh token, and the canonical record names ~/.claude as its source store (ID-18), so a
// jail's bridge file that never held the entry is not a sign-out. Deleting the host's source
// resolver in HostOnceSession (the real home's store) adopts nothing and fails here.
func TestHostOnceSessionAdoptsFromTheRealHomesClaudeStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	refreshes := make(chan string, 4)
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "token_endpoint": base + "/token"})
		case "/token":
			_ = r.ParseForm()
			refreshes <- r.PostForm.Get("refresh_token")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt-2"})
		default:
			http.NotFound(w, r)
		}
	}))
	base = srv.URL
	t.Cleanup(srv.Close)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"accessToken": "at-1", "refreshToken": "rt-1", "expiresAt": time.Now().Add(time.Hour).UnixMilli(),
		"clientId": "client-1", "issuer": base, "serverName": "plugin:slack:slack", "serverUrl": base + "/mcp"}
	doc, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "login"},
		"mcpOAuth": map[string]any{"plugin:slack:slack|0000000000000000": entry}})
	store := filepath.Join(claudeDir, ".credentials.json")
	if err := os.WriteFile(store, doc, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := jsonx.Decode([]byte(`{"packs":["claude"],"mcp_servers":{"slack":{"url":"` + base + `/mcp","sign_in":"once"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	claude := shippedPack(t, "claude")
	session, err := HostOnceSession(cfg.(*jsonx.OrderedMap), []*packload.Pack{claude}, claude, home, t.TempDir(), nil, nil)
	if err != nil || session == nil {
		t.Fatalf("HostOnceSession = %v, %v; want a session", session, err)
	}
	if got := session.Stores(); len(got) != 1 || got[0] != store {
		t.Fatalf("the session's stores = %q, want the real home's %s", got, store)
	}
	state := t.TempDir()
	if err := mcpauthdaemon.BoundaryPass(context.Background(), state, session.share, nil); err != nil {
		t.Fatal(err)
	}
	if rt := <-refreshes; rt != "rt-1" {
		t.Fatalf("redeemed %q, want ~/.claude's own token", rt)
	}
	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Login map[string]any            `json:"claudeAiOauth"`
		MCP   map[string]map[string]any `json:"mcpOAuth"`
	}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if e := back.MCP["plugin:slack:slack|0000000000000000"]; e["accessToken"] != "at-2" || e["refreshToken"] != nil {
		t.Fatalf("~/.claude's entry = %v, want the fresh access token and no refresh token", e)
	}
	if back.Login["accessToken"] != "login" {
		t.Fatalf("the Claude login beside the entry was touched: %v", back.Login)
	}
	once, err := mcpauth.NewOnceStore(filepath.Join(state, "once"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := once.Read()
	if err != nil || len(file.Records) != 1 || file.Records[0].SourceDir != claudeDir {
		t.Fatalf("canonical records = %+v, err %v; want one whose source_dir is %s", file.Records, err, claudeDir)
	}
}

// The host session adopts only from the launched program's own pack, and nothing at all without
// a sign_in = "once" server: `yolo host -- pi` never writes into ~/.claude.
func TestHostOnceSessionKeepsNothingElse(t *testing.T) {
	claude := shippedPack(t, "claude")
	for name, tc := range map[string]struct {
		cfg   string
		agent *packload.Pack
	}{
		"no once server":     {`{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp"}}}`, claude},
		"another program":    {`{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`, &packload.Pack{Name: "pi"}},
		"no launched pack":   {`{"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`, nil},
		"no mcp_servers key": {`{}`, claude},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := jsonx.Decode([]byte(tc.cfg))
			if err != nil {
				t.Fatal(err)
			}
			session, err := HostOnceSession(cfg.(*jsonx.OrderedMap), []*packload.Pack{claude}, tc.agent, t.TempDir(), t.TempDir(), nil, nil)
			if err != nil || session != nil {
				t.Fatalf("HostOnceSession = %v, %v; want none", session, err)
			}
		})
	}
}
