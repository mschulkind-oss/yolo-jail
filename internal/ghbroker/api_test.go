package ghbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `gh api` endpoint rule for percent-encoding (docs/design/boundary-broker.md BB-D58).
// gh sends an endpoint's encoding as the jail spelled it (MEASURED against gh 2.101.0 and a
// local fake API: `repos/o/r/branches/MS%2Fmain` arrives as `/repos/o/r/branches/MS%2Fmain`),
// so the broker decodes the path once, checks the decoded path against the scope, and
// forwards the endpoint unchanged. An encoding is refused only where some server's reading
// of it could name a path outside the repository the broker checked; every such refusal
// names what to write instead.

// nextSteps are the words every refusal here offers its next step with.
var nextSteps = []string{"Spell", "Write", "Encode", "Remove", "Name", "read it with"}

func hasNextStep(reason string) bool {
	for _, s := range nextSteps {
		if strings.Contains(reason, s) {
			return true
		}
	}
	return false
}

func TestAPIEncodedEndpoints(t *testing.T) {
	for _, c := range []struct {
		endpoint string
		run      string // "" when refused
		reason   string // a substring of the refusal
	}{
		// The standard way to name a branch with a slash: decoding leaves repos/o/r alone.
		{endpoint: "repos/o/r/branches/MS%2Fmain", run: "repos/o/r/branches/MS%2Fmain"},
		{endpoint: "repos/o/r/branches/MS%2fmain", run: "repos/o/r/branches/MS%2fmain"},
		{endpoint: "/repos/o/r/branches/MS%2Fmain?per_page=1", run: "/repos/o/r/branches/MS%2Fmain?per_page=1"},
		{endpoint: "repos/o/r/contents/docs%2Fa%20b.md", run: "repos/o/r/contents/docs%2Fa%20b.md"},
		{endpoint: "repos/o/r/contents/a%23b%3F.md", run: "repos/o/r/contents/a%23b%3F.md"},
		{endpoint: "repos/o/r/git/ref/heads%2FMS%2Fmain", run: "repos/o/r/git/ref/heads%2FMS%2Fmain"},
		{endpoint: "repos/o/r/contents/%C3%A9t%C3%A9.md", run: "repos/o/r/contents/%C3%A9t%C3%A9.md"},
		{endpoint: "repos/o/r/pulls/", run: "repos/o/r/pulls/"},

		// Every way to move the call to another repository through an encoding.
		{endpoint: "repos/o%2Fother/r", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o/r%2F..%2F..%2Fother", reason: "dot segment"},
		{endpoint: "repos/o/r%2f..%2f..%2fother", reason: "dot segment"},
		{endpoint: "repos/%6F/r", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "%72epos/other/x", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos%2Fother%2Fx/pulls", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o%23x/r", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o/r%3F/x", reason: "encodes a character in its repos/OWNER/REPO"},
		{endpoint: "repos/o/r/x%2F..%2F..%2F..%2Fother%2Fx", reason: "dot segment"},
		{endpoint: "repos/o/r/%2e%2e/%2e%2e/other/x", reason: "dot segment"},
		{endpoint: "repos/o/r/%2E%2E/%2e%2E/other/x", reason: "dot segment"},
		{endpoint: "repos/o/r/%2e/x", reason: "dot segment"},
		{endpoint: "repos/o/r/../../other/x", reason: "dot segment"},
		{endpoint: "repos/o/r/branches/MS%252Fmain", reason: "still encoded"},
		{endpoint: "repos/o/r/%252e%252e/%252e%252e/other/x", reason: "still encoded"},
		{endpoint: "repos/o/r/a%5C..%5C..%5Cother", reason: "backslash"},
		{endpoint: `repos/o/r/a\..\..\other`, reason: "backslash"},
		{endpoint: "repos/o/r/a%00b", reason: "control character"},
		{endpoint: "repos/o/r/a%0Ab", reason: "control character"},
		{endpoint: "repos/o/r/a%zz", reason: "two-digit hex escape"},
		{endpoint: "repos/o/r/a%2", reason: "two-digit hex escape"},
		{endpoint: "repos/o/r/x%2F%2Fy", reason: "empty segment"},
		{endpoint: "repos/o/r//x", reason: "empty segment"},
		{endpoint: "//repos/o/r", reason: "empty segment"},
		{endpoint: "repos/o/r/%c0%ae%c0%ae/x", reason: "not UTF-8"},
		{endpoint: "repos/o/r#/../../other/x", reason: "%23"},
		{endpoint: "repos/o/r/issues#x", reason: "%23"},
		{endpoint: "", reason: "empty"},
	} {
		t.Run(c.endpoint, func(t *testing.T) {
			d := Classify([]string{"api", c.endpoint}, "", testScope)
			if c.run != "" {
				if d.Outcome != OutcomeStanding || strings.Join(d.Argv, " ") != "api "+c.run {
					t.Fatalf("gh api %q: outcome %q (%s), runs %q; want a standing read of %q",
						c.endpoint, d.Outcome, d.Reason, d.Argv, c.run)
				}
				return
			}
			if d.Outcome != OutcomeRefused || d.Argv != nil {
				t.Fatalf("gh api %q: outcome %q argv %q (%s), want refused", c.endpoint, d.Outcome, d.Argv, d.Reason)
			}
			if !strings.Contains(d.Reason, c.reason) {
				t.Errorf("gh api %q: reason %q, want it to contain %q", c.endpoint, d.Reason, c.reason)
			}
			if !hasNextStep(d.Reason) {
				t.Errorf("gh api %q: the refusal names no next step: %q", c.endpoint, d.Reason)
			}
		})
	}
}

// The production path: a jail's `gh api` with an encoded branch reaches the host gh with
// the encoding it was given, and an encoding that names another repository runs nothing.
func TestServeForwardsAnEncodedEndpointAsGiven(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, out, errOut := f.serve(t, Request{Argv: []string{"api", "repos/{owner}/{repo}/branches/MS%2Fmain"}, Repo: "o/r"})
	if code != 0 || out != "ran api repos/o/r/branches/MS%2Fmain\n" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if got := oneCall(t, f.fakeDir)["argv"]; got != "api\nrepos/o/r/branches/MS%2Fmain\n" {
		t.Fatalf("the host gh received %q, want the endpoint's encoding unchanged", got)
	}
	if ev := lastAudit(t); ev.Outcome != "ran" || ev.Repo != "o/r" {
		t.Fatalf("audit %+v", ev)
	}

	if err := os.RemoveAll(filepath.Join(f.fakeDir, "calls")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = f.serve(t, Request{Argv: []string{"api", "repos/o%2Fother/r"}})
	if code != ExitUsage || !strings.Contains(errOut, "encodes a character in its repos/OWNER/REPO") {
		t.Fatalf("an encoded owner: code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatal("an encoded owner ran gh")
	}
}

// BB-D61: raw GraphQL stays refused, since a query reaches any object the login can read and
// no check of its text holds it to the scope, and the refusal names the gh command that asks
// GitHub's GraphQL the same question for a repository the broker checks.
func TestGraphQLRefusalNamesTheCommandToUseInstead(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"api", "graphql", "-f", `query={repository(owner:"o",name:"r"){pullRequest(number:1){title}}}`}, "gh pr view"},
		{[]string{"api", "graphql", "-f", `query=query{repository(owner:"o",name:"r"){pullRequests(first:5){nodes{title}}}}`}, "gh pr list"},
		{[]string{"api", "graphql", "-F", `query={repository(owner:"o",name:"r"){issue(number:1){title}}}`}, "gh issue view"},
		{[]string{"api", "graphql", "--raw-field", `query={repository(owner:"o",name:"r"){issues(first:5){nodes{title}}}}`}, "gh issue list"},
		{[]string{"api", "graphql", "-f", `query={repository(owner:"o",name:"r"){discussions(first:1){nodes{title}}}}`}, "gh discussion"},
		{[]string{"api", "graphql", "-f", `query={repository(owner:"o",name:"r"){releases(first:1){nodes{name}}}}`}, "gh release"},
		{[]string{"api", "graphql", "-f", `query={repository(owner:"o",name:"r"){description}}`}, "gh repo view"},
		{[]string{"api", "graphql", "-f", "query={viewer{login}}"}, "gh pr view"},
		{[]string{"api", "/graphql", "--input", "-"}, "gh pr view"},
	} {
		d := Classify(c.argv, "o/r", testScope)
		if d.Outcome != OutcomeOutOfScope || !d.AccountWide {
			t.Errorf("gh %q: %q (%s), want account-wide", c.argv, d.Outcome, d.Reason)
			continue
		}
		for _, w := range []string{c.want, "node ID", "repos/OWNER/REPO"} {
			if !strings.Contains(d.Reason, w) {
				t.Errorf("gh %q: reason %q, want it to name %q", c.argv, d.Reason, w)
			}
		}
	}
}

// Every account-wide refusal ends with a next step: what to name instead, or that the host
// user runs it on the host.
func TestEveryAccountWideRefusalNamesANextStep(t *testing.T) {
	for _, c := range []struct {
		argv, field, want string
	}{
		{"api user", "", "repos/OWNER/REPO/"},
		{"api repositories/1/pulls", "", "repos/OWNER/REPO/"},
		{"api search/issues -f q=x", "", "repos/OWNER/REPO/"},
		{"search code foo", "", "--repo OWNER/REPO"},
		{"search issues foo --repo o/r --owner o", "", "--owner"},
		{"pr view 1", "", "-R OWNER/REPO"},
		{"repo view", "", "OWNER/REPO"},
		{"status", "", "on the host"},
		{"gist view abc", "", "on the host"},
		{"repo create x --private", "", "on the host"},
		{"cs ls", "", "on the host"},
		{"secret list --org acme", "", "--org"},
	} {
		d := Classify(splitArgv(c.argv), c.field, testScope)
		if d.Outcome != OutcomeOutOfScope || !d.AccountWide {
			t.Errorf("gh %s: %q (%s), want account-wide", c.argv, d.Outcome, d.Reason)
			continue
		}
		// It says the workspace entry cannot add it, naming the key, never an undefined term.
		if !strings.Contains(d.Reason, c.want) || !strings.Contains(d.Reason, "`brokered.github.repos` entry can add") ||
			strings.Contains(d.Reason, "widening") {
			t.Errorf("gh %s: reason %q, want it to say no `brokered.github.repos` entry can add it and name %q",
				c.argv, d.Reason, c.want)
		}
	}
}

// The production path: the jail is told which command to use instead.
func TestServeRefusesGraphQLNamingTheCommandToUse(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, _, errOut := f.serve(t, Request{Argv: []string{"api", "graphql", "-f",
		`query={repository(owner:"o",name:"r"){pullRequest(number:1){title}}}`}, Repo: "o/r"})
	if code != ExitUsage || !strings.Contains(errOut, "out of scope") || !strings.Contains(errOut, "gh pr view") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatal("a GraphQL call ran gh")
	}
}

