package gitbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/gitremote"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestHelperProcess is the jail's remote helper when the fixture's git runs
// `git-remote-yolo`: the script on git's PATH re-runs this test binary with GO_HELPER=1.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(gitremote.Run(args, gitremote.Env{Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}

const origin = "git@fixture.test:o/r.git"

// lockedBuffer is the broker's log, written from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fixture struct {
	t         *testing.T
	root      string
	bare      string // srv/o/r.git
	ws        string
	bin       string // the broker's fake ssh
	helperBin string // git-remote-yolo
	record    string // where the fake ssh records its argv and env
	scopeFile string
	endpoint  string
	broker    *Broker
	log       *lockedBuffer
	getenv    func(string) string // the broker's environment
}

// sshScript is the fake host ssh: it records its argv and env, then runs its last argument
// (git's remote command) with sh from the directory holding o/r.git, as sshd would run it in
// the login's home.
//
// A body of "tee" runs git's remote command the same way, recording every byte the broker
// sent the upstream in record/stdin.
func sshScript(record, srv, body string) string {
	if body == "tee" {
		body = `PATH="$(git --exec-path):$PATH"
cd ` + srv + ` || exit 1
last=$(printf '%s\n' "$last" | sed "s|'\\/o\\/r.git'|'` + srv + `/o/r.git'|")
tee ` + record + `/stdin | sh -c "$last"`
	}
	if body == "" {
		body = `PATH="$(git --exec-path):$PATH"
cd ` + srv + ` || exit 1
last=$(printf '%s\n' "$last" | sed "s|'\\/o\\/r.git'|'` + srv + `/o/r.git'|")
exec sh -c "$last"`
	}
	return `#!/bin/sh
printf '%s\n' "$@" >> ` + record + `/argv
env > ` + record + `/env
for a in "$@"; do last=$a; done
` + body + "\n"
}

func gitEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "YOLO_SERVICE_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, append([]string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=f@x", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=f@x",
	}, extra...)...)
}

func run(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, gitEnv(), args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newFixture builds the bare repository, the workspace clone, the fake ssh, the scope file
// approving repos, and a broker serving an endpoint. sshBody, when set, replaces the fake
// ssh's default of running git's remote command.
func newFixture(t *testing.T, sshBody string, repos ...string) *fixture {
	t.Helper()
	return newFixtureOpts(t, fixtureOpts{sshBody: sshBody, repos: repos})
}

type fixtureOpts struct {
	sshBody string
	repos   []string
	// sshInWorkspace puts the fake ssh inside the workspace, where an agent can rewrite it.
	sshInWorkspace bool
	// prepare, when set, runs once the repositories exist and before the scope is written: it
	// returns the scope's repositories and a directory to put first on the broker's PATH.
	prepare func(f *fixture) (repos []string, pathFirst string)
	// bound, when set, runs once the broker exists, before it serves.
	bound func(f *fixture)
	// grants are the scope file's approved user grants, after the read grant every fixture
	// holds unless noRead is set (BB-D108).
	grants []brokerscope.Grant
	// noRead leaves the read grant out of both the user config and the scope file: the broker
	// of a selected pack that grants nothing.
	noRead bool
}

// readGrant is the user config's read grant for every workspace (BB-D107), as the gate approves
// it into the scope file.
var readGrant = brokerscope.Grant{Kind: "read", Repo: brokerscope.AnyRepo}

// userConfigBody is the user config allowKinds and the fixture write: the read grant unless
// noRead, and kinds under grant_kinds when any are given.
func userConfigBody(noRead bool, kinds ...string) string {
	body := map[string]any{}
	if !noRead {
		body["grants"] = []map[string]string{{"kind": "read", "repo": "*"}}
	}
	if len(kinds) > 0 {
		body["grant_kinds"] = kinds
	}
	out, _ := json.Marshal(map[string]any{"brokered": map[string]any{"git": body}})
	return string(out)
}

// writeUserConfig writes body as the user's own config.
func writeUserConfig(t *testing.T, body string) {
	t.Helper()
	p := paths.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixtureOpts(t *testing.T, o fixtureOpts) *fixture {
	t.Helper()
	sshBody, repos := o.sshBody, o.repos
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	// Resolved where it is minted: on macOS t.TempDir() is under /var -> /private/var, and a
	// subprocess's pwd under HOME reports the resolved path.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_USER_LAYER", "")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: root, bare: filepath.Join(root, "srv", "o", "r.git"), ws: filepath.Join(root, "ws"),
		bin: filepath.Join(root, "bin"), helperBin: filepath.Join(root, "helperbin"), record: filepath.Join(root, "record"),
		log: &lockedBuffer{}}
	for _, d := range []string{filepath.Dir(f.bare), f.record} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, root, "init", "-q", "--bare", "-b", "main", f.bare)
	seed := filepath.Join(root, "seed")
	mustGit(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, seed, "add", "README")
	mustGit(t, seed, "commit", "-q", "-m", "seed")
	mustGit(t, seed, "push", "-q", f.bare, "main")
	mustGit(t, root, "clone", "-q", f.bare, f.ws)
	mustGit(t, f.ws, "remote", "set-url", "origin", origin)
	if o.sshInWorkspace {
		f.bin = filepath.Join(f.ws, ".tools")
	}

	writeExec(t, filepath.Join(f.bin, "ssh"), sshScript(f.record, filepath.Join(root, "srv"), sshBody))
	writeExec(t, filepath.Join(f.helperBin, "git-remote-yolo"),
		"#!/bin/sh\nexec \"$TEST_BINARY\" -test.run='^TestHelperProcess$' -- \"$@\"\n")
	writeExec(t, filepath.Join(f.helperBin, "ssh-outside-broker"),
		"#!/bin/sh\necho 'direct SSH bypassed the broker' >&2\nexit 97\n")

	pathFirst := f.bin
	if o.prepare != nil {
		var dir string
		repos, dir = o.prepare(f)
		pathFirst = dir + string(os.PathListSeparator) + f.bin
	}
	if len(repos) == 0 {
		repos = []string{origin}
	}
	grants := o.grants
	if !o.noRead {
		grants = append([]brokerscope.Grant{readGrant}, grants...)
	}
	writeUserConfig(t, userConfigBody(o.noRead))
	sf := brokerscope.File{Source: Source, LaunchID: "fixture-launch", PID: os.Getpid(), Container: "fixture-container",
		Workspace: f.ws, Form: brokerscope.FormRemoteURL, Repos: repos, Grants: grants}
	brokerPath := pathFirst + string(os.PathListSeparator) + os.Getenv("PATH")
	f.getenv = func(k string) string {
		if k == "PATH" {
			return brokerPath
		}
		return os.Getenv(k)
	}
	f.launch(sf, o.bound)
	return f
}

