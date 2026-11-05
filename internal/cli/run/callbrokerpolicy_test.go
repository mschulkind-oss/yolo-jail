package run

import (
	"encoding/json"
	"os"
	"path/filepath"
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
