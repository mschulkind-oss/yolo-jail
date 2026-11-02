package gitbroker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// refusedPushCommands runs a real git push of HEAD to main through the fixture's broker with
// no grant covering it, and returns the ref commands the broker read from it, as its audit
// line records them.
func refusedPushCommands(t *testing.T, f *fixture) []gitproto.Command {
	t.Helper()
	f.commit("one")
	before := f.upstreamRef("refs/heads/main")
	if out, err := f.git("push", "origin", "HEAD:main"); err == nil {
		t.Fatalf("an unapproved push to main went through:\n%s", out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved")
	}
	var cmds []gitproto.Command
	for _, r := range lastPush(t).Refs {
		cmds = append(cmds, gitproto.Command{Old: r.Old, New: r.New, Ref: r.Ref})
	}
	if len(cmds) != 1 || cmds[0].Ref != "refs/heads/main" || cmds[0].New != mustGit(t, f.ws, "rev-parse", "HEAD") {
		t.Fatalf("commands %+v", cmds)
	}
	return cmds
}

// pendingID is the request id a refusal reason names.
func pendingID(t *testing.T, reason string) string {
	t.Helper()
	id, ok := strings.CutPrefix(reason, "needs approval: ")
	if !ok || !strings.HasPrefix(id, "r-") || len(id) < 34 {
		t.Fatalf("refusal %q names no request", reason)
	}
	return id[:34]
}

func TestAskForPushSpendsExactGrantOnce(t *testing.T) {
	f := newFixture(t, "")
	f.broker.waitLimit = 50 * time.Millisecond
	cmds := refusedPushCommands(t, f)
	repo, _ := gitproto.Identity(origin)
	ctx := context.Background()

	ev := &brokeraudit.Event{}
	_, reason, ok := f.broker.askForPush(ctx, repo, cmds, nil, []string{"refs/heads/main"}, nil, ev)
	if ok {
		t.Fatal("a push nobody approved was allowed")
	}
	id := pendingID(t, reason.reason)
	if ev.Request != id || ev.CommandDigest == "" {
		t.Fatalf("audit event %+v", ev)
	}
	if !strings.Contains(f.log.String(), "request "+id+" is pending; run `yolo approve "+id+"` on the host") {
		t.Fatalf("the broker log has no notice for %s:\n%s", id, f.log.String())
	}
	if strings.Contains(f.log.String(), "refs/heads/main") || strings.Contains(f.log.String(), f.ws) {
		t.Fatalf("the shared broker log names a ref or the workspace:\n%s", f.log.String())
	}
	reqs, _, err := ApprovalStore().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].ID != id || len(reqs[0].NeedsApproval) != 1 || reqs[0].NeedsApproval[0] != "refs/heads/main" {
		t.Fatalf("filed requests %+v", reqs)
	}
	// The filed request says what it does, for a notifier (pushAbout).
	if a := reqs[0].About; a.Action != brokerstore.ActionPush || a.Headline != "push 1 ref to "+repo || len(a.Refs) != 1 || a.Refs[0] != "refs/heads/main" {
		t.Fatalf("filed request's about %+v", a)
	}

	g, err := ApprovalStore().Decide(id, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	ev = &brokeraudit.Event{}
	spent, reason, ok := f.broker.askForPush(ctx, repo, cmds, nil, []string{"refs/heads/main"}, nil, ev)
	if !ok || spent.ID != g.ID || spent.Uses != 0 || ev.Grant != g.ID || ev.Request != id {
		t.Fatalf("the approved push: ok %v, reason %q, grant %+v, event %+v", ok, reason, spent, ev)
	}

	// Spent: the same push again files a new request.
	_, reason, ok = f.broker.askForPush(ctx, repo, cmds, nil, []string{"refs/heads/main"}, nil, &brokeraudit.Event{})
	if ok || pendingID(t, reason.reason) == id {
		t.Fatalf("a spent Allow once covered a second push: ok %v, %q", ok, reason)
	}

	// A different push (another new id) is not covered by an exact grant on the first.
	other := []gitproto.Command{{Old: cmds[0].Old, New: cmds[0].Old, Ref: cmds[0].Ref}}
	g2id := pendingID(t, reason.reason)
	if _, err := ApprovalStore().Decide(g2id, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.broker.askForPush(ctx, repo, other, nil, []string{"refs/heads/main"}, nil, &brokeraudit.Event{}); ok {
		t.Fatal("an Allow once covered a push it did not name")
	}
}

func TestAskForPushWaitsForTheAnswer(t *testing.T) {
	f := newFixture(t, "")
	// The refused git push leaves its request, for the same push as cmds, pending.
	cmds := refusedPushCommands(t, f)
	f.broker.waitLimit = 10 * time.Second
	repo, _ := gitproto.Identity(origin)
	for _, deny := range []bool{false, true} {
		// The host answers every pending request until the push returns, not only the first
		// it sees: on the first round the request the git push left pending is one, and when
		// it is answered between askForPush's first spend and its filing, the filing makes a
		// new request, which is the one askForPush waits on.
		done := make(chan struct{})
		decided := make(chan error, 1)
		go func() {
			var first error
			defer func() { decided <- first }()
			for {
				reqs, _, err := ApprovalStore().List()
				if err != nil {
					first = err
					return
				}
				for _, q := range reqs {
					if q.State != "pending" {
						continue
					}
					if _, err := ApprovalStore().Decide(q.ID, "pending", brokerstore.Decision{Deny: deny}); err != nil && first == nil {
						first = err
					}
				}
				select {
				case <-done:
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
		start := time.Now()
		push := cmds
		if deny {
			push = []gitproto.Command{{Old: cmds[0].Old, New: cmds[0].Old, Ref: cmds[0].Ref}}
		}
		_, reason, ok := f.broker.askForPush(context.Background(), repo, push, nil, []string{"refs/heads/main"}, nil, &brokeraudit.Event{})
		took := time.Since(start)
		close(done)
		if err := <-decided; err != nil {
			t.Fatal(err)
		}
		if took > 5*time.Second {
			t.Fatalf("the answer took %v to reach the push", took)
		}
		if ok == deny {
			t.Fatalf("deny %v: ok %v, %q", deny, ok, reason)
		}
		if deny && !strings.Contains(reason.reason, "denied") {
			t.Fatalf("a denied push says %q", reason)
		}
	}
}

// The host answers the push's pending request right after askForPush's first store
// transaction. Spending and filing were two transactions, so that answer landed between them:
// the filing found no pending request to join, filed a new one nobody answered, and the push
// waited out its whole wait and was refused while the grant it was answered with went
// unspent. They are one transaction now, so the push joins the request the host then allows
// and spends its grant.
func TestAskForPushSpendsAnAnswerLandingAfterItsFirstTransaction(t *testing.T) {
	f := newFixture(t, "")
	// The refused git push leaves its request, for the same push as cmds, pending.
	cmds := refusedPushCommands(t, f)
	f.broker.waitLimit = 2 * time.Second
	repo, _ := gitproto.Identity(origin)
	reqs, _, err := ApprovalStore().List()
	if err != nil || len(reqs) != 1 || reqs[0].State != "pending" {
		t.Fatalf("the refused push left %+v, %v", reqs, err)
	}
	pending := reqs[0].ID
	fired := false
	f.broker.store.AfterTransaction = func(error) {
		if fired {
			return
		}
		fired = true
		if _, err := ApprovalStore().Decide(pending, "pending", brokerstore.Decision{}); err != nil {
			t.Error(err)
		}
	}
	ev := &brokeraudit.Event{}
	g, reason, ok := f.broker.askForPush(context.Background(), repo, cmds, nil, []string{"refs/heads/main"}, nil, ev)
	if !fired || !ok || g.Request != pending || ev.Request != pending || ev.Grant != g.ID {
		t.Fatalf("answered %v: ok %v, %q, grant %+v, event %+v", fired, ok, reason, g, ev)
	}
	reqs, grants, err := ApprovalStore().List()
	if err != nil || len(reqs) != 1 || len(grants) != 1 || grants[0].Uses != 0 {
		t.Fatalf("after the push: requests %+v, grants %+v, %v", reqs, grants, err)
	}
}

// An Allow once is spent only on the exact push it approved: the same push with only the old
// id, only the repository, or only the workspace changed spends nothing, and the exact push
// then still spends it.
func TestAllowOnceSpendsOnlyOnItsExactPush(t *testing.T) {
	f := newFixture(t, "")
	f.broker.waitLimit = 50 * time.Millisecond
	cmds := refusedPushCommands(t, f)
	repo, _ := gitproto.Identity(origin)
	ctx := context.Background()
	needs := []string{"refs/heads/main"}
	_, first, _ := f.broker.askForPush(ctx, repo, cmds, nil, needs, nil, &brokeraudit.Event{})
	g, err := ApprovalStore().Decide(pendingID(t, first.reason), "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	unspent := func(what string) {
		t.Helper()
		_, gs, err := ApprovalStore().List()
		if err != nil || len(gs) != 1 || gs[0].ID != g.ID || gs[0].Uses != 1 {
			t.Fatalf("%s: the grant was spent: %v %+v", what, err, gs)
		}
	}

	otherOld := []gitproto.Command{{Old: strings.Repeat("a", 40), New: cmds[0].New, Ref: cmds[0].Ref}}
	if _, _, ok := f.broker.askForPush(ctx, repo, otherOld, nil, needs, nil, &brokeraudit.Event{}); ok {
		t.Fatal("a push with another old id spent the Allow once")
	}
	unspent("only the old id differs")

	if _, _, ok := f.broker.askForPush(ctx, "fixture.test/o/other", cmds, nil, needs, nil, &brokeraudit.Event{}); ok {
		t.Fatal("a push to another repository spent the Allow once")
	}
	unspent("only the repository differs")

	bound := f.broker.bound
	f.broker.bound.Workspace = bound.Workspace + "-other"
	_, _, ok := f.broker.askForPush(ctx, repo, cmds, nil, needs, nil, &brokeraudit.Event{})
	f.broker.bound = bound
	if ok {
		t.Fatal("a push from another workspace spent the Allow once")
	}
	unspent("only the workspace differs")

	if spent, why, ok := f.broker.askForPush(ctx, repo, cmds, nil, needs, nil, &brokeraudit.Event{}); !ok || spent.ID != g.ID {
		t.Fatalf("the exact push did not spend the Allow once: %+v %+v", spent, why)
	}
}
