package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	cells, err := packload.ResolvePrivateProgramEnv([]*packload.Pack{selectedPack}, selected.Name, selected.Pack, true)
	if err != nil || len(cells) != 1 {
		t.Fatalf("selected private client cells: %+v %v", cells, err)
	}
	o.privateClientEntrypoints = checkedPrivateClientsForCells(cells)
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
	ownershipSeenBeforeProduce := false
	selectedProducer.Produce = func(ctx LaunchInputContext) ([]byte, error) {
		producerCalls++
		owner, _, present, ownerErr := readPreparedHTTPOwnership(cname)
		if ownerErr == nil && present && owner.Container == cname && owner.Runtime == "podman" && owner.Setup == "podman-self" && owner.PackTree == packTree && len(owner.Generations) == 1 {
			ownershipSeenBeforeProduce = true
		} else {
			t.Errorf("prepared HTTP ownership was not durable before input production: present=%v err=%v record=%+v", present, ownerErr, owner)
		}
		return produce(ctx)
	}
	producer.launchInputProducers = map[string]LaunchInputProducer{"call-broker-scope": selectedProducer}
	producer.writeScopeFiles(cname, []*loopholes.Loophole{selected})
	producer.launchInputMountArgv = argv
	for _, tc := range []struct {
		name   string
		source string
	}{
		{"parent", private}, {"alias", func() string {
			alias := filepath.Join(t.TempDir(), "prepared-state-alias")
			if err := os.Symlink(private, alias); err != nil {
				t.Fatal(err)
			}
			return alias
		}()}, {"ownership record", preparedHTTPOwnershipPath(cname)},
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
	if producerCalls != 1 || !ownershipSeenBeforeProduce {
		t.Fatalf("actual selected producer called %d times with ownershipSeen=%v, want once after owner publication", producerCalls, ownershipSeenBeforeProduce)
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
	entrypointsBytes, err := os.ReadFile(inputPath + ".prepared.entrypoints")
	if err != nil {
		t.Fatalf("producer did not publish the entrypoints record beside the prepared generation: %v", err)
	}
	entrypoints, err := callbrokerinput.DecodePreparedHTTPClientEntrypoints(entrypointsBytes)
	if err == nil {
		err = callbrokerinput.ValidatePreparedHTTPClientEntrypoints(entrypoints, prepared)
	}
	if err != nil || entrypoints.Clients[0].Binding.Generation != prepared.Generation {
		t.Fatalf("published entrypoints do not join the prepared generation: %+v %v", entrypoints, err)
	}
	withEnv, err := producer.insertPrivateClientEnv([]string{"podman", "run", "--rm", "image:ref", "cmd"}, "image:ref")
	wantEnv, _ := entrypoint.MarshalPrivateClients([]entrypoint.PrivateClientBinding{entrypoints.Clients[0].Binding})
	if err != nil || strings.Join(withEnv, " ") != "podman run --rm -e "+entrypoint.PrivateHTTPClientsEnv+"="+wantEnv+" image:ref cmd" {
		t.Fatalf("keeper did not hand the container its published binding: %q %v", withEnv, err)
	}
	unchecked := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil).o
	unchecked.privateClientEntrypoints = nil
	uncheckedCname := cname + "-unchecked"
	unchecked.writeScopeFiles(uncheckedCname, []*loopholes.Loophole{selected})
	// assembledPrivateFixtureMounts moves HOME (t.Setenv): keep this launch's HOME for the steps
	// after this one, whose ownership records live under it.
	launchHome := os.Getenv("HOME")
	unchecked.launchInputMountArgv = assembledPrivateFixtureMounts(t, workspace, uncheckedCname)
	t.Setenv("HOME", launchHome)
	unchecked.launchInputProducers = producer.launchInputProducers
	callsBefore := producerCalls
	if unchecked.writeLaunchInput(selected, uncheckedCname) || producerCalls != callsBefore {
		t.Fatal("a selected client with no checked delivery reached the launch input producer")
	}
	for _, leaf := range []string{".prepared", ".prepared.entrypoints"} {
		if _, err := os.Lstat(filepath.Join(private, "replicas", uncheckedCname+".json"+leaf)); !os.IsNotExist(err) {
			t.Fatalf("an unchecked client left %s: %v", leaf, err)
		}
	}
	beforeInput, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	beforePrepared, err := os.ReadFile(inputPath + ".prepared")
	if err != nil {
		t.Fatal(err)
	}
	preparedInfo, err := os.Lstat(inputPath + ".prepared")
	if err != nil {
		t.Fatal(err)
	}
	preparedAlias := filepath.Join(t.TempDir(), "prepared-alias")
	if err := os.Symlink(inputPath+".prepared", preparedAlias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, source string }{
		{"exact sidecar", inputPath + ".prepared"}, {"symlink alias", preparedAlias},
	} {
		producer.launchInputMountArgv = append(append([]string(nil), argv...), "--volume", tc.source+":/prepared:ro")
		if producer.writeLaunchInput(selected, cname) {
			t.Fatalf("producer accepted a jail mount of the %s", tc.name)
		}
		if producerCalls != 1 {
			t.Fatalf("%s mount reached producer: calls=%d", tc.name, producerCalls)
		}
		if got, err := os.ReadFile(inputPath); err != nil || string(got) != string(beforeInput) {
			t.Fatalf("%s mount partially replaced the launch input: err=%v", tc.name, err)
		}
		if got, err := os.ReadFile(inputPath + ".prepared"); err != nil || string(got) != string(beforePrepared) {
			t.Fatalf("%s mount changed the prepared sidecar: err=%v", tc.name, err)
		}
		if now, err := os.Lstat(inputPath + ".prepared"); err != nil || !os.SameFile(preparedInfo, now) {
			t.Fatalf("%s mount replaced the prepared sidecar inode: err=%v", tc.name, err)
		}
	}
	producer.launchInputMountArgv = append(append([]string(nil), argv...), "--volume", producer.preparedHTTPOwnershipFile+":/identity:ro")
	if err := producer.checkSpawnLaunchInputMounts(producer.launchInputMountArgv); err == nil {
		t.Fatal("final spawn topology allowed a mounted prepared-HTTP ownership record")
	}
	producer.launchInputMountArgv = append(append([]string(nil), argv...), "--volume", inputPath+".prepared:/prepared:ro")
	if err := producer.checkSpawnLaunchInputMounts(producer.launchInputMountArgv); err == nil {
		t.Fatal("final spawn topology allowed a mounted prepared sidecar")
	}
	launchInputMu.Lock()
	_, tracked := producer.preparedHTTPGenerationOwners[inputPath+".prepared"]
	launchInputMu.Unlock()
	if !tracked {
		t.Fatal("actual producer did not transfer sidecar ownership to the keeper")
	}
	spawnMarker := filepath.Join(t.TempDir(), "runtime-spawned")
	dispatchPlan := *plan
	dispatchPlan.Sealed = true
	dispatchPlan.Services, dispatchPlan.Forwards = nil, nil
	dispatchPlan.PackTree, dispatchPlan.Packs = "", nil
	dispatchPlan.Config = json.RawMessage(`{}`)
	dispatchPlan.Uncounted = true
	dispatchPlan.RunCmd = []string{"/bin/sh", "-c", "touch " + spawnMarker, "--volume", producer.preparedHTTPOwnershipFile + ":/identity:ro"}
	dispatch := newKeeper(&dispatchPlan, KeeperSeams{}, nil, nil, nil, nil)
	dispatch.o.preparedHTTPOwnershipFile = producer.preparedHTTPOwnershipFile
	dispatch.o.LookPath = func(string) (string, bool) { return "", false }
	if rc := dispatch.run(); rc != 1 {
		t.Fatalf("keeper dispatch with a prepared-ownership bind returned %d, want pre-spawn refusal", rc)
	}
	if _, err := os.Stat(spawnMarker); !os.IsNotExist(err) {
		t.Fatalf("keeper dispatch spawned the runtime with a mounted prepared-ownership record: %v", err)
	}
	// The keeper hands the container its bound clients, or starts none: bindings it cannot place
	// before the image refuse before the runtime is spawned.
	unbindable := dispatchPlan
	unbindable.RunCmd = []string{"/bin/sh", "-c", "touch " + spawnMarker}
	unbindable.ImageRef = "image-not-in-argv"
	unbound := newKeeper(&unbindable, KeeperSeams{}, nil, nil, nil, nil)
	unbound.o.LookPath = func(string) (string, bool) { return "", false }
	unbound.o.privateClientBindings = []entrypoint.PrivateClientBinding{entrypoints.Clients[0].Binding}
	if rc := unbound.run(); rc != 1 {
		t.Fatalf("keeper with unplaceable private client bindings returned %d, want pre-spawn refusal", rc)
	}
	if _, err := os.Stat(spawnMarker); !os.IsNotExist(err) {
		t.Fatalf("keeper spawned the runtime without binding its private clients: %v", err)
	}
	// B1: a checked private client whose binding was never committed (its front's launch input was
	// refused, or never written) stops the keeper before the container, naming the cause and the next
	// step; the container's ordinary launcher would otherwise run it.
	uncommitted := dispatchPlan
	uncommitted.RunCmd = []string{"/bin/sh", "-c", "touch " + spawnMarker, "image:ref"}
	uncommitted.ImageRef = "image:ref"
	uncommitted.PrivateClientEntrypoints = plan.PrivateClientEntrypoints
	if len(uncommitted.PrivateClientEntrypoints) != 1 {
		t.Fatalf("keeper plan did not carry the checked delivery: %+v", plan.PrivateClientEntrypoints)
	}
	var uncommittedOut strings.Builder
	stopped := newKeeper(&uncommitted, KeeperSeams{}, nil, nil, nil, nil)
	stopped.o.Stderr = &uncommittedOut
	stopped.o.LookPath = func(string) (string, bool) { return "", false }
	if rc := stopped.run(); rc != 1 {
		t.Fatalf("keeper with a checked but uncommitted private client returned %d, want pre-spawn refusal", rc)
	}
	if _, err := os.Stat(spawnMarker); !os.IsNotExist(err) {
		t.Fatalf("keeper spawned the runtime with an uncommitted private client: %v", err)
	}
	if got := uncommittedOut.String(); !strings.Contains(got, "fixture-http-client") || !strings.Contains(got, "no committed binding") || !strings.Contains(got, "rerun") {
		t.Fatalf("keeper refusal does not name the client, its cause and the next step:\n%s", got)
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
	failed.privateClientEntrypoints = plan.PrivateClientEntrypoints
	if err := failed.uncommittedPrivateClients(); err == nil || !strings.Contains(err.Error(), "fixture-http-client") {
		t.Fatalf("a refused launch input left its checked private client looking committed: %v", err)
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

	ownerPath := preparedHTTPOwnershipPath(cname)
	preparedPath := filepath.Join(private, "replicas", cname+".json.prepared")
	succeed := map[string]LaunchInputProducer{"call-broker-scope": selectedProducer}
	knownGone := func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if isExistingProbe(argv, cname) {
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: false}
	}
	couldNotAsk := func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	// Each successful publication binds the scope it was given once; a later one needs a new scope.
	rescope := func() { producer.writeScopeFiles(cname, []*loopholes.Loophole{selected}) }
	referenced := func() (preparedHTTPOwnershipRecord, bool) {
		rec, _, present, err := readPreparedHTTPOwnership(cname)
		return rec, err == nil && present && len(rec.Generations) == 1 && rec.Generations[0].Path == preparedPath
	}

	// The keeper dispatch above ended its jail, and its teardown retired cname's record: publish
	// this launch's generation again before the reuse below.
	producer.Exec = couldNotAsk
	output.Reset()
	producer.Stderr = &output
	producer.launchInputMountArgv = argv
	rescope()
	if !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("the selected producer refused a republication:\n%s", output.String())
	}
	if _, ok := referenced(); !ok {
		t.Fatal("a republished generation is not referenced by its ownership record")
	}

	// m1: a later failure of a launch that REUSED its published generation keeps the reference;
	// the generation is still the running launch's.
	producer.launchInputProducers = map[string]LaunchInputProducer{"call-broker-scope": {StateDir: selectedProducer.StateDir,
		Produce: func(LaunchInputContext) ([]byte, error) { return nil, errors.New("synthetic producer error") }}}
	if producer.writeLaunchInput(selected, cname) {
		t.Fatal("a failing producer published a launch input")
	}
	if _, ok := referenced(); !ok {
		t.Fatal("a failed launch that reused its prepared generation dropped that generation's ownership reference")
	}
	if _, err := os.Lstat(preparedPath); err != nil {
		t.Fatalf("a failed reuse removed the referenced prepared generation: %v", err)
	}
	producer.launchInputProducers = succeed

	// M1: an earlier launch's record (an unclean end) refuses while that launch's container is not
	// known gone, naming the file and the next step; the fresh path's known-gone retirement clears it.
	removePreparedHTTPOwnership(cname)
	if _, err := writePreparedHTTPOwnership(preparedHTTPOwnershipRecord{Version: preparedHTTPOwnershipVersion, Container: cname,
		Runtime: "podman", Setup: "podman-self", PackTree: "/host/earlier-launch-tree", Generations: []preparedHTTPGenerationRef{}}, nil); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if producer.writeLaunchInput(selected, cname) {
		t.Fatal("a launch adopted an earlier launch's prepared-HTTP ownership record")
	}
	if got := output.String(); !strings.Contains(got, ownerPath) || !strings.Contains(got, "ps -a --filter name="+cname) {
		t.Fatalf("the stale-record refusal does not name the record and the next step:\n%s", got)
	}
	producer.Exec = couldNotAsk
	producer.retireStalePreparedHTTPOwnership(cname, "podman")
	if _, err := os.Lstat(ownerPath); err != nil {
		t.Fatalf("a launch that could not ask the runtime retired the earlier record: %v", err)
	}
	producer.Exec = knownGone
	producer.retireStalePreparedHTTPOwnership(cname, "podman")
	if _, err := os.Lstat(ownerPath); !os.IsNotExist(err) {
		t.Fatalf("a known-gone container's earlier record survived: %v", err)
	}
	rescope()
	if !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("a launch after the earlier record's retirement refused:\n%s", output.String())
	}

	// M2: the generation is referenced BEFORE it is published, so the state a kill just before
	// publication leaves is one the next launch reclaims.
	removePreparedHTTPOwnership(cname)
	var crashRecord, crashTemp []byte
	var crashTempLeaf string
	publishPreparedHTTP = func(replicas *os.Root, owner *preparedHTTPGenerationOwner) error {
		rec, ok := referenced()
		if !ok || rec.Generations[0].Generation != owner.generation {
			t.Errorf("the generation was about to be published with no ownership reference: %+v", rec)
		}
		if _, err := os.Lstat(preparedPath); !os.IsNotExist(err) {
			t.Errorf("the prepared generation was at its final path before publication: %v", err)
		}
		var err error
		if crashRecord, err = os.ReadFile(ownerPath); err != nil {
			t.Error(err)
		}
		crashTempLeaf = owner.temp
		if crashTemp, err = os.ReadFile(filepath.Join(private, "replicas", owner.temp)); err != nil {
			t.Error(err)
		}
		return errors.New("synthetic kill before publication")
	}
	t.Cleanup(func() { publishPreparedHTTP = publishPreparedHTTPGeneration })
	rescope()
	if producer.writeLaunchInput(selected, cname) {
		t.Fatal("a failed publication published a launch input")
	}
	publishPreparedHTTP = publishPreparedHTTPGeneration
	if _, err := os.Lstat(ownerPath); !os.IsNotExist(err) {
		t.Fatalf("a failed publication left the ownership record it created: %v", err)
	}
	if crashTempLeaf == "" || len(crashRecord) == 0 || len(crashTemp) == 0 {
		t.Fatal("the publication seam never ran")
	}
	if err := os.WriteFile(ownerPath, crashRecord, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "replicas", crashTempLeaf), crashTemp, 0o600); err != nil {
		t.Fatal(err)
	}
	producer.retireStalePreparedHTTPOwnership(cname, "podman")
	if _, err := os.Lstat(ownerPath); !os.IsNotExist(err) {
		t.Fatalf("the killed launch's record survived a known-gone retirement: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(private, "replicas", crashTempLeaf)); !os.IsNotExist(err) {
		t.Fatalf("the killed launch's unpublished generation survived a known-gone retirement: %v", err)
	}
	rescope()
	if !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("a launch after a killed publication refused:\n%s", output.String())
	}

	// M2: a published generation an earlier launch left that no record names (staged from another
	// pack tree) is reclaimed under the launch-input lock, not mistaken for a pack defect.
	current, err := os.ReadFile(preparedPath)
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := callbrokerinput.DecodePreparedHTTPClientGeneration(current)
	if err != nil {
		t.Fatal(err)
	}
	earlier.PackTree = "/host/earlier-launch-tree"
	removePreparedHTTPOwnership(cname)
	writePreparedTestFile(t, preparedPath, &earlier)
	output.Reset()
	rescope()
	if !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("an unreferenced earlier-launch generation refused the launch:\n%s", output.String())
	}
	if data, err := os.ReadFile(preparedPath); err != nil {
		t.Fatal(err)
	} else if now, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data); err != nil || now.PackTree != packTree {
		t.Fatalf("the earlier launch's generation was not replaced by this launch's: %+v %v", now, err)
	}
	if _, ok := referenced(); !ok {
		t.Fatal("the replacement generation is not referenced by this launch's record")
	}

	// A record, and the generation it names, that a launch of cname under ANOTHER runtime left
	// (an Apple Container jail of this workspace) are not this runtime's to retire: podman's
	// "no such container" says nothing about it. Both are kept, and the launch refuses naming that
	// runtime's commands and the files to remove by hand.
	other := earlier
	other.Runtime, other.Setup, other.PackTree = "container", "apple-container-self", "/host/container-launch-tree"
	other.Generation = "0123456789abcdef0123456789abcdef"
	removePreparedHTTPOwnership(cname)
	writePreparedTestFile(t, preparedPath, &other)
	if _, err := writePreparedHTTPOwnership(preparedHTTPOwnershipRecord{Version: preparedHTTPOwnershipVersion, Container: cname,
		Runtime: other.Runtime, Setup: other.Setup, PackTree: other.PackTree, Generations: []preparedHTTPGenerationRef{{
			Path: preparedPath, Generation: other.Generation, Loophole: other.Loophole, LaunchID: other.LaunchID,
			Source: other.Source, DeclarationHash: other.DeclarationHash}}}, nil); err != nil {
		t.Fatal(err)
	}
	producer.Exec = knownGone
	producer.retireStalePreparedHTTPOwnership(cname, "podman")
	for _, path := range []string{ownerPath, preparedPath} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("a podman launch retired another runtime's prepared-HTTP file %s: %v", path, err)
		}
	}
	output.Reset()
	rescope()
	if producer.writeLaunchInput(selected, cname) {
		t.Fatal("a podman launch adopted another runtime's prepared-HTTP ownership record")
	}
	if got := output.String(); !strings.Contains(got, "container ls --all") || !strings.Contains(got, "container rm --force "+cname) ||
		!strings.Contains(got, "rm -f -- "+ownerPath+" "+preparedPath) {
		t.Fatalf("the other-runtime refusal does not name that runtime's commands and the files to remove:\n%s", got)
	}
	// With the record lost, the sweep must not take the other runtime's published generation for a
	// dead launch of this one either: the launch refuses naming it, and it stays.
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	rescope()
	if producer.writeLaunchInput(selected, cname) {
		t.Fatal("a podman launch swept another runtime's published prepared generation")
	}
	if got := output.String(); !strings.Contains(got, "container rm --force "+cname) || !strings.Contains(got, "rm -f -- "+preparedPath) {
		t.Fatalf("the other-runtime generation refusal does not name the next step:\n%s", got)
	}
	if data, err := os.ReadFile(preparedPath); err != nil {
		t.Fatalf("the sweep removed another runtime's published generation: %v", err)
	} else if now, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data); err != nil || now.Runtime != "container" {
		t.Fatalf("another runtime's published generation was replaced: %+v %v", now, err)
	}
	if err := os.Remove(preparedPath); err != nil {
		t.Fatal(err)
	}
	rescope()
	if !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("a launch after the other runtime's files were removed refused:\n%s", output.String())
	}

	// A KNOWN-GONE container whose record names a generation whose bytes no longer decode: retire
	// keeps both, since it deletes only what it can verify, and so would every later launch. The
	// refusal therefore names the exact files to remove by hand and why, never "launch again".
	removePreparedHTTPOwnership(cname)
	if err := os.WriteFile(preparedPath, []byte("not a prepared identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writePreparedHTTPOwnership(preparedHTTPOwnershipRecord{Version: preparedHTTPOwnershipVersion, Container: cname,
		Runtime: "podman", Setup: "podman-self", PackTree: "/host/earlier-launch-tree", Generations: []preparedHTTPGenerationRef{{
			Path: preparedPath, Generation: other.Generation, Loophole: other.Loophole, LaunchID: other.LaunchID,
			Source: other.Source, DeclarationHash: other.DeclarationHash}}}, nil); err != nil {
		t.Fatal(err)
	}
	producer.Exec = knownGone
	producer.retireStalePreparedHTTPOwnership(cname, "podman")
	for _, path := range []string{ownerPath, preparedPath} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("retirement deleted the unverifiable file %s: %v", path, err)
		}
	}
	output.Reset()
	rescope()
	if producer.writeLaunchInput(selected, cname) {
		t.Fatal("a launch adopted a record naming an unverifiable generation")
	}
	if got := output.String(); !strings.Contains(got, "container is gone") || !strings.Contains(got, "deletes only what it can verify") ||
		!strings.Contains(got, "rm -f -- "+ownerPath+" "+preparedPath) || strings.Contains(got, "not confirmed gone") {
		t.Fatalf("the unverifiable-file refusal does not name the files to remove by hand and why:\n%s", got)
	}
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(preparedPath); err != nil {
		t.Fatal(err)
	}
	rescope()
	if !producer.writeLaunchInput(selected, cname) {
		t.Fatalf("a launch after the unverifiable files were removed by hand refused:\n%s", output.String())
	}
}

