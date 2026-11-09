package brokerstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A host broker is a long-running daemon: it keeps the yolo it started with while `just
// install` replaces the `yolo approve` the host user (or their on_request hook) answers with.
// Before committed answers (workingcommit.go), a broker read an answer's working-set coverage
// from working/<id>.json alone. On 2026-11-06 a broker of that age served
// `gh run cancel 42228358413`; the toast answered it `--for 1h` with a newer yolo, which
// recorded the entry only inside the request record; the broker saw the request allowed, found
// no entry, and refused with "no live grant covers this command". An answer therefore also
// writes each entry it commits as its own record, which every broker reads.
func TestDecideWorkingWritesEachEntryAsTheRecordAnOlderBrokerReads(t *testing.T) {
	for _, add := range []bool{false, true} {
		s, _ := workingFixture(t)
		s.Entries = func(q Request) ([]WorkingEntry, error) {
			return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/agent/x", By: "human"}}, nil
		}
		q, err := s.File(Request{Jail: "j", Start: "start", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r",
			StdinDigest: "digest", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
		if err != nil {
			t.Fatal(err)
		}
		d := Decision{Add: add}
		if !add {
			d.For = time.Hour
		}
		entries, err := s.DecideWorking(q.ID, d)
		if err != nil || len(entries) != 1 {
			t.Fatalf("add=%v: answer: %+v %v", add, entries, err)
		}
		// What a broker that predates committed answers reads: the working/ records alone.
		var old WorkingEntry
		b, err := os.ReadFile(filepath.Join(s.Dir, "working", entries[0].ID+".json"))
		if err != nil {
			t.Fatalf("add=%v: the answer left no working record an older broker reads: %v", add, err)
		}
		if err := json.Unmarshal(b, &old); err != nil || old.ID != entries[0].ID || old.Resource != "refs/heads/agent/x" ||
			old.Workspace != "/ws" || !old.Expires.Equal(entries[0].Expires) || !old.Live() {
			t.Fatalf("add=%v: the older broker's record %+v (%v) differs from the answer's %+v", add, old, err, entries[0])
		}
		// A current reader still sees exactly one entry for the resource, which a revoke ends.
		fresh := &Store{Dir: s.Dir, Now: s.Now}
		held, ok, err := fresh.LiveWorking("/ws", "github.com/o/r", "refs/heads/agent/x")
		if err != nil || !ok || held.ID != entries[0].ID {
			t.Fatalf("add=%v: current reader: %+v %v %v", add, held, ok, err)
		}
		if _, err := s.RevokeIn(held.ID, ""); err != nil {
			t.Fatalf("add=%v: revoke: %v", add, err)
		}
		if _, ok, err := fresh.LiveWorking("/ws", "github.com/o/r", "refs/heads/agent/x"); err != nil || ok {
			t.Fatalf("add=%v: a revoked entry still covers: %v %v", add, ok, err)
		}
	}
}
