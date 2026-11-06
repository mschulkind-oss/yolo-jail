package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func TestGateFreezesSelectedCallBrokerCellsFromApprovedWorkspaceRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeRawUserConfig(t, "config.jsonc", `{"allow":{"tasks":{"on_request":["all.update"]}}}`)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(`{"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"working_set":{"update":true},"all":{"update":true}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":["yolo","internal","call-broker-classify","tasks"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["working_set","all"],"default":{"working_set":false}}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}})
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	var output strings.Builder
	o := &Options{Workspace: workspace, Stderr: &output, AcceptConfigChanges: true}
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStderr = func() bool { return false }
	merged := mustPolicyJSON(t, `{"allow":{"tasks":{"on_request":["all.update"]}},"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"working_set":{"update":true},"all":{"update":true}}}}`)
	if !o.checkConfigChanges(merged, "podman") {
		t.Fatalf("gate rejected selected broker: %s", output.String())
	}
	policy, ok := o.callBrokerCells["tasks"]
	if !ok || policy["update"].Tables["all"].Decision != "run" || policy["update"].Tables["working_set"].Decision != "run" {
		t.Fatalf("gate failed to freeze effective cells: %v (present=%t)", o.callBrokerCells, ok)
	}
	if _, granted := o.approvedLoopholeGrants["tasks"]["read"]; granted {
		t.Fatal("undeclared read grant reached the effective snapshot")
	}
	// A later edit is not a keeper or producer's authority source.
	writeRawUserConfig(t, "config.jsonc", `{}`)
	if o.callBrokerCells["tasks"]["update"].Tables["all"].Decision != "run" {
		t.Fatal("gate snapshot was recomputed from a later user config")
	}
}

func TestGatePolicySurvivesBothKeeperPlansWithoutReloading(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":["yolo","internal","classify"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["working_set","all"]}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}})
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	for _, tc := range []struct{ name, ask, want string }{
		{"false", `,"all":{"update":false}`, "refuse"},
		{"ask", `,"all":{"update":"ask"}`, "ask"},
		{"absent under always", ``, "run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			user := `{"allow":{"tasks":{"always":["all.update"]}}}`
			workspaceConfig := `{"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"working_set":{"update":true}` + tc.ask + `}}}`
			writeRawUserConfig(t, "config.jsonc", user)
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(workspaceConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			o := &Options{Workspace: workspace, Stderr: &output, AcceptConfigChanges: true,
				IsTTYStdin: func() bool { return false }, IsTTYStderr: func() bool { return false }, Getenv: os.Getenv}
			merged := mustPolicyJSON(t, `{"allow":{"tasks":{"always":["all.update"]}},"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"working_set":{"update":true}`+tc.ask+`}}}`)
			if !o.checkConfigChanges(merged, "podman") {
				t.Fatalf("gate refused %s: %s", tc.name, output.String())
			}
			if got := o.callBrokerCells["tasks"]["update"].Tables["all"]; got.Decision != tc.want || got.Ceiling != "always" {
				t.Fatalf("gate policy=%+v, want %s", got, tc.want)
			}
			container, err := o.keeperPlanFor(merged, "podman", "test", stagedPacks{}, nil, nil, nil, "", "", nil, &assembleInput{})
			if err != nil {
				t.Fatal(err)
			}
			o.macosUserKey = &macosUserKeying{}
			macos, err := o.macosUserKeeperPlanFor(merged, "macos-user", "test", stagedPacks{}, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var selected *loopholes.Loophole
			for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All() {
				if lp.CallBroker != nil {
					selected = lp
					break
				}
			}
			if selected == nil {
				t.Fatal("no selected broker")
			}
			private := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(private, 0o700); err != nil {
				t.Fatal(err)
			}
			previous := loopholes.StateDirFor
			loopholes.StateDirFor = func(name string) string {
				if name == selected.Name {
					return private
				}
				return previous(name)
			}
			t.Cleanup(func() { loopholes.StateDirFor = previous })
			o.launchInputMountArgv = assembledPrivateFixtureMounts(t, workspace, "policy-cells")
			o.writeScopeFiles("policy-cells", []*loopholes.Loophole{selected})
			if o.scopeFiles[selected.Name] == "" {
				t.Fatalf("gate-approved live scope was not bound: %s", output.String())
			}
			if !o.writeLaunchInput(selected, "policy-cells") {
				t.Fatalf("private publisher refused: %s", output.String())
			}
			raw, err := os.ReadFile(o.launchInputFiles[selected.Name])
			if err != nil {
				t.Fatal(err)
			}
			var published CallBrokerLaunchInput
			if err := json.Unmarshal(raw, &published); err != nil {
				t.Fatal(err)
			}
			if published.Version != "yolo.call-broker-launch/2" || published.EffectiveCells["update"].Tables["all"].Decision != tc.want {
				t.Fatalf("published policy=%+v", published.EffectiveCells)
			}
			// A keeper owns deep copies, not an alias of the gate's mutable map.
			o.callBrokerCells["tasks"]["update"].Tables["all"] = config.CallBrokerTablePolicy{Decision: "refuse"}
			for _, plan := range []*keeperPlan{container, macos} {
				if got := plan.CallBrokerCells["tasks"]["update"].Tables["all"].Decision; got != tc.want {
					t.Fatalf("keeper policy=%s, want %s", got, tc.want)
				}
			}
		})
	}
}

func TestCallBrokerHTTPApprovedBinding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TASKS_TOKEN", "synthetic-only-host-secret")
	writeRawUserConfig(t, "config.jsonc", `{"allow":{"tasks":{"on_request":["all.update"]}}}`)
	workspace := t.TempDir()
	configJSON := `{"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"all":{"update":"ask"}}}}`
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `name = "tasks"
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
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}})
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	var output strings.Builder
	o := &Options{Workspace: workspace, Stderr: &output, AcceptConfigChanges: true,
		IsTTYStdin: func() bool { return false }, IsTTYStderr: func() bool { return false }}
	fillDefaults(o)
	merged := mustPolicyJSON(t, `{"allow":{"tasks":{"on_request":["all.update"]}},"brokered":{"tasks":{"roots":["team:alpha"]}},"requests":{"tasks":{"all":{"update":"ask"}}}}`)
	if !o.checkConfigChanges(merged, "podman") {
		t.Fatalf("gate refused selected TOML HTTP binding: %s", output.String())
	}
	lp := loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All()[0]
	binding := o.approvedHTTPBindings[lp.Name]
	if binding.Pack != "fixture" || binding.HTTP.Origin != "https://fixture.example" || binding.HTTP.Credential.Source != "host_env" || binding.HTTP.Credential.Key != "TASKS_TOKEN" || binding.HTTP.Credential.Header != "Authorization" || binding.HTTP.Credential.Prefix != "Bearer " || len(binding.ReadOverPost) != 1 || binding.ReadOverPost[0] != "/graphql" {
		t.Fatalf("gate did not freeze selected exact HTTP binding: %+v; %s", binding, output.String())
	}
	if !strings.Contains(output.String(), "http origin=https://fixture.example credential_source=host_env credential_key=TASKS_TOKEN credential_header=Authorization credential_prefix=Bearer ") {
		t.Fatalf("binding not shown at gate: %s", output.String())
	}
	assertBound := func(o *Options) {
		t.Helper()
		input, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants, o.approvedHTTPBindings)
		if err != nil || input.HTTPBinding == nil || input.HTTPBinding.HTTP.Credential.Key != "TASKS_TOKEN" {
			t.Fatalf("selected private producer lost approved HTTP binding: %+v %v", input, err)
		}
	}
	assertBound(o)
	// Current selected declaration must still match the gate's frozen copy.
	lp.CallBroker.HTTP.Credential.Prefix = "changed "
	if _, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants, o.approvedHTTPBindings); err == nil {
		t.Fatal("modified current declaration acquired the earlier binding")
	}
	lp.CallBroker.HTTP.Credential.Prefix = "Bearer "
	for _, tc := range []struct {
		name   string
		change func(*config.ApprovedCallBrokerHTTP)
	}{
		{"origin", func(b *config.ApprovedCallBrokerHTTP) { b.HTTP.Origin = "https://other.example" }},
		{"variable", func(b *config.ApprovedCallBrokerHTTP) { b.HTTP.Credential.Key = "OTHER_TOKEN" }},
		{"source", func(b *config.ApprovedCallBrokerHTTP) { b.HTTP.Credential.Source = "fenced_file" }},
		{"header", func(b *config.ApprovedCallBrokerHTTP) { b.HTTP.Credential.Header = "X-Token" }},
		{"prefix", func(b *config.ApprovedCallBrokerHTTP) { b.HTTP.Credential.Prefix = "Token " }},
		{"POST path", func(b *config.ApprovedCallBrokerHTTP) { b.ReadOverPost[0] = "/other" }},
		{"selection", func(b *config.ApprovedCallBrokerHTTP) { b.Pack = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frozen := cloneHTTPBindings(o.approvedHTTPBindings)
			b := frozen[lp.Name]
			tc.change(&b)
			frozen[lp.Name] = b
			if _, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants, frozen); err == nil {
				t.Fatal("changed binding published")
			}
		})
	}
	if _, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants); err == nil {
		t.Fatal("EnvFromHost alone approved HTTP")
	}
	container, err := o.keeperPlanFor(merged, "podman", "test", stagedPacks{}, nil, nil, nil, "", "", nil, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	o.macosUserKey = &macosUserKeying{}
	macos, err := o.macosUserKeeperPlanFor(merged, "macos-user", "test", stagedPacks{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []*keeperPlan{container, macos} {
		plan.RunCmd = assembledPrivateFixtureMounts(t, workspace, "http-binding")
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		var restored keeperPlan
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(restored.ApprovedHTTPBindings, o.approvedHTTPBindings) {
			t.Fatalf("keeper lost approved HTTP binding: %+v", restored.ApprovedHTTPBindings)
		}
		k := newKeeper(&restored, KeeperSeams{}, nil, nil, nil, nil)
		assertBound(k.o)
		private := filepath.Join(t.TempDir(), "state")
		if err := os.Mkdir(private, 0o700); err != nil {
			t.Fatal(err)
		}
		previous := loopholes.StateDirFor
		loopholes.StateDirFor = func(name string) string {
			if name == lp.Name {
				return private
			}
			return previous(name)
		}
		k.o.writeScopeFiles("http-binding", []*loopholes.Loophole{lp})
		if k.o.scopeFiles[lp.Name] == "" || !k.o.writeLaunchInput(lp, "http-binding") {
			loopholes.StateDirFor = previous
			t.Fatalf("keeper failed to publish selected HTTP input: %s", output.String())
		}
		loopholes.StateDirFor = previous
		raw, err := os.ReadFile(k.o.launchInputFiles[lp.Name])
		if err != nil {
			t.Fatal(err)
		}
		var published CallBrokerLaunchInput
		if err := json.Unmarshal(raw, &published); err != nil {
			t.Fatal(err)
		}
		if published.HTTPBinding == nil || published.HTTPBinding.Pack != "fixture" || published.HTTPBinding.HTTP.Credential.Key != "TASKS_TOKEN" {
			t.Fatalf("keeper publisher lost HTTP binding: %+v", published.HTTPBinding)
		}
		if published.HTTPFrontGeneration != "" {
			t.Fatalf("HTTP launch with no selected private client published generation %q", published.HTTPFrontGeneration)
		}
		if _, err := os.Lstat(k.o.launchInputFiles[lp.Name] + ".prepared"); !os.IsNotExist(err) {
			t.Fatalf("HTTP launch with no selected private client wrote a prepared identity: %v", err)
		}
		if strings.Contains(string(raw), "synthetic-only-host-secret") {
			t.Fatal("private host input contains the host-only synthetic token")
		}
	}
	gateAgain := func(pack string, accept bool) (*Options, bool) {
		t.Helper()
		loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: pack, PackRoot: root, HostExecApproved: true}})
		var notice strings.Builder
		next := &Options{Workspace: workspace, Stderr: &notice, AcceptConfigChanges: accept,
			IsTTYStdin: func() bool { return false }, IsTTYStderr: func() bool { return false }}
		fillDefaults(next)
		passed := next.checkConfigChanges(merged, "podman")
		if !passed && accept {
			t.Fatalf("approved changed declaration refused: %s", notice.String())
		}
		return next, passed
	}
	if _, passed := gateAgain("different-pack", false); passed {
		t.Fatal("changed pack silently passed persisted gate")
	}
	reapproved, passed := gateAgain("different-pack", true)
	if !passed || reapproved.approvedHTTPBindings[lp.Name].Pack != "different-pack" {
		t.Fatal("changed pack was not freshly approved")
	}
	// Repartitioning classifier argv changes the declaration hash without altering its displayed line.
	manifest = strings.Replace(manifest, `"call-broker-classify", "tasks"`, `"call-broker-classify tasks"`, 1)
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, passed := gateAgain("different-pack", false); passed {
		t.Fatal("changed hash with identical disclosure silently passed persisted gate")
	}
	hashSelected := loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All()[0]
	if got := config.CallBrokerDisclosure(hashSelected.CallBroker); !reflect.DeepEqual(got, config.CallBrokerDisclosure(lp.CallBroker)) ||
		reapproved.approvedHTTPBindings[lp.Name].DeclarationHash == config.WithCallBrokerDeclarations(nil, workspace, []config.SelectedCallBrokerScope{{Source: "tasks", Label: lp.Name, Pack: "different-pack", Declaration: hashSelected.CallBroker}}, nil).HTTPBindings()[lp.Name].DeclarationHash {
		t.Fatalf("hash regression did not hold displayed disclosure constant while changing the hash: old=%q new=%q oldHash=%s newHash=%s", config.CallBrokerDisclosure(lp.CallBroker), config.CallBrokerDisclosure(hashSelected.CallBroker), reapproved.approvedHTTPBindings[lp.Name].DeclarationHash, config.WithCallBrokerDeclarations(nil, workspace, []config.SelectedCallBrokerScope{{Source: "tasks", Label: lp.Name, Pack: "different-pack", Declaration: hashSelected.CallBroker}}, nil).HTTPBindings()[lp.Name].DeclarationHash)
	}
	if _, passed := gateAgain("different-pack", true); !passed {
		t.Fatal("changed hash was not freshly approved")
	}
	manifest = strings.Replace(manifest, "request_headers = [\"Content-Type\"]", "request_headers = []", 1)
	manifest = strings.Replace(manifest, "read_over_post = [\"/graphql\"]", "read_over_post = []", 1)
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	empty, passed := gateAgain("different-pack", true)
	if !passed {
		t.Fatal("empty allowlists were not approved")
	}
	emptySelected := loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All()[0]
	emptyBinding := empty.approvedHTTPBindings[emptySelected.Name]
	if emptyBinding.HTTP.RequestHeaders == nil || emptyBinding.ReadOverPost == nil {
		t.Fatalf("explicit empty arrays lost at gate: %+v", emptyBinding)
	}
	if _, err := selectedCallBrokerLaunchInput(emptySelected, empty.approvedScopes, empty.callBrokerCells, empty.approvedLoopholeGrants, empty.approvedHTTPBindings); err != nil {
		t.Fatalf("approved empty allowlists refused by private producer: %v", err)
	}
	emptyPlan, err := empty.keeperPlanFor(merged, "podman", "empty-http", stagedPacks{}, nil, nil, nil, "", "", nil, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	emptyPlan.RunCmd = assembledPrivateFixtureMounts(t, workspace, "empty-http")
	encoded, err := json.Marshal(emptyPlan)
	if err != nil {
		t.Fatal(err)
	}
	var restoredEmpty keeperPlan
	if err := json.Unmarshal(encoded, &restoredEmpty); err != nil {
		t.Fatal(err)
	}
	keeperOptions := newKeeper(&restoredEmpty, KeeperSeams{}, nil, nil, nil, nil).o
	private := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string {
		if name == emptySelected.Name {
			return private
		}
		return previous(name)
	}
	keeperOptions.writeScopeFiles("empty-http", []*loopholes.Loophole{emptySelected})
	published := keeperOptions.scopeFiles[emptySelected.Name] != "" && keeperOptions.writeLaunchInput(emptySelected, "empty-http")
	loopholes.StateDirFor = previous
	if !published {
		t.Fatal("keeper refused approved explicit empty HTTP arrays")
	}
	raw, err := os.ReadFile(keeperOptions.launchInputFiles[emptySelected.Name])
	if err != nil {
		t.Fatal(err)
	}
	var input CallBrokerLaunchInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	if input.HTTPBinding == nil || input.HTTPBinding.HTTP.RequestHeaders == nil || input.HTTPBinding.ReadOverPost == nil {
		t.Fatalf("private input lost explicit empty HTTP arrays: %+v", input.HTTPBinding)
	}
}
