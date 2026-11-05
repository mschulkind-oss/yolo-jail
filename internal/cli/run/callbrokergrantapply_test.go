package run

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// This drives applyPlan -> grantDeltas -> applyStep rather than just the
// Mediated predicate: deleting the production call site leaves the grant live.
func TestConfigApplyRewritesSelectedCallBrokerReadGrantBeforeNextRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	packRoot := filepath.Join(home, "fixture")
	module := filepath.Join(packRoot, "loopholes", "tasks")
	executable := filepath.Join(packRoot, "tools", "classifier", "classifier")
	for _, dir := range []string{module, filepath.Dir(executable)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pack := "name = \"fixture\"\n[[loophole]]\nfrom = \"loopholes/tasks\"\n[[program]]\nbin = \"classifier\"\nvia = \"pack\"\nfrom = \"tools/classifier\"\nrun = \"classifier\"\n"
	if err := os.WriteFile(filepath.Join(packRoot, "pack.toml"), []byte(pack), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","access":"read fixture tasks","transport":"loopback-tls","grants":[{"kind":"read","text":"read fixture tasks"}],"call_broker":{"source":"fixture-tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["all"]}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: packRoot, HostExecApproved: true}})
	workspace := filepath.Join(home, "ws")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	var selected *loopholes.Loophole
	for _, lp := range loopholes.NewHostSet(nil).All() {
		if lp.Name == "tasks" {
			selected = lp
		}
	}
	if selected == nil || len(selected.Grants) != 1 {
		t.Fatalf("selected call broker missing: %+v", selected)
	}
	body, err := loopholegrant.Merge([]byte("{}"), loopholegrant.Block{Pack: "fixture", Workspace: workspace, Container: "fixture-container", PID: os.Getpid(), LaunchID: "fixture-launch",
		Kinds: map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted}}})
	if err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := loopholes.WritePrivateSettingsSnapshot(selected, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	check := &config.ScopeCheck{LoopholeGrants: []config.LoopholeGrantSource{{Pack: "fixture", Loophole: "tasks", Kinds: []config.LoopholeKindState{{Grant: selected.Grants[0], Standing: config.LoopholeKindNotAllowed}}}}}
	o := &Options{Workspace: workspace, approvedLoopholeGrants: check.ApprovedLoopholeGrants()}
	run := &applyRun{rt: "macos-user", cfg: jsonx.NewOrderedMap(), cname: "fixture-container", mode: applyMode{running: true}}
	plan := o.applyPlan(run, check)
	applied := false
	for _, change := range plan {
		if change.kind != changeGrants {
			continue
		}
		if change.outcome != outcomeAppliedNow {
			t.Fatalf("grant was not mediated live: %+v", change)
		}
		if err := o.applyStep(run, &change, time.Now().Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
		applied = true
	}
	if !applied {
		t.Fatalf("applyPlan did not discover the selected grant file: %+v", plan)
	}
	_, grant, err := loopholegrant.Read(path)
	if err != nil || grant == nil || grant.Kinds["read"].Standing != loopholegrant.NotAllowed {
		t.Fatalf("selected read grant remained live after config apply: %+v %v", grant, err)
	}
}