// TestRetireStalePreparedHTTPOwnershipNeedsAKnownGoneContainer: an earlier launch's ownership
// record, and the prepared generation it names, go only when the runtime ANSWERS that no
// container of the name exists; "could not ask" and "still there" keep both.
func TestRetireStalePreparedHTTPOwnershipNeedsAKnownGoneContainer(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	for _, c := range []struct {
		name   string
		answer ExecResult
		gone   bool
	}{
		{"no container of the name", ExecResult{Ran: true, RC: 0}, true},
		{"the probe did not run", ExecResult{Ran: false}, false},
		{"the probe failed", ExecResult{Ran: true, RC: 125}, false},
		{"a container is there", ExecResult{Ran: true, RC: 0, Stdout: "abcd1234\n"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := attachedPrivateHTTPFixture(t, true, true)
			f.o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if isExistingProbe(argv, cname) {
					return c.answer
				}
				return ExecResult{Ran: false}
			}
			f.o.retireStalePreparedHTTPOwnership(cname, "podman")
			for _, path := range []string{preparedHTTPOwnershipPath(cname), f.preparedPath} {
				_, err := os.Lstat(path)
				if c.gone && !os.IsNotExist(err) {
					t.Errorf("%s survived %q (%v)", path, c.name, err)
				}
				if !c.gone && err != nil {
					t.Errorf("%s went on %q: %v", path, c.name, err)
				}
			}
		})
	}
}

