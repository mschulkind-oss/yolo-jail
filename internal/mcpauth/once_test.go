package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// asServer is a loopback authorization server whose refresh tokens are single-use, as the
// owner's Slack server's are: redeeming one spends it and issues the next.
type asServer struct {
	mu      sync.Mutex
	base    string
	valid   map[string]bool
	n       int
	calls   []string
	fail    string // an OAuth error code to answer with, or "" to succeed
	status  int
	issuer  string // the issuer the metadata names; "" means base
	lifeSec int
}

func newASServer(t *testing.T) *asServer {
	a := &asServer{valid: map[string]bool{"rt-1": true}, n: 1, lifeSec: 3600}
	srv := oauthServer(t, func(base string) *http.ServeMux {
		m := http.NewServeMux()
		m.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
			issuer := a.issuer
			if issuer == "" {
				issuer = base
			}
			writeJSON(w, 200, map[string]any{"issuer": issuer, "token_endpoint": base + "/token"})
		})
		m.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			a.mu.Lock()
			defer a.mu.Unlock()
			rt := r.PostForm.Get("refresh_token")
			a.calls = append(a.calls, rt)
			if a.fail != "" {
				writeJSON(w, a.status, map[string]any{"error": a.fail})
				return
			}
			if !a.valid[rt] {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(a.valid, rt)
			a.n++
			next := fmt.Sprintf("rt-%d", a.n)
			a.valid[next] = true
			writeJSON(w, 200, map[string]any{"access_token": fmt.Sprintf("at-%d", a.n), "token_type": "Bearer", "expires_in": a.lifeSec, "refresh_token": next})
		})
		return m
	})
	a.base = srv.URL
	return a
}

func (a *asServer) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

type onceFixture struct {
	t      *testing.T
	clock  *fakeClock
	as     *asServer
	dir    string
	src    OnceSource
	server OnceServer
	events []Event
	broker *OnceBroker
}

const slackKey = "plugin:slack:slack|26a3e4fbcc4adcb9"

func newOnceFixture(t *testing.T) *onceFixture {
	f := &onceFixture{t: t, clock: newFakeClock(), as: newASServer(t), dir: t.TempDir()}
	url := f.as.base + "/mcp"
	f.server = OnceServer{Name: "slack", Identity: strings.Repeat("ab", 32), URL: url}
	store := f.clock.source(f.dir)
	f.src = OnceSource{Pack: "claude", Store: store,
		Fields: packdecl.MCPAuthFields{AccessToken: "accessToken", RefreshToken: "refreshToken", ExpiresAt: packdecl.MCPAuthExpiryField{Path: "expiresAt", Unit: "ms"}, ClientID: "clientId"},
		Match: func(key string, entry json.RawMessage) (string, string, bool) {
			var e struct {
				ServerURL string `json:"serverUrl"`
				Issuer    string `json:"issuer"`
			}
			if json.Unmarshal(entry, &e) != nil || e.ServerURL == "" {
				return "", "", false
			}
			return e.ServerURL, e.Issuer, true
		}}
	canonicalDir := filepath.Join(t.TempDir(), "once")
	f.broker = f.newBroker(canonicalDir)
	f.writeEntry(map[string]any{"accessToken": "at-1", "refreshToken": "rt-1", "expiresAt": f.clock.now.Add(time.Hour).UnixMilli(),
		"clientId": "client-1", "issuer": f.as.base, "serverUrl": url, "serverName": "plugin:slack:slack"})
	return f
}

func (f *onceFixture) newBroker(canonicalDir string) *OnceBroker {
	store, err := NewOnceStore(canonicalDir)
	if err != nil {
		f.t.Fatal(err)
	}
	return &OnceBroker{Canonical: store, Now: f.clock.Now, Audit: func(e Event) { f.events = append(f.events, e) }}
}

