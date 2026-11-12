package ghbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// GW-D75, GW-D79: a refusal names the shared `[repos.<name>]` table and the verbs that write it,
// never the retired `brokered.github.repos` key. A table alone is no runs grant, so a workflow
// command on a repository outside the scope names the table's own runs request too.
func TestAnOutOfScopeWorkflowCommandNamesTheTablesRunsRequest(t *testing.T) {
	const declare = "`yolo request repos <alias> github.com/other/private`"
	const runs = "`yolo request repos.<alias>.github runs <workflow file>`"
	for _, c := range []struct {
		argv     []string
		wantRuns bool
	}{
		{[]string{"workflow", "run", "deploy.yml", "-R", "other/private"}, true},
		{[]string{"run", "rerun", "17", "-R", "other/private"}, true},
		{[]string{"run", "cancel", "17", "-R", "other/private"}, true},
		{[]string{"pr", "view", "1", "-R", "other/private"}, false},
		{[]string{"pr", "create", "--title", "t", "--body", "b", "-R", "other/private"}, false},
	} {
		d := Classify(c.argv, "", testScope)
		if d.Outcome != OutcomeOutOfScope || !strings.Contains(d.Reason, declare) {
			t.Errorf("gh %v: outcome %q, want out of scope naming %s: %s", c.argv, d.Outcome, declare, d.Reason)
			continue
		}
		if got := strings.Contains(d.Reason, runs); got != c.wantRuns {
			t.Errorf("gh %v: names the table's runs request = %t, want %t: %s", c.argv, got, c.wantRuns, d.Reason)
		}
		if strings.Contains(d.Reason, "brokered.") {
			t.Errorf("gh %v: the refusal names a retired key: %s", c.argv, d.Reason)
		}
	}
}

// GW-D79: the unqualified `yolo request github` lines are for the workspace's origin alone, so a
// pending request's lasting-grant hint names the named-table form beside the unqualified one. A
// creation and a workflow run put the unqualified command in the menu, so their message also says
// that command is for the origin, naming the repository the write is on.
func TestAPendingRequestNamesTheNamedTableRequestBesideTheUnqualifiedOne(t *testing.T) {
	const id = "r-00000000000000000000000000000000"
	repo := writeTarget{Kind: targetRepo, Repo: "o/r"}
	const originOnly = "covers this workspace's origin alone, so use it only when o/r is that origin"
	for _, c := range []struct {
		t    writeTarget
		argv []string
		want []string
	}{
		{repo, []string{"run", "cancel", "17"}, []string{"`yolo request github runs <workflow file>`",
			"`yolo request repos.<alias>.github runs <workflow file>` on its `[repos.<alias>]` table"}},
		{repo, []string{"run", "rerun", "17"}, []string{"`yolo request github runs <workflow file>`",
			"`yolo request repos.<alias>.github runs <workflow file>` on its `[repos.<alias>]` table"}},
		{repo, approveArgv, []string{"`yolo request github approve_runs <actor>`",
			"`yolo request repos.<alias>.github approve_runs <actor>` on its `[repos.<alias>]` table"}},
		{writeTarget{Kind: targetCreatePR, Repo: "o/r"}, []string{"pr", "create", "--title", "t", "--body", "b", "--head", "agent/x"},
			[]string{"(`yolo request github create_pr`) " + originOnly,
				"`yolo request repos.<alias>.github create_pr` on its `[repos.<alias>]` table"}},
		{writeTarget{Kind: targetCreateIssue, Repo: "o/r"}, []string{"issue", "create", "--title", "t", "--body", "b"},
			[]string{"(`yolo request github create_issue`) " + originOnly,
				"`yolo request repos.<alias>.github create_issue` on its `[repos.<alias>]` table"}},
		{repo, []string{"workflow", "run", "nightly-macos.yml", "--ref", "main"},
			[]string{"(`yolo request github runs nightly-macos.yml`) " + originOnly,
				"`yolo request repos.<alias>.github runs nightly-macos.yml` on its `[repos.<alias>]` table"}},
	} {
		msg := pendingMessage(c.t, c.argv, id, "")
		for _, want := range c.want {
			if !strings.Contains(msg, want) {
				t.Errorf("gh %v: the pending message does not name %s:\n%s", c.argv, want, msg)
			}
		}
	}
}

// The same hint through the broker's real entry point: a creation or a workflow run on a
// repository a `[repos.<name>]` table put in the scope, which is not the workspace's origin,
// files a request on that repository, and the message the jail gets says the menu's unqualified
// command is for the origin alone and names the named-table request for that repository.
func TestAPendingCreationOrRunOnANamedRepositoryNamesItsTablesRequest(t *testing.T) {
	for _, c := range []struct {
		name                   string
		argv                   []string
		unqualified, qualified string
	}{
		{"pr create", []string{"pr", "create", "--title", "t", "--body", "b", "--head", "agent/x", "-R", "other/lib"},
			"yolo request github create_pr", "yolo request repos.<alias>.github create_pr"},
		{"issue create", []string{"issue", "create", "--title", "t", "--body", "b", "-R", "other/lib"},
			"yolo request github create_issue", "yolo request repos.<alias>.github create_issue"},
		{"workflow run", []string{"workflow", "run", "nightly-macos.yml", "--ref", "main", "-R", "other/lib"},
			"yolo request github runs nightly-macos.yml", "yolo request repos.<alias>.github runs nightly-macos.yml"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := windowFixture(t)
			if err := brokerscope.ReplaceRepos(b.authority.path, b.authority.scope, []string{"o/r", "other/lib"}); err != nil {
				t.Fatal(err)
			}
			code, msg := serveGH(b, c.argv...)
			if code != ExitTempFail {
				t.Fatalf("code %d, want the write pending: %s", code, msg)
			}
			q := pending(t, b)
			if len(q) != 1 || q[0].Repo != "other/lib" {
				t.Fatalf("pending requests %+v, want one on other/lib", q)
			}
			for _, want := range []string{
				"(`" + c.unqualified + "`) covers this workspace's origin alone, so use it only when other/lib is that origin",
				"for a repository other than this workspace's origin, `" + c.qualified + "` on its `[repos.<alias>]` table instead",
				"yolo approve " + q[0].ID, "yolo approve " + q[0].ID + " --for 15m", c.unqualified + " then the human", "yolo config apply", "yolo wait " + q[0].ID,
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the pending message does not hold %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "--add") {
				t.Errorf("the pending message offers the removed --add:\n%s", msg)
			}
		})
	}
}
