package callbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

type onceFixture struct {
	h                                                       *CLIHandler
	home, marker, inputPath, scopePath, grantPath, userPath string
	store                                                   *brokerstore.Store
	scope                                                   brokerscope.File
}

func selectedOnceFixture(t *testing.T) *onceFixture {
	return selectedOnceFixtureWithGrant(t, false, true)
}

func selectedOnceFixtureWithStanding(t *testing.T, standing bool) *onceFixture {
	return selectedOnceFixtureWithGrant(t, standing, true)
}

func selectedOnceFixtureWithGrant(t *testing.T, standing, initialRead bool) *onceFixture {
	t.Helper()
	return selectedOnceFixtureWith(t, standing, initialRead, false)
}

// selectedTimedOnceFixture declares max_for on its cells: change 8h, and a second asking cell
// rename 1h, which the fixture classifier gives a task whose id starts with r.
func selectedTimedOnceFixture(t *testing.T) *onceFixture {
	t.Helper()
	return selectedOnceFixtureWith(t, false, true, true)
}

func selectedOnceFixtureWith(t *testing.T, standing, initialRead, timed bool) *onceFixture {
	t.Helper()
	old, inputPath, marker, _ := testCallHandler(t)
	home := filepath.Dir(marker)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SYNTHETIC_HOST_TOKEN", "synthetic-only-host-value")
	t.Cleanup(packload.ReleaseEmbedded)
	moduleDir := filepath.Join(home, "test-pack", "loopholes", "tasks")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","access":"read fixture tasks","transport":"loopback-tls","grants":[{"kind":"read","text":"read tasks"}],"call_broker":{"source":"local-tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"env_from_host":["SYNTHETIC_HOST_TOKEN"],"scope":{"roots":"declared"},"resource":[{"type":"project"},{"type":"task","parent":"project"},{"type":"pr","parent":"project"}],"cell":[{"name":"change","tables":["working_set","all"]},{"name":"blocked","tables":["all"]}],"lookup":[{"name":"task-project","cli":["lookup","{id}"]}]}}`
	allowCells := `"all.change"`
	if timed {
		manifest = strings.Replace(manifest, `{"name":"change","tables":["working_set","all"]}`,
			`{"name":"change","tables":["working_set","all"],"max_for":"8h"},{"name":"rename","tables":["all"],"max_for":"1h"}`, 1)
		allowCells = `"all.change", "all.rename"`
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "manifest.jsonc"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Dir(filepath.Dir(moduleDir))
	if err := os.WriteFile(filepath.Join(packDir, "pack.toml"), []byte("name = \"test-pack\"\n[[loophole]]\nfrom = \"loopholes/tasks\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(configDir, "config.toml")
	user := fmt.Sprintf("packs = ['src/test-pack']\n[pack_sources]\nsrc = %q\n[allow.local-tasks]\non_request = [%s]\n[allow.\"src/test-pack\"]\non_request = [\"read\"]\n", home, allowCells)
	if err := os.WriteFile(userPath, []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	packs, complete := config.UserScopeSelectedPacks()
	if !complete || len(packs) < 1 {
		entries, loadErr := config.LoadPacks(func(string) {})
		t.Fatalf("current pack selection incomplete: %t %d entries=%+v err=%v", complete, len(packs), entries, loadErr)
	}
	selectedPack := ""
	for _, pack := range packs {
		if pack.Name == "src/test-pack" {
			selectedPack = pack.Name
		}
	}
	if selectedPack == "" {
		t.Fatalf("configured fixture pack not selected: %+v", packs)
	}
	packload.ReleaseEmbedded()
	declared, err := loopholedecl.Decode([]byte(manifest), moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	program := "#!/bin/sh\ncase \"$1\" in lookup) printf 'LOOKUP\\n' >> '" + filepath.Join(home, "lookup-effects") + "'; printf '{\"project\":\"team:alpha\"}';; update-pr|update) cat > '" + filepath.Join(home, "stdin-received") + "'; printf 'WRITE\\n' >> '" + marker + "'; if grep -q 'oversized-response' '" + filepath.Join(home, "stdin-received") + "'; then printf 'OVER_CAP_PRIVATE_SENTINEL'; head -c 33554433 /dev/zero | tr '\\000' x; fi;; read) printf '{\"ok\":true}';; error) exit 13;; *) exit 13;; esac\n"
	if err := os.WriteFile(old.program.path, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	old.input.Pack = selectedPack
	old.input.DeclarationHash = callbrokerinput.DeclarationHash(declared.CallBroker)
	old.input.HostEnvNames = append([]string(nil), declared.CallBroker.EnvFromHost...)
	old.input.Executor = declared.CallBroker.Executor
	old.input.Cells = declared.CallBroker.Cells
	old.input.Resources = declared.CallBroker.Resources
	old.input.EffectiveCells["change"] = config.CallBrokerCellPolicy{Tables: map[string]config.CallBrokerTablePolicy{
		"all": {Request: "ask", Ceiling: "on_request", Decision: "ask"}, "working_set": {Request: "ask", Ceiling: "on_request", Decision: "ask"}}}
	if timed {
		old.input.EffectiveCells["rename"] = config.CallBrokerCellPolicy{Tables: map[string]config.CallBrokerTablePolicy{
			"all": {Request: "ask", Ceiling: "on_request", Decision: "ask"}}}
	}
	if standing {
		p := old.input.EffectiveCells["change"]
		p.Tables["working_set"] = config.CallBrokerTablePolicy{Request: "true", Ceiling: "on_request", Decision: "run"}
		old.input.EffectiveCells["change"] = p
	}
	old.input.FrontJailID = "launch"
	scope := brokerscope.File{Source: old.input.Source, PID: os.Getpid(), Container: "fixture-container", Workspace: home,
		Form: brokerscope.FormCallRoot, Repos: append([]string(nil), old.input.Roots...)}
	id, err := brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	scope.LaunchID = id
	scopePath, err := brokerscope.Write(scope)
	if err != nil {
		t.Fatal(err)
	}
	old.input.ScopeFile = scopePath
	grantStanding := loopholegrant.Granted
	if !initialRead {
		old.input.ReadStanding = false
		old.input.ReadGrantStanding = "pending"
		grantStanding = loopholegrant.Pending
	}
	block := loopholegrant.Block{Pack: selectedPack, Workspace: home, Container: scope.Container, PID: scope.PID, LaunchID: id,
		Kinds: map[string]loopholegrant.Kind{"read": {Standing: grantStanding, Text: "read tasks"}}}
	grantData, err := loopholegrant.Merge([]byte("{}"), block)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := loopholes.StateDirFor("tasks")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	grantPath := filepath.Join(stateDir, "settings-test.json")
	if err := os.WriteFile(grantPath, grantData, 0600); err != nil {
		t.Fatal(err)
	}
	old.input.GrantFile = grantPath
	data, err := json.Marshal(old.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.BindCallInput(scopePath, scope, inputPath); err != nil {
		t.Fatal(err)
	}
	scope, err = brokerscope.ReadFile(scopePath)
	if err != nil {
		t.Fatal(err)
	}
	store := &brokerstore.Store{Dir: paths.BrokerSourceDir(old.input.Source), Source: old.input.Source, PublishRecords: true, Audit: StoreAudit(old.audit, old.input.Source)}
	if err := os.MkdirAll(store.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Working(); err != nil {
		t.Fatal(err)
	}
	h, err := newCLIHandler(inputPath, home, store, old.audit)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, _, err := h.currentAuthority(); initialRead && err != nil {
		t.Fatalf("fixture not live: %v", err)
	} else if !initialRead && err == nil {
		t.Fatal("fixture missing initial read grant became live")
	}
	return &onceFixture{h: h, home: home, marker: marker, inputPath: inputPath, scopePath: scopePath, grantPath: grantPath, userPath: userPath, store: store, scope: bound}
}

func TestSelectedCLIHostEnvironmentIsDeclarationBoundAndPrivate(t *testing.T) {
	f := selectedOnceFixture(t)
	if !strings.Contains(strings.Join(f.h.program.env, "\n"), "SYNTHETIC_HOST_TOKEN=synthetic-only-host-value") ||
		len(f.h.program.secrets) != 1 || string(f.h.program.secrets[0]) != "synthetic-only-host-value" {
		t.Fatalf("selected host-only environment was not captured by the executor")
	}
	if strings.Contains(string(mustRead(t, f.inputPath)), "synthetic-only-host-value") ||
		strings.Contains(fmt.Sprintf("%+v", f.h.classifier), "synthetic-only-host-value") {
		t.Fatal("host environment value reached the private input or classifier")
	}
}

func (f *onceFixture) call(t *testing.T, op string, argv ...string) (Verdict, CLIResult, error) {
	t.Helper()
	return f.h.serveCLI(context.Background(), op, f.h.input.FrontJailID, argv, nil)
}

func (f *onceFixture) pending(t *testing.T) []brokerstore.Request {
	t.Helper()
	snap, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snap.Requests
}
func (f *onceFixture) effects(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(f.marker)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "WRITE\n")
}

func TestSelectedCLIExactOnceFilesOnceAndSpendsOnOnlyOneActualCall(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "evaluate", "update", "a1"); err != nil || v.Verdict != "asks" || len(f.pending(t)) != 0 || f.effects(t) != 0 {
		t.Fatalf("evaluate: %v %+v", err, v)
	}
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" || len(f.pending(t)) != 1 || f.effects(t) != 0 {
		t.Fatalf("ask: %v %+v", err, v)
	}
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" || len(f.pending(t)) != 1 {
		t.Fatalf("duplicate ask: %v %+v", err, v)
	}
	id := f.pending(t)[0].ID
	if _, err := f.store.Decide(id, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if v, _, err := f.call(t, "evaluate", "update", "a1"); err != nil || v.Verdict != "runs" || f.effects(t) != 0 {
		t.Fatalf("approved evaluate: %v %+v", err, v)
	}
	var wg sync.WaitGroup
	responses := make(chan Verdict, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, _ := f.h.serveCLI(context.Background(), "act", f.h.input.FrontJailID, []string{"update", "a1"}, nil)
			responses <- v
		}()
	}
	wg.Wait()
	close(responses)
	runs := 0
	for v := range responses {
		if v.Verdict == "runs" {
			runs++
		}
	}
	if runs != 1 || f.effects(t) != 1 {
		t.Fatalf("race: %d runs, %d effects", runs, f.effects(t))
	}
	snap, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].Uses != 0 || snap.Grants[0].Issued != 1 {
		t.Fatalf("grant did not spend once: %+v", snap.Grants)
	}
}

func TestSelectedCLIExactOnceRefusesChangedAuthorityWithoutSpending(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f *onceFixture)
	}{
		{"withdraw user ceiling", func(t *testing.T, f *onceFixture) {
			if err := os.WriteFile(f.userPath, []byte(strings.ReplaceAll(string(mustRead(t, f.userPath)), "all.change", "all.blocked")), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"remove selected pack", func(t *testing.T, f *onceFixture) {
			if err := os.WriteFile(f.userPath, []byte("packs = []\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"drop root via live apply", func(t *testing.T, f *onceFixture) {
			if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopePath, f.scope, []string{"team:beta"}, nil); err != nil {
				t.Fatal(err)
			}
		}},
		{"remove this launch's grant snapshot", func(t *testing.T, f *onceFixture) {
			other := filepath.Join(filepath.Dir(f.grantPath), "other-launch.json")
			if err := os.WriteFile(other, mustRead(t, f.grantPath), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.grantPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"withdraw read ceiling before apply", func(t *testing.T, f *onceFixture) {
			content := strings.Replace(string(mustRead(t, f.userPath)), "on_request = [\"read\"]", "on_request = []", 1)
			if err := os.WriteFile(f.userPath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"withdraw read standing", func(t *testing.T, f *onceFixture) {
			raw := mustRead(t, f.grantPath)
			if err := os.WriteFile(f.grantPath, []byte(strings.Replace(string(raw), `"standing":"granted"`, `"standing":"not_allowed"`, 1)), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"change host CLI", func(t *testing.T, f *onceFixture) {
			if err := os.WriteFile(f.h.program.path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"change classifier checksum", func(t *testing.T, f *onceFixture) {
			original := f.h.classifier.Identities["classifier"]
			f.h.classifier.Identities["classifier"] = loopholedecl.CallBrokerProgramIdentity{Pack: original.Pack, Bin: original.Bin, Path: original.Path, SHA256: strings.Repeat("0", 64)}
		}},
		{"change selected host environment names", func(t *testing.T, f *onceFixture) {
			path := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
			body := string(mustRead(t, path))
			changed := strings.Replace(body, `"env_from_host":["SYNTHETIC_HOST_TOKEN"]`, `"env_from_host":["OTHER_TOKEN"]`, 1)
			if changed == body {
				t.Fatal("fixture environment declaration not found")
			}
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"change classifier arguments in selected declaration", func(t *testing.T, f *onceFixture) {
			path := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
			body := string(mustRead(t, path))
			changed := strings.Replace(body, `"classifier":["{program:classifier}"]`, `"classifier":["{program:classifier}","--different-mode"]`, 1)
			if changed == body {
				t.Fatal("fixture classifier argv was not found")
			}
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"retarget selected classifier to another built-in command", func(t *testing.T, f *onceFixture) {
			path := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
			body := string(mustRead(t, path))
			changed := strings.Replace(body, `"classifier":["{program:classifier}"]`, `"classifier":["yolo","internal","call-broker-classify","other"]`, 1)
			if changed == body {
				t.Fatal("fixture classifier argv was not found")
			}
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"change selected declaration source", func(t *testing.T, f *onceFixture) {
			path := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
			body := string(mustRead(t, path))
			changed := strings.Replace(body, `"source":"local-tasks"`, `"source":"other-tasks"`, 1)
			if changed == body {
				t.Fatal("fixture source was not found")
			}
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := selectedOnceFixture(t)
			if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
				t.Fatalf("ask: %+v %v", v, err)
			}
			id := f.pending(t)[0].ID
			if _, err := f.store.Decide(id, "pending", brokerstore.Decision{}); err != nil {
				t.Fatal(err)
			}
			tc.change(t, f)
			v, _, _ := f.call(t, "act", "update", "a1")
			if v.Verdict == "runs" || f.effects(t) != 0 {
				t.Fatalf("changed authority dispatched %+v", v)
			}
			snap, err := f.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
				t.Fatalf("changed authority spent %+v", snap.Grants)
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSelectedCLIForwarderHostApproveOnceReportsCompletedOversizedWrite(t *testing.T) {
	f := selectedOnceFixture(t)
	endpointDir := filepath.Join(f.home, "launch")
	if err := os.MkdirAll(endpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(f.home, "upstream.sock")
	endpoint := filepath.Join(endpointDir, "tasks.endpoint")
	stop := make(chan struct{})
	server := make(chan error, 1)
	front := make(chan error, 1)
	go func() { server <- hostservice.ServeFrontedUnix(f.h.Handle, socket, stop) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host daemon not ready")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { front <- svcendpoint.ServeFront(endpoint, "127.0.0.1", socket, stop) }()
	t.Cleanup(func() { close(stop); <-server; <-front })
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("authenticated front not ready")
		}
		time.Sleep(time.Millisecond)
	}
	binary := os.Getenv("YOLO_TEST_MATCHED_YOLO")
	if binary == "" {
		binary = filepath.Join(f.home, "bin", "yolo")
		goTool, err := callBrokerTestGoTool()
		if err != nil {
			t.Fatalf("locate Go compiler for fixture forwarder: %v", err)
		}
		build := callBrokerTestBuild(goTool, binary, "../../cmd/yolo")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build forwarder: %v %s", err, out)
		}
	}
	shimRoot := filepath.Join(f.home, "packroot", "test-pack")
	if err := os.MkdirAll(shimRoot, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"test-pack","contributes":[{"kind":"intercept","bin":"tasks","forward":["yolo","internal","call-broker-forward","tasks","--"]}]}`
	if err := os.WriteFile(filepath.Join(shimRoot, "pack.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	jailHome := filepath.Join(f.home, "jail-home")
	if err := os.Mkdir(jailHome, 0700); err != nil {
		t.Fatal(err)
	}
	boot := entrypoint.NewEnv(map[string]string{"JAIL_HOME": jailHome, "YOLO_PACK_ROOT": filepath.Dir(shimRoot)})
	if err := entrypoint.GenerateShims(boot); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(boot.BlockDir(), "tasks")
	key := paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug("tasks") + paths.ServiceEnvVarSuffix
	invoke := func(stdin string) (int, string) {
		cmd := exec.Command(shim, "update", "a1")
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), key+"="+endpoint, "PATH="+filepath.Dir(binary)+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		if e, ok := err.(*exec.ExitError); ok {
			return e.ExitCode(), string(out)
		}
		t.Fatal(err)
		return -1, ""
	}
	if rc, out := invoke("fixture-stdin-bytes\n"); rc != 75 || !strings.Contains(out, "yolo approve r-") || f.effects(t) != 0 {
		t.Fatalf("initial ACT rc=%d output=%q effects=%d", rc, out, f.effects(t))
	}
	requests := f.pending(t)
	if len(requests) != 1 {
		t.Fatalf("ACT filed %d requests", len(requests))
	}
	hostEnv := []string{"HOME=" + f.home, "XDG_CONFIG_HOME=", "PATH=" + filepath.Dir(binary) + ":" + os.Getenv("PATH"), "USER=fixture"}
	// The change cell declares no max_for, so a timed answer is capped at 15m (BX-D17); a
	// permanent answer is removed for every source (GB-D28, OQ-BX7).
	for _, refused := range []struct {
		extra []string
		exit  int
		says  string
	}{{[]string{"--for", "1h"}, 5, "--for 15m"}, {[]string{"--add"}, 2, "removed"}, {[]string{"--remember"}, 2, "removed"}} {
		args := append([]string{"approve", requests[0].ID}, refused.extra...)
		blocked := exec.Command(binary, args...)
		blocked.Dir, blocked.Env = f.home, hostEnv
		output, err := blocked.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != refused.exit || !strings.Contains(string(output), refused.says) {
			t.Fatalf("pack answer %v was not refused as expected: %v %s", refused.extra, err, output)
		}
	}
	host := exec.Command(binary, "approve", requests[0].ID)
	host.Dir = f.home
	host.Env = hostEnv
	if out, err := host.CombinedOutput(); err != nil || !strings.Contains(string(out), "this broker stops") || strings.Contains(string(out), "across restarts") {
		t.Fatalf("host approve failed or promised a grant past its broker: %v %s", err, out)
	}
	if rc, out := invoke("fixture-stdin-bytes\n"); rc != 0 || f.effects(t) != 1 || string(mustRead(t, filepath.Join(f.home, "stdin-received"))) != "fixture-stdin-bytes\n" {
		t.Fatalf("approved ACT rc=%d %q effects=%d or original stdin changed", rc, out, f.effects(t))
	}
	if rc, _ := invoke("fixture-stdin-bytes\n"); rc != 75 || f.effects(t) != 1 {
		t.Fatalf("repeated ACT rc=%d effects=%d", rc, f.effects(t))
	}

	// This distinct, human-approved write changes host state and then produces a
	// response above the executor cap. The client must not describe it as safely
	// retryable or expose any partial host output.
	oversizedStdin := "oversized-response\n"
	if rc, out := invoke(oversizedStdin); rc != 75 || !strings.Contains(out, "yolo approve r-") || f.effects(t) != 1 {
		t.Fatalf("oversized ACT request rc=%d output=%q effects=%d", rc, out, f.effects(t))
	}
	var oversizedRequest brokerstore.Request
	for _, request := range f.pending(t) {
		if request.State == "pending" && request.StdinDigest != "" && request.Digest != requests[0].Digest {
			oversizedRequest = request
		}
	}
	if oversizedRequest.ID == "" {
		t.Fatalf("oversized ACT did not file a distinct pending request: %+v", f.pending(t))
	}
	approveOversized := exec.Command(binary, "approve", oversizedRequest.ID)
	approveOversized.Dir, approveOversized.Env = f.home, hostEnv
	if output, err := approveOversized.CombinedOutput(); err != nil || !strings.Contains(string(output), "this broker stops") {
		t.Fatalf("host approve for oversized ACT failed: %v %s", err, output)
	}
	if rc, out := invoke(oversizedStdin); rc != 64 || len(out) > 512 ||
		!strings.Contains(out, "may already have executed") || !strings.Contains(out, "inspect host state before retrying") ||
		strings.Contains(out, "Call broker refused: Run yolo check") || strings.Contains(out, "OVER_CAP_PRIVATE_SENTINEL") ||
		strings.Contains(out, "xxxxxxxxxx") || f.effects(t) != 2 {
		t.Fatalf("completed oversized ACT rc=%d output=%q effects=%d", rc, out, f.effects(t))
	}
	event := lastPackAuditEvent(t, filepath.Join(f.home, "audit.jsonl"))
	if event.Event != "call" || event.Set != "write" || event.Outcome != "ran" || event.Reason != "host-cli-response-rejected" || event.CommandDigest != oversizedRequest.Digest ||
		len(event.Argv) != 0 || event.Stdin != nil {
		t.Fatalf("completed oversized write audit = %+v", event)
	}
	auditLog := string(mustRead(t, filepath.Join(f.home, "audit.jsonl")))
	for _, private := range []string{oversizedStdin, "OVER_CAP_PRIVATE_SENTINEL", f.home} {
		if strings.Contains(auditLog, private) {
			t.Fatalf("write audit leaked private value %q", private)
		}
	}
	snapshot, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var spentOversizedGrant bool
	for _, grant := range snapshot.Grants {
		if grant.Request == oversizedRequest.ID && grant.Uses == 0 && grant.EndedBy() == brokerstore.EndUsed {
			spentOversizedGrant = true
		}
	}
	if len(snapshot.Grants) != 2 || !spentOversizedGrant {
		t.Fatalf("completed oversized write grant was not spent exactly once: %+v", snapshot.Grants)
	}
	var spent bool
	for _, request := range snapshot.Requests {
		if request.ID == oversizedRequest.ID {
			spent = request.Spent
		}
	}
	if !spent {
		t.Fatalf("completed oversized write request not recorded spent: %+v", snapshot.Requests)
	}
	if rc, out := invoke(oversizedStdin); rc != 75 || !strings.Contains(out, "yolo approve r-") || f.effects(t) != 2 {
		t.Fatalf("retry without new approval rc=%d output=%q effects=%d", rc, out, f.effects(t))
	}
	got, err := f.store.Snapshot()
	if err != nil {
		t.Fatalf("retry snapshot: %v", err)
	}
	if len(got.Grants) != 2 {
		t.Fatalf("retry created a grant: grants=%+v", got.Grants)
	}
	for _, grant := range got.Grants {
		if grant.Request == oversizedRequest.ID && grant.Uses != 0 {
			t.Fatalf("retry reused the spent grant: %+v", grant)
		}
	}

	// A timed answer from the host CLI covers the same call for its window, more than once.
	timedStdin := "timed\n"
	if rc, out := invoke(timedStdin); rc != 75 || !strings.Contains(out, "--for 15m") {
		t.Fatalf("timed ACT request rc=%d output=%q", rc, out)
	}
	var timedRequest brokerstore.Request
	for _, request := range f.pending(t) {
		if request.State == "pending" && request.Digest != requests[0].Digest && request.Digest != oversizedRequest.Digest {
			timedRequest = request
		}
	}
	timed := exec.Command(binary, "approve", timedRequest.ID, "--for", "15m")
	timed.Dir, timed.Env = f.home, hostEnv
	if output, err := timed.CombinedOutput(); err != nil || !strings.Contains(string(output), "pack writes change on task:") {
		t.Fatalf("timed host approve: %v %s", err, output)
	}
	for i := 0; i < 2; i++ {
		if rc, out := invoke(timedStdin); rc != 0 || f.effects(t) != 3+i {
			t.Fatalf("timed ACT %d rc=%d %q effects=%d", i, rc, out, f.effects(t))
		}
	}
}
