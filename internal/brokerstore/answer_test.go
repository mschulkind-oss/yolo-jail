package brokerstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
)

// answer_test.go pins the jail-readable record's account of HOW a request was answered
// (docs/design/jail-history.md JH-D11): each decision path names its kind and who ran it, a spend
// says when, a broker's stop says why, and the hook's run is recorded.

// answerFixture is a publishing store on a fresh workspace, at a fixed clock, with an Entries
// hook so the per-resource answers apply.
func answerFixture(t *testing.T) (*Store, Request, *time.Time) {
	t.Helper()
	ws := recordWorkspace(t)
	now := time.Date(2026, 11, 2, 22, 40, 16, 0, time.UTC)
	s := &Store{Dir: t.TempDir(), Source: "github", Revalidate: unchangedPolicy, PublishRecords: true,
		Now:           func() time.Time { return now },
		PublishFailed: func(_ Request, err error) { t.Errorf("publish failed: %v", err) },
		Entries: func(q Request) ([]WorkingEntry, error) {
			return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/agent/x", By: "human"}}, nil
		}}
	in := spendOrFileRequest()
	in.Workspace = ws
	return s, in, &now
}

func TestDenyNoteIsStoredWithDenialAndCannotAccompanyGrant(t *testing.T) {
	s, in, _ := answerFixture(t)
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	const note = "use the release branch"
	if _, err := s.Decide(q.ID, "pending", Decision{Deny: true, DenialNote: note, Via: ViaHost}); err != nil {
		t.Fatal(err)
	}
	requests, _, err := s.List()
	if err != nil || len(requests) != 1 || requests[0].DenialNote != note || requests[0].Answer != AnswerDeny {
		t.Fatalf("stored request: %+v err=%v", requests, err)
	}
	if rec := readPublished(t, in.Workspace, q.ID); rec.DenialNote != note || rec.Answer != approvalwait.AnswerDeny {
		t.Fatalf("published request: %+v", rec)
	}

	s, in, _ = answerFixture(t)
	q, err = s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(q.ID, "pending", Decision{DenialNote: note}); err == nil {
		t.Fatal("a denial note accompanied a grant decision")
	}
	if _, err := s.Decide(q.ID, "pending", Decision{Deny: true, DenialNote: strings.Repeat("x", MaxDenialNoteRunes+1)}); err == nil {
		t.Fatal("an oversized denial note was stored")
	}
	requests, _, err = s.List()
	if err != nil || len(requests) != 1 || requests[0].State != "pending" {
		t.Fatalf("invalid note decisions changed request: %+v err=%v", requests, err)
	}
}

