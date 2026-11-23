package run

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

const realOlderBrokerCommit = "7883c182dd61aaf53d0b3e0c934f39ddfc569bff"

// Extraction is a read-only fixed revision, never another live candidate. Missing
// source is a fixture failure, not a skip or a modern --old behavior emulator.
func buildRealOlderBroker(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	archive := filepath.Join(root, "old.tar")
	cmd := exec.Command("git", "archive", "--format=tar", "--output="+archive, realOlderBrokerCommit, "go.mod", "go.sum", "vendor", "cmd", "internal", "packs")
	cmd.Dir = "../../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixed old source unavailable: git archive %s: %v %s; fetch the fixed revision outside this test before retrying", realOlderBrokerCommit, err, out)
	}
	cmd = exec.Command("tar", "-xf", archive, "-C", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract fixed source: %v %s", err, out)
	}
	binary := filepath.Join(root, "yolo-old")
	cmd = exec.Command("go", "build", "-mod=vendor", "-o", binary, "./cmd/yolo")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual old Main compile setup: %v %s", err, out)
	}
	classifier := filepath.Join(root, "classifier")
	cmd = exec.Command("go", "build", "-mod=vendor", "-o", classifier, "../../callbroker/testdata/classifier")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("portable private classifier compile: %v %s", err, out)
	}
	return binary, classifier
}

type realOlderBrokerFixture struct {
	workspace, home, scope, input, grant, socket, jail, effects string
	output                                                      *bytes.Buffer
}

