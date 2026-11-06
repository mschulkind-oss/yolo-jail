package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// TestApprovePerRefLaterInvalidResourceLeavesNoGrant drives the same CLI
// per-resource adapter used by git, against a synthetic, private host store.
func TestApprovePerRefLaterInvalidResourceLeavesNoGrant(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "git")
	s := &brokerstore.Store{Dir: dir, Source: "git", Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	s.Entries = func(q brokerstore.Request, _ bool) ([]brokerstore.WorkingEntry, error) {
		return []brokerstore.WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: brokerstore.WorkingBranch, Resource: "refs/heads/first", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: "unsupported-kind", Resource: "refs/heads/second", By: "human"},
		}, nil
	}
	q, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: t.TempDir(), Argv: []string{"push"}, Repo: "fixture.test/o/r", StdinDigest: "d", Set: "push", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := approvePerRef([]*brokerstore.Store{s}, "git", q.ID, brokerstore.Decision{For: 15 * time.Minute}, &out, &errs); code == 0 {
		t.Fatalf("invalid later resource approved: %s", out.String())
	}
	fresh := &brokerstore.Store{Dir: dir}
	if _, ok, err := fresh.LiveWorking(q.Workspace, q.Repo, "refs/heads/first"); err != nil || ok {
		t.Fatalf("CLI failure left active first resource: %v %v (%s)", ok, err, errs.String())
	}
	if got, err := fresh.Status(q.Jail, q.Start, q.ID); err != nil || got.State != "pending" {
		t.Fatalf("CLI failure changed request: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "working")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errs.String(), "kind") {
		t.Fatalf("failure did not explain invalid entry: %s", errs.String())
	}
}
