package approvalqueue

import (
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
)

// timers is an injected clock: scheduled functions run only when a test fires them.
type timers struct {
	mu  sync.Mutex
	fns []func()
	ds  []time.Duration
}

func (c *timers) after(d time.Duration, f func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := len(c.fns)
	c.fns = append(c.fns, f)
	c.ds = append(c.ds, d)
	return func() { c.mu.Lock(); c.fns[i] = func() {}; c.mu.Unlock() }
}

func (c *timers) fireAll() {
	c.mu.Lock()
	fns := append([]func(){}, c.fns...)
	c.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

// opened is what the injected opener saw: its argument and the file's state at that moment.
type opened struct {
	argv     []string
	fileMode os.FileMode
	dirMode  os.FileMode
	body     string
}

type browserRun struct {
	out      *syncBuf
	opens    chan opened
	timers   *timers
	root     string
	origin   string
	stop     func() error
	stdinW   *io.PipeWriter
	exchange func(code string) (int, string)
	get      func(path, token string) int
}

var codeInBody = regexp.MustCompile(`#code=([A-Za-z0-9_-]{22})`)

// startBrowserRun runs the queue against the fake yolo with an injected opener and clock.
func startBrowserRun(t *testing.T) *browserRun {
	t.Helper()
	dir := t.TempDir()
	fake := fakeyolo.Install(t, fakeyolo.Script{
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}},
		Watch: []fakeyolo.Run{{Stdout: watchOpening(t, dir), Block: true}}})
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &browserRun{out: newSyncBuf(), opens: make(chan opened, 16), timers: &timers{}, root: root}
	open := func(path string) error {
		o := opened{argv: []string{"opener", path}}
		if st, err := os.Stat(path); err == nil {
			o.fileMode = st.Mode().Perm()
		}
		if st, err := os.Stat(filepath.Dir(path)); err == nil {
			o.dirMode = st.Mode().Perm()
		}
		if body, err := os.ReadFile(path); err == nil {
			o.body = string(body)
		}
		b.opens <- o
		return nil
	}
	stdinR, stdinW := io.Pipe()
	b.stdinW = stdinW
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Env{Yolo: fake, Args: []string{"--reconcile", "1h"}, Stdin: stdinR, Stdout: b.out, Stderr: newSyncBuf(),
			StdoutIsTerminal: true, Open: open, After: b.timers.after, LaunchRoot: root})
	}()
	var once sync.Once
	var stopErr error
	b.stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(guard):
				t.Error("run did not return after cancel")
			}
		})
		return stopErr
	}
	t.Cleanup(func() { b.stop() })

	link := waitLine(t, b.out, "Open: ")
	m := regexp.MustCompile(`^Open: (http://127\.0\.0\.1:\d+)/`).FindStringSubmatch(link)
	if m == nil {
		t.Fatalf("link line %q", link)
	}
	b.origin = m[1]
	cli := &http.Client{Timeout: guard}
	b.exchange = func(code string) (int, string) {
		req, _ := http.NewRequest("POST", b.origin+"/api/session", strings.NewReader(`{"code":"`+code+`"}`))
		req.Header.Set("Origin", b.origin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var tok struct{ Token string }
		json.Unmarshal(body, &tok)
		return resp.StatusCode, tok.Token
	}
	b.get = func(path, token string) int {
		req, _ := http.NewRequest("GET", b.origin+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	return b
}

func (b *browserRun) nextOpen(t *testing.T) opened {
	t.Helper()
	select {
	case o := <-b.opens:
		return o
	case <-time.After(guard):
		t.Fatal("the opener was never called")
		return opened{}
	}
}

func TestBrowserIsOpenedOnAPrivateLauncherFileAndArgvHoldsNoCode(t *testing.T) {
	b := startBrowserRun(t)
	o := b.nextOpen(t)
	if len(o.argv) != 2 {
		t.Fatalf("argv %v: the opener gets the launcher path and nothing else", o.argv)
	}
	path := o.argv[1]
	if o.fileMode != 0o600 || o.dirMode != 0o700 {
		t.Fatalf("launcher file mode %v, directory mode %v; want 0600 and 0700", o.fileMode, o.dirMode)
	}
	if !strings.HasPrefix(filepath.Dir(path), b.root) {
		t.Fatalf("launcher %s is not under the runtime root %s", path, b.root)
	}
	m := codeInBody.FindStringSubmatch(o.body)
	if m == nil || !strings.Contains(o.body, b.origin+"/#code="+m[1]) {
		t.Fatalf("launcher does not redirect to the one-time link:\n%s", o.body)
	}
	if strings.Contains(strings.Join(o.argv, " "), m[1]) {
		t.Fatal("the code is in the opener's argv")
	}
	if !strings.Contains(strings.Join(b.timers.dsStrings(), " "), "2m0s") {
		t.Fatalf("expiry was scheduled for %v, want the code's 120 s", b.timers.ds)
	}
	// The terminal also has the link, as the fallback.
	if !strings.Contains(b.out.String(), "#code="+m[1]) {
		t.Fatal("the link was not printed")
	}
}

func (c *timers) dsStrings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var s []string
	for _, d := range c.ds {
		s = append(s, d.String())
	}
	return s
}

func TestLauncherFileIsDeletedAfterTheExchange(t *testing.T) {
	b := startBrowserRun(t)
	o := b.nextOpen(t)
	code := codeInBody.FindStringSubmatch(o.body)[1]
	if _, err := os.Stat(o.argv[1]); err != nil {
		t.Fatal(err)
	}
	if st, _ := b.exchange(code); st != 200 {
		t.Fatalf("exchange: %d", st)
	}
	if _, err := os.Stat(o.argv[1]); !os.IsNotExist(err) {
		t.Fatalf("the launcher file outlived the exchange: %v", err)
	}
}

func TestLauncherFileIsDeletedWhenTheCodeExpiresAndOnExit(t *testing.T) {
	b := startBrowserRun(t)
	o := b.nextOpen(t)
	b.timers.fireAll() // the injected clock reaches the code's lifetime
	if _, err := os.Stat(o.argv[1]); !os.IsNotExist(err) {
		t.Fatalf("the launcher file outlived its code: %v", err)
	}

	// Enter mints a new link and file; exiting removes the file and its directory.
	b.stdinW.Write([]byte("\n"))
	o2 := b.nextOpen(t)
	if _, err := os.Stat(o2.argv[1]); err != nil {
		t.Fatal(err)
	}
	if err := b.stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(o2.argv[1])); !os.IsNotExist(err) {
		t.Fatalf("the launcher directory survived exit: %v", err)
	}
}

