package ghbroker

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// writetarget.go names a GitHub write's kind and resource from the classifier's own parse
// (docs/design/boundary-broker.md §5.7, "Write kinds and their resources"): which working-set
// entry, user grant or timed grant could cover it, and what a human's `yolo approve <id>
// --for` or `--add` would record. It reads the CANONICAL argv the broker runs, never the
// jail's spelling, so the request store's argv gives the same answer on the host.
//
// A command the table does not name is a repository-level write, which only asks; so is a PR
// or issue write whose number the broker cannot read from the argv (a branch selector, or none,
// which gh would resolve from a checkout the broker does not have): that one is
// targetUnresolved, answered Allow once alone, since a timed grant on the whole repository
// would cover far more than the human was shown.

// The write kinds (§5.7).
const (
	targetPR          = "pr-write"     // pr comment/edit/review/ready/close/reopen; gh api under pulls/{n}
	targetIssue       = "issue-write"  // issue comment/edit/close/reopen
	targetIssueOrPR   = "issue-or-pr"  // gh api under issues/{n}: a PR or an issue, one number space
	targetMerge       = "pr-merge"     // pr merge; gh api PUT pulls/{n}/merge
	targetCreatePR    = "pr-create"    // pr create; gh api POST repos/{o}/{r}/pulls
	targetCreateIssue = "issue-create" // issue create; gh api POST repos/{o}/{r}/issues
	targetRepo        = "repo-write"   // every other write: releases, labels, workflow runs, settings, …
	targetUnresolved  = "unresolved"   // a PR or issue write naming no number the broker can read
)

// The user grant kinds the GitHub source defines (OQ-BB19), as packs/github declares them.
const (
	GrantCreatePR    = "create-pr"
	GrantCreateIssue = "create-issue"
)

// writeTarget is one write's kind and resource.
type writeTarget struct {
	Kind   string
	Repo   string // OWNER/REPO as the canonical argv names it
	Number int    // the PR's or issue's number, for the kinds that name one
}

var (
	prWrites    = []string{"pr comment", "pr edit", "pr review", "pr ready", "pr close", "pr reopen"}
	issueWrites = []string{"issue comment", "issue edit", "issue close", "issue reopen"}
)

// targetOf is the write's target, from the canonical argv and the repositories the
// classifier found in it.
func targetOf(argv []string, repos []string) writeTarget {
	if len(repos) != 1 || !ValidRepo(repos[0]) {
		return writeTarget{Kind: targetRepo}
	}
	t := writeTarget{Kind: targetRepo, Repo: repos[0]}
	p, err := parseArgv(argv)
	if err != nil || p.cmd == nil {
		return t
	}
	switch path := p.cmd.path; {
	case path == "api":
		return apiTarget(p, t)
	case path == "pr create":
		t.Kind = targetCreatePR
	case path == "issue create":
		t.Kind = targetCreateIssue
	case path == "pr merge":
		t.Kind, t.Number = targetMerge, selectorNumber(p, t.Repo, "pull")
		if t.Number == 0 {
			t.Kind = targetUnresolved
		}
	case slices.Contains(prWrites, path):
		// Closing with --delete-branch also deletes the PR's head branch, a write on a branch
		// the PR's working-set entry does not cover: it asks, as a repository-level write.
		if path == "pr close" && p.has("delete-branch") {
			return t
		}
		t.Kind, t.Number = targetPR, selectorNumber(p, t.Repo, "pull")
		if t.Number == 0 {
			t.Kind = targetUnresolved
		}
	case slices.Contains(issueWrites, path):
		t.Kind, t.Number = targetIssue, selectorNumber(p, t.Repo, "issues")
		if t.Number == 0 {
			t.Kind = targetUnresolved
		}
	}
	return t
}

// selectorNumber is the number a PR or issue command's first positional names: `32`, `#32`,
// or a github.com URL of repo whose path segment is segment (`pull` or `issues`). 0 for
// anything else, a branch name included.
func selectorNumber(p *parsed, repo, segment string) int {
	if len(p.positionals) == 0 {
		return 0
	}
	sel := p.positionals[0]
	if n := issueNumber(strings.TrimPrefix(sel, "#")); n > 0 {
		return n
	}
	m := githubItemURL.FindStringSubmatch(strings.TrimRight(sel, "/"))
	if m == nil || !strings.EqualFold(m[1]+"/"+m[2], repo) || m[3] != segment {
		return 0
	}
	return issueNumber(m[4])
}