// gh fills six placeholders from the git checkout it finds from its cwd, running `git remote
// -v` on the host to do it (MEASURED against gh 2.101.0): {owner}, {repo} and {branch}, and
// the older :owner, :repo and :branch ending at a word boundary, anywhere in the endpoint, its
// query included, and in a --field value, never in a --raw-field value or a header. The
// broker's cwd is empty, but git looks upward from it, so a checkout above it (a home
// directory kept under git) answers (MEASURED: `repos/o/r/:owner/:repo` reached
// /repos/o/r/<that checkout's owner>/<its name>). So the broker fills both spellings from the
// forwarder's repository, refuses the branch, and refuses a value its filling leaves holding a
// placeholder for gh: `{{owner}wner}` reads `{owner}` once the inner one is filled.
func TestAPIPlaceholdersLeaveTheHostGHNothingToFill(t *testing.T) {
	for _, c := range []struct {
		argv, field string
		run         string // the canonical argv, "" when refused
		reason      string
	}{
		{argv: "api repos/:owner/:repo/pulls", field: "o/r", run: "api repos/o/r/pulls"},
		{argv: "api repos/{owner}/:repo/pulls", field: "o/r", run: "api repos/o/r/pulls"},
		{argv: "api repos/o/r/contents/x?ref=:owner-main", field: "o/r", run: "api repos/o/r/contents/x?ref=o-main"},
		{argv: "api -X GET repos/o/r/issues -F creator=:owner", field: "o/r",
			run: "api --method=GET --field=creator=o repos/o/r/issues"},
		// What gh leaves as typed stays as typed: no word boundary, or a --raw-field value.
		{argv: "api repos/o/r/contents/:ownerx", field: "o/r", run: "api repos/o/r/contents/:ownerx"},
		{argv: "api -X GET repos/o/r/issues -f q=a:repo", field: "o/r",
			run: "api --method=GET --raw-field=q=a:repo repos/o/r/issues"},

		{argv: "api repos/:owner/:repo/pulls", reason: "found none"},
		{argv: "api repos/o/r/branches/:branch", field: "o/r", reason: ":branch"},
		{argv: "api repos/o/r/commits?sha=:branch", field: "o/r", reason: ":branch"},
		{argv: "api -X GET repos/o/r/commits -F sha=:branch", field: "o/r", reason: ":branch"},
		{argv: "api repos/o/r/contents/{{owner}wner}", field: "o/r", reason: "still spells"},
		{argv: "api repos/o/r/contents/:{repo}epo", field: "o/r", reason: "still spells"},
		{argv: "api -X GET repos/o/r/issues -F x={{repo}epo}", field: "o/r", reason: "still spells"},
	} {
		d := Classify(splitArgv(c.argv), c.field, testScope)
		if c.run != "" {
			if d.Outcome != OutcomeStanding || strings.Join(d.Argv, " ") != c.run {
				t.Errorf("gh %s: %q runs %q (%s), want a standing read of %q", c.argv, d.Outcome, d.Argv, d.Reason, c.run)
			}
			continue
		}
		if d.Outcome != OutcomeRefused || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("gh %s: %q (%s), want refused with %q", c.argv, d.Outcome, d.Reason, c.reason)
		}
		if !hasNextStep(d.Reason) {
			t.Errorf("gh %s: the refusal names no next step: %q", c.argv, d.Reason)
		}
	}
}

// The production path: the host gh receives the endpoint with every placeholder filled, and a
// value that would still hold one after the broker's filling runs nothing.
func TestServeLeavesTheHostGHNoPlaceholderToFill(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, out, errOut := f.serve(t, Request{Argv: []string{"api", "repos/:owner/:repo/pulls"}, Repo: "o/r"})
	if code != 0 || out != "ran api repos/o/r/pulls\n" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if err := os.RemoveAll(filepath.Join(f.fakeDir, "calls")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = f.serve(t, Request{Argv: []string{"api", "repos/o/r/contents/{{owner}wner}"}, Repo: "o/r"})
	if code != ExitUsage || !strings.Contains(errOut, "still spells") {
		t.Fatalf("a composed placeholder: code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatal("a composed placeholder ran gh")
	}
}
