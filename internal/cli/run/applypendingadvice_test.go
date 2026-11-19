package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func pendingAWSApplyFixture(t *testing.T, boot string) (*Options, *strings.Builder) {
	t.Helper()
	o, output := applyFixture(t, boot, `{}`, applyJail{running: true, env: "YOLO_VERSION=0.18.0+72.ga2b6554ce443\n"})
	t.Setenv("YOLO_VERSION", "0.18.0+83.g9267155daefd.dirty")
	if err := os.Remove(filepath.Join(o.Workspace, config.WorkspaceLegacyConfigName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "aws-fixture")
	if err := os.MkdirAll(module, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"aws-fixture","transport":"loopback-tls","call_broker":{"source":"aws","front":"cli","program":"aws","classifier":["yolo","internal","call-broker-classify","aws"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"change","tables":["all"]}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture-pack", HostExecApproved: true}})
	return o, output
}

func TestConfigApplyAWSOnlyDeclarationDoesNotInventGitRequestChanges(t *testing.T) {
	o, output := pendingAWSApplyFixture(t, `{}`)
	for attempt := 0; attempt < 2; attempt++ {
		output.Reset()
		answer(o, "y")
		if rc := o.configApply(false); rc != 0 && (rc != 3 || attempt == 1) {
			t.Fatalf("AWS-only apply failed: rc=%d %s", rc, output)
		}
		if strings.Contains(output.String(), "requests.git") {
			t.Fatalf("AWS-only difference invented a Git request result: %s", output)
		}
		if attempt == 1 && strings.Contains(output.String(), "selected call-broker declaration") {
			t.Fatalf("AWS declaration repeated after accepted identical approval: %s", output)
		}
	}
}

func TestConfigApplyPendingRepeatDoesNotPromiseAnUnconditionalLaunch(t *testing.T) {
	o, output := pendingAWSApplyFixture(t, `{"requests":{"git":{"push":["main"]}}}`)
	for attempt := 0; attempt < 2; attempt++ {
		output.Reset()
		answer(o, "y")
		if rc := o.configApply(false); rc != 3 {
			t.Fatalf("pending repeat apply rc=%d: %s", rc, output)
		}
		out := output.String()
		if attempt == 1 && (strings.Contains(out, "selected call-broker declaration") || strings.Contains(out, "Accept these")) {
			t.Errorf("unchanged declaration approval did not survive repeat apply: %s", out)
		}
		if strings.Contains(out, "launch asks nothing") || strings.Contains(out, "launch asks \nnothing") {
			t.Errorf("pending apply promised unconditional future authority on attempt %d: %s", attempt, out)
		}
		if !strings.Contains(out, "next launch rechecks") || !strings.Contains(out, "yolo stop") {
			t.Errorf("pending apply lacks truthful next step on attempt %d: %s", attempt, out)
		}
		wantBrokerSkewNote(t, out, "0.18.0+72.ga2b6554ce443", "0.18.0+83.g9267155daefd.dirty")
		approved, err := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
		if err != nil || strings.TrimSpace(string(approved)) != "{}" {
			t.Fatalf("accepted next-launch workspace approval not persisted: %s %v", approved, err)
		}
		boot, ok, err := config.ReadWorkspaceBootBaseline(o.Workspace, "podman-self")
		if err != nil || !ok {
			t.Fatal(err)
		}
		if requests, _ := boot.Get("requests"); requests == nil {
			t.Fatal("pending Git removal overwrote live boot policy")
		}
		if strings.Contains(out, "+ requests.git") {
			t.Fatalf("removal of a distinct boot Git ask mislabeled as an addition: %s", out)
		}
	}
	output.Reset()
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatalf("next-launch config read failed: %s", output)
	}
	o.IsTTYStdin = func() bool { return false }
	if !o.runConfigGate(cfg, "podman", nil) || strings.Contains(output.String(), "Accept these") {
		t.Fatalf("saved unchanged future-launch approval required another answer: %s", output)
	}
}
