package ghbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// readAll is the read grant on every repository the gate approves into a scope file from the
// user config (docs/design/boundary-broker.md BB-D107): without it the broker refuses every call.
var readAll = []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}}

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
		scope:     NewScope([]string{"o/r"}).WithGrants(readAll...),
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

func TestServeEvaluateAndActUseSameDecision(t *testing.T) {
	b, _ := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	entry, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace,
		Repo: "github.com/o/r", Kind: brokerstore.WorkingPR, Resource: "#17", By: GrantCreatePR})
	if err != nil {
		t.Fatal(err)
	}
	var hostCalls [][]string
	b.execute = func(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
		hostCalls = append(hostCalls, append([]string(nil), argv...))
		if len(argv) == 2 && argv[0] == "api" && argv[1] == "repos/o/r/issues/17" {
			out([]byte(`{"number":17,"pull_request":{"merged_at":"2026-11-04T00:00:00Z"}}`))
		}
		return RunResult{Exit: 0}
	}
	before, err := os.ReadFile(filepath.Join(b.store.Dir, "working", entry.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{"pr", "comment", "17", "--body", "gone", "-R", "o/r"}
	var predicted bytes.Buffer
	code := b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: argv, Repo: "o/r"}, jail,
		func(p []byte) { predicted.Write(p) }, func([]byte) {})
	if code != 0 {
		t.Fatalf("evaluate exit %d: %s", code, predicted.String())
	}
	var answer struct {
		Verdict string   `json:"verdict"`
		Argv    []string `json:"canonical"`
	}
	if err := json.Unmarshal(predicted.Bytes(), &answer); err != nil || answer.Verdict != "asks" {
		t.Fatalf("prediction %q: %v", predicted.String(), err)
	}
	after, err := os.ReadFile(filepath.Join(b.store.Dir, "working", entry.ID+".json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("evaluate changed working-set record: %v", err)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 0 {
		t.Fatalf("evaluate filed/spent a request: %+v, %v", requests, err)
	}
	for _, call := range hostCalls {
		if strings.Join(call, " ") != "api repos/o/r/issues/17" {
			t.Fatalf("evaluate ran a proposed write: %v", call)
		}
	}
	if !slices.Equal(answer.Argv, []string{"pr", "comment", "--body=gone", "--repo=o/r", "17"}) {
		t.Fatalf("canonical argv %v", answer.Argv)
	}

	code = b.ServeContext(context.Background(), Request{Argv: argv, Repo: "o/r"}, jail, func([]byte) {}, func([]byte) {})
	if code != ExitTempFail {
		t.Fatalf("act exit %d, want asks/pending", code)
	}
	if ev := lastAudit(t); !slices.Equal(ev.Argv, answer.Argv) || ev.Outcome != "pending" {
		t.Fatalf("act diverged from prediction: audit=%+v prediction=%+v", ev, answer)
	}
	requests, _, err = b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("act request %+v, %v", requests, err)
	}
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

// Without a production host delivery path, a write fails closed and runs nothing.
func TestServeRefusesWriteWithoutHostDelivery(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "comment", "32", "-R", "o/r", "--body-file", "-"},
		Stdin: []byte("hi"), StdinSent: true})
	if code != ExitNoPerm || !strings.Contains(errOut, "required request notice and answer delivery are unavailable") ||
		!strings.Contains(errOut, "ask the user to run the command on the host") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatalf("a write ran gh")
	}
	ev := lastAudit(t)
	if ev.Set != SetReadWrite || ev.Outcome != "refused" || *ev.Exit != ExitNoPerm || ev.Stdin == nil ||
		ev.Stdin.Bytes != 2 {
		t.Fatalf("audit line %+v", ev)
	}
}

func TestServeRefusesWriteWithoutDeliveryRatherThanOfferingHostRun(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "merge", "42", "-R", "o/r"}})
	if code != ExitNoPerm || !strings.Contains(errOut, "required request notice and answer delivery are unavailable") || strings.Contains(errOut, "yolo host-run") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatalf("a write ran gh")
	}
}