func TestEachAnswerIsRecordedWithWhoGaveIt(t *testing.T) {
	cases := []struct {
		name    string
		decide  func(s *Store, id string) error
		answer  string
		via     string
		until   time.Duration // after the decision; zero for none
		state   string
		hookRan bool
	}{
		{"once", func(s *Store, id string) error {
			_, err := s.Decide(id, "pending", Decision{Via: ViaOnRequest})
			return err
		}, approvalwait.AnswerOnce, approvalwait.ViaOnRequest, 0, approvalwait.StateAllowed, false},
		{"for", func(s *Store, id string) error {
			_, err := s.Decide(id, "pending", Decision{For: 30 * time.Minute, Via: ViaHost})
			return err
		}, approvalwait.AnswerFor, approvalwait.ViaHost, 30 * time.Minute, approvalwait.StateAllowed, false},
		{"remember", func(s *Store, id string) error {
			_, err := s.Decide(id, "pending", Decision{Remember: true, Via: ViaHost})
			return err
		}, approvalwait.AnswerRemember, approvalwait.ViaHost, 0, approvalwait.StateAllowed, false},
		{"deny", func(s *Store, id string) error {
			_, err := s.Decide(id, "pending", Decision{Deny: true, Via: ViaHost})
			return err
		}, approvalwait.AnswerDeny, approvalwait.ViaHost, 0, approvalwait.StateDenied, false},
		{"per-resource for", func(s *Store, id string) error {
			_, err := s.DecideWorking(id, Decision{Via: ViaOnRequest})
			return err
		}, approvalwait.AnswerFor, approvalwait.ViaOnRequest, TimedGrantDefault, approvalwait.StateAllowed, true},
		{"add", func(s *Store, id string) error {
			_, err := s.DecideWorking(id, Decision{Add: true, Via: ViaHost})
			return err
		}, approvalwait.AnswerAdd, approvalwait.ViaHost, 0, approvalwait.StateAllowed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, in, now := answerFixture(t)
			q, err := s.File(in)
			if err != nil {
				t.Fatal(err)
			}
			rec := readPublished(t, in.Workspace, q.ID)
			if rec.Answer != "" || rec.Hook != approvalwait.HookNone || rec.Jail != in.Jail {
				t.Fatalf("filed: %+v", rec)
			}
			if c.hookRan {
				if err := s.NoteHookRan(q.ID); err != nil {
					t.Fatal(err)
				}
			}
			*now = now.Add(13 * time.Second)
			if err := c.decide(s, q.ID); err != nil {
				t.Fatal(err)
			}
			rec = readPublished(t, in.Workspace, q.ID)
			if rec.State != c.state || rec.Answer != c.answer || rec.Via != c.via || !rec.Decided.Equal(*now) {
				t.Fatalf("decided: %+v", rec)
			}
			if want := now.Add(c.until); (c.until == 0) != rec.Until.IsZero() || (c.until != 0 && !rec.Until.Equal(want)) {
				t.Fatalf("until %v, want %v after %v", rec.Until, c.until, *now)
			}
			if wantHook := map[bool]string{true: approvalwait.HookRan, false: approvalwait.HookNone}[c.hookRan]; rec.Hook != wantHook {
				t.Fatalf("hook %q, want %q", rec.Hook, wantHook)
			}
		})
	}
}

func TestASpendRecordsWhenItRan(t *testing.T) {
	s, in, now := answerFixture(t)
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(q.ID, "pending", Decision{Via: ViaHost}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if _, spent, _, err := s.SpendExactOrFileContext(context.Background(), in, s.Revalidate, nil); err != nil || !spent {
		t.Fatalf("spend: %v %v", spent, err)
	}
	if rec := readPublished(t, in.Workspace, q.ID); !rec.Used || !rec.SpentAt.Equal(*now) || rec.Answer != approvalwait.AnswerOnce {
		t.Fatalf("spent: %+v", rec)
	}
}

func TestABrokerStopSaysWhyItClosedTheRequest(t *testing.T) {
	s, in, _ := answerFixture(t)
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.End(in.Jail, in.Start); err != nil {
		t.Fatal(err)
	}
	if rec := readPublished(t, in.Workspace, q.ID); rec.State != approvalwait.StateRefused || rec.Reason != approvalwait.ReasonBrokerStopped || rec.Answer != "" {
		t.Fatalf("closed: %+v", rec)
	}
}

func TestAFailedHookOutranksItsRun(t *testing.T) {
	s, in, _ := answerFixture(t)
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.NoteHookRan(q.ID); err != nil {
		t.Fatal(err)
	}
	if rec := readPublished(t, in.Workspace, q.ID); rec.Hook != approvalwait.HookRan {
		t.Fatalf("ran: %+v", rec)
	}
	if err := s.NoteHookFailure(q.ID, "hook exited 1", ""); err != nil {
		t.Fatal(err)
	}
	if rec := readPublished(t, in.Workspace, q.ID); rec.Hook != approvalwait.HookFailed {
		t.Fatalf("failed: %+v", rec)
	}
}

// A filer cannot pre-answer its own request: the decision's fields are the store's.
func TestFilingDropsDecisionFieldsTheFilerSupplied(t *testing.T) {
	s, in, _ := answerFixture(t)
	in.Answer, in.Via, in.Reason, in.HookRan = AnswerOnce, ViaOnRequest, ReasonBrokerStopped, true
	in.Until, in.SpentAt = time.Now(), time.Now()
	q, err := s.File(in)
	if err != nil {
		t.Fatal(err)
	}
	if q.Answer != "" || q.Via != "" || q.Reason != "" || q.HookRan || !q.Until.IsZero() || !q.SpentAt.IsZero() {
		t.Fatalf("filed with the filer's decision fields: %+v", q)
	}
}