// launch writes sf as a launch's scope file and serves a fresh broker bound to it, as a jail's
// launch does; the fixture's endpoint then reaches the new broker.
func (f *fixture) launch(sf brokerscope.File, bound func(*fixture)) {
	t := f.t
	t.Helper()
	var err error
	f.scopeFile, err = brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, cleanup, err := newBroker(ctx, f.scopeFile, sf, f.ws, f.log, f.getenv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	f.broker = b
	// A push no grant covers asks for approval and waits for it: a short wait, so a test that
	// never answers it is refused at once. A test answering during the wait lengthens it.
	b.waitLimit = 50 * time.Millisecond
	if bound != nil {
		bound(f)
	}
	f.endpoint = serve(t, b.Handle)
}

// serve starts an in-test loopback-TLS endpoint running handler and returns its endpoint file.
func serve(t *testing.T, handler hostservice.Handler) string {
	t.Helper()
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	dir, err := os.MkdirTemp("/tmp", "yj-gitbroker-test-")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(dir, "git-broker.endpoint")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeEndpoint(handler, endpoint, stop)
	}()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(endpoint); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		close(stop)
		<-done
		_ = os.RemoveAll(dir)
	})
	return endpoint
}

func packGitRewriteEnv(t *testing.T) []string {
	t.Helper()
	toml, err := os.ReadFile(filepath.Join("..", "..", "packs", "git", "pack.toml"))
	if err != nil {
		t.Fatal(err)
	}
	decl, problems := packdecl.DecodeTOML(toml)
	if len(problems) != 0 {
		t.Fatalf("the git pack's manifest: %v", problems)
	}
	// The legacy `contributes` rendering of the decoded manifest: the shape read below.
	data, err := json.Marshal(decl)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributes []struct {
			Kind     string            `json:"kind"`
			ServedBy string            `json:"served_by"`
			Vars     map[string]string `json:"vars"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, contribution := range manifest.Contributes {
		if contribution.Kind != "env" || contribution.ServedBy != "git-broker" {
			continue
		}
		count, err := strconv.Atoi(contribution.Vars["GIT_CONFIG_COUNT"])
		if err != nil || count == 0 {
			t.Fatalf("git pack has invalid GIT_CONFIG_COUNT %q", contribution.Vars["GIT_CONFIG_COUNT"])
		}
		env := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(count)}
		for i := 0; i < count; i++ {
			for _, key := range []string{fmt.Sprintf("GIT_CONFIG_KEY_%d", i), fmt.Sprintf("GIT_CONFIG_VALUE_%d", i)} {
				value, ok := contribution.Vars[key]
				if !ok {
					t.Fatalf("git pack rewrite is missing %s", key)
				}
				env = append(env, key+"="+value)
			}
		}
		return env
	}
	t.Fatal("git pack has no served_by git-broker env contribution")
	return nil
}

// git runs the real git in the workspace with the production pack's rewrite contribution:
// rewritten URLs reach the helper, which dispatches through the fixture broker.
func (f *fixture) git(args ...string) (string, error) {
	f.t.Helper()
	extra := append(packGitRewriteEnv(f.t),
		"PATH="+f.helperBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_SSH_COMMAND="+filepath.Join(f.helperBin, "ssh-outside-broker"),
		gitremote.EndpointEnv+"="+f.endpoint, "GO_HELPER=1", "TEST_BINARY="+os.Args[0])
	return run(f.t, f.ws, gitEnv(extra...), args...)
}

// helper runs the jail's helper in-process against the fixture for url and service, with
// git's bytes after the connect line, and returns its exit code, stdout and stderr.
func (f *fixture) helper(url, service string, gitBytes []byte) (int, string, string) {
	f.t.Helper()
	var out, errb bytes.Buffer
	in := append([]byte("capabilities\nconnect "+service+"\n"), gitBytes...)
	rc := gitremote.Run([]string{"origin", url}, gitremote.Env{
		Getenv: func(k string) string {
			if k == gitremote.EndpointEnv {
				return f.endpoint
			}
			return ""
		},
		Stdin: bytes.NewReader(in), Stdout: &out, Stderr: &errb})
	return rc, out.String(), errb.String()
}

func (f *fixture) sshRan() bool {
	_, err := os.Stat(filepath.Join(f.record, "argv"))
	return err == nil
}

func (f *fixture) setScope(repos ...string) {
	f.t.Helper()
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		f.t.Fatal(err)
	}
	sf.Repos = repos
	if _, err := brokerscope.Write(sf); err != nil {
		f.t.Fatal(err)
	}
}

func auditEvents(t *testing.T) []brokeraudit.Event {
	t.Helper()
	data, err := os.ReadFile(paths.BrokerAuditLog())
	if err != nil {
		t.Fatalf("no audit log: %v", err)
	}
	var evs []brokeraudit.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e brokeraudit.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		evs = append(evs, e)
	}
	return evs
}

func auditRaw(t *testing.T) string {
	t.Helper()
	data, _ := os.ReadFile(paths.BrokerAuditLog())
	return string(data)
}

func TestRealGitRoutesBracketedIPv6ScpThroughBroker(t *testing.T) {
	const remote = "git@[::1]:o/r.git"
	f := newFixture(t, "", remote)
	out, err := f.git("ls-remote", remote)
	if err != nil {
		t.Fatalf("git ls-remote %q: %v\n%s", remote, err, out)
	}
	if !strings.Contains(out, "\trefs/heads/main") {
		t.Fatalf("ls-remote did not receive the fixture's main ref:\n%s", out)
	}
	argv, err := os.ReadFile(filepath.Join(f.record, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "git@::1") || !strings.Contains(string(argv), "git-upload-pack 'o/r.git'") {
		t.Fatalf("broker did not dispatch the bracketed IPv6 URL as scp-style SSH:\n%s", argv)
	}
}

func TestGitPackRewritesSSHFormsThroughTheBroker(t *testing.T) {
	wantRules := map[string]string{
		"url.yolo::ssh://.insteadOf":     "ssh://",
		"url.yolo::https://.insteadOf":   "https://",
		"url.yolo::git@.insteadOf":       "git@",
		"url.yolo::git+ssh://.insteadOf": "git+ssh://",
		"url.yolo::ssh+git://.insteadOf": "ssh+git://",
	}
	env := packGitRewriteEnv(t)
	gotRules := map[string]string{}
	for _, item := range env[1:] {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.HasPrefix(key, "GIT_CONFIG_KEY_") {
			continue
		}
		index := strings.TrimPrefix(key, "GIT_CONFIG_KEY_")
		gotRules[value] = envValue(env, "GIT_CONFIG_VALUE_"+index)
	}
	for key, value := range wantRules {
		if got := gotRules[key]; got != value {
			t.Errorf("git pack rewrite %q = %q, want %q", key, got, value)
		}
	}

	for _, remote := range []string{
		"ssh://git@fixture.test/o/r.git",
		"git+ssh://git@fixture.test/o/r.git",
		"ssh+git://git@fixture.test/o/r.git",
		"ssh://git@[::1]:2222/o/r.git",
	} {
		t.Run(remote, func(t *testing.T) {
			f := newFixture(t, "", remote)
			out, err := f.git("ls-remote", remote)
			if err != nil || !strings.Contains(out, "\trefs/heads/main") {
				t.Fatalf("real git ls-remote %q: %v\n%s", remote, err, out)
			}
			events := auditEvents(t)
			if len(events) != 1 || events[0].Set != "read" || events[0].Remote != remote {
				t.Fatalf("the real Git call did not reach the broker with its exact approved URL: %+v", events)
			}
		})
	}
}

func envValue(env []string, key string) string {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

func TestGitPackLeavesOtherValidGitURLsDirect(t *testing.T) {
	f := newFixture(t, "")
	for _, remote := range []string{
		"fixture.test:o/r.git",
		"alice@fixture.test:o/r.git",
		"http://fixture.test/o/r.git",
		"git://fixture.test/o/r.git",
		"file:///srv/o/r.git",
		"/srv/o/r.git",
		"C:/src/o/r.git",
	} {
		out, err := f.git("ls-remote", "--get-url", remote)
		if err != nil || strings.TrimSpace(out) != remote {
			t.Errorf("git's no-network URL expansion for %q = %q, %v; want unchanged direct URL", remote, out, err)
		}
	}
	if f.sshRan() {
		t.Fatal("a valid but unrewritten URL reached the broker's host SSH transport")
	}
}

func TestSameIdentityDoesNotApproveAnotherSSHURL(t *testing.T) {
	f := newFixture(t, "")
	const alias = "git+ssh://git@fixture.test/o/r.git"
	out, err := f.git("ls-remote", alias)
	if err == nil || !strings.Contains(out, "approved scope") {
		t.Fatalf("unapproved alias with the same repository identity was not refused: %v\n%s", err, out)
	}
	if f.sshRan() {
		t.Fatal("the host SSH transport ran for a URL that was not approved exactly")
	}
	events := auditEvents(t)
	if len(events) != 1 || events[0].Set != "out-of-scope" || events[0].Remote != alias {
		t.Fatalf("out-of-scope alias audit: %+v", events)
	}
}

func TestSSHAliasPushStillRefusesAnUncoveredRef(t *testing.T) {
	const remote = "ssh+git://git@fixture.test/o/r.git"
	f := newFixture(t, "tee", remote)
	f.localCommit()
	out, err := f.git("push", remote, "HEAD:main")
	if err == nil || !strings.Contains(out, "! [remote rejected]") {
		t.Fatalf("push through alias was not refused by the per-ref policy: %v\n%s", err, out)
	}
	if sent, err := os.ReadFile(filepath.Join(f.record, "stdin")); err != nil || string(sent) != "0000" {
		t.Fatalf("receive-pack got %q (%v), want only the empty command list", sent, err)
	}
	var receive *brokeraudit.Event
	for _, event := range auditEvents(t) {
		if len(event.Argv) == 1 && event.Argv[0] == serviceReceivePack {
			receive = &event
		}
	}
	if receive == nil || receive.Set != "refused" || receive.Remote != remote {
		t.Fatalf("receive-pack alias audit: %+v", receive)
	}
}

func TestFetchThroughTheBroker(t *testing.T) {
	f := newFixture(t, "")
	out, err := f.git("fetch", "origin")
	if err != nil {
		t.Fatalf("git fetch: %v\n%s", err, out)
	}
	if got, want := mustGit(t, f.ws, "rev-parse", "refs/remotes/origin/main"), mustGit(t, f.bare, "rev-parse", "main"); got != want {
		t.Fatalf("origin/main %s, bare main %s", got, want)
	}
	argv, err := os.ReadFile(filepath.Join(f.record, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(strings.Fields(strings.ReplaceAll(string(argv), "\n", " ")), " ")
	if !strings.HasSuffix(joined, "fixture.test git-upload-pack 'o/r.git'") {
		t.Fatalf("ssh argv %q", joined)
	}
	if !strings.Contains(joined, "-o ClearAllForwardings=yes") {
		t.Fatalf("ssh argv %q has no ClearAllForwardings", joined)
	}
	evs := auditEvents(t)
	if len(evs) != 1 {
		t.Fatalf("audit events %+v", evs)
	}
	raw := auditRaw(t)
	for _, want := range []string{`"service":"git"`, `"set":"read"`, `"repo":"fixture.test/o/r"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("audit log lacks %s:\n%s", want, raw)
		}
	}
	if evs[0].Remote != origin {
		t.Fatalf("audit remote %q", evs[0].Remote)
	}
}

