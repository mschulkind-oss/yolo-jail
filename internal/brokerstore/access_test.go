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
