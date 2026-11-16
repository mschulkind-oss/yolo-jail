package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestConfigApprovalDiffRefusesTheJailsOwnWorkspaceBeforeReadingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_WORKSPACE", "/workspace")
	o := NewDefaultOptions()
	o.Workspace = "/workspace"
	o.Getenv = func(name string) string {
		if name == "YOLO_VERSION" {
			return "test-version"
		}
		return ""
	}
	var out, errw bytes.Buffer
	o.Stdout, o.Stderr = &out, &errw
	fillDefaults(&o)

	if rc := o.configApprovalDiff(); rc != 1 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
	for _, want := range []string{"yolo config approval-diff", "on the host"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("host refusal missing %q: %s", want, errw.String())
		}
	}
	if out.Len() != 0 {
		t.Fatalf("host refusal wrote comparison to stdout: %s", out.String())
	}
	if strings.Count(strings.TrimSpace(errw.String()), "\n") != 0 {
		t.Fatalf("host-only refusal should be one line: %q", errw.String())
	}
	if _, err := os.Stat(config.ApprovalSnapshotPath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatalf("host refusal touched approval state (stat error %v)", err)
	}
}

func TestConfigApprovalDiffAllowsNestedWorkspaceInsideJail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_WORKSPACE", "/workspace")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, config.WorkspaceTOMLConfigName),
		[]byte("[install]\nnix = [\"jq\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := NewDefaultOptions()
	o.Workspace = workspace
	o.Getenv = func(name string) string {
		if name == "YOLO_VERSION" {
			return "test-version"
		}
		return ""
	}
	var out, errw bytes.Buffer
	o.Stdout, o.Stderr = &out, &errw
	fillDefaults(&o)

	if rc := o.configApprovalDiff(); rc != 0 {
		t.Fatalf("nested workspace rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
	if strings.Contains(errw.String(), "on the host") || !strings.Contains(out.String(), "No approval is recorded") {
		t.Fatalf("nested workspace was refused or not inspected:\nstdout:%s\nstderr:%s", out.String(), errw.String())
	}
}
