package brokerstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDecideWorkingRevokedBatchEntrySurvivesRetention verifies that pruning
// a standalone end marker cannot make a retained sibling request grant it again.
func TestDecideWorkingRevokedBatchEntrySurvivesRetention(t *testing.T) {
	s, clock := workingFixture(t)
	s.Entries = func(q Request, _ bool) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/revoked", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/live", By: "human"},
		}, nil
	}
	q := retentionRequest(t, s)
	entries, err := s.DecideWorking(q.ID, Decision{Add: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(entries[0].ID); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(7 * 24 * time.Hour)
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	for i := 0; i < 3; i++ {
		if _, err := fresh.Snapshot(); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/revoked"); err != nil || ok {
			t.Fatalf("read %d restored revoked authority: %v %v", i, ok, err)
		}
		if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/live"); err != nil || !ok {
			t.Fatalf("read %d lost sibling: %v %v", i, ok, err)
		}
	}
	assertRecordExists(t, s, "requests", q.ID)
	assertRecordExists(t, s, "working", entries[0].ID)
	if err := fresh.Revoke(entries[1].ID); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(7 * 24 * time.Hour)
	if _, err := fresh.Snapshot(); err != nil {
		t.Fatal(err)
	}
	assertRecordGone(t, s, "requests", q.ID)
	assertRecordGone(t, s, "working", entries[0].ID)
	if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/revoked"); err != nil || ok {
		t.Fatalf("post-reap revoked authority: %v %v", ok, err)
	}
}

