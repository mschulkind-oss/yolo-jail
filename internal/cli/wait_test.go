package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// waitWorkspace is a workspace with its config and `.yolo`, holding one published record.
func waitWorkspace(t *testing.T, rec approvalwait.Record) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.WorkspaceStateDir(ws), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := approvalwait.Publish(ws, rec); err != nil {
		t.Fatal(err)
	}
	return ws
}

// `yolo wait r-<id>` exits with the host's recorded answer, before the front door: no banner
// in what the agent reads.
func TestYoloWaitExitsWithTheRecordedAnswer(t *testing.T) {
	const id = "r-0123456789abcdef0123456789abcdef"
	ws := waitWorkspace(t, approvalwait.Record{ID: id, Source: "git", State: approvalwait.StateDenied, Decided: time.Now()})
	t.Setenv("YOLO_VERSION", "test")
	rc, out, errOut := runMainCaptured(t, "wait", "-C", ws, id)
	if rc != approvalwait.ExitDenied || !strings.Contains(out, id+" was denied") {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
	if strings.Contains(errOut, "yolo-jail ") {
		t.Fatalf("the banner reached the waiter's output:\n%s", errOut)
	}
}

// The bare form lists a request still waiting on the agent, and names the label form.
func TestYoloWaitWithNoArgumentLists(t *testing.T) {
	const id = "r-0123456789abcdef0123456789abcdef"
	ws := waitWorkspace(t, approvalwait.Record{ID: id, Source: "git", State: approvalwait.StatePending, Expires: time.Now().Add(time.Hour), Command: "git push main"})
	t.Setenv("YOLO_VERSION", "test")
	rc, out, errOut := runMainCaptured(t, "wait", "-C", ws)
	if rc != 0 || !strings.Contains(out, id) || !strings.Contains(out, "yolo wait LABEL") {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
}

// A target that is neither a request id nor a host-run label is a usage error naming help.
func TestYoloWaitRefusesAnUnreadableTarget(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	rc, _, errOut := runMainCaptured(t, "wait", "not a label")
	if rc != approvalwait.ExitUsage || !strings.Contains(errOut, "yolo wait --help") {
		t.Fatalf("rc %d err %q", rc, errOut)
	}
}
