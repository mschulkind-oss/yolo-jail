package ghbroker

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// A request says what it does without the argv: an action from yolo's vocabulary, a target
// and a headline built only from a validated repository and number, and the agent's own text
// (titles, workflow files, API paths, refs) in Title and Refs alone.
func TestAboutOfNamesTheWriteFromTheClassifiersParse(t *testing.T) {
	a, _ := authorityFixture(t)
	for _, c := range []struct {
		argv                     []string
		action, target, headline string
		title                    string
		refs                     []string
	}{
		{[]string{"pr", "create", "--title", "fix: Vec<T> deref && lexer", "--body", "b"},
			brokerstore.ActionPRCreate, "PR creation in o/r", "create a PR in o/r", "fix: Vec<T> deref && lexer", nil},
		{[]string{"pr", "merge", "70", "--squash", "--subject", "feat: <Config> loader"},
			brokerstore.ActionPRMerge, "PR o/r#70", "merge PR o/r#70", "feat: <Config> loader", nil},
		{[]string{"pr", "merge", "70", "--squash"}, brokerstore.ActionPRMerge, "PR o/r#70", "merge PR o/r#70", "", nil},
		{[]string{"pr", "comment", "5", "--body", "x"}, brokerstore.ActionPRWrite, "PR o/r#5", "write to PR o/r#5", "", nil},
		{[]string{"issue", "create", "--title", "t", "--body", "b"},
			brokerstore.ActionIssueCreate, "issue creation in o/r", "create an issue in o/r", "t", nil},
		{[]string{"issue", "comment", "42", "--body", "x"}, brokerstore.ActionIssueWrite, "issue o/r#42", "write to issue o/r#42", "", nil},
		{[]string{"issue", "edit", "5", "--title", "x"}, brokerstore.ActionIssueWrite, "issue o/r#5", "write to issue o/r#5", "x", nil},
		{[]string{"pr", "comment", "feat/x", "--body", "x"}, brokerstore.ActionPRWrite, "a PR in o/r", "write to a PR in o/r", "", nil},
		{[]string{"workflow", "run", "nightly.yml", "--ref", "main"},
			brokerstore.ActionWorkflowDispatch, "workflow runs on o/r", "run a workflow on o/r", "nightly.yml", []string{"main"}},
		{[]string{"api", "-X", "POST", "repos/o/r/labels", "-f", "name=x"},
			brokerstore.ActionAPI, "repository-level writes to o/r", "call the GitHub API on o/r", "repos/o/r/labels", nil},
		{[]string{"api", "-X", "POST", "repos/o/r/issues/5/comments", "-f", "body=x"},
			brokerstore.ActionIssueWrite, "o/r#5", "write to issue or PR o/r#5", "repos/o/r/issues/5/comments", nil},
		{[]string{"release", "create", "v1", "--title", "One & two"},
			brokerstore.ActionRepoWrite, "repository-level writes to o/r", "write to o/r", "One & two", nil},
	} {
		d, _, err := a.Classify(c.argv, "o/r")
		if err != nil {
			t.Errorf("%v: %v", c.argv, err)
			continue
		}
		got := aboutOf(d.Argv, targetOf(d.Argv, d.Repos))
		if got.Action != c.action || got.Target != c.target || got.Headline != c.headline || got.Title != c.title || !slices.Equal(got.Refs, c.refs) {
			t.Errorf("%v: %+v, want %s / %s / %s / %q / %q", c.argv, got, c.action, c.target, c.headline, c.title, c.refs)
		}
		for _, safe := range []string{got.Action, got.Target, got.Headline} {
			if !brokerstore.SafeLabel(safe) {
				t.Errorf("%v: %q is not safe", c.argv, safe)
			}
		}
	}
}

// The headline holds no free text: a title or a ref full of markup and invisible
// characters leaves it, the action and the target exactly as they were.
func TestAboutOfKeepsTheAgentsTextOutOfTheHeadline(t *testing.T) {
	a, _ := authorityFixture(t)
	about := func(argv ...string) brokerstore.About {
		d, _, err := a.Classify(argv, "o/r")
		if err != nil {
			t.Fatal(err)
		}
		return aboutOf(d.Argv, targetOf(d.Argv, d.Repos))
	}
	for _, nasty := range []string{"<b>x</b> & <a href=\"http://e\">y</a>", "ma\u200bin", "\u202eevil", "a'b\"c", "&amp;"} {
		for _, pair := range [][2]brokerstore.About{
			{about("pr", "create", "--title", "plain", "--body", "b"), about("pr", "create", "--title", nasty, "--body", "b")},
			{about("pr", "merge", "70", "--subject", "plain"), about("pr", "merge", "70", "--subject", nasty)},
			{about("workflow", "run", "nightly.yml", "--ref", "main"), about("workflow", "run", "nightly.yml", "--ref", nasty)},
		} {
			plain, got := pair[0], pair[1]
			if got.Action != plain.Action || got.Target != plain.Target || got.Headline != plain.Headline {
				t.Errorf("%q changed the safe fields: %+v, want %+v", nasty, got, plain)
			}
		}
	}
}