func realOlderBroker(t *testing.T, binary, classifier string, modern ...bool) *realOlderBrokerFixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &realOlderBrokerFixture{home: home, workspace: filepath.Join(home, "workspace"), output: &bytes.Buffer{}, effects: filepath.Join(home, "effects")}
	for _, dir := range []string{f.workspace, filepath.Join(home, "bin"), filepath.Join(home, ".config", "yolo-jail"), filepath.Join(home, "fixture", "tools", "classifier"), filepath.Join(home, "fixture", "loopholes", "tasks")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	previousAttempt := defaultPodmanAttempt
	defaultPodmanAttempt = runtime.RunPodmanAttempt
	t.Cleanup(func() { defaultPodmanAttempt = previousAttempt })
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", newerYolo)
	t.Setenv("YOLO_SETUP", "podman-self")
	t.Setenv("YOLO_CONFIG", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("PATH", filepath.Join(home, "bin")+":"+os.Getenv("PATH"))
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(home, "fixture")
	write(filepath.Join(root, "pack.toml"), "name='fixture'\n[[program]]\nbin='classifier'\nvia='pack'\nfrom='tools/classifier'\nrun='classifier'\n[[loophole]]\nfrom='loopholes/tasks'\n", 0600)
	write(filepath.Join(root, "loopholes", "tasks", "manifest.jsonc"), `{"name":"tasks","transport":"loopback-tls","access":"private fixture tasks","grants":[{"kind":"read","text":"read private tasks"}],"call_broker":{"source":"fixture-tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"executor":"core","scope":{"roots":"declared"},"resource":[{"type":"project"},{"type":"pr","parent":"project"}],"cell":[{"name":"change","tables":["all"]}]}}`, 0600)
	data, err := os.ReadFile(classifier)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "classifier", "classifier"), data, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(home, ".config", "yolo-jail", "config.toml"), "packs=['src/fixture']\n[pack_sources]\nsrc='"+home+"'\n[allow.\"src/fixture\"]\non_request=['read']\n[allow.fixture-tasks]\non_request=['all.change']\n", 0600)
	write(filepath.Join(home, "bin", "tasks"), "#!/bin/sh\nprintf 'effect\\n' >> \"$HOME/effects\"\nprintf 'done\\n'\n", 0700)
	write(filepath.Join(home, "bin", "podman"), "#!/bin/sh\ncase \"$1\" in info) printf '{}\\n';; ps) printf 'running\\n';; inspect) printf 'YOLO_VERSION="+olderYolo+"\\n';; *) exit 1;; esac\n", 0700)
	f.requests(t, "true", "true", "")
	initial := mustOldRead(t, filepath.Join(f.workspace, config.WorkspaceTOMLConfigName))
	write(filepath.Join(f.workspace, config.WorkspaceTOMLConfigName), strings.ReplaceAll(string(initial), "['team:alpha','team:beta']", "['team:alpha']"), 0600)
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	cfg, err := config.LoadConfig(f.workspace, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: filepath.Join(root, "loopholes", "tasks"), Pack: "src/fixture", PackRoot: root, HostExecApproved: true}})
	read, err := config.LoadWorkspaceConfig(f.workspace, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteWorkspaceBootBaseline(f.workspace, "podman-self", read); err != nil {
		t.Fatal(err)
	}
	if err := config.RecordApproval(f.workspace, read, nil); err != nil {
		t.Fatal(err)
	}
	var lp *loopholes.Loophole
	for _, item := range loopholes.NewHostSet(nil).All() {
		if item.Name == "tasks" {
			lp = item
		}
	}
	if lp == nil || lp.CallBroker == nil {
		t.Fatal("selected private broker declaration not discovered")
	}
	f.jail = runtime.JailName(f.workspace, "podman-self")
	id, err := brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	f.scope, err = brokerscope.Write(brokerscope.File{Source: "fixture-tasks", LaunchID: id, Container: f.jail, Workspace: f.workspace, PID: os.Getpid(), Form: brokerscope.FormCallRoot, Repos: []string{"team:alpha"}, LiveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := loopholegrant.Merge([]byte("{}"), loopholegrant.Block{Pack: "src/fixture", Workspace: f.workspace, Container: f.jail, LaunchID: id, PID: os.Getpid(), Kinds: map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted, Text: "read private tasks"}}})
	if err != nil {
		t.Fatal(err)
	}
	var cleanup func()
	f.grant, cleanup, err = loopholes.WritePrivateSettingsSnapshot(lp, grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	cells, err := config.EffectiveCallBrokerCells("fixture-tasks", lp.CallBroker, cfg, read)
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: f.workspace, Stderr: f.output, scopeFiles: map[string]string{"tasks": f.scope}, settingsSnapshots: map[string]*ownedSettingsSnapshot{"tasks": {path: f.grant}}, callBrokerCells: map[string]map[string]config.CallBrokerCellPolicy{"tasks": cells}, approvedLoopholeGrants: map[string]map[string]config.LoopholeGrantStanding{"tasks": {"read": {Standing: config.LoopholeGrantGranted}}}}
	fillDefaults(o)
	if len(modern) != 0 && modern[0] {
		o.AcceptConfigChanges = true
		if !o.runConfigGate(cfg, "podman", nil) {
			t.Fatalf("actual modern initial gate: %s", f.output.String())
		}
	}
	o.recordApprovedScopes(map[string][]config.ScopeRepo{"tasks": {{Repo: "team:alpha"}}}, nil, nil)
	o.launchInputMountArgv = assembledPrivateFixtureMounts(t, f.workspace, f.jail)
	t.Setenv("HOME", home)
	previousBind := bindCallInput
	bindCallInput = func(path string, expected brokerscope.File, input string) error {
		err := previousBind(path, expected, input)
		if err != nil {
			t.Logf("real bind setup failed: scope=%q input=%q file=%+v error=%v", path, input, expected, err)
		}
		return err
	}
	t.Cleanup(func() { bindCallInput = previousBind })
	if !o.writeLaunchInput(lp, f.jail) {
		t.Fatalf("normal private publisher: %s", f.output.String())
	}
	bindCallInput = previousBind
	f.input = o.launchInputFiles["tasks"]
	// The fixed old daemon receives the ordinary /2 producer carrier, not a new
	// baseline/proof record or an unknown-shape emulation flag.
	f.socket = filepath.Join(testtmp.ShortDir(), "old.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	cmd := exec.CommandContext(ctx, binary, "internal", "daemon", "call-broker", "--socket", f.socket, "--launch-input", f.input)
	cmd.Dir = f.workspace
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ready, scanned := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(scanned)
		scanner := bufio.NewScanner(stderr)
		first := true
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(&logs, line)
			if first && strings.Contains(line, "serving frame protocol") {
				close(ready)
				first = false
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
			<-scanned
			if waitErr != nil {
				t.Errorf("owned actual old Main exit: %v %s", waitErr, logs.String())
			}
		case <-time.After(30 * time.Second):
			cancel()
			<-done
			<-scanned
			t.Error("owned old Main failed to stop")
		}
		cancel()
	})
	select {
	case <-ready:
	case <-done:
		<-scanned
		cancel()
		t.Fatalf("old Main admission failed (setup, not behavior RED): %v %s", waitErr, logs.String())
	case <-ctx.Done():
		t.Fatal("actual old readiness event missing")
	}
	t.Chdir(f.workspace)
	return f
}

