package mcpauthdaemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// onceDaemonFixture is a broker whose share names one `sign_in = "once"` server and Claude's
// machine-wide store, read with the SHIPPED Claude pack's declaration and derive, against a
// loopback authorization server with a single-use refresh token.
type onceDaemonFixture struct {
	b        *broker
	storeDir string
	refreshs chan string
}

func newOnceDaemonFixture(t *testing.T) *onceDaemonFixture {
	t.Helper()
	f := &onceDaemonFixture{storeDir: t.TempDir(), refreshs: make(chan string, 4)}
	var base string
	var issuerMu sync.Mutex
	currentRefresh, generation := "rt-1", 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "token_endpoint": base + "/token"})
		case "/token":
			_ = r.ParseForm()
			issuerMu.Lock()
			defer issuerMu.Unlock()
			incoming := r.PostForm.Get("refresh_token")
			f.refreshs <- incoming
			if incoming != currentRefresh {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			generation++
			currentRefresh = fmt.Sprintf("rt-%d", generation)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("at-%d", generation), "token_type": "Bearer", "expires_in": 3600, "refresh_token": currentRefresh})
		default:
			http.NotFound(w, r)
		}
	}))
	base = srv.URL
	t.Cleanup(srv.Close)

	root, err := filepath.Abs(filepath.Join("..", "..", "packs", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(root, "claude")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	entry := map[string]any{"accessToken": "at-1", "refreshToken": "rt-1", "expiresAt": time.Now().Add(time.Hour).UnixMilli(),
		"clientId": "client-1", "issuer": base, "serverName": "plugin:slack:slack", "serverUrl": base + "/mcp"}
	doc, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{}, "mcpOAuth": map[string]any{"plugin:slack:slack|0000000000000000": entry}})
	if err := os.WriteFile(filepath.Join(f.storeDir, ".credentials.json"), doc, 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, err := mcpauth.NewCanonicalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	share := mcpauth.Share{Version: 1, Jail: "jail-once", Workspace: t.TempDir(), WrittenAt: time.Now(), Replicas: []mcpauth.Replica{},
		Once:    []mcpauth.OnceServer{{Name: "slack", Identity: strings.Repeat("c", 64), URL: base + "/mcp"}},
		Sources: []mcpauth.Source{{Pack: "claude", DeriveFile: filepath.Join(root, "derive.lua"), Dir: f.storeDir, Store: ".credentials.json", LockDir: ".storage-write.lock", Decl: *pack.Decl.MCPAuth}}}
	if err := share.Validate(); err != nil {
		t.Fatal(err)
	}
	f.b = &broker{share: share, canonical: canonical}
	if err := f.b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestOnceQualifiedSameLeafSourcesKeepScriptsAndAdoptionDistinct(t *testing.T) {
	for _, kind := range []string{"unmatched-identical-grant", "wrong-url", "wrong-client", "wrong-issuer", "unmatched-different-grant", "changed-under-source-lock", "changed-before-record-refresh", "compatible"} {
		t.Run(kind, func(t *testing.T) {
			f := newOnceDaemonFixture(t)
			left := f.b.share.Sources[0]
			left.Pack, left.Adapter = "left/claude", "claude"
			right := left
			right.Pack = "right/claude"
			right.Dir = t.TempDir()
			right.DeriveFile = filepath.Join(t.TempDir(), "derive.lua")
			script := packload.DeriveScript(&packload.Pack{Root: filepath.Dir(left.DeriveFile)})
			if strings.HasPrefix(kind, "unmatched") {
				script = `yolo.mcp_auth("claude", {key=function(s) return "x" end,match=function(k,e) return nil end})`
			}
			if err := os.WriteFile(right.DeriveFile, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(filepath.Join(left.Dir, left.Store))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]map[string]map[string]any
			if err := json.Unmarshal(original, &doc); err != nil {
				t.Fatal(err)
			}
			entry := doc["mcpOAuth"]["plugin:slack:slack|0000000000000000"]
			f.b.share.Once[0].ExpectedClientID = "client-1"
			switch kind {
			case "wrong-url":
				entry["serverUrl"] = "https://wrong-source.invalid/mcp"
			case "wrong-client":
				entry["clientId"] = "wrong-client"
			case "wrong-issuer":
				entry["issuer"] = "https://wrong-source.invalid"
			case "unmatched-different-grant":
				entry["refreshToken"] = "different-private-fixture-grant"
			}
			original, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(right.Dir, right.Store), original, 0600); err != nil {
				t.Fatal(err)
			}
			f.b.share.Sources = []mcpauth.Source{left, right}
			if err := f.b.share.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := f.b.loadScripts(); err != nil {
				t.Fatal(err)
			}
			once, err := f.b.newOnceBroker()
			if err != nil {
				t.Fatal(err)
			}
			sources := f.b.onceSources()
			changedUnderLock := false
			if kind == "changed-under-source-lock" {
				lockDir := filepath.Join(right.Dir, right.LockDir)
				if err := os.Mkdir(lockDir, 0700); err != nil {
					t.Fatal(err)
				}
				sources[1].Store.Sleep = func(time.Duration) {
					changedUnderLock = true
					entry["serverUrl"] = "https://replaced-source.invalid/mcp"
					original, err = json.Marshal(doc)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(right.Dir, right.Store), original, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(lockDir); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "changed-before-record-refresh" {
				if err := f.b.passAll(context.Background(), once, sources[:1]); err != nil {
					t.Fatal(err)
				}
				now := time.Now().Add(2 * time.Hour)
				once.Now = func() time.Time { return now }
				match := sources[1].Match
				calls := 0
				sources[1].Match = func(key string, raw json.RawMessage) (string, string, bool) {
					url, issuer, ok := match(key, raw)
					calls++
					if calls == 2 {
						entry["serverUrl"] = "https://replaced-source.invalid/mcp"
						original, err = json.Marshal(doc)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(right.Dir, right.Store), original, 0600); err != nil {
							t.Fatal(err)
						}
					}
					return url, issuer, ok
				}
				if err := once.Pass(context.Background(), sources[1], f.b.share.Once); err != nil || calls < 2 {
					t.Fatal("actual matching-entry mutation event missing", err, calls)
				}
			} else if err := f.b.passAll(context.Background(), once, sources); err != nil {
				t.Fatal(err)
			}
			if kind == "changed-under-source-lock" && !changedUnderLock {
				t.Fatal("source lock event was not exercised")
			}
			select {
			case <-f.refreshs:
			default:
				t.Fatal("same-leaf script key collision prevented left source adoption")
			}
			select {
			case <-f.refreshs:
				t.Fatal("right source derived an unexpected provider redemption")
			default:
			}
			store, err := f.b.onceStore()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
				if len(file.Records) != 1 || file.Records[0].AdoptedFrom != "left/claude" {
					t.Fatal("logical source identities were conflated")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if kind != "compatible" {
				now := time.Now().Add(2 * time.Hour)
				once.Now = func() time.Time { return now }
				if err := once.Pass(context.Background(), f.b.onceSources()[1], f.b.share.Once); err != nil {
					t.Fatal(err)
				}
				select {
				case <-f.refreshs:
					t.Fatal("expired canonical record made mismatched right source transmit")
				default:
				}
				// Refusing one source must not revoke or stall canonical-only work.
				if err := once.Maintain(context.Background(), f.b.share.Once[0], nil, true); err != nil {
					t.Fatal(err)
				}
				select {
				case grant := <-f.refreshs:
					if grant != "rt-2" {
						t.Fatal("canonical-only refresh used a foreign source grant")
					}
				default:
					t.Fatal("canonical-only refresh became unavailable")
				}
			}
			retained, err := os.ReadFile(filepath.Join(right.Dir, right.Store))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "compatible" {
				if err := json.Unmarshal(retained, &doc); err != nil {
					t.Fatal(err)
				}
				entry = doc["mcpOAuth"]["plugin:slack:slack|0000000000000000"]
				if entry["accessToken"] != "at-2" || entry["refreshToken"] != nil {
					t.Fatal("compatible same-grant source lost intentional writeback")
				}
			} else if string(original) != string(retained) {
				t.Fatal("unmatched same-leaf private source bytes/credentials changed")
			}
		})
	}
}

func (f *onceDaemonFixture) entry(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.storeDir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCP map[string]map[string]any `json:"mcpOAuth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.MCP["plugin:slack:slack|0000000000000000"]
}

// The daemon's poll adopts the Claude sign-in on its first pass and stops when told: the call
// site that turns the share's one-sign-in half into a running broker.
func TestOnceLoopAdoptsClaudesSignInFromTheShare(t *testing.T) {
	f := newOnceDaemonFixture(t)
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 4)
	restore := setOnceSeams(ticks, func() { passes <- struct{}{} })
	defer restore()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { f.b.runOnce(stop); close(done) }()
	if rt := <-f.refreshs; rt != "rt-1" {
		t.Fatalf("redeemed %q, want the source's own token", rt)
	}
	<-passes
	e := f.entry(t)
	if e["accessToken"] != "at-2" || e["refreshToken"] != nil {
		t.Fatal("the first pass did not write the adopted sign-in back without its refresh token")
	}
	// A tick runs another pass; with nothing due it redeems nothing.
	ticks <- time.Now()
	<-passes
	close(stop)
	<-done
	select {
	case rt := <-f.refreshs:
		t.Fatalf("a pass with nothing due redeemed %q", rt)
	default:
	}
	store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.Read()
	if err != nil || len(file.Records) != 1 || file.Records[0].RefreshToken != "rt-2" {
		t.Fatalf("canonical once records = %d, err %v", len(file.Records), err)
	}
}

// ID-19: a launcher where no broker runs takes the sign-in over itself in one pass, with the
// daemon's own canonical store and Claude's write-back rule, and its lines go to the caller,
// never to the daemon's stderr. The audit names the adoption.
func TestBoundaryPassAdoptsClaudesSignInWithNoDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOnceDaemonFixture(t)
	var lines []string
	if err := BoundaryPass(context.Background(), f.b.canonical.Dir, f.b.share, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if rt := <-f.refreshs; rt != "rt-1" {
		t.Fatalf("redeemed %q, want the source's own token", rt)
	}
	e := f.entry(t)
	if e["accessToken"] != "at-2" || e["refreshToken"] != nil {
		t.Fatal("the boundary pass did not write the adopted sign-in back without its refresh token")
	}
	store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
	if err != nil {
		t.Fatal(err)
	}
	if file, err := store.Read(); err != nil || len(file.Records) != 1 || file.Records[0].SourceDir != f.storeDir {
		t.Fatalf("canonical once records = %+v, err %v", file.Records, err)
	}
	audit, _ := os.ReadFile(filepath.Join(paths.BrokerDir(), "audit.jsonl"))
	if !strings.Contains(string(audit), `"adopt"`) || strings.Contains(string(audit), "rt-2") {
		t.Fatalf("audit = %s", audit)
	}
	// A second boundary pass with nothing due redeems nothing.
	if err := BoundaryPass(context.Background(), f.b.canonical.Dir, f.b.share, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case rt := <-f.refreshs:
		t.Fatalf("a boundary pass with nothing due redeemed %q", rt)
	default:
	}
}

// ID-20: a session-long launcher runs the daemon's poll and it ends with stop.
func TestServeOnceRunsThePollUntilStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newOnceDaemonFixture(t)
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 4)
	restore := setOnceSeams(ticks, func() { passes <- struct{}{} })
	defer restore()
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() { done <- ServeOnce(f.b.canonical.Dir, f.b.share, func(string) {}, stop) }()
	if rt := <-f.refreshs; rt != "rt-1" {
		t.Fatalf("redeemed %q, want the source's own token", rt)
	}
	<-passes
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOnceStatusListsTheOneSignInServers(t *testing.T) {
	f := newOnceDaemonFixture(t)
	rows := ReadOnceStatus(f.b.canonical.Dir, f.b.share, f.b.onceSources(), "this launch's broker")
	if len(rows) != 1 || rows[0]["name"] != "slack" || rows[0]["state"] != "not-signed-in" {
		t.Fatalf("rows = %v", rows)
	}
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 4)
	restore := setOnceSeams(ticks, func() { passes <- struct{}{} })
	defer restore()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { f.b.runOnce(stop); close(done) }()
	<-f.refreshs
	<-passes
	close(stop)
	<-done
	rows = ReadOnceStatus(f.b.canonical.Dir, f.b.share, f.b.onceSources(), "this launch's broker")
	if len(rows) != 1 || rows[0]["state"] != "live" || rows[0]["adopted_from"] != "claude" {
		t.Fatalf("rows = %v", rows)
	}
	data, _ := json.Marshal(rows)
	if strings.Contains(string(data), "rt-2") || strings.Contains(string(data), "at-2") {
		t.Fatal("status carries a token")
	}
}

// serve is what Main runs after readiness: it must start the one-sign-in poll, and its status
// answer must carry the poll's rows. Deleting either call site fails here.
func TestServeRunsTheOnePollAndReportsItInStatus(t *testing.T) {
	f := newOnceDaemonFixture(t)
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 4)
	restore := setOnceSeams(ticks, func() { passes <- struct{}{} })
	defer restore()
	parent, err := os.MkdirTemp(testtmp.ShortDir(), "yj-mcp-once-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent)
	frontDir := filepath.Join(parent, f.b.share.Jail)
	if err := os.Mkdir(frontDir, 0o700); err != nil {
		t.Fatal(err)
	}
	upstream := filepath.Join(parent, "upstream.sock")
	endpoint := filepath.Join(frontDir, "front.endpoint")
	f.b.session = BindShareSession(f.b.share, f.b.share.Jail, endpoint)
	stop, frontStop := make(chan struct{}), make(chan struct{})
	served, frontDone := make(chan error, 1), make(chan struct{})
	go func() { served <- f.b.serve(upstream, stop) }()
	if rt := <-f.refreshs; rt != "rt-1" {
		t.Fatalf("serve's poll redeemed %q, want the source's own token", rt)
	}
	<-passes
	waitForMCPStatusPath(t, upstream)
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", upstream, frontStop); close(frontDone) }()
	waitForMCPStatusPath(t, endpoint)
	defer func() { close(frontStop); <-frontDone }()
	conn, err := svcendpoint.DialLocal(endpoint, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := frameproto.WriteRequest(conn, []byte(`{"op":"status"}`)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		if frame.StreamID == frameproto.StreamExit {
			break
		}
		out.Write(frame.Payload)
	}
	if strings.Contains(out.String(), "at-2") || strings.Contains(out.String(), "rt-2") {
		t.Fatal("status carries a token")
	}
	var status struct {
		Once []map[string]any `json:"once"`
	}
	if err := json.Unmarshal([]byte(out.String()), &status); err != nil {
		t.Fatalf("status %q is not one JSON document: %v", out.String(), err)
	}
	if len(status.Once) != 1 || status.Once[0]["name"] != "slack" || status.Once[0]["state"] != "live" {
		t.Fatalf("status once rows = %v", status.Once)
	}
	close(stop)
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
