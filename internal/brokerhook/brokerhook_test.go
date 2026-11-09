package brokerhook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// fixture is a store whose decisions are recorded as the audit sees them, and a request to
// file under broker lifetime "start".
type fixture struct {
	store  *brokerstore.Store
	mu     sync.Mutex
	kinds  []string
	dir    string
	filing brokerstore.Request
	// skew moves the store's clock forward, so a test can expire a request.
	skew atomic.Int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir()}
	f.store = &brokerstore.Store{Dir: filepath.Join(f.dir, "github"), Source: "github",
		Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil },
		Audit: func(_ context.Context, kind string, _ brokerstore.Request, _ brokerstore.Grant) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.kinds = append(f.kinds, kind)
			return nil
		},
		Now: func() time.Time { return time.Now().Add(time.Duration(f.skew.Load())) }}
	f.filing = brokerstore.Request{Jail: "jail", Start: "start", Workspace: "/project", Argv: []string{"pr", "comment", "1", "--body", "hi"},
		Repo: "o/r", StdinDigest: "digest", Set: "read-write",
		Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}}
	return f
}

// script writes an executable hook; its body runs under /bin/sh.
func (f *fixture) script(t *testing.T, name, body string) string {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("the hook runs under /bin/sh")
	}
	p := filepath.Join(f.dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// run starts a runner for lifetime "start" and returns its stop.
func (f *fixture) run(t *testing.T, command string, log *syncBuffer) func() {
	t.Helper()
	return f.runFor(t, command, log, 0, 5*time.Millisecond, nil)
}

// runFor is run with a hook timeout, a poll interval and, unless nil, a limitClock that
// decides when each run's limit (a Timeout, its request's expiry) is reached.
func (f *fixture) runFor(t *testing.T, command string, log *syncBuffer, timeout, interval time.Duration, clock *limitClock) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Store: f.store, Start: "start", Command: command, Interval: interval, Timeout: timeout, Log: log}
	if clock != nil {
		r.afterFunc = clock.afterFunc
	}
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	// Run reads the store once before it watches; a request filed after that is an event.
	time.Sleep(50 * time.Millisecond)
	return func() {
		cancel()
		<-done
	}
}

// limitClock stands in for the clock that ends a hook's run at its limit: a run's limit is
// reached only when the test calls runOut, so a test can see the hook start first however
// slowly it starts, with no real clock racing that start.
type limitClock struct {
	mu     sync.Mutex
	limits []time.Duration
	fires  []func()
	fired  []bool
}

func (c *limitClock) afterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := len(c.fires)
	c.limits, c.fires, c.fired = append(c.limits, d), append(c.fires, f), append(c.fired, false)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := c.fired[i]
		c.fired[i] = true
		return !was
	}
}

// runOut reaches the limit of every run whose clock was started and not yet stopped, and
// returns the limits of every clock started. It fails the test when none was: the runner no
// longer bounds a run by it, and nothing would stop the hook. A clock already stopped (its
// run ended another way first, such as on the store's expiry event) does not fire.
func (c *limitClock) runOut(t *testing.T) []time.Duration {
	t.Helper()
	c.mu.Lock()
	var due []func()
	for i, f := range c.fires {
		if !c.fired[i] {
			c.fired[i] = true
			due = append(due, f)
		}
	}
	limits := append([]time.Duration(nil), c.limits...)
	c.mu.Unlock()
	if len(limits) == 0 {
		t.Fatal("no hook run's limit clock was started")
	}
	for _, f := range due {
		f()
	}
	return limits
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *syncBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fixture) request(t *testing.T, id string) brokerstore.Request {
	t.Helper()
	rs, _, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range rs {
		if q.ID == id {
			return q
		}
	}
	return brokerstore.Request{}
}

func (f *fixture) state(t *testing.T, id string) string {
	t.Helper()
	rs, _, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range rs {
		if q.ID == id {
			return q.State
		}
	}
	return ""
}

