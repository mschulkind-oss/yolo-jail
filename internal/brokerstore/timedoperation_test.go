package brokerstore

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func roleTimedRequest(t *testing.T) Request {
	t.Helper()
	chain := []CallResource{{Type: "repo", ID: "fixture/repo"}}
	return Request{Workspace: t.TempDir(), Jail: "j", Start: "s", Repo: "fixture/repo", Set: "write", Argv: []string{"mixed"}, StdinDigest: "body", Policy: Policy{Window: TimedGrantDefault, MaxWindow: 12 * time.Hour, Uses: 1}, CallReads: []CallRead{{Cell: "view", Chain: chain}}, CallWrites: []CallWrite{{Cell: "change", Chain: chain, Role: RoleWithheldWrite, Candidates: []string{"change", "rename"}}}}
}

func TestRoleTimedLimitNativeAndGenericSmallestRole(t *testing.T) {
	q := roleTimedRequest(t)
	bound, err := RoleTimedLimit(q, nil, true, time.Hour, 12*time.Hour)
	if err != nil || bound.Maximum != TimedGrantDefault || bound.Cell != "change" || bound.Role != RoleWithheldWrite {
		t.Fatalf("native withheld bound: %+v %v", bound, err)
	}
	bound, err = RoleTimedLimit(q, nil, true, time.Hour, 5*time.Minute)
	if err != nil || bound.Maximum != 5*time.Minute {
		t.Fatalf("smaller native write policy: %+v %v", bound, err)
	}
	cells := []TimedCell{{Name: "view", Kind: "read", Maximum: 2 * time.Hour}, {Name: "change", Kind: "write", Maximum: time.Hour}, {Name: "rename", Kind: "write", Maximum: 10 * time.Minute}}
	bound, err = RoleTimedLimit(q, cells, false, 0, 0)
	if err != nil || bound.Maximum != 10*time.Minute || bound.Cell != "rename" || bound.Role != RoleWithheldWrite {
		t.Fatalf("generic candidate bound: %+v %v", bound, err)
	}
	q.CallWrites = nil
	bound, err = RoleTimedLimit(q, nil, true, 0, 12*time.Hour)
	if err != nil || bound.Maximum != TimedGrantDefault || bound.Role != RoleRead {
		t.Fatalf("absent native read cap: %+v %v", bound, err)
	}
}

func TestRoleTimedNativeKnownWriteIgnoresHookAndPreservesMessage(t *testing.T) {
	q := roleTimedRequest(t)
	q.CallReads, q.CallWrites = nil, nil
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	store := &Store{Dir: t.TempDir(), Now: func() time.Time { return now }, NativeTimedPolicy: true, Entries: func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/main", By: "human"}}, nil
	}, TimedLimit: func(Request) (TimedBound, error) {
		t.Fatal("known native write invoked the new-role limiter")
		return TimedBound{}, nil
	}, Revalidate: func(current Request) (Policy, error) { return current.Policy, nil }}
	filed, err := store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecideWorking(filed.ID, Decision{For: 13 * time.Hour}); !errors.Is(err, ErrRefusedByPolicy) || err.Error() != "duration exceeds this set's policy ceiling of 12h0m0s" {
		t.Fatalf("known native message changed: %v", err)
	}
	entries, err := store.DecideWorking(filed.ID, Decision{For: 11 * time.Hour})
	if err != nil || len(entries) != 1 || entries[0].Resource != "refs/heads/main" || entries[0].Kind != WorkingBranch || !entries[0].Expires.Equal(filed.Filed.Add(11*time.Hour)) {
		t.Fatalf("native broad historical coverage: %+v %v", entries, err)
	}
}

func TestRoleTimedNamedReadUsesDeclaredCapIncludingReadSpelling(t *testing.T) {
	q := roleTimedRequest(t)
	q.CallWrites = nil
	q.CallReads[0].Cell = "read"
	bound, err := RoleTimedLimit(q, []TimedCell{{Name: "read", Kind: "read", Maximum: time.Hour}}, false, 0, 0)
	if err != nil || bound.Maximum != time.Hour {
		t.Fatalf("named read bypassed its declared cap: %+v %v", bound, err)
	}
	if _, err := RoleTimedLimit(q, nil, false, 0, 0); err == nil {
		t.Fatal("undeclared named read admitted")
	}
	q.CallReads[0].Cell = ""
	bound, err = RoleTimedLimit(q, nil, false, 0, 0)
	if err != nil || bound.Maximum != TimedGrantDefault {
		t.Fatalf("legacy unnamed read lost its default: %+v %v", bound, err)
	}
}

func TestRoleTimedMixedAnswerAtomicAndNoClamp(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		t.Run(strings.TrimSpace(map[bool]string{false: "commit", true: "rollback"}[failWrite]), func(t *testing.T) {
			q := roleTimedRequest(t)
			now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
			store := &Store{Dir: t.TempDir(), Now: func() time.Time { return now }, NativeTimedPolicy: true, Entries: OperationEntries, Revalidate: func(current Request) (Policy, error) { return current.Policy, nil }, TimedLimit: func(current Request) (TimedBound, error) {
				return RoleTimedLimit(current, nil, true, time.Hour, current.Policy.MaxWindow)
			}}
			filed, err := store.File(q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DecideWorking(filed.ID, Decision{For: time.Hour}); !errors.Is(err, ErrRefusedByPolicy) || !strings.Contains(err.Error(), "role withheld-write") {
				t.Fatalf("over-limit role refused: %v", err)
			}
			if failWrite {
				writes := 0
				store.beforeWrite = func(kind, id string) error {
					if kind == "working" {
						writes++
						if writes == 2 {
							return errors.New("injected second entry failure")
						}
					}
					return nil
				}
			}
			entries, err := store.DecideWorking(filed.ID, Decision{For: TimedGrantDefault})
			store.beforeWrite = nil
			snapshot, snapErr := store.Snapshot()
			if snapErr != nil {
				t.Fatal(snapErr)
			}
			if failWrite {
				if err == nil || len(snapshot.Working) != 0 || snapshot.Requests[0].State != "pending" {
					t.Fatalf("partial commit: %+v %v", snapshot, err)
				}
				return
			}
			if err != nil || len(entries) != 3 || len(snapshot.Working) != 3 {
				t.Fatalf("all-role answer: %+v %v", entries, err)
			}
			for _, entry := range entries {
				if !entry.Expires.Equal(now.Add(TimedGrantDefault)) {
					t.Fatalf("entry had a different expiry: %+v", entry)
				}
			}
		})
	}
}
