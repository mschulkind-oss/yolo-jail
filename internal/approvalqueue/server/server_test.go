package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/hub"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/web"
)

const guard = 60 * time.Second

var base = time.Date(2026, 11, 10, 3, 50, 0, 0, time.UTC)

type clockT struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clockT) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clockT) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// recorder keeps every response header set the test client sees, so header rules can be
// asserted over everything the test did.
type recorder struct {
	mu   sync.Mutex
	resp []*http.Response
	base http.RoundTripper
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err == nil {
		r.mu.Lock()
		r.resp = append(r.resp, resp)
		r.mu.Unlock()
	}
	return resp, err
}

type env struct {
	t     *testing.T
	srv   *Server
	hub   *hub.Hub
	auth  *Auth
	clk   *clockT
	url   string
	port  int
	rec   *recorder
	cli   *http.Client
	burns int
	mu    sync.Mutex
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func newEnv(t *testing.T, interval time.Duration) (*env, *syncBuf) {
	t.Helper()
	clk := &clockT{t: base}
	h := hub.New(model.Options{Reconcile: 15 * time.Second}, clk.Now)
	auth := NewAuth(clk.Now)
	logs := &syncBuf{}
	e := &env{t: t, hub: h, auth: auth, clk: clk}
	auth.OnWarn = func(int) { e.mu.Lock(); e.burns++; e.mu.Unlock() }
	e.srv = New(Config{Hub: h, Auth: auth, Assets: web.FS(), Logger: log.New(logs, "", 0), StreamInterval: interval})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.port = l.Addr().(*net.TCPAddr).Port
	e.url = fmt.Sprintf("http://127.0.0.1:%d", e.port)
	go e.srv.Serve(l)
	t.Cleanup(e.srv.Close)
	e.rec = &recorder{base: &http.Transport{DisableKeepAlives: true}}
	e.cli = &http.Client{Transport: e.rec, Timeout: guard}
	return e, logs
}

func (e *env) origin() string { return e.url }

// do sends a request with the given headers; "Host" sets req.Host.
func (e *env) do(method, path string, hdr map[string]string, body string) *http.Response {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.url+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := e.cli.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func slurp(r *http.Response) string {
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// exchange mints a code, posts it as the page does, and returns the token.
func (e *env) exchange() string {
	e.t.Helper()
	code := e.auth.NewCode()
	resp := e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"`+code+`"}`)
	if resp.StatusCode != 200 {
		e.t.Fatalf("exchange: %d", resp.StatusCode)
	}
	var out struct{ Token string }
	if err := json.Unmarshal([]byte(slurp(resp)), &out); err != nil || out.Token == "" {
		e.t.Fatal(err)
	}
	return out.Token
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func loadHostile(t *testing.T, h *hub.Hub) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "approve-json-populated.json"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := model.ParseState(b)
	if err != nil {
		t.Fatal(err)
	}
	h.Apply(model.SnapshotOK{State: st, FetchStart: base})
	h.Apply(model.WatchStarted{At: base})
	h.Apply(model.SnapshotOK{State: st, FetchStart: base, FromWatch: true})
}

func TestEveryDataRouteNeedsTheToken_T1Local(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	loadHostile(t, e.hub)
	tok := e.exchange()
	for _, p := range []string{"/api/state", "/api/stream", "/api/anything", "/nope", "/api/decision"} {
		for name, h := range map[string]map[string]string{
			"none":  nil,
			"wrong": bearer("not-a-token"),
			"empty": {"Authorization": "Bearer "},
			"basic": {"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("a:b"))},
			"forged": {"Authorization": "Bearer " + tok + "x", "Origin": e.origin(), "Sec-Fetch-Site": "same-origin",
				"Host": fmt.Sprintf("127.0.0.1:%d", e.port)},
		} {
			resp := e.do("GET", p, h, "")
			body := slurp(resp)
			if resp.StatusCode != 401 || body != "" {
				t.Errorf("GET %s (%s): %d body %q, want 401 and no body detail", p, name, resp.StatusCode, body)
			}
		}
	}
	// With the token the data routes answer.
	if r := e.do("GET", "/api/state", bearer(tok), ""); r.StatusCode != 200 {
		t.Fatalf("authorized state: %d", r.StatusCode)
	}
	if r := e.do("GET", "/api/nope", bearer(tok), ""); r.StatusCode != 404 {
		t.Fatalf("unknown path with token: %d", r.StatusCode)
	}
	// The public set carries no request data.
	for _, p := range []string{"/", "/app.js", "/app.css"} {
		resp := e.do("GET", p, nil, "")
		body := slurp(resp)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
		for _, needle := range []string{"r-c31635034bbbaff00f3cb41aaa26cb88", "example/app", "onerror"} {
			if strings.Contains(body, needle) {
				t.Errorf("%s leaks request data %q", p, needle)
			}
		}
	}
}

func TestStreamRefusesAnUnauthenticatedConnection(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	resp := e.do("GET", "/api/stream", nil, "")
	if resp.StatusCode != 401 || resp.Header.Get("Content-Type") == "text/event-stream" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
}

func TestHostMustBeExactly127_0_0_1AndThePort_T3(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	tok := e.exchange()
	for _, host := range []string{
		fmt.Sprintf("evil.example:%d", e.port), fmt.Sprintf("localhost:%d", e.port), "127.0.0.1",
		fmt.Sprintf("127.0.0.1:%d", e.port+1), fmt.Sprintf("[::1]:%d", e.port), fmt.Sprintf("127.0.0.1.evil.example:%d", e.port),
	} {
		for _, p := range []string{"/", "/api/state", "/app.js"} {
			h := bearer(tok)
			h["Host"] = host
			resp := e.do("GET", p, h, "")
			if body := slurp(resp); resp.StatusCode != 421 || body != "" {
				t.Errorf("Host %q GET %s: %d %q", host, p, resp.StatusCode, body)
			}
		}
	}
}

func TestOriginSecFetchSiteAndOptions_T4(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	tok := e.exchange()
	st := func(m, p string, h map[string]string, body string) int {
		r := e.do(m, p, h, body)
		slurp(r)
		return r.StatusCode
	}
	auth := bearer(tok)
	with := func(extra map[string]string) map[string]string {
		h := bearer(tok)
		for k, v := range extra {
			h[k] = v
		}
		return h
	}
	_ = auth
	for _, o := range []string{"http://evil.example", "http://localhost:" + fmt.Sprint(e.port), "null", e.origin() + "/", "https://127.0.0.1:" + fmt.Sprint(e.port)} {
		if c := st("GET", "/api/state", with(map[string]string{"Origin": o}), ""); c != 403 {
			t.Errorf("Origin %q: %d, want 403", o, c)
		}
		if c := st("POST", "/api/session", map[string]string{"Origin": o, "Content-Type": "application/json"}, `{"code":"x"}`); c != 403 {
			t.Errorf("POST Origin %q: %d, want 403", o, c)
		}
	}
	if c := st("POST", "/api/session", map[string]string{"Content-Type": "application/json"}, `{"code":"x"}`); c != 403 {
		t.Errorf("POST without Origin: %d, want 403", c)
	}
	if c := st("GET", "/api/state", with(map[string]string{"Origin": e.origin()}), ""); c != 200 {
		t.Errorf("exact Origin on GET: %d", c)
	}
	for _, v := range []string{"cross-site", "same-site", "none"} {
		if c := st("GET", "/api/state", with(map[string]string{"Sec-Fetch-Site": v}), ""); c != 403 {
			t.Errorf("Sec-Fetch-Site %s: %d, want 403", v, c)
		}
	}
	if c := st("GET", "/api/state", with(map[string]string{"Sec-Fetch-Site": "same-origin"}), ""); c != 200 {
		t.Errorf("same-origin: %d", c)
	}
	// A top-level navigation to the page shell carries Sec-Fetch-Site: none and must work.
	if c := st("GET", "/", map[string]string{"Sec-Fetch-Site": "none"}, ""); c != 200 {
		t.Errorf("navigation to /: %d", c)
	}
	for _, p := range []string{"/", "/api/state", "/api/stream", "/api/session", "/app.js", "/x"} {
		for _, h := range []map[string]string{nil, bearer(tok), {"Origin": e.origin(), "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "authorization"}} {
			if c := st("OPTIONS", p, h, ""); c != 405 {
				t.Errorf("OPTIONS %s: %d, want 405", p, c)
			}
		}
	}
	e.assertNoCORSOrCookies()
}

func (e *env) assertNoCORSOrCookies() {
	e.t.Helper()
	e.rec.mu.Lock()
	defer e.rec.mu.Unlock()
	if len(e.rec.resp) == 0 {
		e.t.Fatal("no responses recorded")
	}
	for _, r := range e.rec.resp {
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				e.t.Errorf("%s %s sent %s", r.Request.Method, r.Request.URL.Path, k)
			}
			if strings.EqualFold(k, "Set-Cookie") {
				e.t.Errorf("%s %s set a cookie", r.Request.Method, r.Request.URL.Path)
			}
		}
	}
}

func TestSecurityHeadersOnEveryResponse_T6(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	tok := e.exchange()
	reqs := []struct {
		m, p string
		h    map[string]string
	}{
		{"GET", "/", nil}, {"GET", "/app.js", nil}, {"GET", "/app.css", nil}, {"HEAD", "/", nil},
		{"GET", "/api/state", bearer(tok)}, {"GET", "/api/state", nil}, {"GET", "/api/nope", bearer(tok)},
		{"GET", "/x", nil}, {"OPTIONS", "/", nil}, {"GET", "/", map[string]string{"Host": "evil.example"}},
		{"GET", "/api/state", map[string]string{"Origin": "http://evil.example"}},
		{"POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}},
		{"POST", "/api/session", map[string]string{"Content-Type": "application/json"}},
	}
	for _, r := range reqs {
		resp := e.do(r.m, r.p, r.h, "")
		slurp(resp)
		want := map[string]string{
			"Content-Security-Policy":      "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
			"X-Frame-Options":              "DENY",
			"X-Content-Type-Options":       "nosniff",
			"Referrer-Policy":              "no-referrer",
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
		}
		for k, v := range want {
			if got := resp.Header.Get(k); got != v {
				t.Errorf("%s %s (%d): %s = %q", r.m, r.p, resp.StatusCode, k, got)
			}
		}
		if strings.HasPrefix(r.p, "/api/") && resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: Cache-Control = %q", r.m, r.p, resp.Header.Get("Cache-Control"))
		}
	}
	if CSP != "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'" {
		t.Fatal("CSP drifted from the trust-boundary doc")
	}
	e.assertNoCORSOrCookies()
}

func TestServedPageHasNoInlineScriptOrHandlers_T7(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	body := slurp(e.do("GET", "/", nil, ""))
	scripts := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllStringSubmatch(body, -1)
	if len(scripts) == 0 {
		t.Fatal("no script tag found")
	}
	for _, s := range scripts {
		if !strings.Contains(s[1], "src=") || strings.TrimSpace(s[2]) != "" {
			t.Errorf("inline script: %q", s[0])
		}
	}
	for name, re := range map[string]string{
		"event handler attribute": `(?i)<[^>]+\son[a-z]+\s*=`,
		"style attribute":         `(?i)<[^>]+\sstyle\s*=`,
		"style element":           `(?i)<style\b`,
		"javascript: url":         `(?i)javascript:`,
		"iframe/object/embed":     `(?i)<(iframe|object|embed|form)\b`,
	} {
		if regexp.MustCompile(re).MatchString(body) {
			t.Errorf("served page has %s", name)
		}
	}
}

func TestBootstrapCodeLifecycle_T9(t *testing.T) {
	clk := &clockT{t: base}
	a := NewAuth(clk.Now)
	warns := 0
	a.OnWarn = func(int) { warns++ }

	code := a.NewCode()
	if b, _ := base64.RawURLEncoding.DecodeString(code); len(b) != CodeBytes {
		t.Fatalf("code is %d bytes", len(b))
	}
	tok, ok := a.Exchange(code)
	if !ok {
		t.Fatal("first use failed")
	}
	if b, _ := base64.RawURLEncoding.DecodeString(tok); len(b) != TokenBytes {
		t.Fatalf("token is %d bytes", len(b))
	}
	if !a.Valid(tok) || a.Valid(tok+"x") || a.Valid("") {
		t.Fatal("token validity")
	}
	if _, ok := a.Exchange(code); ok {
		t.Fatal("a code works twice")
	}
	if a.Valid(tok) {
		t.Fatal("a replayed code must revoke every token")
	}

	// 120 s: alive at 119, dead at 120.
	c2 := a.NewCode()
	clk.Add(CodeTTL - time.Second)
	if _, ok := a.Exchange(c2); !ok {
		t.Fatal("code should live 119 s")
	}
	c3 := a.NewCode()
	clk.Add(CodeTTL)
	if _, ok := a.Exchange(c3); ok {
		t.Fatal("code accepted at 120 s")
	}

	// A new code burns the old one, and only one is live.
	old := a.NewCode()
	fresh := a.NewCode()
	if _, ok := a.Exchange(old); ok {
		t.Fatal("old code survived Enter")
	}
	_ = fresh

	// Six bad codes, then the right one: one warning (at the fifth failure), never a burn,
	// never a throttle, and the right code still succeeds.
	live := a.NewCode()
	for i := 1; i <= 6; i++ {
		if _, ok := a.Exchange(fmt.Sprintf("bad-%d", i)); ok {
			t.Fatal("bad code accepted")
		}
		if want := map[bool]int{true: 0, false: 1}[i < WarnAfter]; warns != want {
			t.Fatalf("after %d failures: %d warnings, want %d", i, warns, want)
		}
	}
	if _, ok := a.Exchange(live); !ok {
		t.Fatal("the correct code must still work after six failures")
	}
	if warns != 1 {
		t.Fatalf("warnings = %d, want exactly 1", warns)
	}
	// The warning is per code: a new code that is also attacked warns again.
	a.NewCode()
	for i := 0; i < WarnAfter; i++ {
		a.Exchange("nope")
	}
	if warns != 2 {
		t.Fatalf("second code: %d warnings", warns)
	}
	// An expired code is a failure, not a burn of anything else.
	c := a.NewCode()
	clk.Add(CodeTTL)
	if _, ok := a.Exchange(c); ok {
		t.Fatal("expired code accepted")
	}
	// An empty code never matches, even when no code is live.
	b0 := NewAuth(clk.Now)
	if _, ok := b0.Exchange(""); ok {
		t.Fatal("empty code accepted with no live code")
	}
}

func TestAtMostEightTokensOldestDropped(t *testing.T) {
	a := NewAuth(nil)
	var toks []string
	for i := 0; i < MaxTokens+3; i++ {
		tok, ok := a.Exchange(a.NewCode())
		if !ok {
			t.Fatal("exchange")
		}
		toks = append(toks, tok)
	}
	if a.TokenCount() != MaxTokens {
		t.Fatalf("%d tokens", a.TokenCount())
	}
	for i, tok := range toks {
		if want := i >= 3; a.Valid(tok) != want {
			t.Errorf("token %d valid = %v, want %v", i, !want, want)
		}
	}
}

func TestSessionExchangeOverHTTPAndReload_T9(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	code := e.auth.NewCode()
	post := func(c string) int {
		r := e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"`+c+`"}`)
		slurp(r)
		return r.StatusCode
	}
	if c := post("wrong"); c != 401 {
		t.Fatalf("wrong: %d", c)
	}
	resp := e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "text/plain"}, `{"code":"`+code+`"}`)
	if slurp(resp); resp.StatusCode != 415 {
		t.Fatalf("content type: %d", resp.StatusCode)
	}
	tok := ""
	r := e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"`+code+`"}`)
	var out struct{ Token string }
	json.Unmarshal([]byte(slurp(r)), &out)
	tok = out.Token
	if r.StatusCode != 200 || tok == "" {
		t.Fatalf("exchange %d", r.StatusCode)
	}
	// "Reload": the same token keeps working.
	for i := 0; i < 3; i++ {
		if r := e.do("GET", "/api/state", bearer(tok), ""); r.StatusCode != 200 {
			t.Fatalf("token after reload: %d", r.StatusCode)
		}
	}
	// A second exchange of the spent code is the race alarm: 409, and every token is revoked.
	if c := post(code); c != 409 {
		t.Fatalf("reuse of a spent code is the race alarm, 409: %d", c)
	}
	if r := e.do("GET", "/api/state", bearer(tok), ""); r.StatusCode != 401 {
		t.Fatalf("token after the race alarm: %d, want 401", r.StatusCode)
	}
	// A body over the limit is refused, not read.
	big := `{"code":"` + strings.Repeat("a", 5000) + `"}`
	r = e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, big)
	if slurp(r); r.StatusCode != 400 {
		t.Fatalf("oversize body: %d", r.StatusCode)
	}
}

func TestServerDropsStalledAndOversizedClients(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	hs := e.srv.httpServer(1)
	if hs.ReadHeaderTimeout <= 0 || hs.ReadTimeout <= 0 || hs.IdleTimeout <= 0 || hs.MaxHeaderBytes <= 0 || hs.MaxHeaderBytes > 64<<10 {
		t.Fatalf("connection limits: read-header %v read %v idle %v max header %d", hs.ReadHeaderTimeout, hs.ReadTimeout, hs.IdleTimeout, hs.MaxHeaderBytes)
	}
	// The listener the test environment really serves on enforces the header limit.
	r := e.do("GET", "/api/state", map[string]string{"X-Big": strings.Repeat("a", 200<<10)}, "")
	slurp(r)
	if r.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized header: %d, want 431", r.StatusCode)
	}
}

func TestEverySpentCodeAlarmsHoweverOldAndOnlyOncePerCode(t *testing.T) {
	a := NewAuth((&clockT{t: base}).Now)
	races := 0
	a.OnRace = func() { races++ }
	var codes []string
	for i := 0; i < 40; i++ { // well past any bounded memory of spent codes
		c := a.NewCode()
		if _, ok := a.Exchange(c); !ok {
			t.Fatal("exchange")
		}
		codes = append(codes, c)
	}
	if _, r := a.ExchangeResult(codes[0]); r != Reused || races != 1 {
		t.Fatalf("the oldest spent code: %v, races %d", r, races)
	}
	for i := 0; i < 5; i++ {
		a.ExchangeResult(codes[0])
	}
	a.ExchangeResult(codes[1])
	if races != 2 {
		t.Fatalf("one alarm per spent code: %d", races)
	}
}

func TestSpentCodeIsARaceAlarmThatRevokesEveryToken(t *testing.T) {
	clk := &clockT{t: base}
	a := NewAuth(clk.Now)
	races, exchanges := 0, 0
	a.OnRace = func() { races++ }
	a.OnExchange = func() { exchanges++ }

	first := a.NewCode()
	t1, _ := a.Exchange(first)
	second := a.NewCode()
	t2, _ := a.Exchange(second)
	if exchanges != 2 || !a.Valid(t1) || !a.Valid(t2) {
		t.Fatalf("setup: %d exchanges", exchanges)
	}
	// The first link is replayed: it lost a race. Both tokens die and the alarm fires once.
	if _, r := a.ExchangeResult(first); r != Reused {
		t.Fatalf("replay of a spent code: %v", r)
	}
	if races != 1 || a.TokenCount() != 0 || a.Valid(t1) || a.Valid(t2) {
		t.Fatalf("races %d, tokens %d", races, a.TokenCount())
	}
	// Fourteen more replays of alarmed codes, in either order, are 409s with no side effect: no
	// second alarm, and a token minted since is not revoked again.
	t3, _ := func() (string, bool) { c := a.NewCode(); return a.Exchange(c) }()
	for i := 0; i < 14; i++ {
		if _, r := a.ExchangeResult(first); r != Reused {
			t.Fatalf("replay %d: %v", i, r)
		}
	}
	if races != 1 || !a.Valid(t3) {
		t.Fatalf("replays of an alarmed code had effects: races %d, token valid %v", races, a.Valid(t3))
	}
	// A code that was never issued is only a failure, not an alarm.
	if _, r := a.ExchangeResult("never-issued"); r != Failed || races != 1 {
		t.Fatalf("unknown code: %v, races %d", r, races)
	}
	// A code replaced before use was never traded, so it is not an alarm either.
	old := a.NewCode()
	a.NewCode()
	if _, r := a.ExchangeResult(old); r != Failed || races != 1 {
		t.Fatalf("replaced code: %v, races %d", r, races)
	}
}

func TestNoMutationRouteExistsAndNoCookieIsEverSet(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	tok := e.exchange()
	paths := []string{"/", "/app.js", "/api/state", "/api/stream", "/api/decision", "/api/approve", "/api/deny", "/api/revoke", "/api/refresh", "/api/session/x"}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, p := range paths {
			for _, h := range []map[string]string{
				{"Origin": e.origin(), "Content-Type": "application/json"},
				{"Origin": e.origin(), "Content-Type": "application/json", "Authorization": "Bearer " + tok},
			} {
				resp := e.do(m, p, h, `{"id":"r-00000000000000000000000000000000","action":"allow-once"}`)
				slurp(resp)
				if m == "POST" && p == "/api/session" {
					continue
				}
				if resp.StatusCode == 200 || resp.StatusCode >= 500 {
					t.Errorf("%s %s: %d", m, p, resp.StatusCode)
				}
				if h["Authorization"] != "" && resp.StatusCode != 404 && resp.StatusCode != 405 {
					t.Errorf("%s %s with a token: %d, want 404 or 405", m, p, resp.StatusCode)
				}
			}
		}
	}
	e.assertNoCORSOrCookies()
}

func readEvent(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var data string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			return data
		}
		if strings.HasPrefix(line, "data: ") {
			data = line[6:]
		}
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x7f || (s[i] < 0x20 && s[i] != '\n') {
			return false
		}
	}
	return true
}

func TestStreamSendsWholeStateOnConnectOnChangeAndOnAnInterval(t *testing.T) {
	e, _ := newEnv(t, 30*time.Millisecond)
	tok := e.exchange()
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+"/api/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%v", resp.Header)
	}
	br := bufio.NewReader(resp.Body)
	var m model.Published
	first := readEvent(t, br)
	if err := json.Unmarshal([]byte(first), &m); err != nil || m.Freshness.State != model.FreshNeverLoaded {
		t.Fatalf("first message: %v %q", err, first)
	}
	// Two more with no change at all: the interval keeps it alive.
	readEvent(t, br)
	readEvent(t, br)
	// A change arrives as whole state.
	loadHostile(t, e.hub)
	for {
		var got model.Published
		data := readEvent(t, br)
		if !isASCII(data) {
			t.Fatalf("stream message is not pure ASCII")
		}
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Pending) == 6 {
			if got.Freshness.State != model.FreshLive {
				t.Fatalf("freshness %+v", got.Freshness)
			}
			break
		}
	}
	// Close ends the stream.
	e.srv.Close()
	for {
		if _, err := br.ReadString('\n'); err != nil {
			break
		}
	}
}

func TestHostileRequestTextIsNeverEmittedRawAndTheJSONStaysWellFormed(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	loadHostile(t, e.hub)
	// Add events and failures that carry hostile text too.
	h := e.hub
	bad := model.RequestView{Schema: 1, ID: "r-" + strings.Repeat("0", 32), Source: "git", Headline: "<script>alert(1)</script>\x1b[31m\u202e\u200b\x00",
		Title: "</script><img src=x onerror=alert(1)>", Description: "javascript:alert(1) data:text/html,<b>", Argv: []string{"push", "--body=\x1b]0;pwned\a\u202e", "-Hx\ufeff", strings.Repeat("A", 5000)},
		Refs: []string{"refs/heads/\u202egnp", strings.Repeat("B", 5000)}, Command: "x", Expires: base.Add(time.Hour)}
	h.Apply(model.EventIn{Event: model.EventView{Schema: 1, Event: model.EvFiled, Source: "git", Time: base.Add(time.Second), Request: &bad}})
	h.Apply(model.SnapshotFail{At: base, Kind: model.FailUnsupported, Message: "yolo printed schema 2 \u202e and this queue reads schema 1 only; update the queue <b>"})
	h.Apply(model.Malformed{At: base, Message: "x"})
	tok := e.exchange()
	body := slurp(e.do("GET", "/api/state", bearer(tok), ""))
	if !isASCII(body) {
		t.Fatal("state is not pure ASCII: a control or bidi character was emitted raw")
	}
	for _, raw := range []string{"<", ">", "\x1b", "\u202e", "\u200b", "\ufeff"} {
		if strings.Contains(body, raw) {
			t.Fatalf("raw %q in the body", raw)
		}
	}
	var p model.Published
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	var hostile *model.PendingOut
	for i := range p.Pending {
		if strings.Contains(p.Pending[i].Title, "onerror") && p.Pending[i].ID == bad.ID {
			hostile = &p.Pending[i]
		}
	}
	if hostile == nil {
		t.Fatal("hostile request missing")
	}
	if !strings.Contains(hostile.Headline, "<script>alert(1)</script>") || !strings.Contains(hostile.Headline, "\u27e8U+001B\u27e9") ||
		!strings.Contains(hostile.Headline, "\u27e8U+202E\u27e9") || !strings.Contains(hostile.Headline, "\u27e8U+200B\u27e9") || !strings.Contains(hostile.Headline, "\u27e8U+0000\u27e9") {
		t.Fatalf("hostile headline not shown as text with markers: %q", hostile.Headline)
	}
	if len(hostile.Argv) != 4 || !hostile.Argv[1].Secret || len(hostile.Argv[3].Value) != 5000 || len(hostile.Refs[1]) != 5000 {
		t.Fatalf("argv or refs elided or mis-masked: %+v", hostile.Argv)
	}
	if strings.Contains(hostile.Command, "pwned") {
		t.Fatalf("masked command shows the secret: %q", hostile.Command)
	}
	if p.Freshness.State != model.FreshStale || !strings.Contains(p.Freshness.Message, "schema 2") {
		t.Fatalf("freshness: %+v", p.Freshness)
	}
	// Unicode beyond the BMP round-trips through the surrogate-pair escape.
	enc := EncodeJSON(map[string]string{"k": "a\U0001F600b\u00e9"})
	if !isASCII(string(enc)) {
		t.Fatal("non-ASCII survived EncodeJSON")
	}
	var back map[string]string
	if err := json.Unmarshal(enc, &back); err != nil || back["k"] != "a\U0001F600b\u00e9" {
		t.Fatalf("round trip: %v %v", err, back)
	}
}

func TestNoCodeTokenOrAuthorizationValueIsEverLogged_T13(t *testing.T) {
	e, logs := newEnv(t, 20*time.Millisecond)
	loadHostile(t, e.hub)
	code := e.auth.NewCode()
	e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"`+code+`"}`)
	tok := e.exchange()
	// A few requests, including bad ones that carry the token in odd places.
	e.do("GET", "/api/state", bearer(tok), "")
	e.do("GET", "/api/state?token="+tok+"&code="+code, nil, "")
	e.do("GET", "/api/state", map[string]string{"Authorization": "Bearer " + tok, "Origin": "http://evil.example"}, "")
	e.do("GET", "/api/state", map[string]string{"Authorization": "Bearer wrong-" + tok, "Host": "evil.example"}, "")
	e.do("OPTIONS", "/api/stream", bearer(tok), "")
	e.auth.NewCode()
	for i := 0; i < WarnAfter+1; i++ {
		e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"`+code+`"}`)
	}
	out := logs.String()
	if out == "" {
		t.Fatal("expected request log lines")
	}
	for _, secret := range []string{code, tok, "Bearer", "Authorization"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains %q:\n%s", secret, out)
		}
	}
	// And the peer address is allowed, request contents are not.
	if !strings.Contains(out, "peer=127.0.0.1:") {
		t.Fatalf("no peer in log:\n%s", out)
	}
	if strings.Contains(out, "onerror") || strings.Contains(out, "example/app") {
		t.Fatalf("request contents in the log:\n%s", out)
	}
}

func TestAnEvictedTokenEndsItsStream_m1(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	first := e.exchange()
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+"/api/stream", nil)
	req.Header.Set("Authorization", "Bearer "+first)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	readEvent(t, br)
	for i := 0; i < MaxTokens; i++ { // the ninth live token evicts the first
		e.exchange()
	}
	if e.auth.Valid(first) {
		t.Fatal("test setup: first token still valid")
	}
	// The next change wakes the stream, which re-checks the token and ends.
	e.hub.Apply(model.Malformed{At: base})
	for {
		if _, err := br.ReadString('\n'); err != nil {
			break
		}
	}
	if r := e.do("GET", "/api/stream", bearer(first), ""); r.StatusCode != 401 {
		t.Fatalf("reconnect with the evicted token: %d", r.StatusCode)
	}
}

func TestCloseBeforeAndDuringServeStopsIt_m2(t *testing.T) {
	// Close first: Serve must return at once and close the listener.
	srv := New(Config{Hub: hub.New(model.Options{}, time.Now), Auth: NewAuth(nil), Assets: web.FS()})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(guard):
		t.Fatal("Serve after Close did not return")
	}
	if _, err := net.Dial("tcp", l.Addr().String()); err == nil {
		t.Fatal("listener still open after Close-then-Serve")
	}
	// Racing Close against Serve (run under -race): both orders end with Serve returning.
	for i := 0; i < 50; i++ {
		srv := New(Config{Hub: hub.New(model.Options{}, time.Now), Auth: NewAuth(nil), Assets: web.FS()})
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		done := make(chan error, 1)
		go func() { done <- srv.Serve(l) }()
		go srv.Close()
		select {
		case <-done:
		case <-time.After(guard):
			t.Fatal("Serve did not return after a racing Close")
		}
	}
}

func TestFailedExchangesNeverBurnOrThrottleOverHTTP(t *testing.T) {
	e, _ := newEnv(t, time.Hour)
	code := e.auth.NewCode()
	for i := 0; i < 2*WarnAfter; i++ {
		r := e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"wrong"}`)
		if slurp(r); r.StatusCode != 401 {
			t.Fatalf("attempt %d: %d, want 401 (never 429)", i, r.StatusCode)
		}
	}
	e.mu.Lock()
	warns := e.burns
	e.mu.Unlock()
	if warns != 1 {
		t.Fatalf("warnings = %d, want 1", warns)
	}
	r := e.do("POST", "/api/session", map[string]string{"Origin": e.origin(), "Content-Type": "application/json"}, `{"code":"`+code+`"}`)
	if slurp(r); r.StatusCode != 200 {
		t.Fatalf("the right code after %d failures: %d", 2*WarnAfter, r.StatusCode)
	}
}