func TestRequestDirectorySyncFailureRetainsRevocationBarrierAcrossRetry(t *testing.T) {
	s, clock := workingFixture(t)
	s.Entries = func(q Request, _ bool) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/revoked", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/live", By: "human"},
		}, nil
	}
	first := retentionRequest(t, s)
	entries, err := s.DecideWorking(first.ID, Decision{For: 20 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(entries[0].ID); err != nil {
		t.Fatal(err)
	}

	// The newer request takes over only the live sibling. The first request can
	// now retire, but its revoked entry still needs its standalone end barrier.
	s.Entries = func(q Request, _ bool) ([]WorkingEntry, error) {
		return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/live", By: "human"}}, nil
	}
	*clock = clock.Add(24 * time.Hour)
	second := retentionRequest(t, s)
	if _, err := s.DecideWorking(second.ID, Decision{For: 30 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(7 * 24 * time.Hour)

	syncErr := errors.New("injected requests directory sync failure")
	syncCalls := 0
	s.syncDir = func(path string) error {
		syncCalls++
		if path != filepath.Join(s.Dir, "requests") {
			t.Fatalf("sync target %q is not the requests directory", path)
		}
		// This is the actual transaction callsite: the obsolete request has
		// been unlinked but the dependent barrier must still exist here.
		assertRecordGone(t, s, "requests", first.ID)
		assertRecordExists(t, s, "working", entries[0].ID)
		return syncErr
	}
	if _, err := s.Snapshot(); !errors.Is(err, syncErr) {
		t.Fatalf("request-directory sync failure was not returned before pruning: %v", err)
	}
	if syncCalls != 1 {
		t.Fatalf("requests directory sync called %d times, want once", syncCalls)
	}
	assertFailedRetentionState(t, s, first, second, entries)

	// Retry after the last transaction already unlinked the request. A sync
	// gated only by a removal-this-call flag would skip this required barrier.
	if _, err := s.Snapshot(); !errors.Is(err, syncErr) {
		t.Fatalf("retry did not sync durable request absence: %v", err)
	}
	if syncCalls != 2 {
		t.Fatalf("retry synced requests directory %d times total, want two", syncCalls)
	}
	assertFailedRetentionState(t, s, first, second, entries)

	// Once the request-directory sync succeeds, retirement is safe and stays
	// safe across fresh readers.
	s.syncDir = nil
	for i := 0; i < 4; i++ {
		if _, err := s.Snapshot(); err != nil {
			t.Fatalf("successful retry snapshot %d: %v", i, err)
		}
		assertRecordGone(t, s, "requests", first.ID)
		assertRecordExists(t, s, "requests", second.ID)
		assertRecordGone(t, s, "working", entries[0].ID)
		if _, ok, err := s.LiveWorking("/ws", first.Repo, "refs/heads/revoked"); err != nil || ok {
			t.Fatalf("successful retry read %d restored revoked authority: %v %v", i, ok, err)
		}
		if _, ok, err := s.LiveWorking("/ws", first.Repo, "refs/heads/live"); err != nil || !ok {
			t.Fatalf("successful retry read %d lost live sibling: %v %v", i, ok, err)
		}
	}
}

func assertFailedRetentionState(t *testing.T, s *Store, first, second Request, entries []WorkingEntry) {
	t.Helper()
	assertRecordGone(t, s, "requests", first.ID)
	assertRecordExists(t, s, "working", entries[0].ID)
	var r records
	if err := readStoreRecords(s.Dir, &r); err != nil {
		t.Fatal(err)
	}
	foundLive, foundRevoked := false, false
	for _, w := range r.working {
		switch w.Resource {
		case "refs/heads/revoked":
			foundRevoked = true
			if w.Live() {
				t.Fatalf("revoked entry became live after failed sync: %+v", w)
			}
		case "refs/heads/live":
			foundLive = true
			if !w.Live() {
				t.Fatalf("live sibling ended after failed sync: %+v", w)
			}
		}
	}
	if !foundRevoked || !foundLive {
		t.Fatalf("failed-sync working state lacks revoked barrier/live sibling: revoked=%v live=%v", foundRevoked, foundLive)
	}
	foundSecond := false
	for _, q := range r.requests {
		if q.ID == first.ID {
			t.Fatal("retired request reappeared")
		}
		foundSecond = foundSecond || q.ID == second.ID
	}
	if !foundSecond {
		t.Fatal("current sibling request disappeared")
	}
}

// TestDecideWorkingSupersededTimedRequestsAreRetired tests the revision,
// rather than entry ID, as the retention dependency for renewed authority.
func TestDecideWorkingSupersededTimedRequestsAreRetired(t *testing.T) {
	s, clock := workingFixture(t)
	s.Entries = func(q Request, _ bool) ([]WorkingEntry, error) {
		return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/renewed", By: "human"}}, nil
	}
	first := retentionRequest(t, s)
	entries, err := s.DecideWorking(first.ID, Decision{For: 20 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(24 * time.Hour)
	second := retentionRequest(t, s)
	renewed, err := s.DecideWorking(second.ID, Decision{For: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if renewed[0].ID != entries[0].ID || renewed[0].Revision <= entries[0].Revision {
		t.Fatalf("not a revision renewal: %+v %+v", entries, renewed)
	}
	*clock = clock.Add(7 * 24 * time.Hour)
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	for i := 0; i < 3; i++ {
		if _, err := fresh.Snapshot(); err != nil {
			t.Fatal(err)
		}
		if e, ok, err := fresh.LiveWorking("/ws", first.Repo, "refs/heads/renewed"); err != nil || !ok || e.Revision != renewed[0].Revision {
			t.Fatalf("renewed coverage: %+v %v %v", e, ok, err)
		}
	}
	assertRecordGone(t, s, "requests", first.ID)
	assertRecordExists(t, s, "requests", second.ID)
	// Repeated renewal must not retain every historical request while the
	// current timed entry remains live.
	previous := second
	for i := 0; i < 4; i++ {
		*clock = clock.Add(8 * 24 * time.Hour)
		next := retentionRequest(t, s)
		if _, err := s.DecideWorking(next.ID, Decision{For: 30 * 24 * time.Hour}); err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.Snapshot(); err != nil {
			t.Fatal(err)
		}
		assertRecordGone(t, s, "requests", previous.ID)
		assertRecordExists(t, s, "requests", next.ID)
		if _, ok, err := fresh.LiveWorking("/ws", first.Repo, "refs/heads/renewed"); err != nil || !ok {
			t.Fatalf("renewal %d lost authority: %v %v", i, ok, err)
		}
		previous = next
	}
}

func TestDecideWorkingMixedBatchRetainsOnlyNeededRevisions(t *testing.T) {
	s, clock := workingFixture(t)
	s.Entries = func(q Request, _ bool) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/renewed", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/sibling", By: "human"},
		}, nil
	}
	first := retentionRequest(t, s)
	entries, err := s.DecideWorking(first.ID, Decision{For: 20 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// Renew only one revision; the other still depends on the original batch.
	s.Entries = func(q Request, _ bool) ([]WorkingEntry, error) {
		return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/renewed", By: "human"}}, nil
	}
	*clock = clock.Add(time.Hour)
	second := retentionRequest(t, s)
	if _, err := s.DecideWorking(second.ID, Decision{For: 30 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(7 * 24 * time.Hour)
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	if _, err := fresh.Snapshot(); err != nil {
		t.Fatal(err)
	}
	assertRecordExists(t, s, "requests", first.ID)
	assertRecordExists(t, s, "requests", second.ID)
	if err := fresh.Revoke(entries[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Snapshot(); err != nil {
		t.Fatal(err)
	}
	assertRecordGone(t, s, "requests", first.ID)
	if _, ok, err := fresh.LiveWorking("/ws", first.Repo, "refs/heads/renewed"); err != nil || !ok {
		t.Fatalf("lost renewed authority: %v %v", ok, err)
	}
	if _, ok, err := fresh.LiveWorking("/ws", first.Repo, "refs/heads/sibling"); err != nil || ok {
		t.Fatalf("revoked sibling: %v %v", ok, err)
	}
}

func retentionRequest(t *testing.T, s *Store) Request {
	t.Helper()
	q, err := s.File(Request{Jail: "j", Start: "s", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "d", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: 60 * 24 * time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func assertRecordExists(t *testing.T, s *Store, kind, id string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(s.Dir, kind, id+".json")); err != nil {
		t.Fatalf("%s %s missing: %v", kind, id, err)
	}
}

func assertRecordGone(t *testing.T, s *Store, kind, id string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(s.Dir, kind, id+".json")); !os.IsNotExist(err) {
		t.Fatalf("%s %s retained: %v", kind, id, err)
	}
}
