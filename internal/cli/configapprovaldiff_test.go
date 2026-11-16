package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestConfigApprovalDiffIsReadOnlyThroughDispatch(t *testing.T) {
	_, workspace := withHomeAndCwd(t)
	writeFile(t, filepath.Join(workspace, config.WorkspaceTOMLConfigName),
		"[install]\nnix = [\"jq\", \"ripgrep\"]\n")
	recordPath := config.ApprovalSnapshotPath(workspace)
	old := `{"install":{"nix":["jq"]}}`
	writeFile(t, recordPath, old+"\n")
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, errw.String())
	}
	for _, want := range []string{recordPath, "current: computed now, not saved",
		"scope and loophole grants are not compared", "--- previous config", "+++ current config",
		"ripgrep"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
	if errw.Len() != 0 {
		t.Fatalf("successful inspection wrote to stderr: %s", errw.String())
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("approval-diff changed the stored record")
	}
}

func TestConfigApprovalDiffReportsEquality(t *testing.T) {
	_, workspace := withHomeAndCwd(t)
	writeFile(t, filepath.Join(workspace, config.WorkspaceTOMLConfigName), "[install]\nnix = [\"jq\"]\n")
	current, err := config.ApprovalRecordPart(workspace)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := config.ApprovalSnapshotPath(workspace)
	writeFile(t, recordPath, current+"\n")

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 0 || errw.Len() != 0 {
		t.Fatalf("rc=%d stdout=%s stderr=%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "No difference: no config approval is pending.") ||
		strings.Contains(out.String(), "--- previous config") {
		t.Fatalf("equality output is wrong:\n%s", out.String())
	}
}

func TestConfigApprovalDiffWithoutRecordDoesNotCreateApprovalDirectory(t *testing.T) {
	_, workspace := withHomeAndCwd(t)
	writeFile(t, filepath.Join(workspace, config.WorkspaceTOMLConfigName), "[install]\nnix = [\"jq\"]\n")
	current, err := config.ApprovalRecordPart(workspace)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := config.ApprovalSnapshotPath(workspace)
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 0 || errw.Len() != 0 {
		t.Fatalf("rc=%d stdout=%s stderr=%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{"No approval is recorded for this workspace yet.", current} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Dir(recordPath)); !os.IsNotExist(err) {
		t.Fatalf("inspection created approval directory (stat error %v)", err)
	}
}

func TestConfigApprovalDiffComparesMalformedRecordWithoutRepair(t *testing.T) {
	_, workspace := withHomeAndCwd(t)
	writeFile(t, filepath.Join(workspace, config.WorkspaceTOMLConfigName), "[install]\nnix = [\"jq\"]\n")
	recordPath := config.ApprovalSnapshotPath(workspace)
	old := "not valid JSON\n"
	writeFile(t, recordPath, old)
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 0 || errw.Len() != 0 {
		t.Fatalf("rc=%d stdout=%s stderr=%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{"The stored record is not valid JSON; comparing it as text.", "--- previous config", "+++ current config"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
	if got, err := os.ReadFile(recordPath); err != nil || string(got) != old {
		t.Fatalf("malformed record changed: bytes=%q err=%v", got, err)
	}
}

func TestConfigApprovalDiffReportsReadAndConfigErrorsOnStderr(t *testing.T) {
	t.Run("unreadable record path", func(t *testing.T) {
		_, workspace := withHomeAndCwd(t)
		recordPath := config.ApprovalSnapshotPath(workspace)
		if err := os.MkdirAll(filepath.Dir(filepath.Dir(recordPath)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Dir(recordPath), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errw bytes.Buffer
		if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 1 || out.Len() != 0 ||
			!strings.Contains(errw.String(), recordPath) {
			t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
		}
	})
	t.Run("invalid workspace config", func(t *testing.T) {
		_, workspace := withHomeAndCwd(t)
		writeFile(t, filepath.Join(workspace, config.WorkspaceTOMLConfigName), "[install\n")
		var out, errw bytes.Buffer
		if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 1 || out.Len() != 0 || errw.Len() == 0 {
			t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
		}
	})
}

func TestConfigApprovalDiffDispatchArgumentAndHelpHandling(t *testing.T) {
	for _, args := range [][]string{{"approval-diff", "extra"}, {"approval-diff", "--accept-config-changes"}} {
		var out, errw bytes.Buffer
		if rc := configRunW(args, &out, &errw); rc != 2 || out.Len() != 0 || errw.Len() == 0 {
			t.Errorf("args=%q rc=%d stdout=%q stderr=%q", args, rc, out.String(), errw.String())
		}
	}
	for _, help := range []string{"--help", "-h"} {
		var out, errw bytes.Buffer
		if rc := configRunW([]string{"approval-diff", help}, &out, &errw); rc != 0 || errw.Len() != 0 ||
			!strings.Contains(out.String(), "Usage: yolo config approval-diff") {
			t.Errorf("help=%q rc=%d stdout=%q stderr=%q", help, rc, out.String(), errw.String())
		}
	}
}
