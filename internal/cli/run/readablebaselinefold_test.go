package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestRunApplyStepsFoldsOnlySuccessfulCanonicalPaths(t *testing.T) {
	workspace := t.TempDir()
	boot := mapOf(t, `{"install":{"nix":["jq"],"mise":{"python":"3.12"}},"machine":{"ports":[{"host":1,"jail":1}],"resources":{"memory":"4g"}}}`)
	if _, err := config.WriteWorkspaceBootBaseline(workspace, "podman-self", boot); err != nil {
		t.Fatal(err)
	}
	current := mapOf(t, `{"install":{"nix":["jq","ripgrep"]},"machine":{"ports":[{"host":2,"jail":2}],"resources":{"memory":"8g"}}}`)
	o := &Options{Workspace: workspace}
	run := &applyRun{rt: "podman", cname: "yolo-test", current: current, mode: applyMode{running: true}}
	result := o.runApplySteps(run, []applyChange{
		{kind: changeKey, key: "install.nix", outcome: outcomeNextSession},
		{kind: changeKey, key: "machine.ports", outcome: outcomeAppliedNow},
		{kind: changeKey, key: "machine.resources.memory", outcome: outcomeNextRestart},
		{kind: changeKey, key: "install.mise", outcome: outcomeNextSession},
	})
	if len(result) != 4 {
		t.Fatalf("runApplySteps returned %d changes, want 4", len(result))
	}
	got, ok, err := config.ReadWorkspaceBootBaseline(workspace, "podman-self")
	if err != nil || !ok {
		t.Fatalf("ReadWorkspaceBootBaseline: ok=%v err=%v", ok, err)
	}
	install, _ := got.Get("install")
	installMap := install.(*jsonx.OrderedMap)
	nix, _ := installMap.Get("nix")
	if s, _ := jsonx.DumpsCompact(nix); s != `["jq", "ripgrep"]` {
		t.Errorf("next-session install.nix was not folded: %s", s)
	}
	if _, present := installMap.Get("mise"); present {
		t.Error("successful install.mise deletion was not folded")
	}
	machine, _ := got.Get("machine")
	machineMap := machine.(*jsonx.OrderedMap)
	ports, _ := machineMap.Get("ports")
	if s, _ := jsonx.DumpsCompact(ports); s != `[{"host": 2, "jail": 2}]` {
		t.Errorf("applied-now machine.ports was not folded: %s", s)
	}
	resources, _ := machineMap.Get("resources")
	memory, _ := resources.(*jsonx.OrderedMap).Get("memory")
	if memory != "4g" {
		t.Errorf("failed machine.resources.memory was incorrectly folded: %v", memory)
	}
}

func TestFoldAppliedReturnsExactCanonicalValueAndPresence(t *testing.T) {
	workspace := t.TempDir()
	boot := mapOf(t, `{"install":{"nix":["jq"],"mise":{"python":"3.12"}}}`)
	if _, err := config.WriteWorkspaceBootBaseline(workspace, "podman-self", boot); err != nil {
		t.Fatal(err)
	}
	current := mapOf(t, `{"install":{"nix":["jq","ripgrep"]}}`)
	o := &Options{Workspace: workspace}
	folded := o.foldApplied(&applyRun{rt: "podman", mode: applyMode{running: true}, current: current}, []applyChange{
		{kind: changeKey, key: "install.nix", outcome: outcomeNextSession},
		{kind: changeKey, key: "install.mise", outcome: outcomeNextSession},
	})
	if len(folded) != 2 {
		t.Fatalf("foldApplied returned %d facts, want 2: %+v", len(folded), folded)
	}
	if folded[0].key != "install.nix" || !folded[0].present {
		t.Fatalf("install.nix fact lacks exact path/presence: %+v", folded[0])
	}
	if s, _ := jsonx.DumpsCompact(folded[0].value); s != `["jq", "ripgrep"]` {
		t.Errorf("install.nix fact has wrong value: %s", s)
	}
	if folded[1].key != "install.mise" || folded[1].present || folded[1].value != nil {
		t.Errorf("deleted install.mise fact should preserve absent presence: %+v", folded[1])
	}
}

func TestFoldAppliedReturnsNoFactsWhenBaselineWriteFails(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(config.WorkspaceConfigBootPath(workspace, "podman-self"), 0o700); err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: workspace}
	folded := o.foldApplied(&applyRun{rt: "podman", mode: applyMode{running: true}, current: mapOf(t, `{"install":{"nix":["jq"]}}`)}, []applyChange{
		{kind: changeKey, key: "install.nix", outcome: outcomeNextSession},
	})
	if len(folded) != 0 {
		t.Fatalf("failed baseline write returned applied facts: %+v", folded)
	}
}

func TestFoldAppliedDoesNotWriteForUnknownNotRunningOrMacosUser(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  *applyRun
	}{
		{name: "unknown", run: &applyRun{rt: "podman", mode: applyMode{running: false, unknown: true}, current: mapOf(t, `{"install":{"nix":["jq"]}}`)}},
		{name: "not-running", run: &applyRun{rt: "podman", mode: applyMode{running: false}, current: mapOf(t, `{"install":{"nix":["jq"]}}`)}},
		{name: "macos-user", run: &applyRun{rt: "macos-user", mode: applyMode{running: true}, current: mapOf(t, `{"install":{"nix":["jq"]}}`)}},
		{name: "current-nil", run: &applyRun{rt: "podman", mode: applyMode{running: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.BootBaselineDigestEnv, "macos-session-digest")
			workspace := t.TempDir()
			boot := mapOf(t, `{"install":{"nix":["old"]}}`)
			if _, err := config.WriteWorkspaceBootBaseline(workspace, "podman-self", boot); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(config.WorkspaceConfigBootPath(workspace, "podman-self"))
			if err != nil {
				t.Fatal(err)
			}
			tc.run.cname = "yolo-test"
			tc.run.cfg = nil
			folded := (&Options{Workspace: workspace}).foldApplied(tc.run, []applyChange{
				{kind: changeKey, key: "install.nix", outcome: outcomeNextSession},
			})
			if len(folded) != 0 {
				t.Fatalf("ineligible apply returned facts: %+v", folded)
			}
			after, err := os.ReadFile(config.WorkspaceConfigBootPath(workspace, "podman-self"))
			if err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv(config.BootBaselineDigestEnv); got != "macos-session-digest" {
				t.Fatalf("folding changed the session baseline digest: %q", got)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("ineligible apply changed baseline:\nbefore %s\nafter %s", before, after)
			}
		})
	}
}

func TestFoldAppliedAbsentBaselineReturnsNoFacts(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "no-baseline")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	folded := (&Options{Workspace: workspace}).foldApplied(&applyRun{
		rt: "podman", mode: applyMode{running: true}, current: mapOf(t, `{"install":{"nix":["jq"]}}`),
	}, []applyChange{{kind: changeKey, key: "install.nix", outcome: outcomeNextSession}})
	if len(folded) != 0 {
		t.Fatalf("missing baseline returned applied facts: %+v", folded)
	}
}
