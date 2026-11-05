package brokerstore

import (
	"context"
	"testing"
	"time"
)

func TestFileIfStillAsksReturnsRunsWithoutSpendingNewGrant(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision Decision
	}{
		{name: "exact allow once", decision: Decision{}},
		{name: "timed grant", decision: Decision{For: time.Minute}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
			input := spendOrFileRequest()
			pending, err := s.File(input)
			if err != nil {
				t.Fatal(err)
			}
			approved, err := s.Decide(pending.ID, "pending", tc.decision)
			if err != nil {
				t.Fatal(err)
			}
			filed, coveringGrant, covered, err := s.FileIfStillAsksContext(context.Background(), input, s.Revalidate)
			if err != nil || !covered || filed.ID != "" || coveringGrant.ID != approved.ID || coveringGrant.Expires != approved.Expires {
				t.Fatalf("filed=%+v grant=%+v covered=%v err=%v", filed, coveringGrant, covered, err)
			}
			requests, grants, err := s.List()
			if err != nil || len(requests) != 1 || len(grants) != 1 || grants[0].ID != approved.ID || grants[0].Uses != approved.Uses || grants[0].Uses == 0 {
				t.Fatalf("the pre-ask consumed or duplicated authority: requests=%+v grants=%+v err=%v", requests, grants, err)
			}
		})
	}
}

func TestFileIfStillAsksJoinsPendingRequest(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy}
	input := spendOrFileRequest()
	pending, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	filed, grant, covered, err := s.FileIfStillAsksContext(context.Background(), input, s.Revalidate)
	if err != nil || covered || grant.ID != "" || filed.ID != pending.ID {
		t.Fatalf("filed=%+v grant=%+v covered=%v err=%v", filed, grant, covered, err)
	}
	if filed.State != "pending" || filed.Expires().Before(time.Now()) {
		t.Fatalf("bad pending record: %+v", filed)
	}
}

func TestFileIfStillAsksLapsedOnceDoesNotConsumeFirstRealRetry(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s := &Store{Dir: t.TempDir(), Source: "git", Revalidate: unchangedPolicy, Now: func() time.Time { return now }}
	input := spendOrFileRequest()
	request, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.Decide(request.ID, "pending", Decision{})
	if err != nil {
		t.Fatal(err)
	}
	now = grant.Expires.Add(time.Second)
	for i := 0; i < 2; i++ {
		filed, _, covered, err := s.FileIfStillAsksContext(context.Background(), input, s.Revalidate)
		if id, ok := IsLapsedAllow(err); !ok || id != request.ID || covered || filed.ID != "" {
			t.Fatalf("pre-ask %d: %+v covered %v err %v", i, filed, covered, err)
		}
	}
	reqs, _, err := s.List()
	if err != nil || len(reqs) != 1 || reqs[0].Lapsed {
		t.Fatalf("pre-ask consumed lapsed marker: %+v %v", reqs, err)
	}
	if _, err := s.File(input); err == nil {
		t.Fatal("real first retry should refuse once")
	} else if id, ok := IsLapsedAllow(err); !ok || id != request.ID {
		t.Fatalf("real first retry: %v", err)
	}
	filed, _, covered, err := s.FileIfStillAsksContext(context.Background(), input, s.Revalidate)
	if err != nil || covered || filed.ID == "" || filed.ID == request.ID {
		t.Fatalf("post-refusal pre-ask: %+v covered %v err %v", filed, covered, err)
	}
}

func TestFileIfStillAsksSpentOnceAndTimedGrantAreNotLapsed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision Decision
		spend    bool
	}{
		{name: "spent once", decision: Decision{}, spend: true},
		{name: "expired timed", decision: Decision{For: time.Minute}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
			s := &Store{Dir: t.TempDir(), Source: "git", Revalidate: unchangedPolicy, Now: func() time.Time { return now }}
			input := spendOrFileRequest()
			request, err := s.File(input)
			if err != nil {
				t.Fatal(err)
			}
			grant, err := s.Decide(request.ID, "pending", tc.decision)
			if err != nil {
				t.Fatal(err)
			}
			if tc.spend {
				if _, spent, _, err := s.SpendExactOrFileContext(context.Background(), input, s.Revalidate, nil); err != nil || !spent {
					t.Fatalf("spend %v %v", spent, err)
				}
			}
			now = grant.Expires.Add(time.Second)
			filed, _, covered, err := s.FileIfStillAsksContext(context.Background(), input, s.Revalidate)
			if _, lapsed := IsLapsedAllow(err); lapsed || err != nil || covered || filed.ID == "" {
				t.Fatalf("not lapsed: %+v covered %v err %v", filed, covered, err)
			}
		})
	}
}