func (f *fixture) localCommit() {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.ws, "new"), []byte("new\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	mustGit(f.t, f.ws, "add", "new")
	mustGit(f.t, f.ws, "commit", "-q", "-m", "local")
}

func TestPushRefusedNothingForwarded(t *testing.T) {
	f := newFixture(t, "tee")
	before := mustGit(t, f.bare, "rev-parse", "main")
	f.localCommit()
	out, err := f.git("push", "origin", "HEAD:main")
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("git push: %v (want exit 1)\n%s", err, out)
	}
	t.Logf("refused push output:\n%s", out)
	for _, want := range []string{"! [remote rejected]", "(needs approval: r-", "yolo approve r-"} {
		if !strings.Contains(out, want) {
			t.Fatalf("push output lacks %q:\n%s", want, out)
		}
	}
	if after := mustGit(t, f.bare, "rev-parse", "main"); after != before {
		t.Fatalf("bare main moved %s -> %s", before, after)
	}
	// The upstream was sent an empty command list and nothing else.
	if sent, err := os.ReadFile(filepath.Join(f.record, "stdin")); err != nil || string(sent) != "0000" {
		t.Fatalf("the upstream received %q (%v), want exactly \"0000\"", sent, err)
	}
	var push *brokeraudit.Event
	evs := auditEvents(t)
	for i, e := range evs {
		if len(e.Argv) == 1 && e.Argv[0] == "git-receive-pack" {
			push = &evs[i]
		}
	}
	if push == nil || push.Set != "refused" || len(push.Refs) != 1 || push.Refs[0].Ref != "refs/heads/main" {
		t.Fatalf("push audit %+v", push)
	}
}

