package ghbroker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// brokerFixture is a broker over a fake gh, writing its audit log under a private HOME.
type brokerFixture struct {
	b       *Broker
	fakeDir string
}

func newBrokerFixture(t *testing.T, version string) brokerFixture {
	t.Helper()
	home := resolvedDir(t)
	t.Setenv("HOME", home)
	f := newRunnerFixture(t, version, nil)
	b := &Broker{
		runner:    f.r,
		scope:     NewScope([]string{"o/r"}),
		workspace: "/home/u/code/app",
		audit:     brokeraudit.Open(paths.BrokerAuditLog(), nil),
		log:       &bytes.Buffer{},
		now:       time.Now,
	}
	return brokerFixture{b: b, fakeDir: f.fakeDir}
}

func (f brokerFixture) serve(t *testing.T, req Request) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := f.b.Serve(req, "jail-1", func(p []byte) { out.Write(p) }, func(p []byte) { errOut.Write(p) })
	return code, out.String(), errOut.String()
}

func lastAudit(t *testing.T) brokeraudit.Event {
	t.Helper()
	events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
	if err != nil || len(events) == 0 {
		t.Fatalf("audit log: %d events, %v", len(events), err)
	}
	return events[len(events)-1]
}

func TestServeRunsAReadAndAuditsIt(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, out, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "32", "-R", "o/r"}})
	if code != 0 || out != "ran pr view --repo=o/r 32\n" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	ev := lastAudit(t)
	if ev.Set != SetReadOnly || ev.Outcome != "ran" || *ev.Exit != 0 || ev.Jail != "jail-1" ||
		ev.Workspace != "/home/u/code/app" || ev.Service != "github" || ev.Repo != "o/r" ||
		strings.Join(ev.Argv, " ") != "pr view --repo=o/r 32" || ev.GHVersion != "2.101.0" {
		t.Fatalf("audit line %+v", ev)
	}
}

// §11 step 1: every read-write command exits 77, runs nothing, and is audited.
func TestServeAnswersEveryWrite77AndRunsNothing(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "comment", "32", "-R", "o/r", "--body-file", "-"},
		Stdin: []byte("hi"), StdinSent: true})
	if code != ExitNoPerm || !strings.Contains(errOut, "writes need approval, which this version cannot ask for") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatalf("a write ran gh")
	}
	ev := lastAudit(t)
	if ev.Set != SetReadWrite || ev.Outcome != "denied" || *ev.Exit != ExitNoPerm || ev.Stdin == nil ||
		ev.Stdin.Bytes != 2 {
		t.Fatalf("audit line %+v", ev)
	}
}

