package callbroker

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
)

func TestAWSShippedS3TimedCellsPreserveCapsAndWholeResourceCoverage(t *testing.T) {
	f := newS3E2E(t)
	put := []string{"s3", "cp", "-", s3Root + "timed.txt"}
	if rc, out := f.aws("first", put...); rc != 75 {
		t.Fatalf("put ask %d %q", rc, out)
	}
	id := f.pending()[0].ID
	if out, err := f.host("approve", id, "--for", "2h"); err == nil || !strings.Contains(out, "--for 1h") {
		t.Fatalf("put cap %v %s", err, out)
	}
	if out, err := f.host("approve", id, "--for", "1h"); err != nil {
		t.Fatalf("put timed answer %v %s", err, out)
	}
	for _, body := range []string{"first", "different body"} {
		if rc, out := f.aws(body, put...); rc != 0 {
			t.Fatalf("timed resource did not cover changed body: %d %q", rc, out)
		}
	}
	if body, ok := f.server.Object("bkt", "team/timed.txt"); !ok || string(body) != "different body" {
		t.Fatalf("put content %q", body)
	}
	remove := []string{"s3", "rm", s3Root + "timed.txt"}
	if rc, out := f.aws("", remove...); rc != 75 {
		t.Fatalf("delete borrowed put permission %d %q", rc, out)
	}
	id = f.pending()[0].ID
	if out, err := f.host("approve", id, "--for", "16m"); err == nil || !strings.Contains(out, "--for 15m") {
		t.Fatalf("delete cap %v %s", err, out)
	}
	if out, err := f.host("approve", id, "--for", "15m"); err != nil {
		t.Fatalf("delete timed answer %v %s", err, out)
	}
	if rc, out := f.aws("", remove...); rc != 0 {
		t.Fatalf("delete execution %d %q", rc, out)
	}
	if _, ok := f.server.Object("bkt", "team/timed.txt"); ok {
		t.Fatal("approved delete did not execute")
	}
	events, _, err := brokeraudit.Read(filepath.Join(f.home, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	write, pending := false, false
	for _, e := range events {
		if e.Service != "aws" {
			t.Fatalf("old audit source: %+v", e)
		}
		write = write || e.Set == "write" && e.Outcome == "ran"
		pending = pending || e.Set == "write" && e.Outcome == "pending"
	}
	if !write || !pending {
		t.Fatalf("missing write/pending audit: %+v", events)
	}
}