func (f *realOlderBrokerFixture) requests(t *testing.T, read, change, extra string) {
	t.Helper()
	body := "[call_roots]\nfixture-tasks=['team:alpha','team:beta']\n[requests.\"src/fixture\"]\nread=" + read + "\n[requests.fixture-tasks.all]\nchange=" + change + "\n" + extra
	if err := os.WriteFile(filepath.Join(f.workspace, config.WorkspaceTOMLConfigName), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *realOlderBrokerFixture) call(t *testing.T) string {
	t.Helper()
	conn, err := net.Dial("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	preamble, _ := json.Marshal(map[string]any{"v": 1, "jail_id": filepath.Base(hostServiceSocketsDir(f.jail, false)), "service": "tasks"})
	if err := frameproto.WriteRequest(conn, preamble); err != nil {
		t.Fatal(err)
	}
	if err := frameproto.WriteRequest(conn, []byte(`{"op":"act","argv":["update-pr"]}`)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		if frame.StreamID == frameproto.StreamExit {
			break
		}
		out.Write(frame.Payload)
	}
	return out.String()
}

func (f *realOlderBrokerFixture) apply(t *testing.T) (int, string) {
	t.Helper()
	var out bytes.Buffer
	rc := ConfigApply([]string{"--accept-config-changes"}, &out, &out, false)
	return rc, out.String()
}

func TestSnapshotApplyRealOldBroker(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an older broker tree and two yolo binaries; the fast pins are in liveapplypins_test.go")
	}
	binary, classifier := buildRealOlderBroker(t)
	current := filepath.Join(t.TempDir(), "yolo-current")
	build := exec.Command("go", "build", "-mod=vendor", "-o", current, "../../../cmd/yolo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("current Main build: %v %s", err, out)
	}
	t.Run("current-real-publisher-roots-only", func(t *testing.T) {
		f := realOlderBroker(t, current, classifier, true)
		beforeInput := mustOldRead(t, f.input)
		if next := f.call(t); !strings.Contains(next, "done") {
			t.Fatalf("current initial request: %s", next)
		}
		f.requests(t, "true", "true", "")
		rc, out := f.apply(t)
		if rc != 0 || !strings.Contains(out, "applied now") || !bytes.Equal(beforeInput, mustOldRead(t, f.input)) {
			t.Fatalf("actual modern delivered asks roots-only: rc=%d %s", rc, out)
		}
		if next := f.call(t); !strings.Contains(next, "done") {
			t.Fatalf("current next request: %s", next)
		}
	})
	for _, adverse := range []string{"cell-false", "scoped-false", "missing-baseline", "wrong-input-pin", "wrong-lease", "null-asks", "copied-baseline", "symlink-baseline", "malformed-snapshot", "missing-scope"} {
		t.Run("current-"+adverse, func(t *testing.T) {
			f := realOlderBroker(t, current, classifier, true)
			sf, err := brokerscope.ReadFile(f.scope)
			if err != nil {
				t.Fatal(err)
			}
			baseline := brokerscope.DeliveredBaselinePath(sf)
			switch adverse {
			case "missing-baseline":
				if err := os.Remove(baseline); err != nil {
					t.Fatal(err)
				}
			case "symlink-baseline":
				target := baseline + ".target"
				if err := os.Rename(baseline, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, baseline); err != nil {
					t.Fatal(err)
				}
			case "malformed-snapshot":
				var raw map[string]any
				if err := json.Unmarshal(mustOldRead(t, f.scope), &raw); err != nil {
					t.Fatal(err)
				}
				raw["policy_snapshot"] = map[string]any{"version": "unknown"}
				data, _ := json.Marshal(raw)
				if err := os.WriteFile(f.scope, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-scope":
				if err := os.Remove(f.scope); err != nil {
					t.Fatal(err)
				}
			case "wrong-input-pin", "wrong-lease", "null-asks", "copied-baseline":
				var b brokerscope.DeliveredBaseline
				if err := json.Unmarshal(mustOldRead(t, baseline), &b); err != nil {
					t.Fatal(err)
				}
				switch adverse {
				case "wrong-input-pin":
					b.Binding.InputDigest = strings.Repeat("a", 64)
				case "wrong-lease":
					b.Start = strings.Repeat("b", 32)
				case "null-asks":
					b.Asks.Requests = json.RawMessage(`null`)
				case "copied-baseline":
					b.Binding.Workspace = filepath.Dir(f.workspace)
				}
				data, _ := json.Marshal(b)
				if err := os.WriteFile(baseline, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			oldInput, oldGrant := mustOldRead(t, f.input), mustOldRead(t, f.grant)
			var oldScope []byte
			if adverse != "missing-scope" {
				oldScope = mustOldRead(t, f.scope)
			}
			cell, extra := "true", ""
			if adverse == "cell-false" {
				cell = "false"
			}
			if adverse == "scoped-false" {
				extra = "[scopes.withdraw]\nsource='fixture-tasks'\nresource='team:alpha'\n[scopes.withdraw.requests.all]\nchange=false\n"
			}
			f.requests(t, "true", cell, extra)
			rc, out := f.apply(t)
			if rc == 0 || !strings.Contains(out, "old policy") || !strings.Contains(out, "yolo stop") {
				t.Fatalf("unproven/changed modern asks applied: rc=%d %s", rc, out)
			}
			if !bytes.Equal(oldInput, mustOldRead(t, f.input)) || !bytes.Equal(oldGrant, mustOldRead(t, f.grant)) || oldScope != nil && !bytes.Equal(oldScope, mustOldRead(t, f.scope)) {
				t.Fatal("unknown or changed delivered asks mutated source authority")
			}
			if adverse == "missing-scope" {
				if _, err := os.Lstat(f.scope); !os.IsNotExist(err) {
					t.Fatal("apply recreated missing authority")
				}
			}
			if adverse != "malformed-snapshot" && adverse != "missing-scope" {
				if next := f.call(t); !strings.Contains(next, "done") {
					t.Fatalf("non-authoritative baseline became request permission: %s", next)
				}
			}
		})
	}
	t.Run("old-unknown-noop-still-pending", func(t *testing.T) {
		f := realOlderBroker(t, binary, classifier)
		scope, input, grant := mustOldRead(t, f.scope), mustOldRead(t, f.input), mustOldRead(t, f.grant)
		rc, out := f.apply(t)
		if rc == 0 || !strings.Contains(out, "old policy") || !strings.Contains(out, "yolo stop") || !bytes.Equal(scope, mustOldRead(t, f.scope)) || !bytes.Equal(input, mustOldRead(t, f.input)) || !bytes.Equal(grant, mustOldRead(t, f.grant)) {
			t.Fatalf("unknown no-op falsely reported complete policy adoption: rc=%d %s", rc, out)
		}
		if next := f.call(t); !strings.Contains(next, "done") {
			t.Fatalf("unknown baseline became authority: %s", next)
		}
	})
	t.Run("G6d-unknown-without-delivered-view", func(t *testing.T) {
		f := realOlderBroker(t, binary, classifier)
		before := mustOldRead(t, f.scope)
		if out := f.call(t); !strings.Contains(out, "done") {
			t.Fatalf("actual old initial effect: %s", out)
		}
		f.requests(t, "true", "true", "")
		rc, out := f.apply(t)
		// Genuine shipped rc0/+root/effects2 baseline was frozen before this
		// production edit. Old /2 lacks raw read-origin/model/scope provenance;
		// this UNKNOWN result is intentionally NOT G6d parity acceptance.
		if rc == 0 || !strings.Contains(out, "old policy") || !bytes.Equal(before, mustOldRead(t, f.scope)) {
			t.Fatalf("unknown asks called roots-only: rc=%d %s", rc, out)
		}
		wantBrokerSkewNote(t, out, olderYolo, newerYolo)
		if next := f.call(t); !strings.Contains(next, "done") {
			t.Fatalf("actual old next effect: %s", next)
		}
	})
	t.Run("real-old-ignores-unknown-v1-locator-not-proof", func(t *testing.T) {
		f := realOlderBroker(t, binary, classifier)
		var raw map[string]any
		if err := json.Unmarshal(mustOldRead(t, f.scope), &raw); err != nil {
			t.Fatal(err)
		}
		raw["policy_snapshot"] = map[string]any{"version": "not-consumed-by-old"}
		data, _ := json.Marshal(raw)
		if err := os.WriteFile(f.scope, data, 0600); err != nil {
			t.Fatal(err)
		}
		before, grant := mustOldRead(t, f.scope), mustOldRead(t, f.grant)
		if next := f.call(t); !strings.Contains(next, "done") {
			t.Fatalf("actual old did not ignore unknown v1 key: %s", next)
		}
		f.requests(t, "true", "false", "")
		rc, out := f.apply(t)
		if rc == 0 || !strings.Contains(out, "old policy") || !bytes.Equal(before, mustOldRead(t, f.scope)) || !bytes.Equal(grant, mustOldRead(t, f.grant)) {
			t.Fatalf("old ignored locator counted as consumption: rc=%d %s", rc, out)
		}
		if next := f.call(t); !strings.Contains(next, "done") {
			t.Fatalf("actual old frozen asks changed: %s", next)
		}
	})
	for _, change := range []struct {
		name, read, cell, extra string
		ahead                   bool
	}{
		{"source-read-and-cell-false", "false", "false", "", false},
		{"cell-ask", "true", "'ask'", "", false},
		{"cell-false-latest-approval-ahead", "true", "false", "", true},
		{"scoped-false", "true", "true", "[scopes.withdraw]\nsource='fixture-tasks'\nresource='team:alpha'\n[scopes.withdraw.requests.all]\nchange=false\n", false},
	} {
		t.Run("G6c-"+change.name, func(t *testing.T) {
			f := realOlderBroker(t, binary, classifier)
			oldScope, oldInput, oldGrant := mustOldRead(t, f.scope), mustOldRead(t, f.input), mustOldRead(t, f.grant)
			f.requests(t, change.read, change.cell, change.extra)
			if change.name == "cell-ask" {
				path := filepath.Join(f.workspace, config.WorkspaceTOMLConfigName)
				data := append([]byte("agents_md_extra='normal-other-key'\n"), mustOldRead(t, path)...)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if change.ahead {
				current, err := config.LoadWorkspaceConfig(f.workspace, false, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
				if err := config.RecordApproval(f.workspace, current, nil); err != nil {
					t.Fatal(err)
				}
			}
			rc, out := f.apply(t)
			if rc == 0 || !strings.Contains(out, "old policy") || !strings.Contains(out, "yolo stop") {
				t.Fatalf("old asks apply falsely admitted: rc=%d %s", rc, out)
			}
			if !bytes.Equal(oldScope, mustOldRead(t, f.scope)) || !bytes.Equal(oldInput, mustOldRead(t, f.input)) || !bytes.Equal(oldGrant, mustOldRead(t, f.grant)) {
				t.Fatal("changed old asks mutated source authority")
			}
			if next := f.call(t); !strings.Contains(next, "done") {
				t.Fatalf("old asks next request changed: %s", next)
			}
			if change.name == "cell-ask" {
				boot, ok, err := config.ReadWorkspaceBootBaseline(f.workspace, "podman-self")
				if err != nil || !ok {
					t.Fatal(err)
				}
				if value, _ := boot.Get("agents_md_extra"); value != "normal-other-key" {
					t.Fatalf("blocked source suppressed normal other-key effect: %s", out)
				}
			}
		})
	}
}

func mustOldRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var _ = jsonx.NewOrderedMap
