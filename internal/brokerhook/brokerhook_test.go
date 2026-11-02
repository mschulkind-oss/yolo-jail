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
	return f.runFor(t, command, log, 0, 5*time.Millisecond)
}

// runFor is run with a hook timeout and a poll interval.
func (f *fixture) runFor(t *testing.T, command string, log *syncBuffer, timeout, interval time.Duration) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Store: f.store, Start: "start", Command: command, Interval: interval, Timeout: timeout, Log: log}
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	// Run reads the store once before it watches; a request filed after that is an event.
	time.Sleep(50 * time.Millisecond)
	return func() {
		cancel()
		<-done
	}
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
