package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestGitHubApprovalBrokerHelper runs the production github-broker Main in a child process.
func TestGitHubApprovalBrokerHelper(t *testing.T) {
	if os.Getenv("YOLO_GH_APPROVAL_BROKER_HELPER") != "1" {
		t.Skip("run as the GitHub broker helper only")
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(ghbroker.Main(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) Contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Contains(b.b.String(), s)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type githubApprovalBroker struct {
	cmd      *exec.Cmd
	done     chan struct{}
	endpoint string
	stop     chan struct{}
	logs     lockedBuffer
}

func startGitHubApprovalBroker(t *testing.T, home, workspace, fakeGH, ghConfig, sfPath, userConfig string) *githubApprovalBroker {
	t.Helper()
	if userConfig != "" {
		configDir := filepath.Join(home, ".config", "yolo-jail")
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "config.jsonc"), []byte(userConfig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sockDir, err := os.MkdirTemp("/tmp", "gh-approval-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "broker.sock")
	tmpDir := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := &githubApprovalBroker{done: make(chan struct{}), stop: make(chan struct{})}
	p.cmd = exec.Command(os.Args[0], "-test.run=^TestGitHubApprovalBrokerHelper$", "-test.count=1", "--",
		"--socket", socket, "--scope-file", sfPath)
	p.cmd.Dir = workspace
	p.cmd.Env = append(os.Environ(), "YOLO_GH_APPROVAL_BROKER_HELPER=1", "YOLO_VERSION=", "YOLO_USER_LAYER=",
		"HOME="+home, "TMPDIR="+tmpDir, "GH_CONFIG_DIR="+ghConfig, "PATH="+filepath.Dir(fakeGH)+":/usr/bin:/bin",
		svcendpoint.AdvertiseHostEnv+"=127.0.0.1")
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	p.cmd.Stdout, p.cmd.Stderr = &p.logs, &p.logs
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			<-p.done
		}
		close(p.stop)
		if t.Failed() {
			t.Logf("github-broker output:\n%s", p.logs.String())
		}
	})
	waitGitHubApproval(t, "the production daemon socket", 10*time.Second, func() bool {
		_, err := os.Stat(socket)
		return err == nil
	})
	endpointRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	endpointDir := filepath.Join(endpointRoot, paths.JailShortHash("approval-fixture"))
	if err := os.MkdirAll(endpointDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(endpointDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p.endpoint = filepath.Join(endpointDir, "github-broker.endpoint")
	go func() { _ = svcendpoint.ServeFront(p.endpoint, "127.0.0.1", socket, p.stop) }()
	waitGitHubApproval(t, "the production service front", 10*time.Second, func() bool {
		return svcendpoint.Probe(p.endpoint)
	})
	return p
}

func waitGitHubApproval(t *testing.T, what string, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *githubApprovalBroker) restart(t *testing.T) {
	t.Helper()
	old := p.cmd
	if err := syscall.Kill(-old.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-old.Process.Pid, syscall.SIGKILL)
		<-p.done
		t.Fatal("the production broker did not drain before restart")
	}
	cmd := exec.Command(old.Args[0], old.Args[1:]...)
	cmd.Dir, cmd.Env = old.Dir, old.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = &p.logs, &p.logs
	p.cmd, p.done = cmd, make(chan struct{})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(p.done) }()
	// The front is this process's and never stopped, so its probe passes at once; what has to be
	// up is the restarted daemon behind it, which says so in its second "listening on" line.
	var socket string
	for i, arg := range old.Args {
		if arg == "--socket" && i+1 < len(old.Args) {
			socket = old.Args[i+1]
		}
	}
	waitGitHubApproval(t, "the restarted production broker socket", 10*time.Second, func() bool {
		return strings.Count(p.logs.String(), "listening on "+socket+" (") >= 2
	})
}

type productionGitHubFixture struct {
	broker         *githubApprovalBroker
	store          *brokerstore.Store
	calls          string
	stdin          string
	hookLog        string
	hookSummaryLog string
	home           string
	workspace      string
	fakeGH         string
	ghConfig       string
	scopeFile      string
	userConfig     string
}

func newProductionGitHubFixture(t *testing.T, userConfig string, withHook bool) *productionGitHubFixture {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeDir := filepath.Join(root, "fake-gh")
	if err := os.MkdirAll(fakeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(fakeDir, "calls")
	stdinReceipt := filepath.Join(fakeDir, "stdin")
	fakeGH := filepath.Join(fakeDir, "gh")
	script := "#!/bin/sh\ncase \"$1\" in\n  --version) echo 'gh version 2.101.0 (fake)' ;;\n  auth) echo 'fake-token-1234567890' ;;\n  *) printf '%s\\000' \"$@\" >> " + shquote.Quote(calls) +
		"; printf '\\000' >> " + shquote.Quote(calls) + "; cat > " + shquote.Quote(stdinReceipt) +
		"; echo 'fake GitHub receipt' ;;\nesac\n"
	if err := os.WriteFile(fakeGH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ghConfig := filepath.Join(root, "gh-config")
	if err := os.MkdirAll(ghConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	hookLog := filepath.Join(fakeDir, "requests.log")
	hookSummaryLog := filepath.Join(fakeDir, "request-summaries.log")
	if withHook {
		hookPath := filepath.Join(fakeDir, "on-request")
		hookScript := fmt.Sprintf("#!/bin/sh\ncat > %s\nprintf '%%s\\n' \"$YOLO_REQUEST_SUMMARY\" >> %s\necho approve\n", shquote.Quote(hookLog), shquote.Quote(hookSummaryLog))
		if err := os.WriteFile(hookPath, []byte(hookScript), 0o755); err != nil {
			t.Fatal(err)
		}
		var config map[string]json.RawMessage
		if userConfig == "" {
			config = make(map[string]json.RawMessage)
		} else if err := json.Unmarshal([]byte(userConfig), &config); err != nil {
			t.Fatal(err)
		}
		hookCommand, err := json.Marshal(shquote.Quote(hookPath))
		if err != nil {
			t.Fatal(err)
		}
		config["on_request"] = hookCommand
		encodedConfig, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		userConfig = string(encodedConfig)
	}
	scopeFile, err := brokerscope.Write(brokerscope.File{Source: ghbroker.Source, LaunchID: "cafe", PID: os.Getpid(),
		Container: "approval-fixture", Workspace: workspace, Repos: []string{"o/r"},
		Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}}})
	if err != nil {
		t.Fatal(err)
	}
	return &productionGitHubFixture{
		broker:         startGitHubApprovalBroker(t, home, workspace, fakeGH, ghConfig, scopeFile, userConfig),
		store:          &brokerstore.Store{Dir: paths.BrokerSourceDir(ghbroker.Source), Source: ghbroker.Source},
		calls:          calls,
		stdin:          stdinReceipt,
		hookLog:        hookLog,
		hookSummaryLog: hookSummaryLog,
		home:           home,
		workspace:      workspace,
		fakeGH:         fakeGH,
		ghConfig:       ghConfig,
		scopeFile:      scopeFile,
		userConfig:     userConfig,
	}
}

func (f *productionGitHubFixture) forward(argv ...string) (int, string, string) {
	return f.forwardInput("", argv...)
}

func (f *productionGitHubFixture) forwardInput(stdin string, argv ...string) (int, string, string) {
	var out, stderr bytes.Buffer
	code := ghbroker.Forward(argv, ghbroker.ForwardEnv{
		Getenv: func(key string) string {
			if key == ghbroker.EndpointEnv {
				return f.broker.endpoint
			}
			return os.Getenv(key)
		},
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &stderr,
		OriginRepo: func() string { return "o/r" },
	})
	return code, out.String(), stderr.String()
}

func stdinSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func requireProductionSpendAudit(t *testing.T, request brokerstore.Request, grant brokerstore.Grant, stdin []byte) {
	t.Helper()
	if len(stdin) == 0 || request.StdinDigest != stdinSHA256(stdin) {
		t.Fatalf("spend request does not bind the nonempty submitted stdin: request=%+v stdin_sha256=%s", request, stdinSHA256(stdin))
	}
	events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal(err)
	}
	var decisions, spends, calls int
	for _, event := range events {
		if event.Service != ghbroker.Source || event.Request != request.ID || event.Grant != grant.ID ||
			event.CommandDigest != request.Digest || event.StdinDigest != request.StdinDigest || !slices.Equal(event.Argv, request.Argv) {
			continue
		}
		switch event.Event {
		case "decision":
			if event.Outcome == "allowed" {
				decisions++
			}
		case "grant-used":
			if event.Outcome == "spent-before-dispatch" {
				spends++
			}
		case "call":
			if event.Outcome == "ran" && event.Stdin != nil && event.Stdin.Bytes == len(stdin) && event.Stdin.SHA256 == request.StdinDigest {
				calls++
			}
		}
	}
	if decisions != 1 || spends != 1 || calls != 1 {
		t.Fatalf("audit must link one allowed decision, required spend and final stdin-identified dispatch: decisions=%d spends=%d calls=%d request=%s grant=%s digest=%s stdin=%s events=%+v", decisions, spends, calls, request.ID, grant.ID, request.Digest, request.StdinDigest, events)
	}
}

func requireProductionDeniedRequestAudit(t *testing.T, request brokerstore.Request, stdin []byte) {
	t.Helper()
	if len(stdin) == 0 || request.StdinDigest != stdinSHA256(stdin) {
		t.Fatalf("denied request does not bind its nonempty submitted stdin: request=%+v stdin_sha256=%s", request, stdinSHA256(stdin))
	}
	events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal(err)
	}
	var filed, denied, refused int
	for _, event := range events {
		if event.Service != ghbroker.Source || event.Request != request.ID || event.CommandDigest != request.Digest ||
			event.StdinDigest != request.StdinDigest || !slices.Equal(event.Argv, request.Argv) {
			continue
		}
		switch event.Event {
		case "request":
			if event.Outcome == "pending" && event.Grant == "" {
				filed++
			}
		case "decision":
			if event.Outcome == "denied" && event.Grant == "" {
				denied++
			}
		case "call":
			if event.Outcome == "refused" && event.Grant == "" && event.Stdin != nil &&
				event.Stdin.Bytes == len(stdin) && event.Stdin.SHA256 == request.StdinDigest {
				refused++
			}
		}
	}
	if filed != 1 || denied != 1 || refused != 1 {
		t.Fatalf("audit must link one exact filed, denied and refused action: filed=%d denied=%d refused=%d request=%s digest=%s stdin=%s events=%+v", filed, denied, refused, request.ID, request.Digest, request.StdinDigest, events)
	}
}

func seedUnusedProductionGrant(t *testing.T, f *productionGitHubFixture, body, seedTarget, grantTarget string) brokerstore.Request {
	t.Helper()
	finished := make(chan productionForwardResult, 1)
	go func() {
		code, out, stderr := f.forwardInput(body, "pr", "comment", seedTarget, "--repo", "o/r", "--body-file", "-")
		finished <- productionForwardResult{code: code, out: out, stderr: stderr}
	}()
	var seed brokerstore.Request
	waitGitHubApproval(t, "the production scope-seed request", 3*time.Second, func() bool {
		requests, _, err := f.store.List()
		if err != nil {
			return false
		}
		for _, q := range requests {
			if q.State == "pending" && len(q.Argv) == 5 && q.Argv[4] == seedTarget {
				seed = q
				return true
			}
		}
		select {
		case got := <-finished:
			t.Fatalf("scope-seed caller returned before request filing: %d %q", got.code, got.stderr)
		default:
		}
		return false
	})
	if rc := Main([]string{"yolo", "approve", seed.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying scope-seed request returned %d", rc)
	}
	select {
	case got := <-finished:
		if got.code != ghbroker.ExitNoPerm {
			t.Fatalf("denied scope-seed caller returned %d: %q", got.code, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("denied scope-seed caller did not return")
	}
	seed.Argv[len(seed.Argv)-1] = grantTarget
	request, err := f.store.File(seed)
	if err != nil {
		t.Fatal(err)
	}
	if rc := Main([]string{"yolo", "approve", request.ID}); rc != 0 {
		t.Fatalf("allowing exact scope-seed request returned %d", rc)
	}
	return request
}

func TestProductionGitHubHookSummariesDistinguishStdinBodies(t *testing.T) {
	fixture := newProductionGitHubFixture(t, "", true)
	for _, body := range []string{"first private body", "second private body"} {
		type result struct {
			code        int
			out, stderr string
		}
		finished := make(chan result, 1)
		go func(body string) {
			code, out, stderr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
			finished <- result{code: code, out: out, stderr: stderr}
		}(body)
		var request brokerstore.Request
		waitGitHubApproval(t, "the pending body-bound production request", 3*time.Second, func() bool {
			requests, _, err := fixture.store.List()
			if err != nil {
				return false
			}
			for _, q := range requests {
				if q.State == "pending" && len(q.Argv) > 0 && q.Argv[0] == "pr" && q.StdinDigest != "" {
					request = q
					return true
				}
			}
			return false
		})
		wantStdin := sha256.Sum256([]byte(body))
		if request.StdinDigest != hex.EncodeToString(wantStdin[:]) {
			t.Fatalf("filed request does not bind submitted stdin bytes: %+v", request)
		}
		waitGitHubApproval(t, "the actual on_request summary", 3*time.Second, func() bool {
			data, err := os.ReadFile(fixture.hookSummaryLog)
			return err == nil && strings.Contains(string(data), request.ID)
		})
		summaries, err := os.ReadFile(fixture.hookSummaryLog)
		if err != nil || !strings.Contains(string(summaries), "command SHA-256 "+request.Digest) ||
			!strings.Contains(string(summaries), "stdin SHA-256 "+request.StdinDigest) || strings.Contains(string(summaries), body) {
			t.Fatalf("production hook summary omits identity or exposes body: %q, %v", summaries, err)
		}
		if rc := Main([]string{"yolo", "approve", request.ID, "--deny"}); rc != 0 {
			t.Fatalf("host yolo approve --deny exited %d", rc)
		}
		select {
		case got := <-finished:
			if got.code != ghbroker.ExitNoPerm {
				t.Fatalf("denied write returned %d, stdout %q, stderr %q", got.code, got.out, got.stderr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the denied production request did not return")
		}
		if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("denied production write reached fake gh: %v", err)
		}
	}
	requests, _, err := fixture.store.List()
	if err != nil || len(requests) != 2 || requests[0].StdinDigest == requests[1].StdinDigest {
		t.Fatalf("different body requests were not distinct: %+v err=%v", requests, err)
	}
}

func TestProductionUnusedExactGrantSurvivesBodyMismatch(t *testing.T) {
	fixture := newProductionGitHubFixture(t, "", false)
	approvedBody := "approved exact body\n"
	startForward := func(body, target string) <-chan struct {
		code        int
		out, stderr string
	} {
		finished := make(chan struct {
			code        int
			out, stderr string
		}, 1)
		go func() {
			callArgv := []string{"pr", "comment", target, "--repo", "o/r", "--body-file", "-"}
			code, out, stderr := fixture.forwardInput(body, callArgv...)
			finished <- struct {
				code        int
				out, stderr string
			}{code, out, stderr}
		}()
		return finished
	}
	pending := func(finished <-chan struct {
		code        int
		out, stderr string
	}, target string) brokerstore.Request {
		t.Helper()
		var request brokerstore.Request
		waitGitHubApproval(t, "an exact-action pending request", 3*time.Second, func() bool {
			requests, _, err := fixture.store.List()
			if err != nil {
				return false
			}
			for _, q := range requests {
				if q.State == "pending" && len(q.Argv) == 5 && q.Argv[0] == "pr" && q.Argv[1] == "comment" && q.Argv[4] == target {
					request = q
					return true
				}
			}
			select {
			case got := <-finished:
				t.Fatalf("production forward returned before request became pending: %d %q", got.code, got.stderr)
			default:
			}
			return false
		})
		return request
	}
	finishDenied := func(finished <-chan struct {
		code        int
		out, stderr string
	}) {
		t.Helper()
		select {
		case got := <-finished:
			if got.code != ghbroker.ExitNoPerm {
				t.Fatalf("denied caller returned %d: %q %q", got.code, got.out, got.stderr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("denied production caller did not return")
		}
	}

	first := startForward(approvedBody, "40")
	original := pending(first, "40")
	if rc := Main([]string{"yolo", "approve", original.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying the seed request returned %d", rc)
	}
	finishDenied(first)

	// Re-file the observed production scope with a new, not previously denied exact target;
	// host CLI approval seeds one unused grant, and each subsequent action traverses Forward.
	original.Argv[len(original.Argv)-1] = "41"
	grantRequest, err := fixture.store.File(original)
	if err != nil {
		t.Fatal(err)
	}
	if rc := Main([]string{"yolo", "approve", grantRequest.ID}); rc != 0 {
		t.Fatalf("approving the unused exact request returned %d", rc)
	}
	_, grants, err := fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Digest != grantRequest.Digest || grants[0].Uses != 1 || !grants[0].Ended.IsZero() {
		t.Fatalf("expected one unused exact grant before mutation: %+v err=%v", grants, err)
	}

	mutation := startForward("mutated private body\n", "41")
	changed := pending(mutation, "41")
	if changed.Digest == grantRequest.Digest || changed.StdinDigest == grantRequest.StdinDigest {
		t.Fatalf("body mutation did not change the production action identity: old=%+v new=%+v", grantRequest, changed)
	}
	_, grants, err = fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 || !grants[0].Ended.IsZero() {
		t.Fatalf("mismatched action spent or ended the unused authority: %+v err=%v", grants, err)
	}
	if rc := Main([]string{"yolo", "approve", changed.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying the mutated request returned %d", rc)
	}
	finishDenied(mutation)
	if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mutated body reached fake gh: %v", err)
	}

	targetMutation := startForward(approvedBody, "42")
	changedTarget := pending(targetMutation, "42")
	if changedTarget.Digest == grantRequest.Digest || changedTarget.Argv[len(changedTarget.Argv)-1] == grantRequest.Argv[len(grantRequest.Argv)-1] {
		t.Fatalf("target mutation did not change the production action identity: old=%+v new=%+v", grantRequest, changedTarget)
	}
	_, grants, err = fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 || !grants[0].Ended.IsZero() {
		t.Fatalf("target mismatch spent or ended the unused authority: %+v err=%v", grants, err)
	}
	if rc := Main([]string{"yolo", "approve", changedTarget.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying the changed target request returned %d", rc)
	}
	finishDenied(targetMutation)
	if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mutated target reached fake gh: %v", err)
	}

	approved := startForward(approvedBody, "41")
	select {
	case got := <-approved:
		if got.code != 0 || !strings.Contains(got.out, "fake GitHub receipt") {
			t.Fatalf("the subsequent exact action was not authorized once: %d %q %q", got.code, got.out, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the exact approved action did not finish")
	}
	wantReceipt := append([]byte(strings.Join(grantRequest.Argv, "\x00")), 0, 0)
	gotReceipt, err := os.ReadFile(fixture.calls)
	if err != nil || !bytes.Equal(gotReceipt, wantReceipt) {
		t.Fatalf("exact action receipt differs or dispatched more than once: got %q want %q err=%v", gotReceipt, wantReceipt, err)
	}
	gotStdin, err := os.ReadFile(fixture.stdin)
	if err != nil || !bytes.Equal(gotStdin, []byte(approvedBody)) || stdinSHA256(gotStdin) != grantRequest.StdinDigest {
		t.Fatalf("exact action stdin differs from approved bytes and digest: got %q digest=%s want=%q digest=%s err=%v", gotStdin, stdinSHA256(gotStdin), approvedBody, grantRequest.StdinDigest, err)
	}
	_, grants, err = fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 0 || grants[0].EndedBy() != brokerstore.EndUsed {
		t.Fatalf("exact authority was not spent exactly once: %+v err=%v", grants, err)
	}
	requireProductionSpendAudit(t, grantRequest, grants[0], []byte(approvedBody))
}

func TestProductionConcurrentForwardersSpendOneExactGrantOnce(t *testing.T) {
	fixture := newProductionGitHubFixture(t, "", false)
	body := "concurrent exact body\n"
	request := seedUnusedProductionGrant(t, fixture, body, "40", "41")
	start := make(chan struct{})
	results := make(chan productionForwardResult, 2)
	for range 2 {
		go func() {
			<-start
			code, out, stderr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
			results <- productionForwardResult{code: code, out: out, stderr: stderr}
		}()
	}
	close(start)
	var loser brokerstore.Request
	var successCount int
	var done []productionForwardResult
	waitGitHubApproval(t, "one live loser request and one completed exact dispatch", 5*time.Second, func() bool {
		requests, _, err := fixture.store.List()
		if err == nil {
			for _, q := range requests {
				if q.State == "pending" && q.Digest == request.Digest && q.ID != request.ID {
					loser = q
				}
			}
		}
	drain:
		for len(done) < 2 {
			select {
			case got := <-results:
				done = append(done, got)
				if got.code == 0 {
					successCount++
				}
			default:
				break drain
			}
		}
		return loser.ID != "" && successCount == 1
	})
	if loser.ID == "" {
		t.Fatal("the losing contender did not wait for new consent")
	}
	if loser.Digest != request.Digest || loser.StdinDigest != stdinSHA256([]byte(body)) || !slices.Equal(loser.Argv, request.Argv) {
		t.Fatalf("losing production request does not bind the exact attempted action: winner=%+v loser=%+v", request, loser)
	}
	if rc := Main([]string{"yolo", "approve", loser.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying concurrent loser returned %d", rc)
	}
	for len(done) < 2 {
		select {
		case got := <-results:
			done = append(done, got)
			if got.code == 0 {
				successCount++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("both real forwarders did not return after denying the loser")
		}
	}
	if successCount != 1 {
		t.Fatalf("concurrent contenders returned %d successful authorizations: %+v", successCount, done)
	}
	denials := 0
	for _, got := range done {
		if got.code == ghbroker.ExitNoPerm {
			denials++
		}
	}
	if denials != 1 {
		t.Fatalf("concurrent loser was not denied/refused cleanly: %+v", done)
	}
	wantReceipt := append([]byte(strings.Join(request.Argv, "\x00")), 0, 0)
	gotReceipt, err := os.ReadFile(fixture.calls)
	if err != nil || !bytes.Equal(gotReceipt, wantReceipt) {
		t.Fatalf("contenders dispatched other than exactly one invocation: %q want %q err=%v", gotReceipt, wantReceipt, err)
	}
	gotStdin, err := os.ReadFile(fixture.stdin)
	if err != nil || !bytes.Equal(gotStdin, []byte(body)) || stdinSHA256(gotStdin) != request.StdinDigest {
		t.Fatalf("concurrent winner received different stdin bytes or digest: got=%q digest=%s want=%q digest=%s err=%v", gotStdin, stdinSHA256(gotStdin), body, request.StdinDigest, err)
	}
	_, grants, err := fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 0 || grants[0].EndedBy() != brokerstore.EndUsed {
		t.Fatalf("concurrent callers did not spend the one-use grant exactly once: %+v err=%v", grants, err)
	}
	requireProductionSpendAudit(t, request, grants[0], []byte(body))
	requireProductionDeniedRequestAudit(t, loser, []byte(body))
}

func TestProductionExactGrantSurvivesBrokerRestartAndScopeFences(t *testing.T) {
	fixture := newProductionGitHubFixture(t, "", false)
	body := "restart-bound exact body\n"
	grantRequest := seedUnusedProductionGrant(t, fixture, body, "40", "41")
	_, grants, err := fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 || !grants[0].Ended.IsZero() {
		t.Fatalf("expected an unused exact grant before restart: %+v err=%v", grants, err)
	}
	fixture.broker.restart(t)

	otherRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherWorkspace := filepath.Join(otherRoot, "workspace")
	if err := os.MkdirAll(otherWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	otherScope, err := brokerscope.Write(brokerscope.File{Source: ghbroker.Source, LaunchID: "other-launch", PID: os.Getpid(),
		Container: "other-approval-fixture", Workspace: otherWorkspace, Repos: []string{"o/r"},
		Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}}})
	if err != nil {
		t.Fatal(err)
	}
	other := *fixture
	other.workspace = otherWorkspace
	other.broker = startGitHubApprovalBroker(t, fixture.home, otherWorkspace, fixture.fakeGH, fixture.ghConfig, otherScope, fixture.userConfig)
	crossWorkspace := make(chan productionForwardResult, 1)
	go func() {
		code, out, stderr := other.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
		crossWorkspace <- productionForwardResult{code: code, out: out, stderr: stderr}
	}()
	var otherRequest brokerstore.Request
	var crossWorkspaceResult *productionForwardResult
	waitGitHubApproval(t, "a refusal instead of cross-workspace grant use", 3*time.Second, func() bool {
		requests, _, err := fixture.store.List()
		if err == nil {
			for _, q := range requests {
				if q.State == "pending" && q.Workspace == otherWorkspace && q.Digest == grantRequest.Digest {
					otherRequest = q
					return true
				}
			}
		}
		select {
		case got := <-crossWorkspace:
			crossWorkspaceResult = &got
			return true
		default:
			return false
		}
	})
	if crossWorkspaceResult != nil {
		if crossWorkspaceResult.code != ghbroker.ExitNoPerm && crossWorkspaceResult.code != ghbroker.ExitUnavailable {
			t.Fatalf("cross-workspace grant attempt was not refused: %d %q", crossWorkspaceResult.code, crossWorkspaceResult.stderr)
		}
	} else {
		if rc := Main([]string{"yolo", "approve", otherRequest.ID, "--deny"}); rc != 0 {
			t.Fatalf("denying cross-workspace request returned %d", rc)
		}
		select {
		case got := <-crossWorkspace:
			if got.code != ghbroker.ExitNoPerm {
				t.Fatalf("cross-workspace action returned %d: %q", got.code, got.stderr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cross-workspace caller did not return")
		}
	}

	outOfScope, _, scopeErr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "outside/private", "--body-file", "-")
	if outOfScope != ghbroker.ExitUsage {
		t.Fatalf("out-of-scope repository returned %d: %q", outOfScope, scopeErr)
	}
	_, grants, err = fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 {
		t.Fatalf("out-of-scope repository spent authority: %+v err=%v", grants, err)
	}

	approved := make(chan productionForwardResult, 1)
	go func() {
		code, out, stderr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
		approved <- productionForwardResult{code: code, out: out, stderr: stderr}
	}()
	select {
	case got := <-approved:
		if got.code != 0 || !strings.Contains(got.out, "fake GitHub receipt") {
			t.Fatalf("same-workspace exact grant failed after restart: %d %q %q", got.code, got.out, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("same-workspace caller did not finish after restart")
	}
	wantReceipt := append([]byte(strings.Join(grantRequest.Argv, "\x00")), 0, 0)
	gotReceipt, err := os.ReadFile(fixture.calls)
	if err != nil || !bytes.Equal(gotReceipt, wantReceipt) {
		t.Fatalf("restart grant receipt differs or repeated: got %q want %q err=%v", gotReceipt, wantReceipt, err)
	}
	gotStdin, err := os.ReadFile(fixture.stdin)
	if err != nil || len(gotStdin) == 0 || !bytes.Equal(gotStdin, []byte(body)) || stdinSHA256(gotStdin) != grantRequest.StdinDigest {
		t.Fatalf("restart grant stdin differs from the approved bytes and SHA-256: got=%q digest=%s want=%q digest=%s err=%v", gotStdin, stdinSHA256(gotStdin), body, grantRequest.StdinDigest, err)
	}
	_, grants, err = fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 0 || grants[0].EndedBy() != brokerstore.EndUsed {
		t.Fatalf("restart did not consume the one-use authority once: %+v err=%v", grants, err)
	}
	requireProductionSpendAudit(t, grantRequest, grants[0], []byte(body))
}

func TestProductionSpendAuditFailureSpendsWithoutDispatchOrRetry(t *testing.T) {
	fixture := newProductionGitHubFixture(t, "", false)
	body := "audit failure body\n"
	seedDone := make(chan productionForwardResult, 1)
	go func() {
		code, out, stderr := fixture.forwardInput(body, "pr", "comment", "40", "--repo", "o/r", "--body-file", "-")
		seedDone <- productionForwardResult{code: code, out: out, stderr: stderr}
	}()
	var seed brokerstore.Request
	waitGitHubApproval(t, "the audit-fault grant seed request", 3*time.Second, func() bool {
		requests, _, err := fixture.store.List()
		if err != nil {
			return false
		}
		for _, q := range requests {
			if q.State == "pending" && len(q.Argv) == 5 && q.Argv[4] == "40" {
				seed = q
				return true
			}
		}
		return false
	})
	if rc := Main([]string{"yolo", "approve", seed.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying grant seed returned %d", rc)
	}
	select {
	case got := <-seedDone:
		if got.code != ghbroker.ExitNoPerm {
			t.Fatalf("seed caller exit %d: %q", got.code, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("seed caller did not return")
	}
	seed.Argv[len(seed.Argv)-1] = "41"
	approved, err := fixture.store.File(seed)
	if err != nil {
		t.Fatal(err)
	}
	if approved.StdinDigest != stdinSHA256([]byte(body)) || approved.Digest != brokerstore.CommandDigest(approved) || approved.Repo != "o/r" {
		t.Fatalf("audit-fault grant request is not the exact submitted action: %+v", approved)
	}
	if rc := Main([]string{"yolo", "approve", approved.ID}); rc != 0 {
		t.Fatalf("allowing exact audit-fault grant returned %d", rc)
	}

	lock := filepath.Join(filepath.Dir(paths.BrokerAuditLog()), ".audit.lock")
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	attempt := make(chan productionForwardResult, 1)
	go func() {
		code, out, stderr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
		attempt <- productionForwardResult{code: code, out: out, stderr: stderr}
	}()
	select {
	case got := <-attempt:
		if got.code != ghbroker.ExitUnavailable {
			t.Fatalf("required spend-audit failure returned %d, want unavailable refusal: %q", got.code, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("audit append failure did not release the production caller")
	}
	if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("required spend-audit failure dispatched fake gh: %v", err)
	}
	_, grants, err := fixture.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 0 || grants[0].EndedBy() != brokerstore.EndUsed ||
		grants[0].Request != approved.ID || grants[0].Digest != approved.Digest {
		t.Fatalf("failed predispatch audit refunded or unlinked the durable spend: %+v err=%v", grants, err)
	}
	if _, err := os.Stat(fixture.stdin); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("required spend-audit failure left a fake gh stdin receipt: %v", err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal(err)
	}
	var allowed int
	for _, event := range events {
		if event.Request != approved.ID || event.Grant != grants[0].ID || event.CommandDigest != approved.Digest ||
			event.StdinDigest != approved.StdinDigest || !slices.Equal(event.Argv, approved.Argv) {
			continue
		}
		if event.Event == "decision" && event.Outcome == "allowed" {
			allowed++
		}
		if event.Event == "grant-used" || event.Event == "call" && event.Outcome == "ran" {
			t.Fatalf("failed required append unexpectedly persisted a spend/dispatch event: %+v", event)
		}
	}
	if allowed != 1 {
		t.Fatalf("durable allowed decision audit lost its exact action linkage: allowed=%d events=%+v", allowed, events)
	}

	// A later explicit invocation cannot retry the spent grant; it needs a fresh decision.
	retry := make(chan productionForwardResult, 1)
	go func() {
		code, out, stderr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
		retry <- productionForwardResult{code: code, out: out, stderr: stderr}
	}()
	var refiled brokerstore.Request
	waitGitHubApproval(t, "a fresh request after the durable failed spend", 3*time.Second, func() bool {
		requests, _, err := fixture.store.List()
		if err != nil {
			return false
		}
		for _, q := range requests {
			if q.State == "pending" && q.Digest == approved.Digest {
				refiled = q
				return true
			}
		}
		return false
	})
	if refiled.StdinDigest != approved.StdinDigest || refiled.Digest != approved.Digest || !slices.Equal(refiled.Argv, approved.Argv) {
		t.Fatalf("fresh consent request after durable spend lost exact action identity: approved=%+v retry=%+v", approved, refiled)
	}
	if rc := Main([]string{"yolo", "approve", refiled.ID, "--deny"}); rc != 0 {
		t.Fatalf("denying fresh retry request returned %d", rc)
	}
	select {
	case got := <-retry:
		if got.code != ghbroker.ExitNoPerm {
			t.Fatalf("freshly denied retry exit %d: %q", got.code, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("freshly denied retry did not return")
	}
	requireProductionDeniedRequestAudit(t, refiled, []byte(body))
	if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry reached fake gh without fresh approval: %v", err)
	}
}

func TestProductionUnusedGrantExpiryAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		config     string
		revoke     bool
		wantReason string
	}{
		{name: "expired", config: `{"brokered":{"github":{"sets":[{"name":"read-write","mode":"windowed","window":"1ns","max_window":"1ns"}]}}}`, wantReason: brokerstore.EndExpired},
		{name: "revoked unused", wantReason: brokerstore.EndRevoked, revoke: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProductionGitHubFixture(t, tc.config, false)
			start := func(target string) chan productionForwardResult {
				finished := make(chan productionForwardResult, 1)
				go func() {
					code, out, stderr := fixture.forwardInput("expiry and revoke body\n", "pr", "comment", target, "--repo", "o/r", "--body-file", "-")
					finished <- productionForwardResult{code: code, out: out, stderr: stderr}
				}()
				return finished
			}
			pending := func(finished <-chan productionForwardResult, target string) brokerstore.Request {
				t.Helper()
				var request brokerstore.Request
				waitGitHubApproval(t, "the production pending exact request", 3*time.Second, func() bool {
					requests, _, err := fixture.store.List()
					if err != nil {
						return false
					}
					for _, q := range requests {
						if q.State == "pending" && len(q.Argv) == 5 && q.Argv[4] == target {
							request = q
							return true
						}
					}
					select {
					case got := <-finished:
						t.Fatalf("caller returned before request filed: %d %q", got.code, got.stderr)
					default:
					}
					return false
				})
				return request
			}
			finish := func(ch <-chan productionForwardResult, want int) {
				t.Helper()
				select {
				case got := <-ch:
					if got.code != want {
						t.Fatalf("production caller exit %d, want %d: %q", got.code, want, got.stderr)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("production caller did not return")
				}
			}
			seedForward := start("40")
			seed := pending(seedForward, "40")
			if rc := Main([]string{"yolo", "approve", seed.ID, "--deny"}); rc != 0 {
				t.Fatalf("denying scope-seed request returned %d", rc)
			}
			finish(seedForward, ghbroker.ExitNoPerm)
			seed.Argv[len(seed.Argv)-1] = "41"
			approved, err := fixture.store.File(seed)
			if err != nil {
				t.Fatal(err)
			}
			if rc := Main([]string{"yolo", "approve", approved.ID}); rc != 0 {
				t.Fatalf("allowing unused grant returned %d", rc)
			}
			if tc.revoke {
				_, grants, err := fixture.store.List()
				if err != nil || len(grants) != 1 {
					t.Fatalf("grant listing: %+v %v", grants, err)
				}
				if rc := Main([]string{"yolo", "approve", "--revoke", grants[0].ID}); rc != 0 {
					t.Fatalf("revoking unused exact grant returned %d", rc)
				}
			}
			attempt := start("41")
			refiled := pending(attempt, "41")
			if refiled.Digest != approved.Digest || refiled.StdinDigest != stdinSHA256([]byte("expiry and revoke body\n")) || !slices.Equal(refiled.Argv, approved.Argv) {
				t.Fatalf("expired/revoked refusal does not record the exact attempted action: approved=%+v refiled=%+v", approved, refiled)
			}
			_, grants, err := fixture.store.List()
			if err != nil || len(grants) != 1 || grants[0].Uses != 1 || grants[0].EndedBy() != tc.wantReason {
				t.Fatalf("production attempt spent or revived unavailable authority: %+v err=%v", grants, err)
			}
			if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unavailable grant dispatched to fake gh: %v", err)
			}
			if rc := Main([]string{"yolo", "approve", refiled.ID, "--deny"}); rc != 0 {
				t.Fatalf("denying re-filed caller returned %d", rc)
			}
			finish(attempt, ghbroker.ExitNoPerm)
			requireProductionDeniedRequestAudit(t, refiled, []byte("expiry and revoke body\n"))
			events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
			if err != nil {
				t.Fatal(err)
			}
			var allowed, spends int
			for _, event := range events {
				if event.Request != approved.ID || event.Grant != grants[0].ID || event.CommandDigest != approved.Digest || event.StdinDigest != approved.StdinDigest || !slices.Equal(event.Argv, approved.Argv) {
					continue
				}
				if event.Event == "decision" && event.Outcome == "allowed" {
					allowed++
				}
				if event.Event == "grant-used" && event.Outcome == "spent-before-dispatch" {
					spends++
				}
			}
			if allowed != 1 || spends != 0 {
				t.Fatalf("expired/revoked grant audit must retain its exact decision without a spend: allowed=%d spends=%d events=%+v", allowed, spends, events)
			}
		})
	}
}

type productionForwardResult struct {
	code        int
	out, stderr string
}

func TestProductionStandingWritePolicyRefusesWritesAndKeepsReads(t *testing.T) {
	userConfig := `{"brokered":{"github":{"sets":[{"name":"read-only","mode":"standing"},{"name":"read-write","mode":"standing"}]}}}`
	fixture := newProductionGitHubFixture(t, userConfig, false)
	if code, out, stderr := fixture.forward("pr", "view", "41", "--repo", "o/r"); code != 0 || !strings.Contains(out, "fake GitHub receipt") {
		t.Fatalf("standing read changed: exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	code, _, stderr := fixture.forward("pr", "comment", "41", "--repo", "o/r", "--body", "must-not-run")
	if code != ghbroker.ExitNoPerm || !strings.Contains(stderr, "brokered.github.sets") || !strings.Contains(stderr, "remove") {
		t.Fatalf("standing write policy was not refused with a repair step: exit %d, stderr %q", code, stderr)
	}
	for _, argv := range [][]string{
		{"pr", "view", "41", "--repo", "outside/secret"},
		{"auth", "token"},
		{"api", "-H", "X-HTTP-Method-Override: DELETE", "repos/o/r/issues/1"},
	} {
		before, err := os.ReadFile(fixture.calls)
		if err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := fixture.forward(argv...); code == 0 {
			t.Fatalf("out-of-scope or secret command was not refused: %v", argv)
		} else if strings.Contains(stderr, "fake GitHub receipt") {
			t.Fatalf("refused command reached fake host gh: %v: %q", argv, stderr)
		}
		after, err := os.ReadFile(fixture.calls)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("refused command reached fake host gh: argv=%v before=%q after=%q err=%v", argv, before, after, err)
		}
	}
	requests, _, err := fixture.store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatalf("unsupported standing write filed a request: %+v", requests)
	}
	data, err := os.ReadFile(fixture.calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "pr comment") {
		t.Fatalf("standing write reached host gh: %q", data)
	}
}

func TestProductionGitHubWriteApprovalFlow(t *testing.T) {
	for _, tc := range []struct {
		name, command  string
		argv, hostArgv []string
		stdin          string
		withHook       bool
	}{
		{name: "PR comment manual approval without on_request", command: "comment",
			argv:     []string{"pr", "comment", "41", "--repo", "o/r", "--body-file", "-"},
			hostArgv: []string{"pr", "comment", "--repo=o/r", "--body-file=-", "41"}, stdin: "stdin-approval-body\n"},
		{name: "issue close despite on_request output", command: "close",
			argv:     []string{"issue", "close", "7", "--repo", "o/r"},
			hostArgv: []string{"issue", "close", "--repo=o/r", "7"}, withHook: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProductionGitHubFixture(t, "", tc.withHook)
			type result struct {
				code        int
				out, stderr string
			}
			finished := make(chan result, 1)
			go func() {
				code, out, stderr := fixture.forwardInput(tc.stdin, tc.argv...)
				finished <- result{code: code, out: out, stderr: stderr}
			}()

			var request brokerstore.Request
			deadline := time.Now().Add(3 * time.Second)
			for request.ID == "" {
				select {
				case got := <-finished:
					t.Fatalf("production forwarder returned before filing an approval request: exit %d, stderr %q; want exit %d and a pending request", got.code, got.stderr, ghbroker.ExitTempFail)
				default:
				}
				requests, _, err := fixture.store.List()
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range requests {
					if candidate.State == "pending" && len(candidate.Argv) > 1 && candidate.Argv[1] == tc.command {
						request = candidate
						break
					}
				}
				if request.ID != "" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the production broker did not file a request for host approval")
				}
				time.Sleep(10 * time.Millisecond)
			}

			stdinSum := sha256.Sum256([]byte(tc.stdin))
			wantStdinDigest := hex.EncodeToString(stdinSum[:])
			if !slices.Equal(request.Argv, tc.hostArgv) || request.Repo != "o/r" || request.StdinDigest != wantStdinDigest ||
				request.Digest != brokerstore.CommandDigest(request) {
				t.Fatalf("filed production action identity differs from the requested exact action: %+v", request)
			}
			view := fixture.store.RequestView(request)
			encodedView, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			if view.CommandDigest != request.Digest || view.StdinDigest != request.StdinDigest ||
				(tc.stdin != "" && bytes.Contains(encodedView, []byte(tc.stdin))) {
				t.Fatalf("request JSON does not expose only the action digests: %s", encodedView)
			}
			var listed bytes.Buffer
			printApproveList(&listed, brokerstore.Snapshot{Requests: []brokerstore.Request{request}})
			if !strings.Contains(listed.String(), "command "+request.Digest) ||
				!strings.Contains(listed.String(), "stdin SHA-256 "+request.StdinDigest) ||
				(tc.stdin != "" && strings.Contains(listed.String(), tc.stdin)) {
				t.Fatalf("host text listing does not identify the exact body without exposing it: %q", listed.String())
			}

			waitGitHubApproval(t, "the host broker-log request", 3*time.Second, func() bool {
				return fixture.broker.logs.Contains(request.ID)
			})
			if tc.withHook {
				waitGitHubApproval(t, "the host on_request receipt", 3*time.Second, func() bool {
					data, err := os.ReadFile(fixture.hookLog)
					return err == nil && bytes.Contains(data, []byte(request.ID))
				})
				hookView, err := os.ReadFile(fixture.hookLog)
				if err != nil || !bytes.Contains(hookView, []byte(`"command_digest":"`+request.Digest+`"`)) ||
					!bytes.Contains(hookView, []byte(`"stdin_digest":"`+request.StdinDigest+`"`)) ||
					(tc.stdin != "" && bytes.Contains(hookView, []byte(tc.stdin))) {
					t.Fatalf("hook request summary lacks safe exact action identity: %s, %v", hookView, err)
				}
			}
			status, err := fixture.store.Status(request.Jail, request.Start, request.ID)
			if err != nil || status.State != "pending" {
				t.Fatalf("on_request output was interpreted as consent: %+v %v", status, err)
			}
			if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a pending request reached host gh before host approval: stat err %v", err)
			}
			if rc := Main([]string{"yolo", "approve", request.ID}); rc != 0 {
				t.Fatalf("host yolo approve exited %d for %s", rc, request.ID)
			}
			select {
			case got := <-finished:
				if got.code != 0 || !strings.Contains(got.out, "fake GitHub receipt") {
					t.Fatalf("approved production write: exit %d, stdout %q, stderr %q", got.code, got.out, got.stderr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the production forwarder did not return after host approval")
			}
			data, err := os.ReadFile(fixture.calls)
			if err != nil {
				t.Fatal(err)
			}
			wantReceipt := append([]byte(strings.Join(tc.hostArgv, "\x00")), 0, 0)
			if !bytes.Equal(data, wantReceipt) {
				t.Fatalf("fake host gh argv receipt differs at argument boundaries or was dispatched more than once: got %q want %q", data, wantReceipt)
			}
			gotStdin, err := os.ReadFile(fixture.stdin)
			if err != nil {
				t.Fatal(err)
			}
			gotStdinSum := sha256.Sum256(gotStdin)
			if hex.EncodeToString(gotStdinSum[:]) != request.StdinDigest || !bytes.Equal(gotStdin, []byte(tc.stdin)) {
				t.Fatalf("fake host gh received stdin other than the approved bytes: sha=%x bytes=%d", gotStdinSum, len(gotStdin))
			}
			events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
			if err != nil {
				t.Fatal(err)
			}
			var spent, dispatched bool
			dispatchCount := 0
			for _, event := range events {
				if event.Request != request.ID || event.Grant == "" || event.CommandDigest != request.Digest || event.StdinDigest != request.StdinDigest {
					continue
				}
				spent = spent || event.Event == "grant-used" && event.Outcome == "spent-before-dispatch"
				if event.Event == "call" && event.Outcome == "ran" {
					dispatchCount++
					dispatched = true
				}
			}
			if !spent || !dispatched || dispatchCount != 1 {
				t.Fatalf("audit did not link exactly one exact request, grant and dispatched action: spent=%t dispatched=%t count=%d events=%+v", spent, dispatched, dispatchCount, events)
			}
			if tc.command == "comment" {
				wantReceipt := append([]byte(strings.Join(tc.hostArgv, "\x00")), 0, 0)
				seenIDs := map[string]bool{request.ID: true}
				mutations := []struct {
					argv, wantArgv []string
					body           string
					wantDigest     string
				}{
					{argv: tc.argv, wantArgv: tc.hostArgv, body: tc.stdin, wantDigest: request.Digest},
					{argv: tc.argv, wantArgv: tc.hostArgv, body: "changed body after approval\n"},
					{argv: []string{"pr", "comment", "42", "--repo", "o/r", "--body-file", "-"},
						wantArgv: []string{"pr", "comment", "--repo=o/r", "--body-file=-", "42"}, body: tc.stdin},
				}
				for _, mutation := range mutations {
					replayFinished := make(chan result, 1)
					go func(argv []string, body string) {
						code, out, stderr := fixture.forwardInput(body, argv...)
						replayFinished <- result{code: code, out: out, stderr: stderr}
					}(mutation.argv, mutation.body)
					var next brokerstore.Request
					deadline := time.Now().Add(3 * time.Second)
					for next.ID == "" {
						requests, _, err := fixture.store.List()
						if err != nil {
							t.Fatal(err)
						}
						for _, candidate := range requests {
							if candidate.State == "pending" && !seenIDs[candidate.ID] {
								next = candidate
								break
							}
						}
						if next.ID != "" {
							break
						}
						select {
						case got := <-replayFinished:
							t.Fatalf("production replay returned before a host decision: exit %d stderr %q", got.code, got.stderr)
						default:
						}
						if time.Now().After(deadline) {
							t.Fatal("production replay did not file a new exact request")
						}
						time.Sleep(10 * time.Millisecond)
					}
					bodySum := sha256.Sum256([]byte(mutation.body))
					if !slices.Equal(next.Argv, mutation.wantArgv) || next.StdinDigest != hex.EncodeToString(bodySum[:]) ||
						(mutation.wantDigest != "" && next.Digest != mutation.wantDigest) ||
						(mutation.wantDigest == "" && next.Digest == request.Digest) {
						t.Fatalf("production replay identity did not bind argv and body exactly: original=%+v next=%+v", request, next)
					}
					seenIDs[next.ID] = true
					if rc := Main([]string{"yolo", "approve", next.ID, "--deny"}); rc != 0 {
						t.Fatalf("deny replay/mutated request %s returned %d", next.ID, rc)
					}
					select {
					case got := <-replayFinished:
						if got.code != ghbroker.ExitNoPerm {
							t.Fatalf("denied production replay ran or returned %d", got.code)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("denied production replay did not return")
					}
					gotReceipt, err := os.ReadFile(fixture.calls)
					if err != nil || !bytes.Equal(gotReceipt, wantReceipt) {
						t.Fatalf("replay/mutated body reached host gh: %q err %v", gotReceipt, err)
					}
				}
			}
		})
	}
}