// The hook gets the event's JSON on stdin and its fields in the environment, and fires only
// for its own broker lifetime's records.
func TestTheHookGetsTheEventAndOnlyItsOwnLifetime(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(f.dir, "events")
	stop := f.run(t, f.script(t, "hook", `{ cat; printf '%s|%s|%s|%s|%s\n' "$YOLO_EVENT" "$YOLO_REQUEST_ID" "$YOLO_SOURCE" "$YOLO_REQUEST_WORKSPACE" "$YOLO_REQUEST_SUMMARY"; } >>'`+out+`'`+"\n"), &syncBuffer{})
	other := f.filing
	other.Start, other.Argv = "another-lifetime", []string{"pr", "comment", "2"}
	if _, err := f.store.File(other); err != nil {
		t.Fatal(err)
	}
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook's output", func() bool { b, _ := os.ReadFile(out); return bytes.Count(b, []byte("\n")) >= 2 })
	stop()
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("the hook ran %d times, want once for its own lifetime:\n%s", len(lines)/2, b)
	}
	var ev struct {
		Schema  int    `json:"schema"`
		Event   string `json:"event"`
		Request struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err, lines[0])
	}
	if ev.Schema != 1 || ev.Event != brokerstore.EventRequestFiled || ev.Request.ID != q.ID || ev.Request.State != "pending" {
		t.Fatalf("stdin %+v", ev)
	}
	fields := strings.Split(lines[1], "|")
	if fields[0] != "request-filed" || fields[1] != q.ID || fields[2] != "github" || fields[3] != "/project" ||
		fields[4] != "github "+q.ID+": set read-write on o/r for jail jail, to run: pr comment 1 --body '[redacted]' [command SHA-256 "+q.Digest+"; stdin SHA-256 "+q.StdinDigest+"]" {
		t.Fatalf("env %q", lines[1])
	}
}

// A hook that hangs is killed at its timeout, and decides nothing.
func TestAHungHookIsStoppedAtItsTimeout(t *testing.T) {
	f := newFixture(t)
	var log syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Store: f.store, Start: "start", Command: f.script(t, "hook", "sleep 30\necho approve\n"),
		Interval: 5 * time.Millisecond, Timeout: 100 * time.Millisecond, Log: &log}
	// Run tears down inside the store's directory after ctx ends (its hooks, its link
	// directory); returning before it finishes races t.TempDir's cleanup.
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond)
	q, _ := f.store.File(f.filing)
	waitFor(t, "the timeout", func() bool { return strings.Contains(log.String(), "stopped") })
	if got := f.state(t, q.ID); got != "pending" {
		t.Fatal(got)
	}
}

// The summary is the agent's words, and a notification daemon renders markup, so the hook
// also gets YOLO_REQUEST_SUMMARY_MARKUP: the same line with &, <, >, " and ' as XML
// entities. The plain field is left exactly as it was.
func TestTheHookGetsAMarkupSummaryForMarkupNotifiers(t *testing.T) {
	f := newFixture(t)
	markup := `<b>x</b> & <a href="http://e">y</a>`
	f.filing.Argv = []string{"pr", "comment", "1", markup}
	out := filepath.Join(f.dir, "summaries")
	stop := f.run(t, f.script(t, "hook", `printf '%s\n%s\n' "$YOLO_REQUEST_SUMMARY" "$YOLO_REQUEST_SUMMARY_MARKUP" >>'`+out+`'`+"\n"), &syncBuffer{})
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook's output", func() bool { b, _ := os.ReadFile(out); return bytes.Count(b, []byte("\n")) >= 2 })
	stop()
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("hook output %q", b)
	}
	plain, escaped := lines[0], lines[1]
	if plain != f.store.RequestSummary(q) || !strings.Contains(plain, markup) {
		t.Fatalf("the plain summary changed: %q", plain)
	}
	want := `&lt;b&gt;x&lt;/b&gt; &amp; &lt;a href=&quot;http://e&quot;&gt;y&lt;/a&gt;`
	if !strings.Contains(escaped, want) || strings.ContainsAny(escaped, `<>"'`) {
		t.Fatalf("escaped summary %q, want it to contain %q and no raw markup or quote", escaped, want)
	}
	// Exactly the plain line, entity for entity: &amp; first, so no entity is escaped twice.
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	if escaped != r.Replace(plain) {
		t.Fatalf("escaped %q is not the plain summary escaped, %q", escaped, r.Replace(plain))
	}
}

// An invisible format character makes `ma<U+200B>in` read as `main` in the line the user
// approves from, so both summaries show it escaped.
func TestTheHookSummariesShowAZeroWidthSpace(t *testing.T) {
	f := newFixture(t)
	f.filing.Argv = []string{"pr", "comment", "1", "--head", "ma\u200bin"}
	out := filepath.Join(f.dir, "summaries")
	stop := f.run(t, f.script(t, "hook", `printf '%s\n%s\n' "$YOLO_REQUEST_SUMMARY" "$YOLO_REQUEST_SUMMARY_MARKUP" >>'`+out+`'`+"\n"), &syncBuffer{})
	if _, err := f.store.File(f.filing); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook's output", func() bool { b, _ := os.ReadFile(out); return bytes.Count(b, []byte("\n")) >= 2 })
	stop()
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("hook output %q", b)
	}
	// The summary shell-quotes an argument holding one as $'...', its bytes escaped.
	for i, l := range lines {
		if strings.ContainsRune(l, '\u200b') || !strings.Contains(l, `ma\xe2\x80\x8bin`) {
			t.Errorf("summary %d does not show the zero-width space escaped: %q", i, l)
		}
	}
}

