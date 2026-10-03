package ghbroker

import (
	"bytes"
	"encoding/json"
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
	f.b.scope = NewScope([]string{"o/r", "o/lib"})
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

// The jail has no endpoint: the message names the host command that turns the broker on for
// this project (docs/design/boundary-broker.md OQ-BB13), with the project as the host names it
// when the launch said, and never a user-config switch, which is refused for it.
func TestForwardWithNoEndpointSaysHowToEnableIt(t *testing.T) {
	for _, c := range []struct {
		hostDir, want string
	}{
		{"", "yolo loopholes enable github-broker\n  in this project, on the host,"},
		{"/home/you/my code/app", "yolo loopholes enable github-broker --workspace '/home/you/my code/app'"},
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
	b, cleanup := newBroker(brokerscope.File{Workspace: "/w", Repos: []string{"o/r"}, Widened: []string{"x/y"}}, "/w", &log)
	defer cleanup()
	// A widened repository joins the scope for every set (OQ-BB9, ruled A).
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
	b, cleanup := newBroker(brokerscope.File{Workspace: ws, Repos: []string{"acme/private-roadmap"},
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

// §12 done criterion 17, the broker's half, through newBroker from a launch's scope file: a
// repository the widening entry added runs like a remote's, a repository outside the scope is
// refused naming the widening entry that would admit it, keyed by THIS workspace, and an
// account-wide command is refused saying no widening entry admits one.
func TestTheBrokerRunsAWidenedRepositoryAndNamesTheEntryForAnother(t *testing.T) {
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
	// A folder name with `&` and a space: the entry the refusal spells must be the folder's
	// own name, which JSON's HTML escaping would write as \u0026.
	ws := filepath.Join(resolvedDir(t), "R&D app")
	b, cleanup := newBroker(brokerscope.File{Workspace: ws, Repos: []string{"o/r"},
		Widened: []string{"org/lib"}}, ws, &bytes.Buffer{})
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
		t.Fatalf("a widened repository: code %d out %q err %q", code, out, errOut)
	}

	code, _, errOut := serve("pr", "view", "1", "-R", "other/private")
	entry := `"brokered": {"github": {"workspaces": {"` + ws + `": {"repos": ["other/private"]}}}}`
	if code != ExitUsage || !strings.Contains(errOut, entry) ||
		!strings.Contains(errOut, paths.UserConfigPath()) || !strings.Contains(errOut, "next fresh launch") {
		t.Fatalf("a repository outside the scope: code %d, want 64 naming %s in %s:\n%s",
			code, entry, paths.UserConfigPath(), errOut)
	}
	if !strings.Contains(errOut, "(o/r, org/lib)") {
		t.Errorf("the refusal does not name the scope, widened repository included:\n%s", errOut)
	}

	for _, argv := range [][]string{{"search", "code", "foo"}, {"search", "code", "foo repo:x/y", "--repo", "o/r"}} {
		code, _, errOut = serve(argv...)
		if code != ExitUsage || !strings.Contains(strings.ToLower(errOut), "no widening entry") ||
			strings.Contains(errOut, `"workspaces"`) {
			t.Errorf("gh %v: code %d, want 64 saying no widening entry admits it, naming none:\n%s", argv, code, errOut)
		}
	}
}
