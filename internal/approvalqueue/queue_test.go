package approvalqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/fakeyolo"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
)

func TestMain(m *testing.M) {
	if fakeyolo.MaybeRun() {
		return
	}
	os.Exit(m.Run())
}

const guard = 60 * time.Second

func td(name string) string {
	p, _ := filepath.Abs(filepath.Join("testdata", name))
	return p
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
	ch chan string // every complete line
}

func newSyncBuf() *syncBuf { return &syncBuf{ch: make(chan string, 1000)} }

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Write(p)
	for _, l := range strings.SplitAfter(string(p), "\n") {
		if strings.HasSuffix(l, "\n") {
			select {
			case s.ch <- strings.TrimRight(l, "\n"):
			default:
			}
		}
	}
	return len(p), nil
}
func (s *syncBuf) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitLine(t *testing.T, s *syncBuf, prefix string) string {
	t.Helper()
	timeout := time.After(guard)
	for {
		select {
		case l := <-s.ch:
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-timeout:
			t.Fatalf("no line starting %q; output:\n%s", prefix, s.String())
		}
	}
}

func TestRefusesNonLoopbackListen(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.5:8080", ":0", "[::]:0", "example.com:80", "nonsense"} {
		out, errb := newSyncBuf(), newSyncBuf()
		err := run(context.Background(), Env{Args: []string{"--listen", addr}, Stdin: strings.NewReader(""), Stdout: out, Stderr: errb, StdoutIsTerminal: true})
		if err == nil || !strings.Contains(err.Error(), "--listen") || !strings.Contains(err.Error(), "127.0.0.1:0") {
			t.Errorf("%q: %v", addr, err)
		}
		if strings.Contains(out.String(), "#code=") {
			t.Errorf("%q printed a link", addr)
		}
	}
	// Other spellings of loopback are refused with their own explanation.
	err := run(context.Background(), Env{Args: []string{"--listen", "[::1]:0"}, Stdin: strings.NewReader(""), Stdout: newSyncBuf(), Stderr: newSyncBuf(), StdoutIsTerminal: true})
	if err == nil || !strings.Contains(err.Error(), "loopback but not 127.0.0.1") {
		t.Fatalf("::1: %v", err)
	}
}

func TestRefusesToStartWhenStandardOutputIsNotATerminal_T12(t *testing.T) {
	out, errb := newSyncBuf(), newSyncBuf()
	err := run(context.Background(), Env{Args: nil, Stdin: strings.NewReader(""), Stdout: out, Stderr: errb, StdoutIsTerminal: false})
	if err == nil || !strings.Contains(err.Error(), "not a terminal") || !strings.Contains(err.Error(), "run `yolo approve --web` directly in a terminal") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(out.String()+errb.String(), "#code=") || strings.Contains(out.String(), "http://") {
		t.Fatalf("a link was printed:\n%s%s", out.String(), errb.String())
	}
}

func TestStartupErrors(t *testing.T) {
	err := run(context.Background(), Env{Yolo: filepath.Join(t.TempDir(), "nope"), Stdin: strings.NewReader(""), Stdout: newSyncBuf(), Stderr: newSyncBuf(), StdoutIsTerminal: true})
	if err == nil || !strings.Contains(err.Error(), "yolo approve --json") {
		t.Fatalf("missing yolo: %v", err)
	}
	refusal, _ := os.ReadFile(td("injail-approve-refusal.stderr.txt"))
	fake := fakeyolo.Install(t, fakeyolo.Script{JSON: []fakeyolo.Run{{Exit: 2, Stderr: string(refusal)}}})
	out := newSyncBuf()
	err = run(context.Background(), Env{Yolo: fake, Stdin: strings.NewReader(""), Stdout: out, Stderr: newSyncBuf(), StdoutIsTerminal: true})
	if err == nil || !strings.Contains(err.Error(), "host-only") {
		t.Fatalf("in-jail refusal must be the startup error: %v", err)
	}
	if strings.Contains(out.String(), "#code=") {
		t.Fatal("printed a link despite the refusal")
	}
}

