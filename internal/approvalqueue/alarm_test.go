package approvalqueue

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/fakeyolo"
)

func TestReplaysOfASpentCodeWarnOnceRevokeOnceAndOpenNothing(t *testing.T) {
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
	for i := 0; i < 14; i++ {
		if st, _ := b.exchange(code); st != http.StatusConflict {
			t.Fatalf("replay %d: %d, want 409", i, st)
		}
	}
	if st := b.get("/api/state", tok); st != 401 {
		t.Fatalf("the token survived the race alarm: %d", st)
	}
	out := b.out.String()
	if n := strings.Count(out, "WARNING: a link was used a second time"); n != 1 {
		t.Fatalf("%d warnings for 14 replays, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "Open: "); n != 1 {
		t.Fatalf("%d links printed: the alarm must not make a new one:\n%s", n, out)
	}
	select {
	case extra := <-b.opens:
		t.Fatalf("the alarm opened a browser on %v", extra.argv)
	default:
	}
	if !strings.Contains(out, "press Enter for a new link") {
		t.Fatalf("the warning does not name the next step:\n%s", out)
	}

	// The person recovers with Enter, which makes the one new link and opens it.
	b.stdinW.Write([]byte("\n"))
	o2 := b.nextOpen(t)
	code2 := codeInBody.FindStringSubmatch(o2.body)[1]
	if code2 == code {
		t.Fatal("Enter did not make a fresh code")
	}
	if st, _ := b.exchange(code2); st != 200 {
		t.Fatalf("the new link: %d", st)
	}
}

// hookWriter calls onLink, once and synchronously, when the link line is written.
type hookWriter struct {
	*syncBuf
	once   sync.Once
	onLink func(line string)
}

func (h *hookWriter) Write(p []byte) (int, error) {
	n, err := h.syncBuf.Write(p)
	if strings.HasPrefix(string(p), "Open: ") {
		h.once.Do(func() { h.onLink(strings.TrimSpace(string(p))) })
	}
	return n, err
}

// An exchange arriving the instant the link is printed must still find, and delete, the
// launcher file: the file is written and recorded before the code can be exchanged.
func TestAnExchangeCannotOutrunTheLauncherFileRecord(t *testing.T) {
	dir := t.TempDir()
	fake := fakeyolo.Install(t, fakeyolo.Script{
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}},
		Watch: []fakeyolo.Run{{Stdout: watchOpening(t, dir), Block: true}}})
	status := 0
	out := &hookWriter{syncBuf: newSyncBuf()}
	out.onLink = func(line string) {
		m := regexp.MustCompile(`^Open: (http://127\.0\.0\.1:\d+)/#code=([A-Za-z0-9_-]+)$`).FindStringSubmatch(line)
		if m == nil {
			t.Errorf("link line %q", line)
			return
		}
		req, _ := http.NewRequest("POST", m[1]+"/api/session", strings.NewReader(`{"code":"`+m[2]+`"}`))
		req.Header.Set("Origin", m[1])
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: guard}).Do(req)
		if err != nil {
			t.Errorf("exchange: %v", err)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		status = resp.StatusCode
	}
	opened := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Env{Yolo: fake, Args: []string{"--reconcile", "1h"}, Stdin: strings.NewReader(""), Stdout: out, Stderr: newSyncBuf(),
			StdoutIsTerminal: true, LaunchRoot: t.TempDir(), After: (&timers{}).after,
			Open: func(p string) error { opened <- p; return nil }})
	}()
	defer func() { cancel(); <-done }()
	var path string
	select {
	case path = <-opened:
	case <-time.After(guard):
		t.Fatal("the opener was never called")
	}
	if status != 200 {
		t.Fatalf("the immediate exchange: %d", status)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the exchange ran before the launcher file was recorded, and the file stayed: %v", err)
	}
}

// Main is what yolo approve --web runs; its terminal check must be reached on the real standard
// output. The test binary re-executes itself as Main with a pipe for standard output.
func TestMainRefusesWhenStandardOutputIsNotATerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), envRunMain+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr // buffers make both a pipe
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != ExitUsage {
		t.Fatalf("Main with a piped stdout: %v, want exit %d\nstderr: %s", err, ExitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not a terminal") || !strings.Contains(stderr.String(), "yolo approve --web") {
		t.Fatalf("the refusal does not say why and what to do:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "#code=") || strings.Contains(stderr.String(), "#code=") {
		t.Fatal("a link was printed")
	}
}

// systemOpen builds the opener's real argv: the opener and the launcher path, nothing else.
func TestSystemOpenArgvIsTheOpenerAndThePathOnly(t *testing.T) {
	want := openerFor(runtime.GOOS)
	if want == "" {
		t.Skip("no opener on " + runtime.GOOS)
	}
	var gotName string
	var gotArgs []string
	old := newOpenerCmd
	newOpenerCmd = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		return exec.Command(os.Args[0], "-test.run=^$") // a real, harmless child
	}
	t.Cleanup(func() { newOpenerCmd = old })
	path := filepath.Join(t.TempDir(), "open-1.html")
	if err := systemOpen(path); err != nil {
		t.Fatal(err)
	}
	if gotName != want || len(gotArgs) != 1 || gotArgs[0] != path {
		t.Fatalf("opener argv %q %q, want %q [%q]", gotName, gotArgs, want, path)
	}
}

// The production launchDir call in run, with no LaunchRoot: the launcher lands in the runtime
// directory on Linux and the system temporary directory elsewhere.
func TestRunMakesTheLauncherDirectoryInTheRuntimeDirectory(t *testing.T) {
	xdg, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	t.Setenv("TMPDIR", tmp)
	dir := t.TempDir()
	fake := fakeyolo.Install(t, fakeyolo.Script{
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}},
		Watch: []fakeyolo.Run{{Stdout: watchOpening(t, dir), Block: true}}})
	opened := make(chan string, 4)
	out := newSyncBuf()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Env{Yolo: fake, Args: []string{"--reconcile", "1h"}, Stdin: strings.NewReader(""), Stdout: out, Stderr: newSyncBuf(),
			StdoutIsTerminal: true, After: (&timers{}).after,
			Open: func(p string) error { opened <- p; return nil }})
	}()
	var path string
	select {
	case path = <-opened:
	case <-time.After(guard):
		t.Fatal("the opener was never called")
	}
	wantRoot := tmp
	if runtime.GOOS == "linux" {
		wantRoot = xdg
	}
	launchDir := filepath.Dir(path)
	if filepath.Dir(launchDir) != wantRoot || !strings.HasPrefix(filepath.Base(launchDir), "yolo-approve-web-") {
		t.Fatalf("launcher %s is not in a fresh directory under %s", path, wantRoot)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launchDir); !os.IsNotExist(err) {
		t.Fatalf("the launcher directory survived exit: %v", err)
	}
}
