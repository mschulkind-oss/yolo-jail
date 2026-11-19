package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func configApplyAWSFixture(t *testing.T, boot string) (*Options, *strings.Builder) {
	t.Helper()
	o, output := applyFixture(t, boot, `{}`, applyJail{running: true})
	if err := os.Remove(filepath.Join(o.Workspace, config.WorkspaceLegacyConfigName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "aws-fixture")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"aws-fixture","transport":"loopback-tls","call_broker":{"source":"aws","front":"cli","program":"aws","classifier":["yolo","internal","call-broker-classify","aws"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"change","tables":["all"]}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture-pack", HostExecApproved: true}})
	return o, output
}

func TestConfigApplyAWSOnlyDeclarationDoesNotInventGitRequestChanges(t *testing.T) {
	o, output := configApplyAWSFixture(t, `{}`)
	for attempt := 0; attempt < 2; attempt++ {
		output.Reset()
		answer(o, "y")
		if rc := o.configApply(false); rc != 0 && (rc != 3 || attempt == 1) {
			t.Fatalf("AWS-only apply rc=%d on attempt %d: %s", rc, attempt, output)
		}
		if strings.Contains(output.String(), "requests.git") {
			t.Fatalf("AWS-only apply invented a Git request result: %s", output)
		}
		if attempt == 1 && strings.Contains(output.String(), "selected call-broker declaration") {
			t.Fatalf("unchanged AWS declaration repeated after approval: %s", output)
		}
	}
}

func TestConfigApplyPendingRepeatPreservesBootAskAndNextLaunchGate(t *testing.T) {
	o, output := configApplyAWSFixture(t, `{"requests":{"git":{"push":["main"]}}}`)
	for attempt := 0; attempt < 2; attempt++ {
		output.Reset()
		answer(o, "y")
		if rc := o.configApply(false); rc != 3 {
			t.Fatalf("pending repeat apply rc=%d, want 3: %s", rc, output)
		}
		out := output.String()
		if attempt == 1 && (strings.Contains(out, "selected call-broker declaration") || strings.Contains(out, "Accept these")) {
			t.Fatalf("unchanged declaration repeated its approval prompt: %s", out)
		}
		if strings.Contains(out, "launch asks nothing") || strings.Contains(out, "launch asks \nnothing") || !strings.Contains(out, "next launch rechecks") || !strings.Contains(out, "yolo stop") {
			t.Fatalf("pending apply footer is not truthful: %s", out)
		}
		approved, err := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
		if err != nil || strings.TrimSpace(string(approved)) != "{}" {
			t.Fatalf("accepted workspace approval not persisted: %s %v", approved, err)
		}
		boot, ok, err := config.ReadWorkspaceBootBaseline(o.Workspace, "podman-self")
		if err != nil || !ok {
			t.Fatalf("boot baseline unavailable: %v", err)
		}
		requests, _ := boot.Get("requests")
		if requests == nil {
			t.Fatal("pending Git removal overwrote the live boot request")
		}
		if strings.Contains(out, "+ requests.git") {
			t.Fatalf("removal of the boot Git ask was mislabeled as an addition: %s", out)
		}
	}

	output.Reset()
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatalf("next-launch config read failed: %s", output)
	}
	o.IsTTYStdin = func() bool { return false }
	if !o.runConfigGate(cfg, "podman", nil) || strings.Contains(output.String(), "Accept these") {
		t.Fatalf("unchanged saved next-launch approval asked again: %s", output)
	}
}