// The hook gets what the request does in fields that need no assembly: ACTION, TARGET and
// HEADLINE are safe anywhere, TITLE and REFS are the agent's text, DESCRIPTION is the
// headline plus that text, and DESCRIPTION_MARKUP is it escaped for notification markup.
func TestTheHookGetsTheRequestsActionTargetAndDescription(t *testing.T) {
	f := newFixture(t)
	f.filing.Argv = []string{"pr", "create", "--title", "fix: Vec<T> && lexer", "--body", "b"}
	f.filing.About = brokerstore.About{Action: brokerstore.ActionPRCreate, Target: "PR creation in o/r",
		Headline: "create a PR in o/r", Title: "fix: Vec<T> && lexer\nsecond", Refs: []string{"refs/heads/a<b>", "refs/heads/c"}}
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range Env(f.store, q, "") {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	description := "create a PR in o/r\ntitle: fix: Vec<T> && lexer\\nsecond\nref: refs/heads/a<b>\nref: refs/heads/c"
	for k, want := range map[string]string{
		"YOLO_REQUEST_ACTION":             "pr-create",
		"YOLO_REQUEST_TARGET":             "PR creation in o/r",
		"YOLO_REQUEST_HEADLINE":           "create a PR in o/r",
		"YOLO_REQUEST_TITLE":              `fix: Vec<T> && lexer\nsecond`,
		"YOLO_REQUEST_REFS":               "refs/heads/a<b>\nrefs/heads/c",
		"YOLO_REQUEST_DESCRIPTION":        description,
		"YOLO_REQUEST_DESCRIPTION_MARKUP": brokerstore.MarkupEscape(description),
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if strings.ContainsAny(env["YOLO_REQUEST_DESCRIPTION_MARKUP"], `<>"'`) {
		t.Errorf("markup description holds raw markup: %q", env["YOLO_REQUEST_DESCRIPTION_MARKUP"])
	}
}

// The broker outlives the terminal pane that started it, so the pane identity it inherited
// (herdr's, tmux's, zellij's, wezterm's, kitty's) is stale by the time a hook runs, and a
// hook calling `herdr pane ...` would act on the wrong pane: the hook does not inherit it.
// The session's identity outlives the pane, so it is kept, and so is everything else.
func TestTheHookDoesNotInheritTheLaunchingPanesIdentity(t *testing.T) {
	stale := map[string]string{"HERDR_PANE_ID": "p1", "HERDR_TAB_ID": "t1", "HERDR_WORKSPACE_ID": "w1",
		"TMUX_PANE": "%3", "ZELLIJ_PANE_ID": "2", "WEZTERM_PANE": "4", "KITTY_WINDOW_ID": "5"}
	kept := map[string]string{"YOLO_TEST_KEPT": "yes", "TMUX_TMPDIR": "/tmp/t",
		"HERDR_SOCKET_PATH": "/run/herdr.sock", "HERDR_SESSION": "s1", "HERDR_ENV": "1",
		"TMUX": "/tmp/tmux-1/default,1,0", "ZELLIJ": "0", "ZELLIJ_SESSION_NAME": "s"}
	for k, v := range stale {
		t.Setenv(k, v)
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}
	f := newFixture(t)
	out := filepath.Join(f.dir, "env")
	stop := f.run(t, f.script(t, "hook", `env >'`+out+`.tmp' && mv '`+out+`.tmp' '`+out+`'`+"\n"), &syncBuffer{})
	if _, err := f.store.File(f.filing); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook's environment", func() bool { _, err := os.Stat(out); return err == nil })
	stop()
	b, _ := os.ReadFile(out)
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for k := range stale {
		if v, ok := env[k]; ok {
			t.Errorf("the hook inherited %s=%s", k, v)
		}
	}
	for k, want := range kept {
		if env[k] != want {
			t.Errorf("%s = %q, want %q kept", k, env[k], want)
		}
	}
}