func TestServeRefusesAndAuditsAsRefused(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	for _, argv := range [][]string{
		{"auth", "token"},
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

// BB-D64: a jq filter's `env` and `$ENV` read the environment of the gh evaluating them,
// which is the broker's, built from nothing (§4.1). Through Serve, against a fake gh that
// answers those two filters by printing its environment as gh's jq would: the call runs, and
// no token the host holds, in its environment or in its gh login, is in what crosses. The
// redaction count stays zero, so the token was never there to be cut, rather than cut.
func TestServeRunsAJqFilterWhoseEnvironmentHoldsNoToken(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	for _, filter := range []string{"env", "$ENV"} {
		code, out, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/r",
			"--json", "title", "--jq", filter}})
		if code != 0 || !strings.Contains(out, "GH_HOST=github.com") {
			t.Fatalf("--jq %s: code %d out %q err %q", filter, code, out, errOut)
		}
		for _, secret := range []string{fakeToken, "gho_from_the_environment", "ghp_from_the_environment"} {
			if strings.Contains(out+errOut, secret) {
				t.Errorf("--jq %s printed a token:\n%s%s", filter, out, errOut)
			}
		}
		if ev := lastAudit(t); ev.Set != SetReadOnly || ev.Outcome != "ran" || ev.Redactions != 0 {
			t.Errorf("--jq %s: audit %+v", filter, ev)
		}
	}
}

