package brokerstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOperationBindingKeysSeparateRolesAndContexts(t *testing.T) {
	context := strings.Repeat("a", 64)
	keys := map[string]bool{}
	for _, role := range []string{RoleRead, RoleKnownWrite, RoleWithheldWrite} {
		for _, identity := range []string{"", context} {
			key, err := OperationResourceKey(role, `task:"a1"`, identity)
			if err != nil || keys[key] {
				t.Fatalf("colliding key %q: %v", key, err)
			}
			keys[key] = true
			if role == RoleKnownWrite && identity == "" && key != `task:"a1"` {
				t.Fatal("legacy key changed")
			}
		}
	}
	for _, role := range []string{"", "write", "READ"} {
		if _, err := OperationResourceKey(role, "x", ""); err == nil {
			t.Fatalf("invalid role accepted: %q", role)
		}
	}
}

func TestOperationBindingDigestBindsEveryNewSubject(t *testing.T) {
	base := Request{Argv: []string{"query", "one"}, Repo: "local-tasks", StdinDigest: "body", CallReads: []CallRead{{Cell: "view", Chain: []CallResource{{Type: "task", ID: "a1"}}}}, CallWrites: []CallWrite{{Cell: "change", Chain: []CallResource{{Type: "task", ID: "a1"}}, Role: RoleWithheldWrite, Candidates: []string{"change", "rename"}}}, ContextName: "Case 雪", ContextDigest: strings.Repeat("a", 64)}
	for _, mutate := range []func(*Request){
		func(q *Request) { q.Argv[1] = "two" },
		func(q *Request) { q.StdinDigest = "other-body" },
		func(q *Request) { q.CallReads[0].Chain[0].ID = "a2" },
		func(q *Request) { q.CallReads[0].Cell = "other" },
		func(q *Request) { q.CallWrites[0].Cell = "rename" },
		func(q *Request) { q.CallWrites[0].Candidates[1] = "destroy" },
		func(q *Request) { q.CallWrites[0].Role = RoleKnownWrite },
		func(q *Request) { q.ContextName = "case 雪" },
		func(q *Request) { q.ContextDigest = strings.Repeat("b", 64) },
	} {
		raw, _ := json.Marshal(base)
		var changed Request
		if err := json.Unmarshal(raw, &changed); err != nil {
			t.Fatal(err)
		}
		mutate(&changed)
		if CommandDigest(changed) == CommandDigest(base) {
			t.Fatalf("changed subject did not change digest: %+v", changed)
		}
	}
}

