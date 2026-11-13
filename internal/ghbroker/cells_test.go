package ghbroker

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// cells_test.go pins the working set's cells (docs/design/github-write-scopes.md §1, §3,
// GW-D2 to GW-D5, GW-D41, GW-D42, GW-D56): the verb each write names, and that a working-set
// entry covers its item's comments and edits, the shipped defaults, but not an approval, which
// asks.

func TestTargetOfNamesTheCellVerb(t *testing.T) {
	a, _ := authorityFixture(t)
	for _, c := range []struct {
		argv []string
		kind string
		verb string
	}{
		{[]string{"pr", "comment", "32", "--body", "x"}, targetPR, verbComment},
		{[]string{"issue", "comment", "5", "--body", "x"}, targetIssue, verbComment},
		{[]string{"pr", "review", "32", "--approve"}, targetPR, verbApprove},
		{[]string{"pr", "review", "32", "--request-changes", "--body", "no"}, targetPR, verbEdit},
		{[]string{"pr", "review", "32", "--comment", "--body", "hm"}, targetPR, verbComment},
		{[]string{"pr", "review", "32", "--body", "hm"}, targetPR, verbComment},
		{[]string{"pr", "edit", "32", "--title", "x"}, targetPR, verbEdit},
		{[]string{"pr", "ready", "32"}, targetPR, verbEdit},
		{[]string{"pr", "ready", "32", "--undo"}, targetPR, verbEdit},
		{[]string{"pr", "close", "32"}, targetPR, verbEdit},
		{[]string{"pr", "reopen", "32"}, targetPR, verbEdit},
		{[]string{"issue", "close", "5"}, targetIssue, verbEdit},
		{[]string{"issue", "edit", "5", "--title", "x"}, targetIssue, verbEdit},
		{[]string{"pr", "merge", "32", "--auto", "--squash"}, targetMerge, verbMerge},
		{[]string{"pr", "merge", "32", "--disable-auto"}, targetMerge, verbMerge},
		{[]string{"api", "-X", "PUT", "repos/o/r/pulls/5/merge"}, targetMerge, verbMerge},
		{[]string{"api", "-X", "PATCH", "repos/o/r/pulls/5", "-f", "title=x"}, targetPR, verbEdit},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/requested_reviewers", "-f", "reviewers[]=x"}, targetPR, verbEdit},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/comments", "-f", "body=x"}, targetPR, verbComment},
		{[]string{"api", "-X", "PUT", "repos/o/r/pulls/5/reviews/77", "-f", "body=x"}, targetPR, verbComment},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "body=x"}, targetPR, verbComment},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "event=COMMENT"}, targetPR, verbComment},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "event=APPROVE"}, targetPR, verbApprove},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-F", "event=APPROVE"}, targetPR, verbApprove},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "event=REQUEST_CHANGES"}, targetPR, verbEdit},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews/77/events", "-f", "event=APPROVE"}, targetPR, verbApprove},
		{[]string{"api", "-X", "PATCH", "repos/o/r/issues/5", "-f", "state=closed"}, targetIssueOrPR, verbEdit},
		{[]string{"api", "-X", "POST", "repos/o/r/issues/5/comments", "-f", "body=x"}, targetIssueOrPR, verbComment},
		{[]string{"api", "-X", "POST", "repos/o/r/issues/5/assignees", "-f", "assignees[]=x"}, targetIssueOrPR, verbEdit},
		// A verdict not written plainly on the command line asks as a repository write (GW-D2).
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "--input", "-"}, targetRepo, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "Event=APPROVE"}, targetRepo, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "event[]=APPROVE"}, targetRepo, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "event=approve"}, targetRepo, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews", "-f", "event=COMMENT", "-f", "event=APPROVE"}, targetRepo, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/5/reviews/77/events", "--input", "-"}, targetRepo, ""},
		{[]string{"api", "-X", "POST", "repos/o/r/labels", "-f", "name=x"}, targetRepo, ""},
		{[]string{"pr", "create", "--title", "t", "--body", "b"}, targetCreatePR, ""},
	} {
		d, _, err := a.Classify(c.argv, "o/r")
		if err != nil {
			t.Errorf("%v: %v", c.argv, err)
			continue
		}
		if got := targetOf(d.Argv, d.Repos); got.Kind != c.kind || got.Verb != c.verb {
			t.Errorf("%v: target %+v, want %s %q", c.argv, got, c.kind, c.verb)
		}
	}
}

func TestCellOfReadsPROrIssueFromTheItem(t *testing.T) {
	for _, c := range []struct {
		t    writeTarget
		pr   bool
		want string
	}{
		{writeTarget{Kind: targetIssueOrPR, Verb: verbComment}, true, "comment_prs"},
		{writeTarget{Kind: targetIssueOrPR, Verb: verbComment}, false, "comment_issues"},
		// A command names a PR, but the item read is what decides.
		{writeTarget{Kind: targetIssue, Verb: verbEdit}, true, "edit_prs"},
		{writeTarget{Kind: targetPR, Verb: verbEdit}, false, "edit_issues"},
		{writeTarget{Kind: targetPR, Verb: verbApprove}, false, "approve_prs"},
		{writeTarget{Kind: targetMerge, Verb: verbMerge}, false, "merge_prs"},
		{writeTarget{Kind: targetRepo}, true, ""},
	} {
		if got := cellOf(c.t, c.pr); got != c.want {
			t.Errorf("cellOf(%+v, %v) = %q, want %q", c.t, c.pr, got, c.want)
		}
	}
}

