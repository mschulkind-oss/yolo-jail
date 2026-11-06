package run

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func TestSelectedHTTPClientPreparedInputUsesActualLaunchInputProducer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TASKS_TOKEN", "synthetic-only-host-secret")
	writeRawUserConfig(t, "config.jsonc", `{"allow":{"tasks":{"on_request":["all.update"]}}}`)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(`{"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"all":{"update":"ask"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	packTree := t.TempDir()
	packDir := filepath.Join(packTree, "fixture")
	if err := os.MkdirAll(packDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"fixture","contributes":[` +
		`{"kind":"program","bin":"fixture-http-client","via":"npm","package":"fixture-http-client"},` +
		`{"kind":"env","bin":"fixture-http-client","served_by":"tasks","vars":{"FIXTURE_API_TOKEN":"{caller_token}","FIXTURE_BASE_URL":"https://{listen}","SSL_CERT_FILE":"{client_trust}"}}]}`
	if err := os.WriteFile(filepath.Join(packDir, packdecl.ManifestLegacyJSONCName), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	selectedPack, problems := packload.LoadDir(packDir, "fixture")
	if selectedPack == nil || len(problems) != 0 {
		t.Fatalf("selected synthetic fixture pack: %v", problems)
	}
	if err := packload.WritePackTreeRecord(packTree, []*packload.Pack{selectedPack}); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "loopholes", "tasks")
	if err := os.MkdirAll(module, 0700); err != nil {
		t.Fatal(err)
	}
	frontDecl := `name = "tasks"
transport = "loopback-tls"
[call_broker]
source = "tasks"
front = "http"
classifier = ["yolo", "internal", "call-broker-classify", "tasks"]
env_from_host = ["TASKS_TOKEN"]
read_over_post = ["/graphql"]
[call_broker.scope]
roots = "declared"
[[call_broker.resource]]
type = "project"
[[call_broker.cell]]
name = "update"
tables = ["all"]
[call_broker.http]
origin = "https://fixture.example"
request_headers = ["Content-Type"]
[call_broker.http.credential]
source = "host_env"
key = "TASKS_TOKEN"
header = "Authorization"
prefix = "Bearer "
`
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(frontDecl), 0600); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: filepath.Dir(module), HostExecApproved: true}})
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	var output strings.Builder
	o := &Options{Workspace: workspace, Stderr: &output, AcceptConfigChanges: true, Setup: setup.Setup{Name: "podman-self"}, runtime: "podman", packTree: packTree}
	fillDefaults(o)
	merged := mustPolicyJSON(t, `{"allow":{"tasks":{"on_request":["all.update"]}},"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"all":{"update":"ask"}}}}`)
	if !o.checkConfigChanges(merged, "podman") {
		t.Fatalf("gate rejected synthetic fixture: %s", output.String())
	}
	selected := loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All()[0]
	private := filepath.Join(t.TempDir(), "host-state")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	oldStateDir := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string {
		if name == selected.Name {
			return private
		}
		return oldStateDir(name)
	}
	t.Cleanup(func() { loopholes.StateDirFor = oldStateDir })
	cname := "private-http-selected-fixture"
	argv := assembledPrivateFixtureMounts(t, workspace, cname)
	plan, err := o.keeperPlanFor(merged, "podman", cname, stagedPacks{root: packTree, packs: []*packload.Pack{selectedPack}},
		[]string{selected.Name}, nil, nil, "", "", argv, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Setup != "podman-self" {
		t.Fatalf("keeper plan lost selected setup identity: %q", plan.Setup)
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(planBytes), "synthetic-only-host-secret") {
		t.Fatal("host keeper plan contains the host-only synthetic token")
	}
	producer := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil).o
	if producer.SetupName != "podman-self" {
		t.Fatalf("keeper did not restore setup identity for the host producer: %q", producer.SetupName)
	}
	registry := launchInputProducers()
	selectedProducer := registry["call-broker-scope"]
	produce := selectedProducer.Produce
	producerCalls := 0
	selectedProducer.Produce = func(ctx LaunchInputContext) ([]byte, error) {
		producerCalls++
		return produce(ctx)
	}
	producer.launchInputProducers = map[string]LaunchInputProducer{"call-broker-scope": selectedProducer}
	producer.writeScopeFiles(cname, []*loopholes.Loophole{selected})
	for _, tc := range []struct {
		name   string
		source string
	}{
		{name: "parent", source: private},
		{name: "alias", source: func() string {
			alias := filepath.Join(t.TempDir(), "prepared-state-alias")
			if err := os.Symlink(private, alias); err != nil {
				t.Fatal(err)
			}
			return alias
		}()},
	} {
		t.Run("mount-alias-"+tc.name, func(t *testing.T) {
			producer.launchInputMountArgv = append(append([]string(nil), argv...), "--volume", tc.source+":/shadow:ro")
			if producer.writeLaunchInput(selected, cname) {
				t.Fatal("selected HTTP producer accepted a mounted private state parent")
			}
			if producerCalls != 0 {
				t.Fatalf("mounted private state reached launch input producer %d times", producerCalls)
			}
			if _, err := os.Lstat(filepath.Join(private, "replicas", cname+".json.prepared")); !os.IsNotExist(err) {
				t.Fatalf("mounted private state refusal staged a prepared record: %v", err)
			}
		})
	}
	producer.launchInputMountArgv = argv
	if producer.scopeFiles[selected.Name] == "" || !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("selected keeper producer refused: %s", output.String())
	}
	if producerCalls != 1 {
		t.Fatalf("actual selected producer called %d times, want once", producerCalls)
	}
	inputPath := producer.launchInputFiles[selected.Name]
	inputBytes, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inputBytes), "synthetic-only-host-secret") {
		t.Fatal("private host launch input contains the host-only synthetic token")
	}
	var input CallBrokerLaunchInput
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		t.Fatal(err)
	}
	if input.HTTPFrontGeneration == "" {
		t.Fatal("actual writeLaunchInput producer omitted the selected private generation reference")
	}
	preparedBytes, err := os.ReadFile(inputPath + ".prepared")
	if err != nil {
		t.Fatalf("producer did not publish the prepared identity before returning: %v", err)
	}
	prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(preparedBytes)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := brokerscope.ReadFile(input.ScopeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := callbrokerinput.ValidatePreparedHTTPClientLaunch(prepared, input, scope.Container, scope.LaunchID, scope.Source, scope.Repos); err != nil {
		t.Fatalf("actual launch producer output failed the host's pre-lease identity validator: %v", err)
	}
	if strings.Contains(string(preparedBytes), "synthetic-only-host-secret") {
		t.Fatal("prepared identity contains the host-only synthetic token")
	}
	if prepared.Generation != input.HTTPFrontGeneration || prepared.Container != cname || prepared.Runtime != "podman" || prepared.Setup != "podman-self" || prepared.PackTree != packTree || prepared.Loophole != selected.Name || prepared.Pack != selected.Pack || prepared.Front != "http" || len(prepared.Clients) != 1 {
		t.Fatalf("producer's prepared identity does not bind the selected launch: %+v", prepared)
	}
	for _, forbidden := range []string{"TASKS_TOKEN", "broker_start", "broker_pid", "listen_address", "certificate", "trust_path", "readiness"} {
		if strings.Contains(string(preparedBytes), forbidden) {
			t.Errorf("prepared identity contains forbidden value/field %q", forbidden)
		}
	}
	if mode := mustFileMode(t, inputPath+".prepared"); mode != 0600 {
		t.Errorf("prepared identity mode=%o, want 0600", mode)
	}
	failedCname := cname + "-producer-failure"
	failedPlan := *plan
	failedPlan.Cname = failedCname
	failedPlan.RunCmd = assembledPrivateFixtureMounts(t, workspace, failedCname)
	failed := newKeeper(&failedPlan, KeeperSeams{}, nil, nil, nil, nil).o
	failed.writeScopeFiles(failedCname, []*loopholes.Loophole{selected})
	failedProducerCalls := 0
	failed.launchInputProducers = map[string]LaunchInputProducer{"call-broker-scope": {
		StateDir: selectedProducer.StateDir,
		Produce: func(LaunchInputContext) ([]byte, error) {
			failedProducerCalls++
			return nil, errors.New("synthetic producer error")
		},
	}}
	if failed.writeLaunchInput(selected, failedCname) || failedProducerCalls != 1 {
		t.Fatalf("producer failure did not stop the selected launch input: calls=%d", failedProducerCalls)
	}
	if _, err := os.Lstat(filepath.Join(private, "replicas", failedCname+".json.prepared")); !os.IsNotExist(err) {
		t.Fatalf("producer failure published a prepared record: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(private, "replicas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".prepared-http-client-") {
			t.Fatalf("producer failure left unpublished identity temp %q", entry.Name())
		}
	}
}

func TestForgetGoneRemovesOnlyItsPreparedHTTPGeneration(t *testing.T) {
	const cname = "yolo-prepared-http-cleanup"
	o, _, _ := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	private := filepath.Join(t.TempDir(), "replicas")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(private, cname+".json.prepared")
	if err := os.WriteFile(path, []byte("prepared"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	o.preparedHTTPGenerationOwners = map[string]preparedHTTPGenerationOwner{path: {path: path, file: info, container: cname, generation: strings.Repeat("a", 32)}}
	o.forgetGoneContainer(cname, "podman", "")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("known-gone container retained its prepared identity: %v", err)
	}
}

func TestPreparedHTTPGenerationCleanupPreservesAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generation.prepared")
	if err := os.WriteFile(path, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	owned, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("new generation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if removePreparedHTTPGenerationFile(preparedHTTPGenerationOwner{path: path, file: owned}) {
		t.Fatal("cleanup reported removing a replaced generation")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new generation" {
		t.Fatalf("cleanup removed a replacement generation: %q %v", data, err)
	}
}

func mustFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