func (f *onceFixture) writeEntry(entry map[string]any) {
	f.t.Helper()
	doc := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "login"}, "mcpOAuth": map[string]any{}}
	if entry != nil {
		doc["mcpOAuth"] = map[string]any{slackKey: entry}
	}
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(f.dir, ".credentials.json"), data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *onceFixture) entry() map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, ".credentials.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var doc struct {
		MCP map[string]map[string]any `json:"mcpOAuth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		f.t.Fatal(err)
	}
	return doc.MCP[slackKey]
}

func (f *onceFixture) record(b *OnceBroker) *Record {
	f.t.Helper()
	file, err := b.Canonical.Read()
	if err != nil {
		f.t.Fatal(err)
	}
	for i := range file.Records {
		if file.Records[i].Server == f.server.Name {
			return &file.Records[i]
		}
	}
	return nil
}

func (f *onceFixture) pass(b *OnceBroker) {
	f.t.Helper()
	if err := b.Pass(context.Background(), f.src, []OnceServer{f.server}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *onceFixture) actions() string {
	var out []string
	for _, e := range f.events {
		out = append(out, e.Action)
	}
	return strings.Join(out, ",")
}

// §9.3 + MA-D17: adoption is a successful first refresh. The broker then holds the new refresh
// token, and the source entry keeps only the fresh access token and its expiry.
func TestOnceAdoptsTheSignInAndTakesItsRefreshToken(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	if f.as.callCount() != 1 || f.as.calls[0] != "rt-1" {
		t.Fatalf("refresh calls = %v, want one with the source's token", f.as.calls)
	}
	r := f.record(f.broker)
	if r == nil || r.State != "live" || r.RefreshToken != "rt-2" || r.AccessToken != "at-2" || r.Generation != 1 || r.SourceKey != slackKey || r.AdoptedFrom != "claude" {
		t.Fatalf("record = %+v", r)
	}
	e := f.entry()
	if e["accessToken"] != "at-2" || e["refreshToken"] != nil {
		t.Fatalf("source entry still holds a refresh token or the old access token: access %v, refresh present %v", e["accessToken"] == "at-2", e["refreshToken"] != nil)
	}
	if want := f.clock.now.Add(time.Hour).UnixMilli(); int64(e["expiresAt"].(float64)) != want {
		t.Fatalf("expiresAt = %v, want %d", e["expiresAt"], want)
	}
	if e["serverUrl"] == nil || e["clientId"] != "client-1" {
		t.Fatal("the rest of Claude's entry was not kept")
	}
	if f.actions() != "adopt" {
		t.Fatalf("audit = %s", f.actions())
	}
	// A second pass with nothing due does nothing.
	f.pass(f.broker)
	if f.as.callCount() != 1 {
		t.Fatalf("a pass with nothing due refreshed: %v", f.as.calls)
	}
}

func TestOnceRefusesAnIssuerTheServerDoesNotName(t *testing.T) {
	f := newOnceFixture(t)
	f.as.issuer = "https://elsewhere.test"
	f.pass(f.broker)
	if f.as.callCount() != 0 {
		t.Fatal("a refresh token was sent though discovery did not confirm its issuer")
	}
	if f.entry()["refreshToken"] != "rt-1" || f.record(f.broker) != nil {
		t.Fatal("an unconfirmed sign-in was adopted")
	}
}

// OQ-MA8: an agent whose sign-in the broker cannot take keeps signing itself in.
func TestOnceLeavesASignInItCannotRefresh(t *testing.T) {
	f := newOnceFixture(t)
	f.as.fail, f.as.status = "invalid_client", 401
	f.pass(f.broker)
	if f.entry()["refreshToken"] != "rt-1" || f.record(f.broker) != nil {
		t.Fatal("a sign-in the broker could not refresh lost its refresh token")
	}
	if f.actions() != "adopt-refused" {
		t.Fatalf("audit = %s", f.actions())
	}
	f.pass(f.broker)
	if f.as.callCount() != 1 {
		t.Fatalf("a refused refresh token was retried: %d calls", f.as.callCount())
	}
}

// MA-D10: the broker refreshes when a quarter of the lifetime or 10 minutes remain, whichever
// is later, and writes the access token back.
func TestOnceRefreshesWhenDueAndWritesBack(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.clock.now = f.clock.now.Add(44 * time.Minute) // 16 min left of 60: not yet a quarter
	f.pass(f.broker)
	if f.as.callCount() != 1 {
		t.Fatalf("refreshed early: %v", f.as.calls)
	}
	f.clock.now = f.clock.now.Add(time.Minute + time.Second) // under 15 min left
	f.pass(f.broker)
	if f.as.callCount() != 2 || f.as.calls[1] != "rt-2" {
		t.Fatalf("calls = %v, want the broker's own token redeemed", f.as.calls)
	}
	if r := f.record(f.broker); r.RefreshToken != "rt-3" || r.AccessToken != "at-3" || r.Generation != 1 {
		t.Fatalf("record = %+v", r)
	}
	if e := f.entry(); e["accessToken"] != "at-3" || e["refreshToken"] != nil {
		t.Fatal("the refreshed access token was not written back")
	}
	if f.actions() != "adopt,refresh" {
		t.Fatalf("audit = %s", f.actions())
	}
}

// ID-6's rule for the one-sign-in tier: every jail's broker passes, and one refresh happens.
func TestOnceTwoBrokersRefreshOnce(t *testing.T) {
	f := newOnceFixture(t)
	other := f.newBroker(f.broker.Canonical.Dir)
	f.pass(f.broker)
	f.pass(other)
	f.clock.now = f.clock.now.Add(50 * time.Minute)
	f.pass(other)
	f.pass(f.broker)
	if f.as.callCount() != 2 {
		t.Fatalf("calls = %v, want one adoption and one refresh across two brokers", f.as.calls)
	}
}

func TestOnceDeadRecordSignsOutTheSource(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.as.fail, f.as.status = "invalid_grant", 400
	f.clock.now = f.clock.now.Add(55 * time.Minute)
	f.pass(f.broker)
	if r := f.record(f.broker); r == nil || r.State != "dead" || r.RefreshToken != "" || r.AccessToken != "" {
		t.Fatalf("record = %+v, want dead and holding no token", r)
	}
	if f.entry() != nil {
		t.Fatal("the source still holds the dead sign-in's entry; Claude would not ask to sign in again")
	}
}

// OQ-MA4: an entry gone from a cleanly parsed store is a sign-out everywhere; a store that does
// not parse is not.
func TestOnceSignOutNeedsACleanlyParsedStore(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	if err := os.WriteFile(filepath.Join(f.dir, ".credentials.json"), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.pass(f.broker)
	if f.record(f.broker) == nil {
		t.Fatal("an unparseable store signed out")
	}
	f.writeEntry(nil)
	f.pass(f.broker)
	if f.record(f.broker) != nil {
		t.Fatal("the entry left the store and the record stayed")
	}
	if !strings.HasSuffix(f.actions(), "sign-out") {
		t.Fatalf("audit = %s", f.actions())
	}
}

// Latest sign-in wins: a new refresh token in the source after adoption is a new sign-in.
func TestOnceANewSignInIsAdoptedAgain(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	f.as.mu.Lock()
	f.as.valid["rt-fresh"] = true
	f.as.mu.Unlock()
	f.writeEntry(map[string]any{"accessToken": "at-fresh", "refreshToken": "rt-fresh", "expiresAt": f.clock.now.Add(time.Hour).UnixMilli(),
		"clientId": "client-1", "issuer": f.as.base, "serverUrl": f.server.URL})
	f.pass(f.broker)
	if r := f.record(f.broker); r.Generation != 2 || r.RefreshToken != "rt-3" {
		t.Fatalf("record = %+v, want generation 2 from the new sign-in", r)
	}
	if f.entry()["refreshToken"] != nil {
		t.Fatal("the new sign-in kept its refresh token in the source")
	}
}

// Canonical commits before the source is stripped; when the write-back did not happen, the
// spent token left in the source is recognized and written over, never redeemed again.
func TestOnceASpentTokenLeftInTheSourceIsWrittenOverNotRedeemed(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	// Put back the pre-adoption entry, as if the write-back lost to Claude's lock.
	f.writeEntry(map[string]any{"accessToken": "at-1", "refreshToken": "rt-1", "expiresAt": f.clock.now.Add(time.Hour).UnixMilli(),
		"clientId": "client-1", "issuer": f.as.base, "serverUrl": f.server.URL})
	f.pass(f.broker)
	if f.as.callCount() != 1 {
		t.Fatalf("the spent token was redeemed again: %v", f.as.calls)
	}
	if e := f.entry(); e["accessToken"] != "at-2" || e["refreshToken"] != nil {
		t.Fatal("the source was not brought up to the broker's sign-in")
	}
}

func TestOnceTransientFailureBacksOff(t *testing.T) {
	f := newOnceFixture(t)
	f.as.fail, f.as.status = "temporarily_unavailable", 503
	f.pass(f.broker)
	f.pass(f.broker)
	if f.as.callCount() != 1 {
		t.Fatalf("retried before the backoff: %d calls", f.as.callCount())
	}
	f.clock.now = f.clock.now.Add(30 * time.Second)
	f.as.fail = ""
	f.pass(f.broker)
	if f.as.callCount() != 2 || f.record(f.broker) == nil {
		t.Fatalf("not retried after 30 s: %d calls", f.as.callCount())
	}
}

func TestOnceAuditNamesNoToken(t *testing.T) {
	f := newOnceFixture(t)
	f.pass(f.broker)
	data, _ := json.Marshal(f.events)
	for _, token := range []string{"rt-1", "rt-2", "at-1", "at-2"} {
		if strings.Contains(string(data), token) {
			t.Fatalf("an audit event carries a token: %s", data)
		}
	}
}

func validOnceShare(dir string) Share {
	lock := packdecl.MCPAuthLock{Protocol: "proper-lockfile", Path: ".claude/.storage-write.lock", StaleSeconds: 15}
	return Share{Version: 1, Jail: "jail", Workspace: "/ws", WrittenAt: time.Now(), Replicas: []Replica{},
		Once: []OnceServer{{Name: "slack", Identity: strings.Repeat("ab", 32), URL: "https://mcp.slack.com/mcp"}},
		Sources: []Source{{Pack: "claude", DeriveFile: "/packs/claude/derive.lua", Dir: dir, Store: ".credentials.json", LockDir: ".storage-write.lock",
			Decl: packdecl.MCPAuth{Format: "json", EntriesAt: []string{"mcpOAuth"}, Lock: &lock,
				Fields: packdecl.MCPAuthFields{AccessToken: "accessToken", RefreshToken: "refreshToken"}}}}}
}

func TestShareValidatesOneSignInSources(t *testing.T) {
	if err := validOnceShare("/home/u/.local/share/yolo-jail/home/.claude-shared-credentials").Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Share){
		"relative dir":     func(s *Share) { s.Sources[0].Dir = "home/.claude-shared-credentials" },
		"store with a dir": func(s *Share) { s.Sources[0].Store = "x/.credentials.json" },
		"flock lock":       func(s *Share) { s.Sources[0].Decl.Lock.Protocol = "flock" },
		"no stale limit":   func(s *Share) { s.Sources[0].Decl.Lock.StaleSeconds = 0 },
		"bad identity":     func(s *Share) { s.Once[0].Identity = "x" },
	} {
		s := validOnceShare("/abs")
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