func TestPushOutsideHeadsAndTagsRefused(t *testing.T) {
	f := newFixture(t, "")
	f.localCommit()
	out, err := f.git("push", "origin", "HEAD:refs/notes/x")
	if err == nil {
		t.Fatalf("push to refs/notes/x succeeded:\n%s", out)
	}
	if !strings.Contains(out, "! [remote rejected]") || !strings.Contains(out, "refs/heads/") || !strings.Contains(out, "refs/tags/") {
		t.Fatalf("push output:\n%s", out)
	}
	if _, err := run(t, f.bare, gitEnv(), "rev-parse", "--verify", "-q", "refs/notes/x"); err == nil {
		t.Fatal("refs/notes/x reached the upstream")
	}
}

func TestOutOfScopeRefused(t *testing.T) {
	f := newFixture(t, "")
	mustGit(t, f.ws, "remote", "add", "other", "git@other.test:o/r.git")
	out, err := f.git("fetch", "other")
	if err == nil {
		t.Fatalf("out-of-scope fetch succeeded:\n%s", out)
	}
	if !strings.Contains(out, "approved scope") || !strings.Contains(out, "config-change gate") {
		t.Fatalf("fetch output:\n%s", out)
	}
	if f.sshRan() {
		t.Fatal("the fake ssh ran for an out-of-scope remote")
	}
	if !strings.Contains(auditRaw(t), `"set":"out-of-scope"`) {
		t.Fatalf("no out-of-scope audit event:\n%s", auditRaw(t))
	}
}

func TestSSHHostStartingWithDashRefused(t *testing.T) {
	const bad = "ssh://-oProxyCommand=x/r"
	f := newFixture(t, "", bad)
	rc, stdout, stderr := f.helper(bad, "git-upload-pack", nil)
	if rc == 0 || stdout != "connect\n\n" {
		t.Fatalf("rc %d stdout %q stderr %q", rc, stdout, stderr)
	}
	if f.sshRan() {
		t.Fatal("the fake ssh ran for a host starting with -")
	}
}

func TestOnlySSHAndHTTPSTransports(t *testing.T) {
	f := newFixture(t, "")
	for _, bad := range []string{"file:///srv/r.git", "ext::sh -c x", "ext::sh", "fd::3", "-oProxyCommand=x:r",
		"git@fixture.test:-oFoo", "git+http://fixture.test/o/r", "http://fixture.test/o/r.git", "/srv/o/r.git"} {
		f.setScope(bad)
		rc, stdout, stderr := f.helper(bad, "git-upload-pack", nil)
		if rc == 0 || stdout != "connect\n\n" {
			t.Errorf("%q: rc %d stdout %q stderr %q", bad, rc, stdout, stderr)
		}
		if f.sshRan() {
			t.Fatalf("%q: the fake ssh ran", bad)
		}
	}
	if tr, err := transportOf("https://fixture.test/o/r.git"); err != nil || tr != transportHTTPS {
		t.Fatalf("https: transport %q, %v", tr, err)
	}
	for _, remote := range []string{"git+ssh://fixture.test/o/r.git", "ssh+git://fixture.test/o/r.git"} {
		if tr, err := transportOf(remote); err != nil || tr != transportSSH {
			t.Errorf("%s: transport %q, %v", remote, tr, err)
		}
	}
}