func TestServeRefusesAndAuditsAsRefused(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	for _, argv := range [][]string{
		{"auth", "token"},
		{"pr", "view", "32", "--jq", "env.GH_TOKEN", "-R", "o/r"},
		{"api", "http://example.com/x"},
		{"-R", "x", "co", "1"},
		{"api", "-F", "q=@/etc/passwd", "user"},
	} {
		code, _, errOut := f.serve(t, Request{Argv: argv})
		if code != ExitUsage || !strings.Contains(errOut, "refused") {
			t.Errorf("gh %v: code %d err %q", argv, code, errOut)
		}
		if ev := lastAudit(t); ev.Set != "refused" || ev.Outcome != "refused" {
			t.Errorf("gh %v: audit %+v", argv, ev)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatalf("a refused command ran gh")
	}
}

func TestServeOutOfScopeRingsNobodyAndRunsNothing(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	for _, argv := range [][]string{
		{"pr", "view", "1", "-R", "other/private"},
		{"search", "code", "foo"},
		{"api", "graphql", "-f", "query={viewer{login}}"},
	} {
		code, _, errOut := f.serve(t, Request{Argv: argv})
		if code != ExitUsage || !strings.Contains(errOut, "out of scope") {
			t.Errorf("gh %v: code %d err %q", argv, code, errOut)
		}
		if ev := lastAudit(t); ev.Set != "out-of-scope" || ev.Outcome != "refused" {
			t.Errorf("gh %v: audit %+v", argv, ev)
		}
	}
}

func TestServeAnUntestedGHRunsNothing(t *testing.T) {
	f := newBrokerFixture(t, "2.99.0")
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/r"}})
	if code != ExitNoPerm || !strings.Contains(errOut, "outside the 2.101.x range") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

// §3.5: a host gh with no login answers gh's own exit 4; the jail gets 69 naming
// `gh auth status` on the host, after gh's stderr.
func TestServeMapsNoHostLoginTo69(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.scope = NewScope([]string{"o/r", "o/nologin"})
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/nologin"}})
	if code != ExitUnavailable || !strings.Contains(errOut, "gh auth login") ||
		!strings.Contains(errOut, "`gh auth status` on the host") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if ev := lastAudit(t); ev.Outcome != "ran" || *ev.Exit != ExitUnavailable {
		t.Fatalf("audit %+v", ev)
	}
}

func TestServeWithNoHostGH(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.runner = nil
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/r"}})
	if code != ExitUnavailable || !strings.Contains(errOut, "no gh") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestServeRefusesStdinOverTheCap(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, _, _ := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/r"},
		Stdin: make([]byte, StdinCap+1), StdinSent: true})
	if code != ExitUsage {
		t.Fatalf("code %d", code)
	}
}

// The forwarder and the broker over the production transport: yolo's own front
// (svcendpoint.ServeFront) in front of hostservice.ServeFrontedUnix, so the connection
// preamble, the token and the frames are all the real ones. No network beyond loopback.
func TestForwardThroughTheFront(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	// AF_UNIX paths are short on darwin; t.TempDir() there is not.
	sockDir, err := os.MkdirTemp("/tmp", "ghb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "d.sock")
	epDir := resolvedDir(t)
	// svcendpoint publishes a credential only into an owner-only directory.
	if err := os.Chmod(epDir, 0o700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(epDir, "github-broker.endpoint")
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = hostservice.ServeFrontedUnix(f.b.Handle, sock, stop) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	frontErr := make(chan error, 1)
	go func() { frontErr <- svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, stop) }()
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			select {
			case err := <-frontErr:
				t.Fatalf("endpoint never published: %v", err)
			default:
				t.Fatal("endpoint never published")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	env := func(k string) string {
		if k == EndpointEnv {
			return endpoint
		}
		return ""
	}
	var out, errOut bytes.Buffer
	code := Forward([]string{"--", "pr", "view", "32"}, ForwardEnv{Getenv: env, Stdin: strings.NewReader(""),
		Stdout: &out, Stderr: &errOut, OriginRepo: func() string { return "o/r" }})
	if code != 0 || out.String() != "ran pr view 32 --repo=o/r\n" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
	ev := lastAudit(t)
	if ev.Jail == "" || ev.Jail == "unknown" || ev.Outcome != "ran" {
		t.Fatalf("the audit line must carry the preamble's jail id: %+v", ev)
	}

	// stdin crosses when the argv reads it, and a write answers 77 through the front.
	out.Reset()
	errOut.Reset()
	code = Forward([]string{"pr", "comment", "1", "--body-file", "-"}, ForwardEnv{Getenv: env,
		Stdin: strings.NewReader("body"), Stdout: &out, Stderr: &errOut, OriginRepo: func() string { return "o/r" }})
	if code != ExitNoPerm {
		t.Fatalf("a forwarded write: code %d err %q", code, errOut.String())
	}
	if ev := lastAudit(t); ev.Stdin == nil || ev.Stdin.Bytes != 4 {
		t.Fatalf("stdin was not forwarded: %+v", ev)
	}
}

func TestForwardWithNoEndpointSaysHowToEnableIt(t *testing.T) {
	var errOut bytes.Buffer
	code := Forward([]string{"pr", "view"}, ForwardEnv{Getenv: func(string) string { return "" },
		Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &errOut, OriginRepo: func() string { return "" }})
	if code != ExitUnavailable || !strings.Contains(errOut.String(), `"github-broker": {"enabled": true}`) {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

func TestForwardRefusesStdinOverTheCap(t *testing.T) {
	var errOut bytes.Buffer
	code := Forward([]string{"pr", "comment", "1", "--body-file", "-"}, ForwardEnv{
		Getenv: func(string) string { return "/nonexistent.endpoint" },
		Stdin:  bytes.NewReader(make([]byte, StdinCap+1)), Stdout: &bytes.Buffer{}, Stderr: &errOut,
		OriginRepo: func() string { return "" }})
	if code != ExitUsage || !strings.Contains(errOut.String(), "cap") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

// The broker reads its launch's scope file and nothing else; a missing one refuses the
// start rather than guessing a scope.
func TestMainRefusesWithoutAScopeFile(t *testing.T) {
	if code := Main([]string{"--socket", "/tmp/x.sock", "--scope-file", filepath.Join(t.TempDir(), "none.json")}); code != 1 {
		t.Fatalf("code %d", code)
	}
	if code := Main([]string{"--socket", "/tmp/x.sock"}); code != 2 {
		t.Fatalf("code %d", code)
	}
}

func TestNewBrokerTakesTheScopeFromTheFile(t *testing.T) {
	t.Setenv("HOME", resolvedDir(t))
	t.Setenv("PATH", resolvedDir(t)) // no gh: the broker still starts and answers 69
	var log bytes.Buffer
	b, cleanup := newBroker(brokerscope.File{Workspace: "/w", Repos: []string{"o/r"}, Widened: []string{"x/y"}}, &log)
	defer cleanup()
	if got := b.scope.Repos(); strings.Join(got, ",") != "o/r,x/y" {
		t.Fatalf("scope %v", got)
	}
	if !strings.Contains(log.String(), "scope for /w: o/r, x/y") || !strings.Contains(log.String(), "no host gh") {
		t.Fatalf("log %q", log.String())
	}
}