func TestSelectedPrivateProgramPacksUsesRecordedForkProjection(t *testing.T) {
	tree := t.TempDir()
	baseDir, forkDir := filepath.Join(tree, "base"), filepath.Join(tree, "fork")
	for _, dir := range []string{baseDir, forkDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	baseManifest := `{"name":"base","contributes":[{"kind":"program","bin":"fixture-client","via":"npm","package":"base-client"},{"kind":"env","bin":"fixture-client","served_by":"http-front","vars":{"TOKEN":"{caller_token}","URL":"https://{listen}","TRUST":"{client_trust}"}}]}`
	forkManifest := `{"name":"fork","contributes":[{"kind":"program","bin":"fixture-client","via":"source","fork_of":"base","source":"git+https://example.com/client-fork?ref=main","build":"make install","produces":[".local/bin/fixture-client"]}]}`
	for _, entry := range []struct{ dir, body string }{{baseDir, baseManifest}, {forkDir, forkManifest}} {
		if err := os.WriteFile(filepath.Join(entry.dir, packdecl.ManifestLegacyJSONCName), []byte(entry.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base, problems := packload.LoadDir(baseDir, "base")
	if base == nil || len(problems) != 0 {
		t.Fatalf("base fixture: %v", problems)
	}
	fork, problems := packload.LoadDir(forkDir, "fork")
	if fork == nil || len(problems) != 0 {
		t.Fatalf("fork fixture: %v", problems)
	}
	if err := packload.WritePackTreeRecord(tree, []*packload.Pack{base, fork}); err != nil {
		t.Fatal(err)
	}
	rawPlans, err := packload.ResolvePrivateProgramEnv([]*packload.Pack{base, fork}, "http-front", "front-pack", true)
	if err != nil || len(rawPlans) != 1 {
		t.Fatalf("raw base plan: %+v %v", rawPlans, err)
	}
	loaded, err := selectedPrivateProgramPacks(tree)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := packload.ResolvePrivateProgramEnv(loaded, "http-front", "front-pack", true)
	if err != nil || len(plans) != 1 {
		t.Fatalf("recorded fork plan: %+v %v", plans, err)
	}
	if plans[0].RecipeIdentity == rawPlans[0].RecipeIdentity || plans[0].RecipePack != "base" {
		t.Fatalf("private recipe ignored the selected fork delivery: raw=%+v projected=%+v", rawPlans[0], plans[0])
	}
}

func TestAttachExistingAdoptsTheFrozenPreparedHTTPIdentity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		includeClient bool
		writeInput    bool
		mutate        func(string, string, *callbrokerinput.PreparedHTTPClientGeneration)
		wantRefusal   bool
	}{
		{"valid running tree", true, true, nil, false},
		{"missing prepared generation", true, true, func(_, sidecar string, _ *callbrokerinput.PreparedHTTPClientGeneration) { _ = os.Remove(sidecar) }, true},
		{"replaced generation", true, true, func(_, sidecar string, g *callbrokerinput.PreparedHTTPClientGeneration) {
			g.Generation = strings.Repeat("f", 32)
			writePreparedTestFile(t, sidecar, g)
		}, true},
		{"symlink prepared generation", true, true, func(_, sidecar string, g *callbrokerinput.PreparedHTTPClientGeneration) {
			target := sidecar + ".target"
			writePreparedTestFile(t, target, g)
			if err := os.Remove(sidecar); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, sidecar); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"different frozen tree", true, true, func(_, _ string, g *callbrokerinput.PreparedHTTPClientGeneration) { g.PackTree = "/host/other-tree" }, true},
		{"missing lock with valid generation", true, true, func(_, sidecar string, _ *callbrokerinput.PreparedHTTPClientGeneration) {
			if err := os.Remove(filepath.Join(filepath.Dir(sidecar), ".launch-input.lock")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"missing lock with mismatched generation", true, true, func(_, sidecar string, g *callbrokerinput.PreparedHTTPClientGeneration) {
			g.Generation = strings.Repeat("f", 32)
			writePreparedTestFile(t, sidecar, g)
			if err := os.Remove(filepath.Join(filepath.Dir(sidecar), ".launch-input.lock")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"missing lock with orphan record", true, true, func(input, sidecar string, _ *callbrokerinput.PreparedHTTPClientGeneration) {
			if err := os.Remove(input); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(filepath.Dir(sidecar), ".launch-input.lock")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"unselected front", true, false, nil, false},
		{"zero client legacy input", false, true, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := attachedPrivateHTTPFixture(t, tc.includeClient, tc.writeInput)
			if tc.mutate != nil {
				tc.mutate(f.inputPath, f.preparedPath, &f.prepared)
				if tc.name == "different frozen tree" {
					writePreparedTestFile(t, f.preparedPath, &f.prepared)
				}
			}
			producerCalls := 0
			f.o.launchInputProducers = map[string]LaunchInputProducer{"call-broker-scope": {
				StateDir: func(LaunchInputContext) string { return f.stateDir },
				Produce:  func(LaunchInputContext) ([]byte, error) { producerCalls++; return []byte("no attach producer"), nil },
			}}
			rc, restarted, execed := attachToExec(t, f.o, f.cfg, f.packs, f.channel)
			if tc.wantRefusal {
				if rc != 1 || restarted || execed {
					t.Fatalf("invalid running prepared identity attached: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, f.stderr.String())
				}
			} else if rc != 0 || restarted || !execed {
				t.Fatalf("valid/legacy attach refused: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, f.stderr.String())
			}
			if strings.Contains(tc.name, "missing lock") {
				if _, err := os.Lstat(filepath.Join(filepath.Dir(f.preparedPath), ".launch-input.lock")); !os.IsNotExist(err) {
					t.Fatalf("attach created or repaired the missing private lock: %v", err)
				}
			}
			if producerCalls != 0 {
				t.Fatalf("attach restaged through a producer %d times", producerCalls)
			}
		})
	}
}

func TestAttachUsesHostRunningScopeWhenTheRunningTreeIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		includeClient bool
		unreadable    bool
		retireScope   bool
		retireInput   bool
		wantRefusal   bool
	}{
		{"missing tree with prepared generation", true, false, false, false, true},
		{"unreadable tree with prepared generation", true, true, false, false, true},
		{"missing tree after scope retirement", true, false, true, false, true},
		{"missing tree after scope and input retirement", true, false, true, true, true},
		{"missing tree with verified legacy input", false, false, false, false, false},
		// The record lost while the scope runs: the scope's input names a prepared generation,
		// and that alone must refuse.
		{"missing tree with no ownership record and a running prepared scope", true, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := attachedPrivateHTTPFixture(t, tc.includeClient, true)
			f.packs = nil // current staging no longer selects any HTTP pack.
			lostRecord := strings.Contains(tc.name, "no ownership record")
			if lostRecord {
				if err := os.Remove(preparedHTTPOwnershipPath("yolo-ws-abcd1234")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.unreadable {
				if err := os.WriteFile(filepath.Join(f.tree, packload.PackTreeRecordName), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
				getenv := f.o.Getenv
				f.o.Getenv = func(key string) string {
					if key == AllowAttachSkewEnv {
						return "1"
					}
					return getenv(key)
				}
			} else if err := os.RemoveAll(f.tree); err != nil {
				t.Fatal(err)
			}
			if tc.retireScope || tc.retireInput {
				data, err := os.ReadFile(f.inputPath)
				if err != nil {
					t.Fatal(err)
				}
				var input callbrokerinput.Launch
				if err := json.Unmarshal(data, &input); err != nil {
					t.Fatal(err)
				}
				info, err := os.Lstat(f.inputPath)
				if err != nil {
					t.Fatal(err)
				}
				f.o.scopeFiles = map[string]string{"tasks": input.ScopeFile}
				trackLaunchInput(f.o, "tasks", f.inputPath, info)
				if tc.retireInput {
					f.o.removeLaunchInput("tasks", f.inputPath, info)
				}
				f.o.removeScopeFile("tasks")
				if tc.retireInput {
					if _, err := os.Lstat(f.inputPath); !os.IsNotExist(err) {
						t.Fatalf("ordinary service cleanup did not retire its input: %v", err)
					}
				}
				if _, err := os.Lstat(f.preparedPath); err != nil {
					t.Fatalf("service cleanup removed the container-lifetime prepared identity: %v", err)
				}
			}
			producerCalls := 0
			f.o.launchInputProducers = map[string]LaunchInputProducer{"call-broker-scope": {
				StateDir: func(LaunchInputContext) string { return f.stateDir },
				Produce:  func(LaunchInputContext) ([]byte, error) { producerCalls++; return nil, nil },
			}}
			rc, restarted, execed := attachToExec(t, f.o, f.cfg, f.packs, f.channel)
			if tc.wantRefusal {
				if rc != 1 || restarted || execed {
					t.Fatalf("unverifiable G4 attach was not refused: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, f.stderr.String())
				}
				if lostRecord && !strings.Contains(f.stderr.String(), "retains a prepared HTTP identity") {
					t.Fatalf("the lost-record refusal did not come from the running scope's prepared identity:\n%s", f.stderr.String())
				}
			} else if rc != 0 || restarted || !execed {
				t.Fatalf("verified legacy attach changed behavior: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, f.stderr.String())
			}
			if producerCalls != 0 {
				t.Fatalf("attach invoked producer %d times", producerCalls)
			}
		})
	}
}

func TestAttachUsesAffirmativeEmptyOwnerWithoutScopeOrInput(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	f := attachedPrivateHTTPFixture(t, false, false)
	f.packs = nil
	f.o.runtime, f.o.packTree = "podman", f.tree
	f.o.launchInputMountArgv = assembledPrivateFixtureMounts(t, f.o.Workspace, cname)
	f.o.preparedHTTPOwnershipFile = preparedHTTPOwnershipPath(cname)
	writeCurrentKeeperRecord(t, cname, keeperRecord{Workspace: f.o.Workspace, Runtime: "podman", PackTree: f.tree, MCPTeardownKnown: true})
	if _, err := f.o.ensurePreparedHTTPOwnership(cname); err != nil {
		t.Fatalf("cannot create the frozen empty HTTP owner: %v", err)
	}
	_ = os.Remove(paths.BrokerScopeFile("tasks", "attachfixture01"))
	_ = os.Remove(filepath.Join(loopholes.StateDirFor("tasks"), "replicas", cname+".json"))
	if err := os.MkdirAll(paths.PackTreeRoot(cname), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeLivePackTree(cname, f.tree); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.tree); err != nil {
		t.Fatal(err)
	}
	rc, restarted, execed := attachToExec(t, f.o, f.cfg, f.packs, f.channel)
	if rc != 0 || restarted || !execed {
		t.Fatalf("affirmative non-G4 attach without disposable scope/input did not proceed: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, f.stderr.String())
	}
	owner, _, present, err := readPreparedHTTPOwnership(cname)
	if err != nil || !present || len(owner.Generations) != 0 {
		t.Fatalf("empty owner was not retained after the attach: present=%v err=%v record=%+v", present, err, owner)
	}
}

// A LOST RUNNING TREE REFUSES ONLY ON HTTP EVIDENCE IT CANNOT VERIFY. A jail whose host holds no
// HTTP ownership record and no call-root scope never ran an HTTP front, and attaches as main did
// (TestAttachDeliversWhenNothingItNeedsIsMissing is the plain non-HTTP case); an ownership record
// for another tree, or a running scope whose input is gone, refuses and names what it found.
func TestAttachWithLostTreeRefusesOnlyUnverifiableHTTPEvidence(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	for _, tc := range []struct {
		name       string
		writeInput bool
		mutate     func(t *testing.T, f attachedPrivateHTTP)
		wantRefuse string
	}{
		{"no HTTP evidence at all", false, func(*testing.T, attachedPrivateHTTP) {
			_ = os.Remove(paths.BrokerScopeFile("tasks", "attachfixture01"))
		}, ""},
		{"ownership record for another pack tree", false, func(t *testing.T, _ attachedPrivateHTTP) {
			_ = os.Remove(paths.BrokerScopeFile("tasks", "attachfixture01"))
			if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := writePreparedHTTPOwnership(preparedHTTPOwnershipRecord{Version: preparedHTTPOwnershipVersion, Container: cname,
				Runtime: "podman", Setup: "podman-self", PackTree: "/host/other-tree", Generations: []preparedHTTPGenerationRef{}}, nil); err != nil {
				t.Fatal(err)
			}
		}, "<ownership record>"},
		{"running HTTP scope whose input is gone", true, func(t *testing.T, f attachedPrivateHTTP) {
			if err := os.Remove(f.inputPath); err != nil {
				t.Fatal(err)
			}
		}, "has no verifiable launch input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := attachedPrivateHTTPFixture(t, false, tc.writeInput)
			f.packs = nil
			if err := os.MkdirAll(paths.PackTreeRoot(cname), 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeLivePackTree(cname, f.tree); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(f.tree); err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, f)
			if tc.wantRefuse == "<ownership record>" {
				tc.wantRefuse = preparedHTTPOwnershipPath(cname) // under the fixture's HOME
			}
			rc, restarted, execed := attachToExec(t, f.o, f.cfg, f.packs, f.channel)
			if tc.wantRefuse == "" {
				if rc != 0 || restarted || !execed {
					t.Fatalf("a lost tree with no HTTP evidence did not attach: rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, f.stderr.String())
				}
				return
			}
			if rc != 1 || restarted || execed || !strings.Contains(f.stderr.String(), tc.wantRefuse) ||
				!strings.Contains(f.stderr.String(), "Stop this jail with") {
				t.Fatalf("unverifiable HTTP evidence did not refuse naming %q: rc=%d restarted=%v execed=%v\n%s", tc.wantRefuse, rc, restarted, execed, f.stderr.String())
			}
		})
	}
}

type attachedPrivateHTTP struct {
	o                                       *Options
	cfg                                     *jsonx.OrderedMap
	channel                                 *packChannel
	stderr                                  *bytes.Buffer
	packs                                   []*packload.Pack
	stateDir, inputPath, preparedPath, tree string
	prepared                                callbrokerinput.PreparedHTTPClientGeneration
}

func attachedPrivateHTTPFixture(t *testing.T, includeClient, writeInput bool) attachedPrivateHTTP {
	t.Helper()
	root := t.TempDir()
	frontDir, clientDir := filepath.Join(root, "front"), filepath.Join(root, "client")
	_ = os.Mkdir(frontDir, 0700)
	if includeClient {
		_ = os.Mkdir(clientDir, 0700)
	}
	frontPackJSON := `{"name":"front-pack","contributes":[{"kind":"loophole","from":"loopholes/tasks"}]}`
	if err := os.WriteFile(filepath.Join(frontDir, packdecl.ManifestLegacyJSONCName), []byte(frontPackJSON), 0600); err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(frontDir, "loopholes", "tasks")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	frontManifest := `name = "tasks"
transport = "loopback-tls"
[call_broker]
source = "tasks"
front = "http"
classifier = ["yolo", "internal", "call-broker-classify", "tasks"]
env_from_host = ["TASKS_TOKEN"]
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
	if err := os.WriteFile(filepath.Join(moduleDir, loopholedecl.ManifestTOMLName), []byte(frontManifest), 0600); err != nil {
		t.Fatal(err)
	}
	front, problems := packload.LoadDir(frontDir, "front-pack")
	if front == nil || len(problems) != 0 {
		t.Fatalf("front pack: %v", problems)
	}
	packs := []*packload.Pack{front}
	if includeClient {
		clientJSON := `{"name":"client-pack","contributes":[{"kind":"program","bin":"fixture-client","via":"npm","package":"fixture-client"},{"kind":"env","bin":"fixture-client","served_by":"tasks","vars":{"TOKEN":"{caller_token}","URL":"https://{listen}","TRUST":"{client_trust}"}}]}`
		if err := os.WriteFile(filepath.Join(clientDir, packdecl.ManifestLegacyJSONCName), []byte(clientJSON), 0600); err != nil {
			t.Fatal(err)
		}
		client, issues := packload.LoadDir(clientDir, "client-pack")
		if client == nil || len(issues) != 0 {
			t.Fatalf("client pack: %v", issues)
		}
		packs = append(packs, client)
	}
	if err := packload.WritePackTreeRecord(root, packs); err != nil {
		t.Fatal(err)
	}
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(), nil)
	const cname = "yolo-ws-abcd1234"
	treeSource := root
	root = filepath.Join(paths.PackTreeRoot(cname), "attach-fixture")
	homeSuffix := string(os.PathSeparator) + filepath.Join(paths.GlobalStorageRel(), "agents", cname, "pack-trees", "attach-fixture")
	home := strings.TrimSuffix(root, homeSuffix)
	if home == root {
		t.Fatal("could not derive the attached fixture home from its pack tree")
	}
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(root, os.DirFS(treeSource)); err != nil {
		t.Fatal(err)
	}
	selectedPacks, err := loadPackTree(root)
	if err != nil {
		t.Fatal(err)
	}
	packs = selectedPacks
	moduleDir = filepath.Join(root, "front", "loopholes", "tasks")
	if err := os.MkdirAll(filepath.Dir(paths.LivePackTreeRecord(cname)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeLivePackTree(cname, root); err != nil {
		t.Fatal(err)
	}
	oldStateDir := loopholes.StateDirFor
	stateDir := filepath.Join(t.TempDir(), "front-state")
	if err := os.Mkdir(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	loopholes.StateDirFor = func(name string) string {
		if name == "tasks" {
			return stateDir
		}
		return oldStateDir(name)
	}
	t.Cleanup(func() {
		loopholes.StateDirFor = oldStateDir
		loopholes.SetPackModules(nil)
		loopholes.SetPackSupersessions(nil)
	})
	loopholes.SetPackModules(packLoopholeModules(packs))
	call, err := loopholedecl.LoadDir(moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	declHash := loopholedecl.CallBrokerDeclarationHash(call.CallBroker)
	launchID := "attachfixture01"
	var mountArgv []string
	if includeClient {
		// assembledPrivateFixtureMounts moves HOME: take it BEFORE the scope is written, so the
		// running call-root scope lives under the HOME the attach scans (runningHTTPPreparedExpected).
		mountArgv = assembledPrivateFixtureMounts(t, o.Workspace, cname)
	}
	scopePath, err := brokerscope.Write(brokerscope.File{Source: "tasks", LaunchID: launchID, PID: os.Getpid(), Container: cname, Workspace: o.Workspace, Form: brokerscope.FormCallRoot, Repos: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	input := callbrokerinput.Launch{Version: "yolo.call-broker-launch/2", ScopeFile: scopePath, FrontJailID: filepath.Base(hostServiceSocketsDir(cname, false)),
		Loophole: "tasks", Pack: "front-pack", DeclarationHash: declHash, Source: "tasks", Front: "http", ProgramIdentities: map[string]loopholedecl.CallBrokerProgramIdentity{}, Roots: []string{}}
	input.HTTPBinding = &config.ApprovedCallBrokerHTTP{Pack: input.Pack, Source: input.Source, DeclarationHash: declHash, HTTP: *call.CallBroker.HTTP}
	plans, err := packload.ResolvePrivateProgramEnv(packs, "tasks", "front-pack", true)
	if err != nil {
		t.Fatal(err)
	}
	prepared := callbrokerinput.PreparedHTTPClientGeneration{Version: callbrokerinput.PreparedHTTPClientGenerationVersion, Phase: "prepared", Generation: strings.Repeat("a", 32),
		Container: cname, FrontJailID: input.FrontJailID, Runtime: "podman", Setup: "podman-self", PackTree: root, LaunchID: launchID,
		Loophole: input.Loophole, Front: input.Front, Pack: input.Pack, Source: input.Source, DeclarationHash: declHash,
		Roots: []string{}, ProgramIdentities: input.ProgramIdentities, Clients: make([]callbrokerinput.PreparedHTTPClientPlan, len(plans))}
	for i, p := range plans {
		prepared.Clients[i] = callbrokerinput.PreparedHTTPClientPlan{Bin: p.Bin, Pack: p.Pack, Front: p.Front, RecipePack: p.RecipePack, RecipeIdentity: p.RecipeIdentity, TokenVariable: p.TokenVariable, URLVariable: p.URLVariable, TrustVariable: p.TrustVariable}
	}
	replicas := filepath.Join(stateDir, "replicas")
	if err := os.Mkdir(replicas, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(replicas, ".launch-input.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()
	inputPath := filepath.Join(replicas, cname+".json")
	preparedPath := inputPath + ".prepared"
	if writeInput {
		if includeClient {
			writePreparedTestFile(t, preparedPath, &prepared)
			input.HTTPFrontGeneration = prepared.Generation
		}
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inputPath, data, 0600); err != nil {
			t.Fatal(err)
		}
		scope, err := brokerscope.ReadFile(scopePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := brokerscope.BindCallInput(scopePath, scope, inputPath); err != nil {
			t.Fatal(err)
		}
		if includeClient {
			o.launchInputMountArgv = mountArgv
			o.runtime, o.packTree = "podman", root
			writeCurrentKeeperRecord(t, cname, keeperRecord{Workspace: o.Workspace, Runtime: "podman", PackTree: root, MCPTeardownKnown: true})
			o.preparedHTTPOwnershipFile = preparedHTTPOwnershipPath(cname)
			info, err := os.Lstat(preparedPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := o.recordPreparedHTTPGeneration(preparedHTTPGenerationOwner{path: preparedPath, file: info, container: cname, generation: prepared.Generation}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if includeClient && writeInput {
		finalTree := filepath.Join(paths.PackTreeRoot(cname), "attach-fixture")
		if finalTree != root {
			if err := os.MkdirAll(filepath.Dir(finalTree), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.CopyFS(finalTree, os.DirFS(root)); err != nil {
				t.Fatal(err)
			}
			root = finalTree
			prepared.PackTree = root
			writePreparedTestFile(t, preparedPath, &prepared)
			o.packTree = root
			writeCurrentKeeperRecord(t, cname, keeperRecord{Workspace: o.Workspace, Runtime: "podman", PackTree: root, MCPTeardownKnown: true})
			if err := os.Remove(o.preparedHTTPOwnershipFile); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(preparedPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := o.recordPreparedHTTPGeneration(preparedHTTPGenerationOwner{path: preparedPath, file: info, container: cname, generation: prepared.Generation}); err != nil {
				t.Fatal(err)
			}
			if err := writeLivePackTree(cname, root); err != nil {
				t.Fatal(err)
			}
		}
	}
	return attachedPrivateHTTP{o: o, cfg: cfg, channel: channel, stderr: stderr, packs: packs, stateDir: stateDir, inputPath: inputPath, preparedPath: preparedPath, tree: root, prepared: prepared}
}

func writePreparedTestFile(t *testing.T, path string, generation *callbrokerinput.PreparedHTTPClientGeneration) {
	t.Helper()
	data, err := json.Marshal(generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, ".prepared") && len(generation.Clients) > 0 {
		writePreparedEntrypointsTestFile(t, path, generation)
	}
}

func TestPreparedHTTPOwnershipRejectsSymlinkAndHardlink(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	attachedPrivateHTTPFixture(t, true, true)
	path := preparedHTTPOwnershipPath(cname)
	target := path + ".target"
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, present, err := readPreparedHTTPOwnership(cname); !present || err == nil {
		t.Fatalf("symlinked prepared owner was accepted: present=%v err=%v", present, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, present, err := readPreparedHTTPOwnership(cname); !present || err == nil {
		t.Fatalf("multiply-linked prepared owner was accepted: present=%v err=%v", present, err)
	}
}

func TestForgetGoneRemovesPersistentPreparedHTTPOwnershipOnlyAfterKnownAbsence(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	f := attachedPrivateHTTPFixture(t, true, true)
	ownerPath := preparedHTTPOwnershipPath(cname)
	f.o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	f.o.forgetGoneContainer(cname, "podman", "")
	if _, err := os.Stat(ownerPath); err != nil {
		t.Fatalf("unknown container state retired its expected prepared owner: %v", err)
	}
	if _, err := os.Stat(f.preparedPath); err != nil {
		t.Fatalf("unknown container state retired the prepared generation: %v", err)
	}
	if _, err := os.Stat(f.preparedPath + ".entrypoints"); err != nil {
		t.Fatalf("unknown container state retired the entrypoints record: %v", err)
	}
	f.o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "ps" {
			return ExecResult{Ran: true, RC: 0, Stdout: ""}
		}
		return ExecResult{Ran: false}
	}
	f.o.forgetGoneContainer(cname, "podman", "")
	if _, err := os.Lstat(ownerPath); !os.IsNotExist(err) {
		t.Fatalf("known-absent container retained its prepared ownership record: %v", err)
	}
	if _, err := os.Lstat(f.preparedPath); !os.IsNotExist(err) {
		t.Fatalf("known-absent container retained its prepared generation: %v", err)
	}
	if _, err := os.Lstat(f.preparedPath + ".entrypoints"); !os.IsNotExist(err) {
		t.Fatalf("known-absent container retained its entrypoints record: %v", err)
	}
	if _, err := f.o.ensurePreparedHTTPOwnership(cname); err != nil {
		t.Fatalf("cannot establish an empty owner for cleanup control: %v", err)
	}
	f.o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	f.o.forgetGoneContainer(cname, "podman", "")
	if _, err := os.Stat(ownerPath); err != nil {
		t.Fatalf("unknown container state retired its affirmative empty owner: %v", err)
	}
	f.o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "ps" {
			return ExecResult{Ran: true, RC: 0, Stdout: ""}
		}
		return ExecResult{Ran: false}
	}
	f.o.forgetGoneContainer(cname, "podman", "")
	if _, err := os.Lstat(ownerPath); !os.IsNotExist(err) {
		t.Fatalf("known-absent container retained its affirmative empty owner: %v", err)
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

func TestPreparedHTTPEntrypointsCleanupPreservesAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json.prepared")
	entrypoints := path + ".entrypoints"
	for _, p := range []string{path, entrypoints} {
		if err := os.WriteFile(p, []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	owned, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	ownedEntrypoints, err := os.Lstat(entrypoints)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("new entrypoints"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, entrypoints); err != nil {
		t.Fatal(err)
	}
	owner := preparedHTTPGenerationOwner{path: path, file: owned, entrypointsFile: ownedEntrypoints}
	if removePreparedHTTPGenerationFile(owner) {
		t.Fatal("cleanup reported removing a replaced entrypoints record")
	}
	for p, want := range map[string]string{entrypoints: "new entrypoints", path: "owned"} {
		if data, err := os.ReadFile(p); err != nil || string(data) != want {
			t.Fatalf("cleanup removed a replacement generation's file %s: %q %v", p, data, err)
		}
	}
	if err := os.Remove(entrypoints); err != nil {
		t.Fatal(err)
	}
	if !removePreparedHTTPGenerationFile(owner) {
		t.Fatal("cleanup did not remove its own generation")
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