func TestUploadArchiveRefused(t *testing.T) {
	f := newFixture(t, "")
	var out, errb bytes.Buffer
	// The helper itself refuses the service; ask the broker directly through a raw session.
	rc := f.broker.serveRequest(context.Background(), request{Service: "git-upload-archive", URL: origin}, "fixture",
		nil, &out, &errb)
	if rc == 0 || f.sshRan() || !strings.Contains(errb.String(), "git-upload-archive") {
		t.Fatalf("rc %d stderr %q", rc, errb.String())
	}
}

// The receive-pack path forwards the bytes the command reader buffered, once judged, and never
// re-encodes them: a shallow line, the client's capability order and push options survive.
func TestReceivePackForwardsTheTeedBytes(t *testing.T) {
	var adv bytes.Buffer
	aw := gitproto.NewWriter(&adv)
	aw.Line("0000000000000000000000000000000000000000 capabilities^{}\x00report-status side-band-64k push-options\n")
	aw.Flush()
	root := t.TempDir()
	advFile, stdinFile := filepath.Join(root, "adv"), filepath.Join(root, "stdin")
	if err := os.WriteFile(advFile, adv.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, "cat "+advFile+"\ncat > "+stdinFile)
	f.broker.cover = func(c gitproto.Command) (string, string, bool) { return "create-branch", "create-branch", true }

	const one, zero = "1111111111111111111111111111111111111111", "0000000000000000000000000000000000000000"
	var push bytes.Buffer
	pw := gitproto.NewWriter(&push)
	pw.Line("shallow 2222222222222222222222222222222222222222\n")
	pw.Line(zero + " " + one + " refs/heads/agent/x\x00side-band-64k report-status push-options agent=git/2.55.0\n")
	pw.Flush()
	pw.Line("ci.skip\n")
	pw.Flush()
	push.WriteString("PACK-not-really-a-pack")
	rc, stdout, stderr := f.helper(origin, "git-receive-pack", push.Bytes())
	if rc != 0 {
		t.Fatalf("rc %d stdout %q stderr %q", rc, stdout, stderr)
	}
	got, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, push.Bytes()) {
		t.Fatalf("upstream received %q\nwant %q", got, push.Bytes())
	}
	if !strings.HasPrefix(stdout, "connect\n\n\n"+adv.String()) {
		t.Fatalf("client did not get the advertisement: %q", stdout)
	}
}

func TestSSHEnvBuiltFromNothing(t *testing.T) {
	t.Setenv("GH_TOKEN", "leak")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "leak")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/fixture-agent.sock")
	f := newFixture(t, "")
	if out, err := f.git("fetch", "origin"); err != nil {
		t.Fatalf("git fetch: %v\n%s", err, out)
	}
	env, err := os.ReadFile(filepath.Join(f.record, "env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "SSH_AUTH_SOCK=/tmp/fixture-agent.sock") || strings.Contains(string(env), "leak") {
		t.Fatalf("ssh env:\n%s", env)
	}
}

func TestNoHostCredentialNamesTheFix(t *testing.T) {
	f := newFixture(t, "echo 'Permission denied (publickey).' >&2\nexit 255")
	out, err := f.git("fetch", "origin")
	if err == nil {
		t.Fatalf("fetch succeeded:\n%s", out)
	}
	if !strings.Contains(out, "Permission denied (publickey).") || !strings.Contains(out, "ssh-add -l") {
		t.Fatalf("fetch output:\n%s", out)
	}
}

func TestBrokerLogNamesNoRepository(t *testing.T) {
	// The framework's access lines go to the daemon's stderr too, so they are read with it.
	access := &lockedBuffer{}
	saved := hostservice.Logger
	hostservice.Logger = log.New(access, "", 0)
	t.Cleanup(func() { hostservice.Logger = saved })
	f := newFixture(t, "")
	if out, err := f.git("fetch", "origin"); err != nil {
		t.Fatalf("git fetch: %v\n%s", err, out)
	}
	f.localCommit()
	if out, err := f.git("push", "origin", "HEAD:main"); err == nil {
		t.Fatalf("push succeeded:\n%s", out)
	}
	logText := f.log.String() + access.String()
	if strings.TrimSpace(logText) == "" {
		t.Fatal("the broker logged nothing")
	}
	for _, leak := range []string{"fixture.test", "o/r", f.ws} {
		if strings.Contains(logText, leak) {
			t.Fatalf("broker log names %q:\n%s", leak, logText)
		}
	}
}

