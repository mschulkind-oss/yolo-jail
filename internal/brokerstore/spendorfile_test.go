package brokerstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func spendOrFileRequest() Request {
	return Request{Jail: "jail-1", Start: "start-1", Workspace: "/workspace/app", Argv: []string{"pr", "merge", "1"}, Repo: "owner/repo", StdinDigest: "sha256:empty", Set: "read-write", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}}
}

// approveAfterFirstTransaction arms s so that, once, right after the next transaction on s
// releases the store's lock, every pending request is allowed by a second Store value on the
// same directory: a host decision landing exactly between two transactions of a caller.
func approveAfterFirstTransaction(t *testing.T, s *Store) *bool {
	t.Helper()
	host := &Store{Dir: s.Dir, Source: s.Source, Revalidate: s.Revalidate}
	fired := false
	s.AfterTransaction = func(error) {
		if fired {
			return
		}
		fired = true
		reqs, _, err := host.List()
		if err != nil {
			t.Error(err)
			return
		}
		for _, q := range reqs {
			if q.State == "pending" {
				if _, err := host.Decide(q.ID, "pending", Decision{}); err != nil {
					t.Error(err)
				}
			}
		}
	}
	return &fired
}

// A host decision that lands right after the caller's first transaction cannot fall between
// finding no grant and joining the pending request: SpendExactOrFileContext does both in one
// transaction, so it joins the pending request the decision then allows, and that grant is
// what the caller spends. No second request is filed.
func TestSpendExactOrFileJoinsTheRequestAnApprovalThenAllows(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	pending, err := s.File(spendOrFileRequest())
	if err != nil {
		t.Fatal(err)
	}
	fired := approveAfterFirstTransaction(t, s)
	g, spent, filed, err := s.SpendExactOrFileContext(context.Background(), spendOrFileRequest(), s.Revalidate, nil)
	if err != nil || spent || !*fired {
		t.Fatalf("spent %v, fired %v, err %v, grant %+v", spent, *fired, err, g)
	}
	if filed.ID != pending.ID {
		t.Fatalf("filed %s, not the pending %s the host then allowed", filed.ID, pending.ID)
	}
	if _, found, err := s.ConsumeExactDispatchContext(context.Background(), filed.Workspace, filed.Jail, filed.Set, filed.Digest, s.Revalidate, nil); err != nil || !found {
		t.Fatalf("the allowed request's grant was not spendable: found %v, %v", found, err)
	}
	reqs, _, err := s.List()
	if err != nil || len(reqs) != 1 {
		t.Fatalf("requests %+v, %v", reqs, err)
	}
}

// The approved-but-unspent state the two-transaction race left behind: the grant is spent,
// dispatch runs under the lock, and nothing is filed.
func TestSpendExactOrFileSpendsAnAllowedGrantAndFilesNothing(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	q, err := s.File(spendOrFileRequest())
	if err != nil {
		t.Fatal(err)
	}
	granted, err := s.Decide(q.ID, "pending", Decision{})
	if err != nil {
		t.Fatal(err)
	}
	dispatched := false
	g, spent, filed, err := s.SpendExactOrFileContext(context.Background(), spendOrFileRequest(), s.Revalidate, func() error {
		dispatched = true
		return nil
	})
	if err != nil || !spent || !dispatched || g.ID != granted.ID || g.Uses != 0 || filed.ID != "" {
		t.Fatalf("spent %v, dispatched %v, grant %+v, filed %+v, err %v", spent, dispatched, g, filed, err)
	}
	reqs, _, err := s.List()
	if err != nil || len(reqs) != 1 {
		t.Fatalf("requests %+v, %v", reqs, err)
	}
	// Single use: the next identical call files a new request.
	_, spent, filed, err = s.SpendExactOrFileContext(context.Background(), spendOrFileRequest(), s.Revalidate, nil)
	if err != nil || spent || filed.ID == "" || filed.ID == q.ID || filed.State != "pending" {
		t.Fatalf("after the spend: spent %v, filed %+v, err %v", spent, filed, err)
	}
}

// A failure while filing is a *FilingError carrying the store's typed cause, so callers keep
// their denial-cooldown and pending-limit answers; a failure while spending is not.
func TestSpendExactOrFileTypesFilingFailures(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	q, err := s.File(spendOrFileRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(q.ID, "pending", Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	_, spent, _, err := s.SpendExactOrFileContext(context.Background(), spendOrFileRequest(), s.Revalidate, nil)
	var filing *FilingError
	var denial *DenialCooldownError
	if spent || !errors.As(err, &filing) || !errors.As(err, &denial) || denial.Request != q.ID {
		t.Fatalf("a denied command refiled: spent %v, err %v", spent, err)
	}

	other := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	r, err := other.File(spendOrFileRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decide(r.ID, "pending", Decision{}); err != nil {
		t.Fatal(err)
	}
	refuse := func(Request) (Policy, error) { return Policy{}, errors.New("scope gone") }
	_, spent, _, err = other.SpendExactOrFileContext(context.Background(), spendOrFileRequest(), refuse, nil)
	if spent || err == nil || errors.As(err, &filing) {
		t.Fatalf("a spend failure: spent %v, err %v", spent, err)
	}
}

// A call that waited for its own answer spends it or finds it gone; it never files a second
// request, which is what the loser of two calls on one joined request would otherwise do.
func TestSpendExactPreparedSpendsOnceAndNeverFiles(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	q, err := s.File(spendOrFileRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(q.ID, "pending", Decision{}); err != nil {
		t.Fatal(err)
	}
	prepared, dispatched := 0, 0
	spend := func() (bool, error) {
		_, spent, err := s.SpendExactPreparedContext(context.Background(), spendOrFileRequest(), s.Revalidate,
			func() error { prepared++; return nil }, func() error { dispatched++; return nil })
		return spent, err
	}
	if spent, err := spend(); err != nil || !spent || prepared != 1 || dispatched != 1 {
		t.Fatalf("first: spent=%v prepared=%d dispatched=%d err=%v", spent, prepared, dispatched, err)
	}
	if spent, err := spend(); err != nil || spent || prepared != 1 || dispatched != 1 {
		t.Fatalf("second spent the used answer or ran: spent=%v prepared=%d dispatched=%d err=%v", spent, prepared, dispatched, err)
	}
	if reqs, _, err := s.List(); err != nil || len(reqs) != 1 {
		t.Fatalf("the loser filed a request: %+v %v", reqs, err)
	}
}