func TestEnterReplacesTheLauncherFile(t *testing.T) {
	b := startBrowserRun(t)
	o := b.nextOpen(t)
	b.stdinW.Write([]byte("\n"))
	o2 := b.nextOpen(t)
	if _, err := os.Stat(o.argv[1]); !os.IsNotExist(err) {
		t.Fatalf("the old launcher file stayed after a new link: %v", err)
	}
	if o2.body == o.body {
		t.Fatal("Enter did not mint a fresh code")
	}
}

func TestSpentLinkIsARaceAlarmThatRevokesEveryTokenAndMintsANewLink(t *testing.T) {
	b := startBrowserRun(t)
	o := b.nextOpen(t)
	code := codeInBody.FindStringSubmatch(o.body)[1]
	st, tok := b.exchange(code)
	if st != 200 || tok == "" {
		t.Fatalf("exchange: %d", st)
	}
	if st := b.get("/api/state", tok); st != 200 {
		t.Fatalf("state: %d", st)
	}
	// The same link again: the person's tab lost a race to whatever read it first.
	if st, _ := b.exchange(code); st != http.StatusConflict {
		t.Fatalf("second exchange: %d, want 409", st)
	}
	if st := b.get("/api/state", tok); st != 401 {
		t.Fatalf("the token survived the race alarm: %d", st)
	}
	waitLine(t, b.out, "WARNING: a link was used a second time")
	o2 := b.nextOpen(t)
	code2 := codeInBody.FindStringSubmatch(o2.body)[1]
	if code2 == code {
		t.Fatal("no fresh code after the alarm")
	}
	if st, _ := b.exchange(code2); st != 200 {
		t.Fatalf("the new link: %d", st)
	}
}

func TestNoOpenSkipsTheBrowserButStillPrintsTheLink(t *testing.T) {
	dir := t.TempDir()
	fake := fakeyolo.Install(t, fakeyolo.Script{
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}},
		Watch: []fakeyolo.Run{{Stdout: watchOpening(t, dir), Block: true}}})
	out := newSyncBuf()
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Env{Yolo: fake, Args: []string{"--no-open", "--reconcile", "1h"}, Stdin: strings.NewReader(""), Stdout: out, Stderr: newSyncBuf(),
			StdoutIsTerminal: true, LaunchRoot: t.TempDir(),
			Open: func(p string) error { opened <- p; return nil }})
	}()
	waitLine(t, out, "Open: ")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-opened:
		t.Fatalf("--no-open still opened %s", p)
	default:
	}
}

func TestLaunchDirChoice(t *testing.T) {
	xdg := t.TempDir()
	env := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return xdg
		}
		return ""
	}
	d, err := launchDir("", "linux", env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	if filepath.Dir(d) != xdg {
		t.Fatalf("linux with a runtime dir used %s", d)
	}
	if st, _ := os.Stat(d); st.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v", st.Mode().Perm())
	}
	// Without one, and on darwin, the system temporary directory.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, goos := range []string{"linux", "darwin"} {
		d2, err := launchDir("", goos, func(string) string { return "" })
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(d2) != tmp {
			t.Errorf("%s: %s is not under TMPDIR", goos, d2)
		}
		os.RemoveAll(d2)
	}
	d3, _ := launchDir("", "darwin", env)
	defer os.RemoveAll(d3)
	if filepath.Dir(d3) == xdg {
		t.Error("darwin must not use XDG_RUNTIME_DIR")
	}
	if openerFor("linux") != "xdg-open" || openerFor("darwin") != "open" {
		t.Error("opener names")
	}
}