func TestSSHArgv(t *testing.T) {
	for _, tc := range []struct {
		url, protocol string
		want          []string
	}{
		{"git@github.com:o/r.git", "", []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-o", "ConnectTimeout=30", "--", "git@github.com", "git-upload-pack 'o/r.git'"}},
		{"ssh://git@host:2222/~/repo.git", "version=2", []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-o", "ConnectTimeout=30", "-p", "2222", "-o", "SendEnv=GIT_PROTOCOL", "--", "git@host", "git-upload-pack '~/repo.git'"}},
		{"ssh://host/srv/it's.git", "version=9", []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-o", "ConnectTimeout=30", "--", "host", `git-upload-pack '/srv/it'\''s.git'`}},
		{"git@[::1]:o/r.git", "", []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-o", "ConnectTimeout=30", "--", "git@::1", "git-upload-pack 'o/r.git'"}},
		{"ssh://git@[::1]:2222/o/r.git", "", []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-o", "ConnectTimeout=30", "-p", "2222", "--", "git@::1", "git-upload-pack '/o/r.git'"}},
		{"git+ssh://alice@host/o/r.git", "", []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-o", "ConnectTimeout=30", "--", "alice@host", "git-upload-pack '/o/r.git'"}},
	} {
		got, err := sshArgv(tc.url, "git-upload-pack", tc.protocol)
		if err != nil || strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("sshArgv(%q) = %q, %v\nwant %q", tc.url, got, err, tc.want)
		}
	}
}

// --- create-branch and the working set (docs/design/git-broker-plan.md task 9) ---

const fixtureRepo = "fixture.test/o/r"

// createBranchGrant is the workspace's approved `create-branch` grant on the fixture's
// repository.
var createBranchGrant = []brokerscope.Grant{{Kind: "create-branch", Repo: fixtureRepo}}

// allowKinds writes the user's own config listing kinds under brokered.git.grant_kinds beside
// the read grant every fixture holds; none writes a config without the key.
func allowKinds(t *testing.T, kinds ...string) {
	t.Helper()
	writeUserConfig(t, userConfigBody(false, kinds...))
}

// grantedFixture is the SSH fixture with the grant approved and the kind allowed.
func grantedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixtureOpts(t, fixtureOpts{grants: createBranchGrant})
	allowKinds(t, "create-branch")
	return f
}

// workingStore is the git source's store, as `yolo approve` opens it.
func workingStore() *brokerstore.Store {
	return &brokerstore.Store{Dir: paths.BrokerSourceDir(Source), Source: Source}
}

// liveEntries is the store's live working-set entries.
func liveEntries(t *testing.T) []brokerstore.WorkingEntry {
	t.Helper()
	all, err := workingStore().Working()
	if err != nil {
		t.Fatal(err)
	}
	var live []brokerstore.WorkingEntry
	for _, e := range all {
		if e.Live() {
			live = append(live, e)
		}
	}
	return live
}

// lastPush is the newest receive-pack audit event.
func lastPush(t *testing.T) brokeraudit.Event {
	t.Helper()
	var push brokeraudit.Event
	for _, e := range auditEvents(t) {
		if len(e.Argv) == 1 && e.Argv[0] == serviceReceivePack {
			push = e
		}
	}
	return push
}

// commit adds a commit on the workspace's HEAD.
func (f *fixture) commit(name string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.ws, name), []byte(name+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	mustGit(f.t, f.ws, "add", name)
	mustGit(f.t, f.ws, "commit", "-q", "-m", name)
}

func (f *fixture) mustPush(args ...string) string {
	f.t.Helper()
	out, err := f.git(append([]string{"push"}, args...)...)
	if err != nil {
		f.t.Fatalf("git push %v: %v\n%s\nbroker log:\n%s", args, err, out, f.log.String())
	}
	return out
}

func (f *fixture) upstreamRef(ref string) string {
	f.t.Helper()
	out, err := run(f.t, f.bare, gitEnv(), "rev-parse", "--verify", "-q", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func TestCreateBranchUnderGrantJoinsWorkingSet(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	if got, want := f.upstreamRef("refs/heads/agent/x"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream agent/x %q, local HEAD %q", got, want)
	}
	live := liveEntries(t)
	if len(live) != 1 {
		t.Fatalf("live entries %+v, want one", live)
	}
	e := live[0]
	if e.Workspace != f.ws || e.Repo != fixtureRepo || e.Resource != "refs/heads/agent/x" || e.By != "create-branch" ||
		e.Kind != brokerstore.WorkingBranch {
		t.Fatalf("entry %+v", e)
	}
	if p := lastPush(t); p.Set != "create-branch" || len(p.Refs) != 1 || p.Refs[0].CoveredBy != "create-branch" {
		t.Fatalf("creation audit %+v", p)
	}

	// A second push is an update the working set covers.
	f.commit("two")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	if p := lastPush(t); p.Set != "working-set" || len(p.Refs) != 1 || p.Refs[0].CoveredBy != e.ID {
		t.Fatalf("update audit %+v, want covered_by %s", p, e.ID)
	}
	// A force push is an update too, in this step.
	mustGit(t, f.ws, "commit", "-q", "--amend", "-m", "two, amended")
	f.mustPush("--force", "origin", "HEAD:refs/heads/agent/x")
	if got, want := f.upstreamRef("refs/heads/agent/x"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after the force push, upstream agent/x %q, local HEAD %q", got, want)
	}
	if p := lastPush(t); p.Set != "working-set" || p.Refs[0].CoveredBy != e.ID {
		t.Fatalf("force-push audit %+v", p)
	}
	if n := len(liveEntries(t)); n != 1 {
		t.Fatalf("%d live entries after updates, want 1", n)
	}
}

func TestWorkingSetSurvivesANewBroker(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	first := f.broker.start

	// A new launch of the same workspace: a new scope file, a new broker with a new start id,
	// and no grant this time, so only the working set can cover the push.
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	sf.LaunchID, sf.Grants, sf.BrokerStart, sf.BrokerPID, sf.LiveScope = "fixture-launch-2", []brokerscope.Grant{readGrant}, "", 0, false
	f.launch(sf, nil)
	if f.broker.start == first {
		t.Fatal("the new broker kept the old start id")
	}
	f.commit("two")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	if got, want := f.upstreamRef("refs/heads/agent/x"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream agent/x %q, local HEAD %q", got, want)
	}
}

func TestNoGrantPushesNothing(t *testing.T) {
	f := newFixture(t, "")
	allowKinds(t, "create-branch") // the kind alone, with no grant in the scope
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:refs/heads/agent/x")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("an ungranted creation: %v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/agent/x") != "" {
		t.Fatal("the creation reached the upstream")
	}
	if all, err := workingStore().Working(); err != nil || len(all) != 0 {
		t.Fatalf("store %+v %v, want empty", all, err)
	}
}

func TestUserKindRemovedTakesEffectAtNextPush(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	allowKinds(t) // the user takes the kind away on the host
	out, err := f.git("push", "origin", "HEAD:refs/heads/agent/y")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a creation after the kind was removed: %v\n%s", err, out)
	}
	// The refusal names the verb that would let the grant work, behind the conversion a
	// legacy JSONC user config needs first.
	convertThenAllow(t, out, grantCreateBranch)
	if f.upstreamRef("refs/heads/agent/y") != "" {
		t.Fatal("agent/y reached the upstream")
	}
}

func TestRejectedCreationRecordsNothing(t *testing.T) {
	f := grantedFixture(t)
	writeExec(t, filepath.Join(f.bare, "hooks", "pre-receive"), "#!/bin/sh\necho 'fixture: rejected' >&2\nexit 1\n")
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:refs/heads/agent/x")
	if err == nil {
		t.Fatalf("a push the hook rejects succeeded:\n%s", out)
	}
	if !strings.Contains(out, "pre-receive hook declined") {
		t.Fatalf("the upstream's own report did not reach git:\n%s", out)
	}
	if all, err := workingStore().Working(); err != nil || len(all) != 0 {
		t.Fatalf("store %+v %v, want empty", all, err)
	}
}

func TestDeleteEndsEntry(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	e := liveEntries(t)[0]
	f.mustPush("origin", ":refs/heads/agent/x")
	if f.upstreamRef("refs/heads/agent/x") != "" {
		t.Fatal("agent/x survived its delete")
	}
	if p := lastPush(t); p.Set != "working-set" || p.Refs[0].CoveredBy != e.ID {
		t.Fatalf("delete audit %+v", p)
	}
	all, _ := workingStore().Working()
	if len(all) != 1 || all[0].ID != e.ID || all[0].EndReason != brokerstore.EndDeleted {
		t.Fatalf("entries after the delete %+v", all)
	}
	// Deleted, the branch is no longer the workspace's: an update to it needs approval again.
	if _, err := run(t, f.ws, gitEnv(), "push", "-q", f.bare, "HEAD~1:refs/heads/agent/x"); err != nil {
		t.Fatal(err)
	}
	if out, err := f.git("push", "origin", "HEAD:refs/heads/agent/x"); err == nil {
		t.Fatalf("an update to a deleted entry's branch succeeded:\n%s", out)
	}
}

func TestGoneUpstreamEndsEntry(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	old := liveEntries(t)[0]
	mustGit(t, f.bare, "update-ref", "-d", "refs/heads/agent/x")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	all, _ := workingStore().Working()
	var ended, live []brokerstore.WorkingEntry
	for _, e := range all {
		if e.Live() {
			live = append(live, e)
		} else {
			ended = append(ended, e)
		}
	}
	if len(ended) != 1 || ended[0].ID != old.ID || ended[0].EndReason != brokerstore.EndGone {
		t.Fatalf("ended entries %+v", ended)
	}
	if len(live) != 1 || live[0].ID == old.ID || live[0].Resource != "refs/heads/agent/x" {
		t.Fatalf("live entries %+v", live)
	}
	if p := lastPush(t); p.Set != "create-branch" || p.Refs[0].CoveredBy != "create-branch" {
		t.Fatalf("re-creation audit %+v", p)
	}
}

func TestMainStillRefused(t *testing.T) {
	f := grantedFixture(t)
	before := f.upstreamRef("refs/heads/main")
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, "! [remote rejected]") || !strings.Contains(out, "("+reasonNeedsApprovalPrefix+"r-") {
		t.Fatalf("a push to main under create-branch: %v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved")
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("%d entries after a refused push", n)
	}
}

// The broker re-reads the live scope file at every push: a grant withdrawn between two
// creations (as `yolo config apply` withdraws it) refuses the second.
func TestGrantWithdrawnBetweenCreationsRefusesTheSecond(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos, []brokerscope.Grant{readGrant}); err != nil {
		t.Fatal(err)
	}
	out, err := f.git("push", "origin", "HEAD:refs/heads/agent/y")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a creation after the grant was withdrawn: %v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/agent/y") != "" {
		t.Fatal("agent/y reached the upstream")
	}
}

// A create-branch grant covers branches only: a tag creation under it is refused.
func TestTagCreationUnderGrantRefused(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:refs/tags/v1")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a tag creation under create-branch: %v\n%s", err, out)
	}
	if f.upstreamRef("refs/tags/v1") != "" {
		t.Fatal("the tag reached the upstream")
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("%d entries after a refused tag", n)
	}
}

