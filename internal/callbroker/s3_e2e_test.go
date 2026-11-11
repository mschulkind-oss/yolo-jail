package callbroker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/s3fixture"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// s3E2E is the AE-E4 path for the shipped aws-s3 pack, in process: the generated `aws` shim,
// `yolo internal call-broker-forward`, the authenticated front, the real handler, the real
// classifier (the built `yolo internal call-broker-classify aws-s3`), the approval store, a
// host `yolo approve`, and core's CLI executor running a fake host `aws` against a local
// fake S3 service. No AWS account, credential or endpoint is involved.
type s3E2E struct {
	t        *testing.T
	home     string
	yolo     string
	h        *CLIHandler
	store    *brokerstore.Store
	server   *s3fixture.Server
	shim     string
	endpoint string
	envKey   string
	hostEnv  []string
	outputs  []string // every jail-visible output
}

const s3Root = "s3://bkt/team/"

func newS3E2E(t *testing.T) *s3E2E {
	t.Helper()
	return newS3E2EWithBuild(t, (*exec.Cmd).CombinedOutput)
}

func newS3E2EWithBuild(t *testing.T, build func(*exec.Cmd) ([]byte, error)) *s3E2E {
	t.Helper()
	home := shortCallBrokerTempDir(t)
	t.Cleanup(packload.ReleaseEmbedded)
	goTool, err := callBrokerTestGoTool()
	if err != nil {
		t.Fatalf("locate Go compiler: %v", err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	yolo := os.Getenv("YOLO_TEST_MATCHED_YOLO")
	if yolo == "" {
		yolo = filepath.Join(bin, "yolo")
		if out, err := build(callBrokerTestBuild(goTool, yolo, "../../cmd/yolo")); err != nil {
			t.Fatalf("build yolo: %v %s", err, out)
		}
	}
	// The host program: the fixture binary under the name `aws`, a copy, never a link, so
	// its basename stays `aws` after the executor resolves it.
	if out, err := build(callBrokerTestBuild(goTool, filepath.Join(bin, "aws"), "../s3fixture/cmd/s3fixture")); err != nil {
		t.Fatalf("build fake host aws: %v %s", err, out)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	hostPath := bin + string(os.PathListSeparator) + filepath.Dir(yolo) + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", hostPath)

	server := s3fixture.NewServer(s3fixture.Options{
		Credentials: sigv4.Credentials{AccessKeyID: s3fixture.AccessKeyID, SecretAccessKey: s3fixture.SecretAccessKey},
		Region:      s3fixture.Region,
		Buckets: map[string]map[string][]byte{
			"bkt":       {"team/a.txt": []byte("alpha from the fixture\n"), "other/b.txt": []byte("outside the root\n")},
			"other-bkt": {"x": []byte("another bucket\n")},
		},
	})
	s3fixture.MustStart(t, server)
	t.Cleanup(server.Close)
	for _, name := range []string{"AWS_PROFILE", "AWS_DEFAULT_REGION", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_SESSION_TOKEN", "AWS_ENDPOINT_URL"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	t.Setenv("AWS_ENDPOINT_URL_S3", server.URL())
	t.Setenv("AWS_ACCESS_KEY_ID", s3fixture.AccessKeyID)
	t.Setenv("AWS_SECRET_ACCESS_KEY", s3fixture.SecretAccessKey)
	t.Setenv("AWS_REGION", s3fixture.Region)

	// The user's ceiling: read is requestable, both write cells ask (never run unasked).
	configDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(configDir, "config.toml")
	user := "packs = ['aws-s3']\n[allow.aws-s3]\nalways = [\"read\"]\non_request = [\"all.put_objects\", \"all.delete_objects\"]\n"
	if err := os.WriteFile(userPath, []byte(user), 0600); err != nil {
		t.Fatal(err)
	}

	// The SHIPPED declaration, read by the same loader production uses.
	packs, complete := config.UserScopeSelectedPacks()
	if !complete {
		t.Fatalf("aws-s3 pack selection incomplete: %+v", packs)
	}
	var module *loopholedecl.Manifest
	var moduleName string
	for _, pack := range packs {
		if pack.Name != "aws-s3" {
			continue
		}
		modules, refusals, warnings, err := packload.ReadableLoopholeModules(pack)
		if err != nil || len(refusals)+len(warnings) != 0 {
			t.Fatalf("read shipped aws-s3 declaration: %v %v %v", err, refusals, warnings)
		}
		for _, m := range modules {
			if m.Decl != nil && m.Decl.CallBroker != nil && m.Decl.CallBroker.Source == "aws-s3" {
				module, moduleName = m.Decl, m.Name
			}
		}
	}
	if module == nil {
		t.Fatal("shipped aws-s3 pack declares no aws-s3 call broker")
	}
	b := module.CallBroker
	if !slices.Equal(b.Classifier, []string{"yolo", "internal", "call-broker-classify", "aws-s3"}) || b.Front != "cli" || b.Program != "aws" || b.Executor != "core" {
		t.Fatalf("shipped declaration changed shape: %+v", b)
	}

	roots := []string{s3Root}
	put := config.CallBrokerTablePolicy{Request: "ask", Ceiling: "on_request", Decision: "ask"}
	effective := map[string]config.CallBrokerCellPolicy{
		"put_objects":    {Tables: map[string]config.CallBrokerTablePolicy{"all": put, "working_set": put}},
		"delete_objects": {Tables: map[string]config.CallBrokerTablePolicy{"all": put}},
	}
	input := callbrokerinput.Launch{Version: "yolo.call-broker-launch/2", Loophole: moduleName, Pack: "aws-s3",
		DeclarationHash: callbrokerinput.DeclarationHash(b), Source: b.Source, Front: b.Front, Program: b.Program,
		Classifier: b.Classifier, Executor: b.Executor, HostEnvNames: append([]string(nil), b.EnvFromHost...),
		ProgramIdentities: map[string]loopholedecl.CallBrokerProgramIdentity{}, Roots: roots,
		Resources: b.Resources, Cells: b.Cells, EffectiveCells: effective, Lookups: b.Lookups,
		ReadStanding: true, ReadGrantStanding: "granted", FrontJailID: "launch"}

	scope := brokerscope.File{Source: input.Source, PID: os.Getpid(), Container: "fixture-container", Workspace: home,
		Form: brokerscope.FormCallRoot, Repos: slices.Clone(roots)}
	id, err := brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	scope.LaunchID = id
	scopePath, err := brokerscope.Write(scope)
	if err != nil {
		t.Fatal(err)
	}
	input.ScopeFile = scopePath
	block := loopholegrant.Block{Pack: "aws-s3", Workspace: home, Container: scope.Container, PID: scope.PID, LaunchID: id,
		Kinds: map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted, Text: "read"}}}
	grantData, err := loopholegrant.Merge([]byte("{}"), block)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := loopholes.StateDirFor(moduleName)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	grantPath := filepath.Join(stateDir, "settings-test.json")
	if err := os.WriteFile(grantPath, grantData, 0600); err != nil {
		t.Fatal(err)
	}
	input.GrantFile = grantPath
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(home, "selected.json")
	if err := os.WriteFile(inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.BindCallInput(scopePath, scope, inputPath); err != nil {
		t.Fatal(err)
	}
	if scope, err = brokerscope.ReadFile(scopePath); err != nil {
		t.Fatal(err)
	}
	audit := brokeraudit.Open(filepath.Join(home, "audit.jsonl"), nil)
	store := &brokerstore.Store{Dir: paths.BrokerSourceDir(input.Source), Source: input.Source, PublishRecords: true, Audit: StoreAudit(audit, input.Source)}
	if err := os.MkdirAll(store.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Working(); err != nil {
		t.Fatal(err)
	}
	h, err := newCLIHandler(inputPath, home, store, audit)
	if err != nil {
		t.Fatal(err)
	}
	// Production resolves a `yolo` classifier to its own executable. In this test that
	// executable is the test binary, so the in-process handler is pointed at the built yolo
	// instead, with the shipped arguments unchanged. The recorded launch input keeps the
	// shipped argv, so the host `yolo approve` subprocess resolves it exactly as production.
	sum := sha256.Sum256(mustRead(t, yolo))
	resolved, err := filepath.EvalSymlinks(yolo)
	if err != nil {
		t.Fatal(err)
	}
	h.classifier.Argv = append([]string{"{program:classifier}"}, b.Classifier[1:]...)
	h.classifier.Identities = map[string]loopholedecl.CallBrokerProgramIdentity{
		"classifier": {Pack: "aws-s3", Bin: "classifier", Path: resolved, SHA256: hex.EncodeToString(sum[:])}}

	start, err := brokerstore.NewStartID()
	if err != nil {
		t.Fatal(err)
	}
	bound, lease, err := brokercore.BindLease(h.input.Source, scopePath, scope, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.BindCallCLI(scopePath, bound, h.program.path, h.program.sha256); err != nil {
		t.Fatal(err)
	}
	bound.CallCLIHash, bound.CallCLIPath = h.program.sha256, h.program.path
	h.scopePath, h.bound, h.start = scopePath, bound, start
	store.Revalidate = h.revalidate
	t.Cleanup(func() {
		if err := brokercore.CloseLease(h.input.Source, lease); err != nil {
			t.Error(err)
		}
	})
	if _, _, err := h.currentAuthority(); err != nil {
		t.Fatalf("fixture not live: %v", err)
	}

	// Front, daemon and the generated shim, as in the forwarder template.
	endpointDir := filepath.Join(home, "launch")
	if err := os.MkdirAll(endpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(home, "upstream.sock")
	endpoint := filepath.Join(endpointDir, moduleName+".endpoint")
	stop := make(chan struct{})
	serverDone := make(chan error, 1)
	frontDone := make(chan error, 1)
	go func() { serverDone <- hostservice.ServeFrontedUnix(h.Handle, socket, stop) }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host daemon not ready")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { frontDone <- svcendpoint.ServeFront(endpoint, "127.0.0.1", socket, stop) }()
	t.Cleanup(func() { close(stop); <-serverDone; <-frontDone })
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("authenticated front not ready")
		}
		time.Sleep(time.Millisecond)
	}
	// The shim comes from the shipped pack's own intercept declaration: the pack tree is
	// copied, so a change to the pack changes what this test generates.
	packRoot := filepath.Join(home, "packroot")
	if err := os.CopyFS(filepath.Join(packRoot, "aws-s3"), os.DirFS("../../packs/aws-s3")); err != nil {
		t.Fatal(err)
	}
	jailHome := filepath.Join(home, "jail-home")
	if err := os.Mkdir(jailHome, 0700); err != nil {
		t.Fatal(err)
	}
	boot := entrypoint.NewEnv(map[string]string{"JAIL_HOME": jailHome, "YOLO_PACK_ROOT": packRoot})
	if err := entrypoint.GenerateShims(boot); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(boot.BlockDir(), "aws")
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("shipped pack generated no aws shim: %v", err)
	}
	f := &s3E2E{t: t, home: home, yolo: yolo, h: h, store: store, server: server, shim: shim, endpoint: endpoint,
		envKey:  paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug(moduleName) + paths.ServiceEnvVarSuffix,
		hostEnv: []string{"HOME=" + home, "XDG_CONFIG_HOME=", "PATH=" + hostPath, "USER=fixture"}}
	return f
}

// aws runs the jail's `aws` shim with stdin and returns the exit code and combined output.
func (f *s3E2E) aws(stdin string, args ...string) (int, string) {
	f.t.Helper()
	cmd := exec.Command(f.shim, args...)
	cmd.Stdin = strings.NewReader(stdin)
	// The jail's environment: no host AWS variable, no host PATH entry holding the host aws.
	cmd.Env = []string{f.envKey + "=" + f.endpoint, "PATH=" + filepath.Dir(f.yolo) + ":/usr/bin:/bin", "HOME=" + filepath.Join(f.home, "jail-home")}
	out, err := cmd.CombinedOutput()
	f.outputs = append(f.outputs, string(out))
	if err == nil {
		return 0, string(out)
	}
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), string(out)
	}
	f.t.Fatal(err)
	return -1, ""
}

func (f *s3E2E) pending() []brokerstore.Request {
	f.t.Helper()
	snap, err := f.store.Snapshot()
	if err != nil {
		f.t.Fatal(err)
	}
	var out []brokerstore.Request
	for _, r := range snap.Requests {
		if r.State == "pending" {
			out = append(out, r)
		}
	}
	return out
}

func (f *s3E2E) host(args ...string) (string, error) {
	cmd := exec.Command(f.yolo, args...)
	cmd.Dir, cmd.Env = f.home, f.hostEnv
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *s3E2E) requests() int { return len(f.server.RequestLog()) }

func TestS3ShippedPackThroughRealMediation(t *testing.T) {
	f := newS3E2E(t)

	// (a) reads run and return fixture data.
	if rc, out := f.aws("", "s3", "ls", "s3://bkt/team/"); rc != 0 || !strings.Contains(out, "a.txt") {
		t.Fatalf("ls rc=%d out=%q", rc, out)
	}
	if rc, out := f.aws("", "s3", "cp", "s3://bkt/team/a.txt", "-"); rc != 0 || out != "alpha from the fixture\n" {
		t.Fatalf("cp read rc=%d out=%q", rc, out)
	}
	if f.requests() == 0 {
		t.Fatal("reads never reached the fixture")
	}

	// (b) a write asks and changes nothing.
	baseline := f.requests()
	if rc, out := f.aws("hello\n", "s3", "cp", "-", "s3://bkt/team/new.txt"); rc != 75 || !strings.Contains(out, "yolo approve r-") {
		t.Fatalf("put did not ask: rc=%d out=%q", rc, out)
	}
	if f.requests() != baseline || f.server.Writes() != 0 {
		t.Fatalf("asking reached the fixture: %+v writes=%d", f.server.RequestLog()[baseline:], f.server.Writes())
	}
	if _, ok := f.server.Object("bkt", "team/new.txt"); ok {
		t.Fatal("object exists before approval")
	}
	pending := f.pending()
	if len(pending) != 1 {
		t.Fatalf("expected one pending request, got %+v", pending)
	}

	// Evidence: what the human sees before answering.
	view, err := f.host("approve", "--json")
	if err != nil || !strings.Contains(view, pending[0].ID) {
		t.Fatalf("host approve --json: %v %s", err, view)
	}
	t.Logf("yolo approve --json (pending put):\n%s", view)

	// (c) the host approves once; the same call runs and the object has the exact bytes.
	if out, err := f.host("approve", pending[0].ID); err != nil {
		t.Fatalf("host approve: %v %s", err, out)
	}
	if rc, out := f.aws("hello\n", "s3", "cp", "-", "s3://bkt/team/new.txt"); rc != 0 {
		t.Fatalf("approved put rc=%d out=%q", rc, out)
	}
	if got, ok := f.server.Object("bkt", "team/new.txt"); !ok || string(got) != "hello\n" || f.server.Writes() != 1 {
		t.Fatalf("object=%q ok=%v writes=%d", got, ok, f.server.Writes())
	}

	// (d) the Allow-once is spent: the same call asks again and changes nothing.
	if rc, out := f.aws("hello\n", "s3", "cp", "-", "s3://bkt/team/new.txt"); rc != 75 || !strings.Contains(out, "yolo approve r-") || f.server.Writes() != 1 {
		t.Fatalf("repeat rc=%d out=%q writes=%d", rc, out, f.server.Writes())
	}

	// (e) different stdin is a different command: it asks again.
	before := len(f.pending())
	if rc, out := f.aws("different\n", "s3", "cp", "-", "s3://bkt/team/new.txt"); rc != 75 || !strings.Contains(out, "yolo approve r-") || f.server.Writes() != 1 {
		t.Fatalf("different stdin rc=%d out=%q writes=%d", rc, out, f.server.Writes())
	}
	if got, _ := f.server.Object("bkt", "team/new.txt"); string(got) != "hello\n" {
		t.Fatalf("object changed without approval: %q", got)
	}
	if len(f.pending()) <= before {
		t.Fatalf("different stdin filed no new request: %+v", f.pending())
	}

	// (f) refusals: rc 64 and not one request reaches the fixture.
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"rm in another bucket", "", []string{"s3", "rm", "s3://other-bkt/x"}},
		{"listing outside the root", "", []string{"s3", "ls", "s3://bkt/"}},
		{"jail endpoint", "", []string{"--endpoint-url", "http://127.0.0.1:1", "s3", "ls", "s3://bkt/team/"}},
		{"sync", "", []string{"s3", "sync", ".", "s3://bkt/team/"}},
		{"other service", "", []string{"ec2", "describe-instances"}},
		{"version", "", []string{"--version"}},
	} {
		n := f.requests()
		if rc, out := f.aws(tc.stdin, tc.args...); rc != 64 {
			t.Errorf("%s: rc=%d out=%q", tc.name, rc, out)
		}
		if f.requests() != n {
			t.Errorf("%s: refused call reached the fixture: %+v", tc.name, f.server.RequestLog()[n:])
		}
	}
	if _, ok := f.server.Object("other-bkt", "x"); !ok {
		t.Error("refused rm deleted the object")
	}

	// (g) the synthetic secret is never jail-visible and never in any host record we keep.
	for i, out := range f.outputs {
		if strings.Contains(out, s3fixture.SecretAccessKey) || strings.Contains(out, s3fixture.AccessKeyID) {
			t.Errorf("jail-visible output %d contains a credential: %q", i, out)
		}
	}
	_ = filepath.WalkDir(f.home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == "aws" || d.Name() == "yolo" || strings.Contains(path, string(filepath.Separator)+"bin"+string(filepath.Separator)) || strings.Contains(path, string(filepath.Separator)+".cache"+string(filepath.Separator)) {
			return nil // the built binaries embed the fixture constants
		}
		if data, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(data), s3fixture.SecretAccessKey) {
			t.Errorf("host record %s contains the secret", path)
		}
		return nil
	})
	if audit := mustRead(t, filepath.Join(f.home, "audit.jsonl")); len(audit) == 0 || strings.Contains(string(audit), s3fixture.SecretAccessKey) {
		t.Errorf("audit log empty or leaks the secret (%d bytes)", len(audit))
	}
}