// BB-D65: `gh auth status` is the broker's to answer. It asks the host gh for its active
// github.com login and says who the jail is logged in as, through what, and which
// repositories it may use. gh's own answer is the host's: the run dir's hosts.yml path, the
// token's prefix and its scopes, which mean nothing in the jail.
func TestServeAnswersAuthStatusItself(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.scope = NewScope([]string{"o/r", "o/lib"}).WithGrants(readAll...)
	code, out, errOut := f.serve(t, Request{Argv: []string{"auth", "status"}})
	if code != 0 {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	for _, want := range []string{"github.com\n", "Logged in to github.com account me through yolo's github-broker",
		"the token stays on the host", "o/lib, o/r"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	for _, host := range []string{f.fakeDir, "hosts.yml", "gho_", "read:org", "scopes"} {
		if strings.Contains(out+errOut, host) {
			t.Errorf("the answer carries the host's %q:\n%s%s", host, out, errOut)
		}
	}
	// The host gh was asked exactly the broker's own question, never the jail's argv.
	if got := oneCall(t, f.fakeDir)["argv"]; got != "auth\nstatus\n--hostname\ngithub.com\n--active\n--json\nhosts\n" {
		t.Errorf("the host gh ran %q", got)
	}
	if ev := lastAudit(t); ev.Set != SetReadOnly || ev.Outcome != "ran" || *ev.Exit != 0 {
		t.Errorf("audit %+v", ev)
	}
}

func TestServeAnswersAuthStatusJSONWithoutTheHostsFacts(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, out, errOut := f.serve(t, Request{Argv: []string{"auth", "status", "--json", "hosts", "-a"}})
	if code != 0 {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	var doc struct {
		Hosts map[string][]map[string]any `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	entries := doc.Hosts["github.com"]
	if len(entries) != 1 || entries[0]["login"] != "me" || entries[0]["state"] != "success" ||
		entries[0]["active"] != true || entries[0]["tokenSource"] != "github-broker (host)" {
		t.Fatalf("hosts %v", doc.Hosts)
	}
	if _, ok := entries[0]["scopes"]; ok || strings.Contains(out, f.fakeDir) {
		t.Fatalf("the JSON carries the host's scopes or a host path: %s", out)
	}
	// gh's own refusal of a field it does not have.
	code, _, errOut = f.serve(t, Request{Argv: []string{"auth", "status", "--json", "login"}})
	if code != 1 || !strings.Contains(errOut, "Unknown JSON field: \"login\"") {
		t.Fatalf("--json login: code %d err %q", code, errOut)
	}
}

func TestServeAuthStatusWithNoHostLoginSaysSo(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	if err := os.WriteFile(filepath.Join(f.fakeDir, "nologin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := f.serve(t, Request{Argv: []string{"auth", "status"}})
	if code != ExitUnavailable || out != "" || !strings.Contains(errOut, "`gh auth status` on the host") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

// A filter has nothing of gh's to run over, since the broker writes the answer itself; the
// refusal names the pipe that does the same.
func TestServeRefusesAFilterOnAuthStatusAndNamesThePipe(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	for _, argv := range [][]string{
		{"auth", "status", "--json", "hosts", "--jq", ".hosts"},
		{"auth", "status", "--json", "hosts", "--template", "{{.hosts}}"},
	} {
		code, _, errOut := f.serve(t, Request{Argv: argv})
		if code != ExitUsage || !strings.Contains(errOut, "pipe `gh auth status --json hosts` into jq") {
			t.Errorf("gh %v: code %d err %q", argv, code, errOut)
		}
	}
	// --help is gh's own text and still runs.
	code, out, _ := f.serve(t, Request{Argv: []string{"auth", "status", "--help"}})
	if code != 0 || out != "ran auth status --help\n" {
		t.Fatalf("auth status --help: code %d out %q", code, out)
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

// §5.1 rule 6, revised 2026-10-09 by the owner's ruling: a host gh of any version serves a
// reviewed read with the grammar as-is, and the audit line records which version ran it.
func TestServeAnyGHVersionRunsAReviewedRead(t *testing.T) {
	for _, version := range []string{"2.102.0", "2.99.0", "3.0.1"} {
		f := newBrokerFixture(t, version)
		code, out, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/r"}})
		if code != 0 || out != "ran pr view --repo=o/r 1\n" || errOut != "" {
			t.Fatalf("gh %s: code %d out %q err %q", version, code, out, errOut)
		}
		if ev := lastAudit(t); ev.Outcome != "ran" || ev.GHVersion != version {
			t.Fatalf("gh %s: audit %+v", version, ev)
		}
		// Grammar is still grammar: an unknown flag is refused at any version.
		if code, _, _ := f.serve(t, Request{Argv: []string{"pr", "view", "1", "-R", "o/r", "--no-such-flag"}}); code != ExitUsage {
			t.Fatalf("gh %s: unknown flag exit %d, want %d", version, code, ExitUsage)
		}
	}
}

// §3.5: a host gh with no login answers gh's own exit 4; the jail gets 69 naming
// `gh auth status` on the host, after gh's stderr.
func TestServeMapsNoHostLoginTo69(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.scope = NewScope([]string{"o/r", "o/nologin"}).WithGrants(readAll...)
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

	// This transport fixture has no production request store or delivery, so the write fails
	// closed immediately. The separate production-caller tests exercise the manual host path.
	out.Reset()
	errOut.Reset()
	start := time.Now()
	code = Forward([]string{"pr", "comment", "1", "--body-file", "-"}, ForwardEnv{Getenv: env,
		Stdin: strings.NewReader("body"), Stdout: &out, Stderr: &errOut, OriginRepo: func() string { return "o/r" }})
	if code != ExitNoPerm || !strings.Contains(errOut.String(), "required request notice and answer delivery are unavailable") {
		t.Fatalf("a forwarded write without required delivery: code %d err %q", code, errOut.String())
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a write without delivery took %s", took)
	}
	if ev := lastAudit(t); ev.Stdin == nil || ev.Stdin.Bytes != 4 {
		t.Fatalf("stdin was not forwarded: %+v", ev)
	}
}

// The jail has no endpoint: the message names the host command that turns the broker on for
// this project (docs/design/boundary-broker.md OQ-BB13), with the project as the host names it
// when the launch said, and never a user-config switch, which is refused for it.
func TestForwardWithNoEndpointSaysHowToEnableIt(t *testing.T) {
	for _, c := range []struct {
		hostDir, want string
	}{
		{"", "run `yolo check` on\n  the host, in this project,"},
		{"/home/you/my code/app", "the host, in '/home/you/my code/app',"},
	} {
		var errOut bytes.Buffer
		getenv := func(k string) string {
			if k == "YOLO_HOST_DIR" {
				return c.hostDir
			}
			return ""
		}
		code := Forward([]string{"pr", "view"}, ForwardEnv{Getenv: getenv,
			Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &errOut, OriginRepo: func() string { return "" }})
		if code != ExitUnavailable || !strings.Contains(errOut.String(), c.want) {
			t.Errorf("YOLO_HOST_DIR=%q: code %d, want %q in:\n%s", c.hostDir, code, c.want, errOut.String())
		}
		if strings.Contains(errOut.String(), "config.jsonc") {
			t.Errorf("the message still sends the reader to the user config:\n%s", errOut.String())
		}
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
	b, cleanup := newBroker(brokerscope.File{Grants: readAll, Workspace: "/w", Repos: []string{"o/r"}, Widened: []string{"x/y"}}, "/w", &log)
	defer cleanup()
	// A `widened` list a previous build wrote still joins the scope for every set (OQ-BB9, ruled
	// A); no launch writes one now (WW-D12).
	if got := b.scope.Repos(); strings.Join(got, ",") != "o/r,x/y" {
		t.Fatalf("scope %v", got)
	}
	if !strings.Contains(log.String(), "no gh on the host's PATH") {
		t.Fatalf("log %q", log.String())
	}
}

// The daemon's log is one file per loophole name under logs/, shared by every jail on the
// machine and commonly mounted into them, so it names no workspace, no repository and no
// host path (newBroker).
func TestTheBrokersLogNamesNoWorkspaceRepositoryOrHostPath(t *testing.T) {
	root := resolvedDir(t)
	gh := fakeGH(t, filepath.Join(root, "fake"), "2.101.0")
	cfg := filepath.Join(root, "host-gh-config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "hosts.yml"), []byte("github.com:\n    user: me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", resolvedDir(t))
	t.Setenv("PATH", filepath.Dir(gh)+":/usr/bin:/bin")
	t.Setenv("GH_CONFIG_DIR", cfg)
	ws := filepath.Join(resolvedDir(t), "secret-client-project")
	var log bytes.Buffer
	b, cleanup := newBroker(brokerscope.File{Grants: readAll, Workspace: ws, Repos: []string{"acme/private-roadmap"},
		Widened: []string{"acme/other-private"}}, ws, &log)
	defer cleanup()
	if b.runner == nil {
		t.Fatalf("no runner: %s", log.String())
	}
	for _, leak := range []string{ws, "secret-client-project", "acme/private-roadmap", "acme/other-private",
		gh, cfg} {
		if strings.Contains(log.String(), leak) {
			t.Errorf("the daemon's log names %q:\n%s", leak, log.String())
		}
	}
	if !strings.Contains(log.String(), "host gh version 2.101.0") {
		t.Fatalf("log %q", log.String())
	}
}

// The broker's half of the workspace entry (docs/design/workspace-widening.md §3.4), through
// newBroker from a launch's scope file: a repository the entry admitted runs like a remote's; one
// outside the scope is refused telling the agent to add it to the `repos` list under
// `brokered.github` in the exact files the scope file names, to check its edit, and to ask for
// a restart, naming no host path and no user config; without the names it falls back to the
// file the briefing names; and an account-wide command is refused saying no entry can add it.
func TestTheBrokerRunsAnEntrysRepositoryAndNamesTheEntryForAnother(t *testing.T) {
	root := resolvedDir(t)
	gh := fakeGH(t, filepath.Join(root, "fake"), "2.101.0")
	cfg := filepath.Join(root, "host-gh-config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "hosts.yml"), []byte("github.com:\n    user: me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := resolvedDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", filepath.Dir(gh)+":/usr/bin:/bin")
	t.Setenv("GH_CONFIG_DIR", cfg)
	ws := filepath.Join(resolvedDir(t), "R&D app")
	b, cleanup := newBroker(brokerscope.File{Grants: readAll, Workspace: ws, Repos: []string{"o/r", "org/lib"},
		ConfigFile: "yolo-jail.json", LocalFile: "yolo-jail.local.jsonc"}, ws, &bytes.Buffer{})
	defer cleanup()
	if b.runner == nil {
		t.Fatal("no runner")
	}
	serve := func(argv ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := b.Serve(Request{Argv: argv}, "jail-1", func(p []byte) { out.Write(p) }, func(p []byte) { errOut.Write(p) })
		return code, out.String(), errOut.String()
	}

	if code, out, errOut := serve("pr", "view", "1", "-R", "org/lib"); code != 0 || out != "ran pr view --repo=org/lib 1\n" {
		t.Fatalf("an approved entry's repository: code %d out %q err %q", code, out, errOut)
	}

	code, _, errOut := serve("pr", "view", "1", "-R", "other/private")
	flat := strings.Join(strings.Fields(errOut), " ")
	for _, want := range []string{"run `yolo request repos <alias> github.com/other/private`, which adds it as a `[repos.<alias>]` table in this project's config",
		"run `yolo check --no-build`", "ask the user to restart the jail and approve the repository scope at launch",
		"Nothing sent through gh adds a repository", "(o/r, org/lib)"} {
		if code != ExitUsage || !strings.Contains(flat, want) {
			t.Errorf("a repository outside the scope: code %d, want 64 saying %q:\n%s", code, want, errOut)
		}
	}
	for _, leak := range []string{ws, paths.UserConfigPath(), `"workspaces"`, "yolo-jail.jsonc", "yolo-jail.json", "hand-edit", `"brokered": {`} {
		if strings.Contains(errOut, leak) {
			t.Errorf("the refusal names %q:\n%s", leak, errOut)
		}
	}

	for _, argv := range [][]string{{"search", "code", "foo"}, {"search", "code", "foo repo:x/y", "--repo", "o/r"}} {
		code, _, errOut = serve(argv...)
		if code != ExitUsage || !strings.Contains(errOut, "`brokered.github.repos` entry can add") {
			t.Errorf("gh %v: code %d, want 64 saying no `brokered.github.repos` entry can add it:\n%s", argv, code, errOut)
		}
	}

	// A scope file without the file names gives the same verb: the advice never depended on them.
	b2, cleanup2 := newBroker(brokerscope.File{Grants: readAll, Workspace: ws, Repos: []string{"o/r"}}, ws, &bytes.Buffer{})
	defer cleanup2()
	var errOut2 bytes.Buffer
	b2.Serve(Request{Argv: []string{"pr", "view", "1", "-R", "other/private"}}, "jail-1", func([]byte) {},
		func(p []byte) { errOut2.Write(p) })
	if !strings.Contains(strings.Join(strings.Fields(errOut2.String()), " "),
		"run `yolo request repos <alias> github.com/other/private`") {
		t.Errorf("without the names, the refusal does not name the verb:\n%s", errOut2.String())
	}
}

// A BROKER STOPPING ENDS NO GRANT (boundary-broker.md BB-D99): its teardown refuses the
// requests its jails left pending, since the call waiting on each is gone, and leaves every
// grant live, so a restart or relaunch of the workspace keeps what the human granted.
func TestBrokerLifetimeIsFreshAndItsTeardownKeepsGrants(t *testing.T) {
	t.Setenv("HOME", resolvedDir(t))
	t.Setenv("PATH", resolvedDir(t)) // no host gh is needed to test lifecycle teardown
	var log bytes.Buffer
	b, cleanup := newBroker(brokerscope.File{Grants: readAll, Workspace: "/workspace/project"}, "/workspace/project", &log)
	other, cleanupOther := newBroker(brokerscope.File{Grants: readAll, Workspace: "/workspace/project"}, "/workspace/project", &bytes.Buffer{})
	defer cleanupOther()
	if b.startID == "" || b.startID == other.startID {
		t.Fatalf("broker lifetimes are not unique: %q, %q", b.startID, other.startID)
	}
	store := b.store
	store.Revalidate = func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }
	store.Audit = StoreAudit(b.audit) // as a bound broker sets it, so filing and deciding are audited
	file := func(pr string) brokerstore.Request {
		q, err := store.File(brokerstore.Request{Jail: "jail-one", Start: b.startID,
			Workspace: b.workspace, Argv: []string{"pr", "merge", pr}, Repo: "o/r",
			StdinDigest: "none", Set: "read-write", Policy: brokerstore.Policy{
				Window: 15 * time.Minute, MaxWindow: 12 * time.Hour, Session: true, Uses: 25,
			}})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	granted := file("1")
	g, err := store.Decide(granted.ID, "pending", brokerstore.Decision{For: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	waiting := file("2")
	b.trackJail("jail-one")
	cleanup()
	rs, grants, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range grants {
		if h.ID == g.ID && (!h.Ended.IsZero() || h.Workspace != "/workspace/project") {
			t.Fatalf("broker teardown ended or unbound a workspace's grant: %+v", h)
		}
	}
	for _, q := range rs {
		if q.ID == waiting.ID && q.State != "refused" {
			t.Fatalf("a pending request outlived its broker: %+v", q)
		}
	}
	if !strings.Contains(log.String(), "no gh on the host") {
		t.Fatalf("fake external executor setup did not take the unavailable path: %s", log.String())
	}
	// The filing and the decision were audited, so the log exists; a missing one would make
	// the check below pass on nothing.
	audit, err := os.ReadFile(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(audit, []byte(g.ID)) {
		t.Fatalf("the decision issuing %s was not audited: %s", g.ID, audit)
	}
	for _, line := range bytes.Split(audit, []byte("\n")) {
		var ev brokeraudit.Event
		if json.Unmarshal(line, &ev) == nil && ev.Event == "grant-ended" {
			t.Fatalf("broker teardown audited a grant ending: %s", line)
		}
	}
}

func TestBrokerTeardownReportsStoreFailureAndRetries(t *testing.T) {
	t.Setenv("HOME", resolvedDir(t))
	t.Setenv("PATH", resolvedDir(t))
	var log bytes.Buffer
	b, cleanup := newBroker(brokerscope.File{Grants: readAll, Workspace: "/project"}, "/project", &log)
	defer cleanup()
	b.store.Revalidate = func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }
	q, err := b.store.File(brokerstore.Request{Jail: "jail", Start: b.startID, Workspace: "/project", Argv: []string{"pr", "merge", "1"}, Repo: "o/r", StdinDigest: "none", Set: "read-write", Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 2}})
	if err != nil {
		t.Fatal(err)
	}
	b.trackJail(q.Jail)
	path := filepath.Join(b.store.Dir, "requests", q.ID+".json")
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	b.endLifetimes()
	if !strings.Contains(log.String(), "teardown incomplete") {
		t.Fatal(log.String())
	}
	b.lifetimes.Mu.Lock()
	_, tracked := b.lifetimes.Jails[q.Jail]
	b.lifetimes.Mu.Unlock()
	if !tracked {
		t.Fatal("failed teardown forgotten")
	}
	if err = os.WriteFile(path, saved, 0600); err != nil {
		t.Fatal(err)
	}
	b.endLifetimes()
	rs, _, err := b.store.List()
	if err != nil || len(rs) != 1 || rs[0].State != "refused" {
		t.Fatalf("retried teardown left %+v, %v", rs, err)
	}
	b.lifetimes.Mu.Lock()
	_, tracked = b.lifetimes.Jails[q.Jail]
	b.lifetimes.Mu.Unlock()
	if tracked {
		t.Fatal("finished teardown still tracked")
	}
}