// TestAWorkingSetPRRunsItsShippedCellsAndAsksForAnApproval drives the broker's entry point: on
// a PR the agent created, a comment, an edit and a review that does not approve run unasked,
// each audited with its cell; an approval, by flag or by REST event, asks and names the cell it
// lacks; a human's timed answer to that request then covers it.
func TestAWorkingSetPRRunsItsShippedCellsAndAsksForAnApproval(t *testing.T) {
	b, gh := workingSetBroker(t)
	b.store.Entries = RequestEntries
	if _, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r",
		Kind: brokerstore.WorkingPR, Resource: "#7", By: GrantCreatePR}); err != nil {
		t.Fatal(err)
	}
	writes := 0
	for _, c := range []struct {
		argv []string
		cell string
	}{
		{[]string{"pr", "comment", "7", "--body", "x"}, "working_set.comment_prs"},
		{[]string{"pr", "edit", "7", "--title", "y"}, "working_set.edit_prs"},
		{[]string{"pr", "review", "7", "--request-changes", "--body", "no"}, "working_set.edit_prs"},
		{[]string{"api", "-X", "POST", "repos/o/r/pulls/7/reviews", "-f", "event=COMMENT", "-f", "body=hm"}, "working_set.comment_prs"},
	} {
		code, msg := serveGH(b, c.argv...)
		writes++
		if code != 0 || gh.writes() != writes {
			t.Fatalf("%v on a working-set PR: code %d, writes %d: %s", c.argv, code, gh.writes(), msg)
		}
		if ev := lastAudit(t); ev.Set != setWorkingSet || ev.Cell != c.cell {
			t.Fatalf("%v audited as set %q cell %q, want %q", c.argv, ev.Set, ev.Cell, c.cell)
		}
	}
	for _, argv := range [][]string{
		{"pr", "review", "7", "--approve"},
		{"api", "-X", "POST", "repos/o/r/pulls/7/reviews", "-f", "event=APPROVE"},
	} {
		code, msg := serveGH(b, argv...)
		if code != ExitTempFail || gh.writes() != writes {
			t.Fatalf("%v on a working-set PR ran unasked: code %d, writes %d: %s", argv, code, gh.writes(), msg)
		}
		if !strings.Contains(msg, "approving PR o/r#7 asks here") || !strings.Contains(msg, "working_set.approve_prs is not granted") {
			t.Fatalf("%v: the stop does not name the cell: %s", argv, msg)
		}
	}
	// The human answers the flag spelling's request for fifteen minutes; the approval runs
	// under that timed entry, not under the working-set member.
	var approve brokerstore.Request
	for _, q := range pending(t, b) {
		if len(q.Argv) > 1 && q.Argv[1] == "review" {
			approve = q
		}
	}
	if approve.ID == "" {
		t.Fatalf("no request for the approval: %+v", pending(t, b))
	}
	entries, err := b.store.DecideWorking(approve.ID, brokerstore.Decision{For: 15 * time.Minute})
	if err != nil || len(entries) != 1 || !entries[0].Timed() {
		t.Fatalf("--for on the approval: %+v %v", entries, err)
	}
	if code, msg := serveGH(b, "pr", "review", "7", "--approve"); code != 0 || gh.writes() != writes+1 {
		t.Fatalf("an approval under a timed answer: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if ev := lastAudit(t); ev.Set != setTimed || ev.Grant != entries[0].ID || ev.Cell != "" {
		t.Fatalf("the timed approval audited as %+v", ev)
	}
}

// TestAWorkingSetIssueRunsItsIssueCells pins that the cell's noun is the item's: a comment on
// an issue in the working set is a comment_issues write, whichever command spelled it.
func TestAWorkingSetIssueRunsItsIssueCells(t *testing.T) {
	b, gh := workingSetBroker(t)
	gh.issue = true
	if _, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r",
		Kind: brokerstore.WorkingIssue, Resource: "#9", By: GrantCreateIssue}); err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct {
		argv []string
		cell string
	}{
		{[]string{"issue", "comment", "9", "--body", "x"}, "working_set.comment_issues"},
		{[]string{"api", "-X", "POST", "repos/o/r/issues/9/comments", "-f", "body=x"}, "working_set.comment_issues"},
		{[]string{"issue", "close", "9"}, "working_set.edit_issues"},
	} {
		if code, msg := serveGH(b, c.argv...); code != 0 || gh.writes() != i+1 {
			t.Fatalf("%v on a working-set issue: code %d, writes %d: %s", c.argv, code, gh.writes(), msg)
		}
		if ev := lastAudit(t); ev.Cell != c.cell {
			t.Fatalf("%v audited with cell %q, want %q", c.argv, ev.Cell, c.cell)
		}
	}
}

// TestAMergeThatDeletesItsBranchAsksEvenUnderATimedMerge pins GW-D56 through the broker: a
// human's timed answer on merging a PR covers the merge, not a merge that also deletes the
// head branch, which asks as a repository-level write.
func TestAMergeThatDeletesItsBranchAsksEvenUnderATimedMerge(t *testing.T) {
	b, gh := workingSetBroker(t)
	if _, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r",
		Kind: brokerstore.WorkingMerge, Resource: "#7", By: "human", Expires: b.now().Add(15 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if code, msg := serveGH(b, "pr", "merge", "7", "--squash", "--delete-branch"); code != ExitTempFail || gh.writes() != 0 {
		t.Fatalf("a merge deleting its branch under a timed merge: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if code, msg := serveGH(b, "pr", "merge", "7", "--squash"); code != 0 || gh.writes() != 1 {
		t.Fatalf("the same merge without --delete-branch: code %d, writes %d: %s", code, gh.writes(), msg)
	}
}
