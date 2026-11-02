package brokerstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// recordWorkspace is a workspace with its `.yolo`, as one that launched a jail has.
func recordWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.WorkspaceStateDir(ws), 0o700); err != nil {
		t.Fatal(err)
	}
	return ws
}

func readPublished(t *testing.T, ws, id string) approvalwait.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(ws), approvalwait.DirName, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec approvalwait.Record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// Every write of a request publishes its answer where the workspace's jail reads it: pending
// when filed, allowed when decided, and used once its Allow once is spent.
func TestRequestRecordsArePublishedForTheJail(t *testing.T) {
	ws := recordWorkspace(t)
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy, PublishRecords: true,
		PublishFailed: func(_ Request, err error) { t.Errorf("publish failed: %v", err) }}
	in := spendOrFileRequest()
	in.Workspace = ws
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if rec := readPublished(t, ws, q.ID); rec.State != approvalwait.StatePending || rec.Source != "github" || rec.Expires.IsZero() {
		t.Fatalf("filed: %+v", rec)
	}
	if _, err := s.Decide(q.ID, "pending", Decision{}); err != nil {
		t.Fatal(err)
	}
	if rec := readPublished(t, ws, q.ID); rec.State != approvalwait.StateAllowed || rec.Used {
		t.Fatalf("allowed: %+v", rec)
	}
	if _, spent, _, err := s.SpendExactOrFileContext(context.Background(), in, s.Revalidate, nil); err != nil || !spent {
		t.Fatalf("spend: %v %v", spent, err)
	}
	if rec := readPublished(t, ws, q.ID); !rec.Used {
		t.Fatalf("spent, but the record does not say used: %+v", rec)
	}
}

// A store that does not publish (every test store, and any not wired to) writes nothing.
func TestAStoreThatDoesNotPublishWritesNoRecord(t *testing.T) {
	ws := recordWorkspace(t)
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	in := spendOrFileRequest()
	in.Workspace = ws
	if _, err := s.File(in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.WorkspaceStateDir(ws), approvalwait.DirName)); !os.IsNotExist(err) {
		t.Fatalf("a non-publishing store wrote records: %v", err)
	}
}

// A retry after an Allow once lapsed unspent is refused once, naming the request, and marks it
// lapsed; the next run files a new request.
func TestARetryAfterALapsedAllowOnceIsRefusedOnceThenFiles(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy, Now: func() time.Time { return now }}
	in := spendOrFileRequest()
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(q.ID, "pending", Decision{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(16 * time.Minute)
	_, spent, _, err := s.SpendExactOrFileContext(context.Background(), in, s.Revalidate, nil)
	var filing *FilingError
	if spent || !errors.As(err, &filing) {
		t.Fatalf("a retry after the window: spent %v, err %v", spent, err)
	}
	if id, ok := IsLapsedAllow(filing.Err); !ok || id != q.ID {
		t.Fatalf("not refused as lapsed naming %s: %v", q.ID, err)
	}
	_, spent, filed, err := s.SpendExactOrFileContext(context.Background(), in, s.Revalidate, nil)
	if err != nil || spent || filed.ID == "" || filed.ID == q.ID || filed.State != "pending" {
		t.Fatalf("the next run: spent %v, filed %+v, err %v", spent, filed, err)
	}
}