// --- a working-set branch declared in config (docs/design/git-broker.md GB-D12, GB-D13) ---

// configBranchFixture is the SSH fixture with a `branch` grant on the fixture's repository for
// branch approved, and the kind allowed in the user config.
func configBranchFixture(t *testing.T, branch string) *fixture {
	t.Helper()
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{{Kind: "branch", Repo: fixtureRepo, Branch: branch}}})
	allowKinds(t, "branch")
	return f
}

// An update of main declared in config lands, is audited as the working set's, covered by the
// grant, and records nothing in the store.
func TestConfigBranchUpdatesMain(t *testing.T) {
	f := configBranchFixture(t, "main")
	f.commit("one")
	f.mustPush("origin", "HEAD:main")
	if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream main %q, local HEAD %q", got, want)
	}
	if p := lastPush(t); p.Set != "working-set" || p.Outcome != "ran" || len(p.Refs) != 1 || p.Refs[0].CoveredBy != "branch:main" {
		t.Fatalf("update audit %+v", p)
	}
	if all, err := workingStore().Working(); err != nil || len(all) != 0 {
		t.Fatalf("store %+v %v, want empty: a branch declared in config is not recorded", all, err)
	}
}

// A force update of the declared branch lands, as for any working-set branch; the remote's own
// protection is the backstop.
func TestConfigBranchForceUpdate(t *testing.T) {
	f := configBranchFixture(t, "main")
	f.commit("one")
	f.mustPush("origin", "HEAD:main")
	mustGit(t, f.ws, "commit", "-q", "--amend", "-m", "one, amended")
	f.mustPush("--force", "origin", "HEAD:main")
	if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after the force push, upstream main %q, local HEAD %q", got, want)
	}
	if p := lastPush(t); p.Set != "working-set" || p.Refs[0].CoveredBy != "branch:main" {
		t.Fatalf("force-push audit %+v", p)
	}
}

