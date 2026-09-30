package run

// keeperplancheck_test.go pins the keeper's plan check (checkPlan) against the pack tree the launch
// staged: the keeper resolves its packs from that tree, in its own process, and runs exactly the
// host services the launch disclosed (docs/design/jail-lifetime-last-session-wins.md JL-D20,
// JL-D35).

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// stagedLoopholeTree writes a staged pack tree holding one pack whose loophole has a host daemon,
// with the record a launch writes, and returns the tree's root.
func stagedLoopholeTree(t *testing.T) string {
	t.Helper()
	tree := t.TempDir()
	root := filepath.Join(tree, "acme")
	mod := filepath.Join(root, "loopholes", "acme-proxy")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme-proxy","transport":"none","default_enabled":true,"host_daemon":{"cmd":["/bin/true"],"publishes":"socket"}}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("the pack fixture does not load: %v", probs)
	}
	if err := packload.WritePackTreeRecord(tree, []*packload.Pack{p}); err != nil {
		t.Fatal(err)
	}
	return tree
}

// planCheckKeeper is a keeper for a plan over tree, in a process whose pack records are empty, as a
// keeper's own process starts: nothing the test's launches recorded reaches it.
func planCheckKeeper(t *testing.T, tree string, packs, services []string) *keeper {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	t.Cleanup(packRecordScope())
	cfg, err := encodeConfig(jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: t.TempDir(), Cname: "yolo-plan-check",
		Runtime: "podman", Config: cfg, PackTree: tree, Packs: packs, Services: services,
		RunCmd: []string{"podman", "run"}}
	return newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
}

// TestAKeeperResolvesItsPacksFromTheStagedTree is JL-D35 at checkPlan's call site: the keeper's own
// process knows no pack until it reads the launch's staged tree, and the host services it then plans
// are that tree's. Without adoptPackRecords it would plan none of a pack's services, and a check that
// only refused extra services let that through.
func TestAKeeperResolvesItsPacksFromTheStagedTree(t *testing.T) {
	k := planCheckKeeper(t, stagedLoopholeTree(t), []string{"acme"}, []string{"acme-proxy"})
	cfg, err := k.checkPlan()
	if err != nil {
		t.Fatalf("the keeper refused the plan it was disclosed with: %v", err)
	}
	if names := k.o.plannedLoopholeNames("podman", cfg); !slices.Contains(names, "acme-proxy") {
		t.Errorf("the keeper plans %v, without the staged pack's acme-proxy", names)
	}
}

// TestAKeeperRefusesAPlanNamingAServiceItWouldNotStart is JL-D20's "exactly that plan", the other way
// round from a daemon the launch did not name: a disclosed service this keeper would not start is a
// launch that told the terminal something runs that never does, so the keeper refuses it and says
// which, rather than starting a jail short of it.
func TestAKeeperRefusesAPlanNamingAServiceItWouldNotStart(t *testing.T) {
	k := planCheckKeeper(t, stagedLoopholeTree(t), []string{"acme"}, []string{"acme-proxy", "ghost-service"})
	_, err := k.checkPlan()
	if err == nil {
		t.Fatal("the keeper accepted a plan naming a service it would not start")
	}
	if !strings.Contains(err.Error(), `"ghost-service"`) {
		t.Errorf("the refusal does not name the service: %v", err)
	}
}
