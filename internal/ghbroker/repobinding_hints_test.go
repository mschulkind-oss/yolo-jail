package ghbroker

import (
	"strings"
	"testing"
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
// pending request's lasting-grant hint names the named-table form beside the unqualified one.
func TestAPendingRequestNamesTheNamedTableRequestBesideTheUnqualifiedOne(t *testing.T) {
	const id = "r-00000000000000000000000000000000"
	repo := writeTarget{Kind: targetRepo, Repo: "o/r"}
	for _, c := range []struct {
		argv []string
		want []string
	}{
		{[]string{"run", "cancel", "17"}, []string{"`yolo request github runs <workflow file>`",
			"`yolo request repos.<alias>.github runs <workflow file>` on its `[repos.<alias>]` table"}},
		{[]string{"run", "rerun", "17"}, []string{"`yolo request github runs <workflow file>`",
			"`yolo request repos.<alias>.github runs <workflow file>` on its `[repos.<alias>]` table"}},
		{approveArgv, []string{"`yolo request github approve_runs <actor>`",
			"`yolo request repos.<alias>.github approve_runs <actor>` on its `[repos.<alias>]` table"}},
	} {
		msg := pendingMessage(repo, c.argv, id, "")
		for _, want := range c.want {
			if !strings.Contains(msg, want) {
				t.Errorf("gh %v: the pending message does not name %s:\n%s", c.argv, want, msg)
			}
		}
	}
}
