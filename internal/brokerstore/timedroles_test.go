package brokerstore

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// H16: a known write beside an asking read is capped at the read's current limit (the shared 15 m
// default when the declaration states none), not the write's 12 h; an over-limit answer leaves zero
// new entries, and an in-limit one gives both entries one expiry.
func TestRoleTimedKnownWriteBesideAskingReadTakesTheReadLimit(t *testing.T) {
	q := roleTimedRequest(t)
	q.CallWrites = []CallWrite{{Cell: "change", Chain: q.CallReads[0].Chain, Role: RoleKnownWrite}}
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	store := &Store{Dir: t.TempDir(), Now: func() time.Time { return now }, NativeTimedPolicy: true, Entries: OperationEntries, Revalidate: func(current Request) (Policy, error) { return current.Policy, nil }, TimedLimit: func(current Request) (TimedBound, error) {
		return RoleTimedLimit(current, nil, true, 0, current.Policy.MaxWindow)
	}}
	filed, err := store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecideWorking(filed.ID, Decision{For: time.Hour}); !errors.Is(err, ErrRefusedByPolicy) || !strings.Contains(err.Error(), "role read") {
		t.Fatalf("one hour over the read limit: %v", err)
	}
	if held, err := store.Working(); err != nil || len(held) != 0 {
		t.Fatalf("a refused answer left entries: %+v %v", held, err)
	}
	entries, err := store.DecideWorking(filed.ID, Decision{For: TimedGrantDefault})
	if err != nil || len(entries) != 2 || !entries[0].Expires.Equal(entries[1].Expires) || !entries[0].Expires.Equal(filed.Filed.Add(TimedGrantDefault)) {
		t.Fatalf("at the limit: %+v %v", entries, err)
	}
}

// H20: an entry recorded before this revision (a write at an eleven-hour expiry, no read role)
// keeps covering its write and its expiry, and never covers a read of the same resource chain.
func TestAnOldEleventHourWriteEntryCoversNoRead(t *testing.T) {
	q := roleTimedRequest(t)
	q.CallWrites = nil
	reads, err := OperationEntries(q)
	if err != nil || len(reads) != 1 {
		t.Fatalf("read entry: %+v %v", reads, err)
	}
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	store := &Store{Dir: t.TempDir(), Now: func() time.Time { return now }}
	old, _, err := store.AddWorking(WorkingEntry{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/main", By: "human", Expires: now.Add(11 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if held, ok, err := store.LiveWorking(q.Workspace, q.Repo, "refs/heads/main"); err != nil || !ok || !held.Expires.Equal(old.Expires) {
		t.Fatalf("old write coverage: %+v %v %v", held, ok, err)
	}
	if _, ok, err := store.LiveWorking(q.Workspace, q.Repo, reads[0].Resource); err != nil || ok {
		t.Fatalf("an old write entry covered a read: %v %v", ok, err)
	}
}
