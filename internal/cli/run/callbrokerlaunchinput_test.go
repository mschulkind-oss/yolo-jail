package run

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func mustPolicyJSON(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	value, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*jsonx.OrderedMap)
}

// A selected staged pack, the gate's recorded scope, and the production launch-input
// publisher are used here. The pack has no manifest-supplied host daemon: core
// orders its selected daemon and publishes the carrier for that daemon alone.
func TestSelectedCallBrokerScopeUsesPrivateLaunchInputCarrier(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","transport":"loopback-tls","access":"read tasks","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["all"],"default":{"all":false}}]},"grants":[{"kind":"read","text":"read tasks"}]}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := "name = \"fixture\"\n[[program]]\nbin = \"classifier\"\nvia = \"pack\"\nfrom = \"tools/classifier\"\nrun = \"classifier\"\n[[loophole]]\nfrom = \"loopholes/tasks\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte(pack), 0o644); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "tools", "classifier", "classifier")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	items := loopholes.Discover(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	if len(items) != 1 || items[0].CallBroker == nil {
		t.Fatalf("selected broker not discovered: %+v", items)
	}
	lp := items[0]
	selected := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	order, specs := hostDaemonOrder(selected, selected.Enabled(), jsonx.NewOrderedMap(), func(string) bool { return true })
	if len(order) != 1 || order[0] != lp.Name {
		t.Fatalf("selected call broker not started by production host daemon order: %v", order)
	}
	command := asAnyList(mapGet(specs[lp.Name], "command"))
	if !reflect.DeepEqual(command, []any{"yolo", "internal", "daemon", "call-broker", "--socket", "{socket}", "--launch-input", "{launch_input}"}) {
		t.Fatalf("selected core daemon argv: %v", command)
	}
	unselected := loopholes.NewSet(loopholes.DiscoverOptions{})
	if off, _ := hostDaemonOrder(unselected, unselected.Enabled(), jsonx.NewOrderedMap(), func(string) bool { return true }); len(off) != 0 {
		t.Fatalf("unselected broker acquired a daemon: %v", off)
	}
	if off, _ := hostDaemonOrder(selected, nil, jsonx.NewOrderedMap(), func(string) bool { return false }); len(off) != 0 {
		t.Fatalf("disabled broker acquired a daemon: %v", off)
	}
	// The declaration excludes host_daemon. Core supplies the daemon command,
	// never a pack-controlled one.
	if lp.HostDaemon != nil {
		t.Fatal("selected call broker supplied a host daemon")
	}
	private := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	mountArgv := assembledPrivateFixtureMounts(t, workspace, "pack-call-scope")
	previous := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string {
		if name == lp.Name {
			return private
		}
		return previous(name)
	}
	t.Cleanup(func() { loopholes.StateDirFor = previous })
	var output strings.Builder
	o := &Options{Workspace: workspace, Stderr: &output, launchInputMountArgv: mountArgv}
	o.recordApprovedScopes(map[string][]config.ScopeRepo{lp.Name: {{Repo: "team:ENG"}}}, nil, nil)
	policy, err := config.EffectiveCallBrokerCells(lp.CallBroker.Source, lp.CallBroker,
		mustPolicyJSON(t, `{"allow":{"tasks":{"on_request":["all.update"]}}}`),
		mustPolicyJSON(t, `{"requests":{"tasks":{"all":{"update":"ask"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	o.callBrokerCells = map[string]map[string]config.CallBrokerCellPolicy{lp.Name: policy}
	o.approvedLoopholeGrants = map[string]map[string]config.LoopholeGrantStanding{lp.Name: {"read": {Standing: config.LoopholeGrantGranted}}}
	o.writeScopeFiles("pack-call-scope", []*loopholes.Loophole{lp})
	if o.scopeFiles[lp.Name] == "" {
		t.Fatalf("selected host scope missing: %s", output.String())
	}
	grantBody, err := loopholegrant.Merge([]byte("{}"), loopholegrant.Block{Pack: lp.Pack, Workspace: workspace,
		Container: "pack-call-scope", PID: os.Getpid(), Kinds: map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted}}})
	if err != nil {
		t.Fatal(err)
	}
	grantPath := filepath.Join(private, "grant-test.json")
	if err := os.WriteFile(grantPath, grantBody, 0o600); err != nil {
		t.Fatal(err)
	}
	o.settingsSnapshots = map[string]*ownedSettingsSnapshot{lp.Name: {path: grantPath}}
	if !o.writeLaunchInput(lp, "pack-call-scope") {
		t.Fatalf("production publisher refused: %s", output.String())
	}
	path := o.launchInputFiles[lp.Name]
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("published permissions: %s", info.Mode())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got CallBrokerLaunchInput
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Lookups != nil {
		t.Fatalf("a classifier with no declared lookups must publish the same nil form the host rereads, got %#v", got.Lookups)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("#!/bin/sh\nexit 0\n")))
	if got.Version != "yolo.call-broker-launch/2" || got.Source != "tasks" || len(got.Roots) != 1 || got.Roots[0] != "team:ENG" || got.ProgramIdentities["classifier"].SHA256 != hash || len(got.Cells) != 1 || got.EffectiveCells["update"].Tables["all"].Ceiling != "on_request" || got.EffectiveCells["update"].Tables["all"].Decision != "ask" || !got.ReadStanding || got.ReadGrantStanding != config.LoopholeGrantGranted {
		t.Fatalf("unapproved or incomplete selected snapshot: %+v", got)
	}
	if strings.Contains(string(raw), "credential_paths") || strings.Contains(string(raw), "env_from_host") {
		t.Fatalf("credential configuration leaked into input")
	}
	declaredGrants := lp.Grants
	lp.Grants = nil
	withoutGrant, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants)
	if err != nil || withoutGrant.ReadStanding || withoutGrant.ReadGrantStanding != "" {
		t.Fatalf("undeclared read standing became authority: %+v, %v", withoutGrant, err)
	}
	lp.Grants = declaredGrants
	// The gate's withheld read request must stay distinguishable from an
	// absent grant or a requestable one in the private input.
	withheld, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells,
		map[string]map[string]config.LoopholeGrantStanding{lp.Name: {"read": {Standing: config.LoopholeGrantNotAllowed, Withheld: true}}})
	if err != nil || withheld.ReadStanding || withheld.ReadGrantStanding != config.LoopholeGrantNotAllowed || !withheld.ReadWithheld {
		t.Fatalf("withheld read grant was collapsed: %+v, %v", withheld, err)
	}
	delete(o.approvedScopes, lp.Name)
	if o.writeLaunchInput(lp, "another-launch") {
		t.Fatal("no gate-approved scope must refuse publication")
	}
	o.approvedScopes[lp.Name] = []config.ScopeRepo{{Repo: "team:ENG"}}
	delete(o.callBrokerCells, lp.Name)
	if o.writeLaunchInput(lp, "another-launch") {
		t.Fatal("no frozen effective cells must refuse publication")
	}
	o.callBrokerCells[lp.Name] = policy
	lp.Source = loopholes.SourceConfig
	if o.writeLaunchInput(lp, "another-launch") {
		t.Fatal("unselected config loophole must refuse production input")
	}
	lp.Source = loopholes.SourcePack
	delete(lp.CallBroker.ProgramIdentities, "classifier")
	if o.writeLaunchInput(lp, "another-launch") {
		t.Fatal("missing selected staged classifier identity must refuse production input")
	}
}