func TestOperationBindingMutationNeverSpendsOrDispatches(t *testing.T) {
	for _, mutate := range []func(*Request){
		func(q *Request) { q.Argv[1] = "two" },
		func(q *Request) { q.StdinDigest = "other-body" },
		func(q *Request) { q.CallReads[0].Chain[0].ID = "other" },
		func(q *Request) { q.CallReads[0].Cell = "other" },
		func(q *Request) { q.CallWrites[0].Candidates[1] = "other" },
		func(q *Request) { q.ContextDigest = strings.Repeat("b", 64) },
	} {
		q := roleTimedRequest(t)
		q.Argv = []string{"query", "one"}
		q.ContextName, q.ContextDigest = "Case 雪", strings.Repeat("a", 64)
		q.Digest = CommandDigest(q)
		expected := q.Digest
		store := &Store{Dir: t.TempDir(), Revalidate: func(current Request) (Policy, error) {
			if CommandDigest(current) != expected {
				return Policy{}, errors.New("fixture intent changed")
			}
			return current.Policy, nil
		}}
		filed, err := store.File(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Decide(filed.ID, "pending", Decision{}); err != nil {
			t.Fatal(err)
		}
		mutate(&q)
		dispatched := false
		_, spent, _, err := store.SpendExactOrFileContext(context.Background(), q, store.Revalidate, func() error { dispatched = true; return nil })
		snapshot, snapErr := store.Snapshot()
		if snapErr != nil {
			t.Fatal(snapErr)
		}
		if err == nil || spent || dispatched || len(snapshot.Grants) != 1 || snapshot.Grants[0].Uses != 1 || len(snapshot.Requests) != 1 {
			t.Fatalf("mutation affected store/effect: spent=%t dispatched=%t err=%v snapshot=%+v", spent, dispatched, err, snapshot)
		}
	}
}

func TestOperationBindingHTTPReadIntentIsNonReplayable(t *testing.T) {
	q := roleTimedRequest(t)
	q.CallWrites = nil
	q.Argv = []string{"http"}
	q.HTTPOriginalDigest, q.HTTPCanonicalDigest, q.HTTPDeclarationHash = strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	q.StdinDigest = q.HTTPOriginalDigest
	store := &Store{Dir: t.TempDir()}
	filed, err := store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(filed.CallReads) != 1 || len(filed.CallWrites) != 0 || filed.Digest != CommandDigest(q) {
		t.Fatalf("HTTP read subject omitted: %+v", filed)
	}
	q.Argv = []string{"http", "archived-body"}
	if _, err := store.File(q); err == nil {
		t.Fatal("HTTP intent admitted replayable argv")
	}
}

func TestOperationBindingRefusedBundleCreatesNothing(t *testing.T) {
	q := roleTimedRequest(t)
	q.Digest = CommandDigest(q)
	store := &Store{Dir: t.TempDir(), Revalidate: func(current Request) (Policy, error) {
		if current.Digest != q.Digest || len(current.CallReads) != 1 || len(current.CallWrites) != 1 {
			return Policy{}, errors.New("unexpected fixture intent")
		}
		return Policy{}, errors.New("one eligible-looking member is refused by current host policy")
	}}
	if _, err := store.File(q); err == nil {
		t.Fatal("refused bundle filed")
	}
	snapshot, err := store.Snapshot()
	if err != nil || len(snapshot.Requests)+len(snapshot.Grants)+len(snapshot.Working) != 0 {
		t.Fatalf("refused bundle mutated store: %+v %v", snapshot, err)
	}
}

func TestOperationBindingContextHasOnlyDeclaredIdentity(t *testing.T) {
	fields := map[string]string{"profile": "PRIVATE_SENTINEL", "region": "exact 雪"}
	first, err := ContextIdentity("local-tasks", "team:alpha", "Case 雪", fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]string){func(f map[string]string) { f["profile"] = "other" }, func(f map[string]string) { f["region"] = "exact snow" }} {
		copy := map[string]string{"profile": fields["profile"], "region": fields["region"]}
		mutate(copy)
		if digest, err := ContextIdentity("local-tasks", "team:alpha", "Case 雪", copy); err != nil || digest == first {
			t.Fatalf("identity unchanged: %q %v", digest, err)
		}
	}
	q := Request{ContextName: "Case 雪", ContextDigest: first}.WithWorkingState([]WorkingEntry{{Resource: "PRIVATE_SENTINEL"}}, time.Now())
	raw, _ := json.Marshal(q)
	if strings.Contains(string(raw), "PRIVATE_SENTINEL") || strings.Contains(string(raw), "currentWorking") {
		t.Fatalf("private values persisted: %s", raw)
	}
}

func TestOperationBindingLockedCallbackSeesCurrentMembership(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	q := Request{Workspace: t.TempDir(), Jail: "j", Start: "s", Repo: "r", Set: "write", Argv: []string{"update"}, StdinDigest: "body", Policy: Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 1}}
	store.Revalidate = func(current Request) (Policy, error) { return current.Policy, nil }
	filed, err := store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddWorking(WorkingEntry{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/main", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	seen := false
	store.Revalidate = func(current Request) (Policy, error) {
		working, now, bound := current.WorkingState()
		if !bound || now.IsZero() || len(working) != 1 || working[0].Resource != "refs/heads/main" {
			return Policy{}, errors.New("no locked membership state")
		}
		seen = true
		return current.Policy, nil
	}
	if _, err := store.Decide(filed.ID, "pending", Decision{}); err != nil || !seen {
		t.Fatalf("answer state: %v seen=%t", err, seen)
	}
	seen = false
	_, eligible, err := store.ExactGrantEligibleContext(context.Background(), q, store.Revalidate)
	if err != nil || !eligible || !seen {
		t.Fatalf("spend state: %v eligible=%t seen=%t", err, eligible, seen)
	}
}

func TestOperationBindingLegacyDigestVectors(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    Request
		want string
	}{
		{"synthetic-noncanonical", Request{Argv: []string{"push", "origin", "main"}, Repo: "acme/app", StdinDigest: "stdin"}, "0572ac1c03e43409a38304383d4ba3a6906baa2ca0a00830c1759abe9a1d4871"},
		{"generic", Request{Argv: []string{"update", "a1"}, OriginalArgv: []string{"update", "a1"}, Repo: "local-tasks", StdinDigest: "stdin", CallWrites: []CallWrite{{Cell: "change", Chain: []CallResource{{Type: "task", ID: "a1"}, {Type: "project", ID: "team:alpha"}}}}}, "a0c204d5b16777a9515480aa95f97778f0612d0c3a670c04d425f7e8b1f108a3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CommandDigest(tc.q); got != tc.want {
				t.Fatalf("legacy digest changed: %s", got)
			}
		})
	}
}
