package ghbroker

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// RequestAbout is what the request for the canonical argv on repos does, as the broker
// records it when it files one.
func RequestAbout(argv, repos []string) brokerstore.About {
	return aboutOf(argv, targetOf(argv, repos))
}

// aboutOf is what a GitHub write request does, for a person (brokerstore.About), read from
// the canonical argv and its target as the broker classified them. Action, Target and
// Headline are built only from yolo's vocabulary, a ValidRepo name and a number; the title,
// the workflow file, the API path and the dispatch ref are the agent's, and go in Title and
// Refs alone.
func aboutOf(argv []string, t writeTarget) brokerstore.About {
	repo, where := t.Repo, t.Repo
	if !ValidRepo(repo) {
		// targetOf names a repository only when the command names exactly one valid one.
		repo, where = "", "several repositories"
	}
	p, err := parseArgv(argv)
	path := ""
	if err == nil && p.cmd != nil {
		path = p.cmd.path
	}
	last := func(long string) string {
		if p == nil {
			return ""
		}
		if v := p.values(long); len(v) > 0 {
			return v[len(v)-1]
		}
		return ""
	}
	var a brokerstore.About
	if w, ok := dispatchOf(argv, t); ok || path == "workflow run" {
		a.Action, a.Target, a.Headline = brokerstore.ActionWorkflowDispatch, "workflow runs on "+where, "run a workflow on "+where
		switch {
		case ok:
			a.Title = w.Workflow
			if w.Ref != "" {
				a.Refs = []string{w.Ref}
			}
		case len(p.positionals) > 0:
			a.Title = p.positionals[0]
			if r := last("ref"); r != "" {
				a.Refs = []string{r}
			}
		}
		return a
	}
	if repo != "" {
		a.Target = t.describe()
	}
	switch t.Kind {
	case targetCreatePR:
		a.Action, a.Headline = brokerstore.ActionPRCreate, "create a PR in "+where
	case targetCreateIssue:
		a.Action, a.Headline = brokerstore.ActionIssueCreate, "create an issue in "+where
	case targetMerge:
		a.Action, a.Headline = brokerstore.ActionPRMerge, "merge "+t.describe()
	case targetPR:
		a.Action, a.Headline = brokerstore.ActionPRWrite, "write to "+t.describe()
	case targetIssue:
		a.Action, a.Headline = brokerstore.ActionIssueWrite, "write to "+t.describe()
	case targetIssueOrPR:
		a.Action, a.Headline = brokerstore.ActionIssueWrite, fmt.Sprintf("write to issue or PR %s#%d", t.Repo, t.Number)
	case targetUnresolved:
		// A PR or issue write naming no number the broker can read: its selector is the
		// agent's, so the target says only which kind, in which repository.
		switch {
		case path == "pr merge":
			a.Action, a.Target, a.Headline = brokerstore.ActionPRMerge, "a PR in "+where, "merge a PR in "+where
		case len(path) > 3 && path[:3] == "pr ":
			a.Action, a.Target, a.Headline = brokerstore.ActionPRWrite, "a PR in "+where, "write to a PR in "+where
		default:
			a.Action, a.Target, a.Headline = brokerstore.ActionIssueWrite, "an issue in "+where, "write to an issue in "+where
		}
	default:
		a.Action, a.Headline = brokerstore.ActionRepoWrite, "write to "+where
		if path == "api" {
			a.Action, a.Headline = brokerstore.ActionAPI, "call the GitHub API on "+where
		}
		if repo == "" {
			a.Target = "repository-level writes to " + where
		}
	}
	switch {
	case path == "api" && len(p.positionals) > 0:
		a.Title = p.positionals[0]
	case path == "pr merge":
		a.Title = last("subject")
	default:
		a.Title = last("title")
	}
	return a
}
