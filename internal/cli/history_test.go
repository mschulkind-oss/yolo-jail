package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
)

const historyTestID = "r-0123456789abcdef0123456789abcdef"

// `yolo history r-<id>` in a jail prints how the request was answered, before the front door:
// no banner in what the agent reads.
func TestYoloHistoryOfARequestPrintsItsAnswerInAJail(t *testing.T) {
	now := time.Now()
	ws := waitWorkspace(t, approvalwait.Record{ID: historyTestID, Source: "github", State: approvalwait.StateAllowed,
		Filed: now.Add(-time.Minute), Decided: now.Add(-30 * time.Second), Answer: approvalwait.AnswerOnce,
		Via: approvalwait.ViaOnRequest, Hook: approvalwait.HookRan, Used: true, SpentAt: now, Command: "gh workflow run x.yml"})
	t.Setenv("YOLO_VERSION", "test")
	rc, out, errOut := runMainCaptured(t, "history", "-C", ws, historyTestID)
	if rc != 0 || !strings.Contains(out, "allowed once, by `yolo approve` from the on_request hook") || !strings.Contains(out, "Next: nothing to do: it ran.") {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
	if strings.Contains(errOut, "yolo-jail ") {
		t.Fatalf("the banner reached the agent's output:\n%s", errOut)
	}
}

// `yolo history requests` lists the workspace's requests, through the registry too.
func TestYoloHistoryRequestsLists(t *testing.T) {
	now := time.Now()
	ws := waitWorkspace(t, approvalwait.Record{ID: historyTestID, Source: "git", State: approvalwait.StatePending,
		Filed: now, Expires: now.Add(time.Hour), Command: "git push main"})
	t.Setenv("YOLO_VERSION", "test")
	rc, out, errOut := runMainCaptured(t, "history", "-C", ws, "requests")
	if rc != 0 || !strings.Contains(out, historyTestID) || !strings.Contains(out, "yolo wait "+historyTestID) {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
	rc, out, errOut = captureStd(t, func() int { return registry["history"]([]string{"history", "-C", ws, "--since", "1h", "requests"}) })
	if rc != 0 || !strings.Contains(out, historyTestID) {
		t.Fatalf("through the registry: rc %d out %q err %q", rc, out, errOut)
	}
}

// A subject that needs the workspace event record, not built yet, names what answers today.
func TestYoloHistoryWithoutTheRecordNamesWhatAnswers(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	for _, args := range [][]string{{"history"}, {"history", "launches"}} {
		rc, _, errOut := runMainCaptured(t, args...)
		if rc != approvalwait.ExitUsage || !strings.Contains(errOut, "`yolo history requests`") || !strings.Contains(errOut, "launch.log") {
			t.Fatalf("%v: rc %d err %q", args, rc, errOut)
		}
	}
	rc, _, errOut := runMainCaptured(t, "history", "--since", "soon", "requests")
	if rc != approvalwait.ExitUsage || !strings.Contains(errOut, "yolo history --help") {
		t.Fatalf("bad --since: rc %d err %q", rc, errOut)
	}
}