// A delete of the declared branch lands, as for any working-set branch, and does not end the
// declaration: the config still names the branch, so creating it again lands too.
func TestConfigBranchDeleteAndRecreate(t *testing.T) {
	f := configBranchFixture(t, "other")
	if _, err := run(t, f.ws, gitEnv(), "push", "-q", f.bare, "HEAD:refs/heads/other"); err != nil {
		t.Fatal(err)
	}
	f.mustPush("origin", ":refs/heads/other")
	if f.upstreamRef("refs/heads/other") != "" {
		t.Fatal("other survived its delete")
	}
	if p := lastPush(t); p.Set != "working-set" || p.Refs[0].CoveredBy != "branch:other" {
		t.Fatalf("delete audit %+v", p)
	}
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/other")
	if f.upstreamRef("refs/heads/other") != mustGit(t, f.ws, "rev-parse", "HEAD") {
		t.Fatal("re-creating the declared branch did not land")
	}
	if p := lastPush(t); p.Set != "working-set" || p.Refs[0].CoveredBy != "branch:other" {
		t.Fatalf("re-creation audit %+v", p)
	}
	if all, err := workingStore().Working(); err != nil || len(all) != 0 {
		t.Fatalf("store %+v %v, want empty", all, err)
	}
}

// `branch: "main"` declares main and no other branch, and no tag of the same name.
func TestConfigBranchCoversOnlyItsBranch(t *testing.T) {
	f := configBranchFixture(t, "main")
	f.commit("one")
	for _, ref := range []string{"refs/heads/other", "refs/tags/main"} {
		out, err := f.git("push", "origin", "HEAD:"+ref)
		if err == nil || !strings.Contains(out, reasonNeedsApproval) {
			t.Fatalf("a push to %s under a branch grant on main: %v\n%s", ref, err, out)
		}
		if f.upstreamRef(ref) != "" {
			t.Fatalf("%s reached the upstream", ref)
		}
	}
}

// A branch grant names its repository: one on another repository declares nothing here, so a
// push to the same branch name of this repository is refused.
func TestConfigBranchOnAnotherRepoRefuses(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{{Kind: "branch", Repo: "other.test/o/r", Branch: "main"}}})
	allowKinds(t, "branch")
	f.commit("one")
	before := f.upstreamRef("refs/heads/main")
	out, err := f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a push to %s's main under a branch grant on other.test/o/r: %v\n%s", fixtureRepo, err, out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved")
	}
}

// A branch declared in config whose kind the user config no longer lists says so only when the
// push is refused: a creation a create-branch grant covers lands with no "not in effect" line.
func TestConfigBranchKindMissingSilentWhenCreateBranchCovers(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{
		{Kind: "branch", Repo: fixtureRepo, Branch: "feature"}, {Kind: "create-branch", Repo: fixtureRepo}}})
	allowKinds(t, "create-branch")
	f.commit("one")
	out := f.mustPush("origin", "HEAD:refs/heads/feature")
	if strings.Contains(out, "not in effect") {
		t.Fatalf("a push create-branch covered printed a refusal line:\n%s", out)
	}
	if p := lastPush(t); p.Set != "create-branch" || p.Refs[0].CoveredBy != "create-branch" {
		t.Fatalf("creation audit %+v", p)
	}
}

// The broker reads the user config's grant_kinds at every push: the kind taken away on the host
// refuses the next push, naming the verb that allows it again.
func TestConfigBranchKindRemovedRefusesNextPush(t *testing.T) {
	f := configBranchFixture(t, "main")
	f.commit("one")
	f.mustPush("origin", "HEAD:main")
	before := f.upstreamRef("refs/heads/main")
	allowKinds(t, "create-branch")
	f.commit("two")
	out, err := f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a push after the kind was removed: %v\n%s", err, out)
	}
	convertThenAllow(t, out, grantBranch)
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved")
	}
}

// The broker reads the live scope file at every push: the grant withdrawn (as `yolo config
// apply` withdraws it) takes the branch out of the working set at the next push.
func TestConfigBranchGrantRemovedRefusesNextPush(t *testing.T) {
	f := configBranchFixture(t, "main")
	f.commit("one")
	f.mustPush("origin", "HEAD:main")
	before := f.upstreamRef("refs/heads/main")
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos, []brokerscope.Grant{readGrant}); err != nil {
		t.Fatal(err)
	}
	f.commit("two")
	out, err := f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a push after the grant was withdrawn: %v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved")
	}
}