// watchOpening turns the recorded snapshot into the "state" line that opens a watch.
func watchOpening(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(td("approve-json-populated.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["event"] = "state"
	line, _ := json.Marshal(m)
	p := filepath.Join(dir, "watch-open.jsonl")
	if err := os.WriteFile(p, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEndToEndAgainstTheFakeYolo(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}},
		Watch: []fakeyolo.Run{{Stdout: watchOpening(t, dir), Block: true}}})
	out, errb := newSyncBuf(), newSyncBuf()
	stdinR, stdinW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Env{Yolo: fake, Args: []string{"--reconcile", "50ms", "--no-open"}, Stdin: stdinR, Stdout: out, Stderr: errb, StdoutIsTerminal: true})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run returned %v", err)
			}
		case <-time.After(guard):
			t.Error("run did not return after cancel")
		}
	}()

	link := waitLine(t, out, "Open: ")
	m := regexp.MustCompile(`^Open: (http://127\.0\.0\.1:\d+)/#code=([A-Za-z0-9_-]{22})$`).FindStringSubmatch(link)
	if m == nil {
		t.Fatalf("link line %q", link)
	}
	origin, code := m[1], m[2]
	cli := &http.Client{Timeout: guard}
	do := func(method, path string, hdr map[string]string, body string) (int, string) {
		req, _ := http.NewRequest(method, origin+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	session := func(c string) (int, string) {
		return do("POST", "/api/session", map[string]string{"Origin": origin, "Content-Type": "application/json"}, `{"code":"`+c+`"}`)
	}
	st, body := session(code)
	if st != 200 {
		t.Fatalf("exchange: %d", st)
	}
	var tok struct{ Token string }
	json.Unmarshal([]byte(body), &tok)

	// Wait, by polling the model through the API, until the queue is Live with six pending.
	var pub model.Published
	deadline := time.Now().Add(guard)
	for {
		st, body := do("GET", "/api/state", map[string]string{"Authorization": "Bearer " + tok.Token}, "")
		if st != 200 {
			t.Fatalf("state: %d", st)
		}
		pub = model.Published{}
		if err := json.Unmarshal([]byte(body), &pub); err != nil {
			t.Fatal(err)
		}
		if pub.Freshness.State == model.FreshLive && len(pub.Pending) == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never live with 6 pending: %+v", pub.Freshness)
		}
		time.Sleep(10 * time.Millisecond) // polling cadence only; the pass condition is the state
	}
	if len(pub.Sources) != 3 {
		t.Fatalf("sources: %d", len(pub.Sources))
	}

	// Enter mints a new link and burns the old one.
	stdinW.Write([]byte("\n"))
	var link2 string
	for {
		link2 = waitLine(t, out, "Open: ")
		if link2 != link {
			break
		}
	}
	code2 := link2[strings.Index(link2, "#code=")+6:]
	if code2 == code {
		t.Fatal("Enter did not mint a fresh code")
	}
	if st, _ := session(code2); st != 200 {
		t.Fatalf("fresh code: %d", st)
	}

	// Five failed exchanges print one warning on the terminal and burn nothing: the right code
	// still works afterwards.
	stdinW.Write([]byte("\n"))
	var link3 string
	for {
		link3 = waitLine(t, out, "Open: ")
		if link3 != link2 {
			break
		}
	}
	code3 := link3[strings.Index(link3, "#code=")+6:]
	for i := 0; i < 5; i++ {
		if st, _ := session("wrong-code"); st != 401 {
			t.Fatalf("bad code: %d", st)
		}
	}
	if w := waitLine(t, out, "5 failed attempts"); !strings.Contains(w, "128-bit") || !strings.Contains(w, "press Enter") {
		t.Fatalf("warning text: %q", w)
	}
	if st, _ := session(code3); st != 200 {
		t.Fatalf("the right code after five failures: %d", st)
	}

	// Wait for at least two more --json children after the exchange, then check that neither
	// the code nor the token reached a child's argv or environment, or any output but the
	// one line that prints the code.
	n0 := len(fakeyolo.Calls(t, rec))
	for len(fakeyolo.Calls(t, rec)) < n0+3 {
		time.Sleep(10 * time.Millisecond)
		if time.Now().After(deadline.Add(guard)) {
			t.Fatal("no further children")
		}
	}
	recBytes, _ := os.ReadFile(rec)
	for _, secret := range []string{code, code2, code3, tok.Token} {
		if bytes.Contains(recBytes, []byte(secret)) {
			t.Fatalf("a secret reached a child's argv or environment")
		}
		if strings.Contains(errb.String(), secret) {
			t.Fatalf("a secret was logged:\n%s", errb.String())
		}
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(l, "Open: ") && (strings.Contains(l, code) || strings.Contains(l, code2) || strings.Contains(l, tok.Token)) {
			t.Fatalf("secret printed outside the link line: %q", l)
		}
	}
	for _, c := range fakeyolo.Calls(t, rec) {
		if len(c.Args) < 2 || c.Args[0] != "approve" || c.Args[1] == "allow" || len(c.Args) > 3 {
			t.Fatalf("a child ran with unexpected arguments %v: the queue must only list and watch", c.Args)
		}
	}
	cmdline, _ := os.ReadFile("/proc/self/cmdline")
	if bytes.Contains(cmdline, []byte(tok.Token)) {
		t.Fatal("token in argv")
	}
}