// githubItemURL is a PR's or issue's page: https://github.com/OWNER/REPO/pull/N or /issues/N,
// optionally followed by a sub-page (`/files`) or a fragment.
var githubItemURL = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/(pull|issues)/([0-9]+)(?:[/#].*)?$`)

// issueNumber is s as a PR or issue number, 0 when s is not one written plainly: digits only,
// no leading zero, at most nine of them. A spelling GitHub might read as another number never
// names a resource.
func issueNumber(s string) int {
	if s == "" || len(s) > 9 || s[0] == '0' {
		return 0
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
	}
	n, _ := strconv.Atoi(s)
	return n
}

// apiTarget is a `gh api` write's target, by its method and REST path under the repository.
func apiTarget(p *parsed, t writeTarget) writeTarget {
	if len(p.positionals) != 1 {
		return t
	}
	path, why := apiEndpointPath(p.positionals[0])
	if why != "" {
		return t
	}
	m := apiRepoPathRE.FindStringSubmatch(path)
	if m == nil || !strings.EqualFold(m[1]+"/"+m[2], t.Repo) {
		return t
	}
	rest := strings.Split(strings.Trim(m[3], "/"), "/")
	method := "GET"
	if vals := p.values("method"); len(vals) > 0 {
		method = strings.ToUpper(vals[len(vals)-1])
	} else if p.has("field") || p.has("raw-field") || p.has("input") {
		method = "POST"
	}
	switch {
	case len(rest) == 1 && rest[0] == "pulls" && method == "POST":
		t.Kind = targetCreatePR
	case len(rest) == 1 && rest[0] == "issues" && method == "POST":
		t.Kind = targetCreateIssue
	case len(rest) >= 2 && rest[0] == "pulls" && issueNumber(rest[1]) > 0:
		switch {
		case len(rest) == 3 && rest[2] == "merge" && method == "PUT":
			t.Kind, t.Number = targetMerge, issueNumber(rest[1])
		case len(rest) >= 3 && (rest[2] == "merge" || rest[2] == "update-branch"):
			// update-branch writes the PR's head branch; any other method on merge is not
			// one the table names. Both ask, as repository-level writes.
		default:
			t.Kind, t.Number = targetPR, issueNumber(rest[1])
		}
	case len(rest) >= 2 && rest[0] == "issues" && issueNumber(rest[1]) > 0:
		t.Kind, t.Number = targetIssueOrPR, issueNumber(rest[1])
	}
	return t
}

// numbered reports whether the target names a PR or an issue.
func (t writeTarget) numbered() bool {
	switch t.Kind {
	case targetPR, targetIssue, targetIssueOrPR, targetMerge:
		return t.Number > 0
	}
	return false
}

// storeRepo is the target repository as the working set records it: the grant identity
// `github.com/owner/repo`, lower-cased since GitHub names are case-insensitive.
func (t writeTarget) storeRepo() string { return storeRepoOf(t.Repo) }

func storeRepoOf(repo string) string { return SourceHost + "/" + strings.ToLower(repo) }

// resource is the working-set resource the target writes: "#<n>" for a PR or an issue, the
// collection for a creation, and the repository for a repository-level write.
func (t writeTarget) resource() string {
	switch t.Kind {
	case targetCreatePR:
		return brokerstore.ResourcePulls
	case targetCreateIssue:
		return brokerstore.ResourceIssues
	case targetRepo:
		return brokerstore.ResourceRepository
	}
	if t.Number > 0 {
		return "#" + strconv.Itoa(t.Number)
	}
	return ""
}

// coveringKinds are the working-set kinds whose live entry on the target's resource covers it:
// a PR or an issue in the working set covers every non-merge write to that number; a merge, a
// creation and a repository-level write only their own timed grant.
func (t writeTarget) coveringKinds() []string {
	switch t.Kind {
	case targetPR, targetIssue, targetIssueOrPR:
		return []string{brokerstore.WorkingPR, brokerstore.WorkingIssue}
	case targetMerge:
		return []string{brokerstore.WorkingMerge}
	case targetCreatePR:
		return []string{brokerstore.WorkingCreatePR}
	case targetCreateIssue:
		return []string{brokerstore.WorkingCreateIssue}
	case targetRepo:
		return []string{brokerstore.WorkingRepo}
	}
	return nil
}

// describe is the target for a human: "PR o/r#32", "the ability to create PRs in o/r".
func (t writeTarget) describe() string {
	switch t.Kind {
	case targetPR, targetMerge:
		return fmt.Sprintf("PR %s#%d", t.Repo, t.Number)
	case targetIssue:
		return fmt.Sprintf("issue %s#%d", t.Repo, t.Number)
	case targetIssueOrPR:
		return fmt.Sprintf("%s#%d", t.Repo, t.Number)
	case targetCreatePR:
		return "PR creation in " + t.Repo
	case targetCreateIssue:
		return "issue creation in " + t.Repo
	}
	return "repository-level writes to " + t.Repo
}

// createdNumber is the number of the PR or issue a creation made, read from gh's standard
// output, or 0 when it cannot be read with certainty (§7: a creation whose result the broker
// could not read records nothing). `gh pr create` and `gh issue create` print the new item's
// URL as their last line; `gh api` prints the REST object, whose top-level `number` and
// `html_url` must agree. A `gh api` call shaping its output (--jq, --template, --include,
// --paginate, --slurp, --silent) is never read: its output is the jail's own text, and could
// name any number.
func createdNumber(t writeTarget, argv []string, out []byte) int {
	segment := "pull"
	if t.Kind == targetCreateIssue {
		segment = "issues"
	}
	fromURL := func(u string) int {
		m := githubItemURL.FindStringSubmatch(u)
		if m == nil || u != "https://github.com/"+m[1]+"/"+m[2]+"/"+m[3]+"/"+m[4] ||
			!strings.EqualFold(m[1]+"/"+m[2], t.Repo) || m[3] != segment {
			return 0
		}
		return issueNumber(m[4])
	}
	p, err := parseArgv(argv)
	if err != nil || p.cmd == nil {
		return 0
	}
	if p.cmd.path != "api" {
		if p.has("dry-run") {
			return 0
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return fromURL(strings.TrimSpace(lines[len(lines)-1]))
	}
	for _, shaping := range []string{"jq", "template", "include", "paginate", "slurp", "silent"} {
		if p.has(shaping) {
			return 0
		}
	}
	var obj struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(out, &obj) != nil || obj.Number <= 0 || fromURL(obj.HTMLURL) != obj.Number {
		return 0
	}
	return obj.Number
}
