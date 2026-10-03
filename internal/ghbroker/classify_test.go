package ghbroker

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The classifier is tested on argv alone, against the table measured from gh 2.101.0
// (docs/design/boundary-broker.md §11, testing constraints). No case here runs gh.

var testScope = NewScope([]string{"o/r", "me/r-fork"})

type classifyCase struct {
	name    string
	argv    string // space-separated; `'…'` keeps one argument with spaces
	field   string
	outcome Outcome
	set     string
	run     string // the canonical argv, space-joined, when the command may run
	reason  string // a substring of the reason, when it may not
}

func splitArgv(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, have := false, false
	for _, r := range s {
		switch {
		case r == '\'':
			quoted, have = !quoted, true
		case r == ' ' && !quoted:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

func TestClassify(t *testing.T) {
	cases := []classifyCase{
		// §12 done criterion 1: a read of the workspace's own repository runs.
		{name: "read with -R", argv: "pr view 32 -R o/r", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view --repo=o/r 32"},
		{name: "read takes the forwarder's repository", argv: "pr view 32", field: "o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "pr view 32 --repo=o/r"},
		{name: "flags before the subcommand parse (H6)", argv: "-R o/r pr view 1",
			outcome: OutcomeStanding, set: SetReadOnly, run: "pr view --repo=o/r 1"},
		{name: "glued short value", argv: "pr view -Ro/r 1", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view --repo=o/r 1"},
		{name: "short value with =", argv: "pr view -R=o/r 1", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view --repo=o/r 1"},
		{name: "long value with =", argv: "pr view --repo=o/r 1", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view --repo=o/r 1"},
		{name: "scope is case-insensitive", argv: "pr view 1 -R O/R", outcome: OutcomeStanding,
			set: SetReadOnly, run: "pr view --repo=O/R 1"},
		{name: "bundled short booleans", argv: "pr view -c 1 -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "pr view --comments --repo=o/r 1"},
		{name: "json fields allowed", argv: "pr list --json number,title -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "pr list --json=number,title --repo=o/r"},
		{name: "a built-in alias resolves", argv: "pr ls -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "pr list --repo=o/r"},
		{name: "a group alias resolves", argv: "rs ls -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "ruleset list --repo=o/r"},
		{name: "pr checks --watch is a read", argv: "pr checks 1 --watch -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "pr checks --watch --repo=o/r 1"},
		{name: "run view --log", argv: "run view 7 --log -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "run view --log --repo=o/r 7"},
		{name: "issue develop --list is a read", argv: "issue develop 5 --list -R o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "issue develop --list --repo=o/r 5"},
		{name: "repo view names the forwarder's repository", argv: "repo view", field: "o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "repo view o/r"},
		{name: "repo view of an in-scope repository", argv: "repo view me/r-fork", field: "o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "repo view me/r-fork"},
		{name: "repo read-file is a read", argv: "repo read-file README.md", field: "o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "repo read-file README.md --repo=o/r"},
		{name: "a github.com URL names the repository", argv: "pr view https://github.com/o/r/pull/1",
			field: "me/r-fork", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view https://github.com/o/r/pull/1 --repo=o/r"},
		{name: "secret list is a read", argv: "secret list -R o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "secret list --repo=o/r"},
		{name: "auth status names no repository", argv: "auth status", outcome: OutcomeStanding,
			set: SetReadOnly, run: "auth status"},
		{name: "gitignore templates are public", argv: "repo gitignore list", outcome: OutcomeStanding,
			set: SetReadOnly, run: "repo gitignore list"},
		{name: "--help runs alone", argv: "pr view --help", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view --help"},
		{name: "gh --version", argv: "--version", outcome: OutcomeStanding, set: SetReadOnly, run: "--version"},
		{name: "--version with anything else is not the version read", argv: "--version pr view 1 -R o/r",
			outcome: OutcomeRefused, reason: "flag --version is not one"},
		{name: "api GET under the repository", argv: "api repos/o/r/pulls", outcome: OutcomeStanding,
			set: SetReadOnly, run: "api repos/o/r/pulls"},
		{name: "api placeholders take the forwarder's repository", argv: "api repos/{owner}/{repo}/pulls",
			field: "o/r", outcome: OutcomeStanding, set: SetReadOnly, run: "api repos/o/r/pulls"},
		{name: "api Accept header", argv: "api -H 'Accept: application/vnd.github.raw+json' repos/o/r/readme",
			outcome: OutcomeStanding, set: SetReadOnly,
			run: "api --header=Accept: application/vnd.github.raw+json repos/o/r/readme"},
		{name: "api -X GET with fields is a read", argv: "api -X GET repos/o/r/issues -f state=open",
			outcome: OutcomeStanding, set: SetReadOnly, run: "api --method=GET --raw-field=state=open repos/o/r/issues"},
		{name: "search with in-scope --repo", argv: "search issues bug --repo o/r", outcome: OutcomeStanding,
			set: SetReadOnly, run: "search issues --repo=o/r bug"},

		// Writes: in scope, parsed, not refused — read-write alone (§5.2's last paragraph).
		{name: "pr comment is a write", argv: "pr comment 32 -R o/r --body-file -", outcome: OutcomeWindowed,
			set: SetReadWrite, run: "pr comment --repo=o/r --body-file=- 32"},
		{name: "issue edit is a write", argv: "issue edit 5 -R o/r --add-label bug", outcome: OutcomeWindowed,
			set: SetReadWrite, run: "issue edit --repo=o/r --add-label=bug 5"},
		{name: "api POST is a write", argv: "api -X POST repos/o/r/issues/5/comments -f body=x",
			outcome: OutcomeWindowed, set: SetReadWrite,
			run: "api --method=POST --raw-field=body=x repos/o/r/issues/5/comments"},
		{name: "api with fields defaults to POST", argv: "api repos/o/r/issues -f title=x",
			outcome: OutcomeWindowed, set: SetReadWrite, run: "api --raw-field=title=x repos/o/r/issues"},
		{name: "issue develop without --list creates a branch", argv: "issue develop 5 -R o/r",
			outcome: OutcomeWindowed, set: SetReadWrite, run: "issue develop --repo=o/r 5"},
		{name: "issue create's -T names a GitHub template", argv: "issue create -T bug -R o/r",
			outcome: OutcomeWindowed, set: SetReadWrite, run: "issue create --template=bug --repo=o/r"},
		{name: "pr merge is a write", argv: "pr merge 3 -R o/r --squash", outcome: OutcomeWindowed,
			set: SetReadWrite, run: "pr merge --repo=o/r --squash 3"},

		// BB-D64: --jq and the formatting --template run where gh runs, on the host, whose
		// environment the broker builds from nothing, so `env` there holds no token (H2, now
		// closed by §4.1 rather than by a refusal). A filter is never query text: gh applies it
		// to its own output and sends it to no one, so a parenthesis in one does not widen a
		// search (queryWidens).
		{name: "--jq runs: the host env it can read holds no token", argv: "pr view 32 --jq env.GH_TOKEN",
			field: "o/r", outcome: OutcomeStanding, set: SetReadOnly, run: "pr view --jq=env.GH_TOKEN 32 --repo=o/r"},
		{name: "-q is --jq", argv: "pr view 32 -q .title -R o/r", outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr view --jq=.title --repo=o/r 32"},
		{name: "the formatting --template runs", argv: "pr view 1 -R o/r --template '{{.title}}'",
			outcome: OutcomeStanding, set: SetReadOnly, run: "pr view --repo=o/r --template={{.title}} 1"},
		{name: "a jq filter on a search-backed list is not query text",
			argv:    "pr list --json number,title --jq 'map(select(.title | test(\"x\"))) | length' -R o/r",
			outcome: OutcomeStanding, set: SetReadOnly,
			run: "pr list --json=number,title --jq=map(select(.title | test(\"x\"))) | length --repo=o/r"},
		{name: "a template on a search-backed list is not query text",
			argv:    "issue list -S bug --json title --template '{{range .}}{{printf \"%s\" (.title)}} OR {{end}}' -R o/r",
			outcome: OutcomeStanding, set: SetReadOnly,
			run: "issue list --search=bug --json=title --template={{range .}}{{printf \"%s\" (.title)}} OR {{end}} --repo=o/r"},
		{name: "api --jq", argv: "api repos/o/r/pulls --jq '.[].number'", outcome: OutcomeStanding,
			set: SetReadOnly, run: "api --jq=.[].number repos/o/r/pulls"},
		{name: "api -t is the formatting --template", argv: "api repos/o/r -t '{{.full_name}}'",
			outcome: OutcomeStanding, set: SetReadOnly, run: "api --template={{.full_name}} repos/o/r"},
		{name: "a filter does not make an api write a read", argv: "api -X POST repos/o/r/issues -f title=x --jq .number",
			outcome: OutcomeWindowed, set: SetReadWrite},
		{name: "the query text is still checked beside a filter", argv: "pr list -S 'x) OR (is:pr' --json number --jq . -R o/r",
			outcome: OutcomeOutOfScope, reason: "a parenthesis"},

		// §12 done criterion 2 and §5.4: refused, whatever set is held.
		{name: "auth token", argv: "auth token", outcome: OutcomeRefused, reason: "host's GitHub login"},
		{name: "auth status -t", argv: "auth status -t", outcome: OutcomeRefused, reason: "prints the host's token"},
		{name: "auth status --hostname", argv: "auth status --hostname x.example", outcome: OutcomeRefused,
			reason: "--hostname"},
		{name: "api to a URL (H1)", argv: "api http://example.com/x", outcome: OutcomeRefused, reason: "URL"},
		{name: "api https URL", argv: "api https://api.github.com/user", outcome: OutcomeRefused, reason: "URL"},
		{name: "co is pr checkout", argv: "-R x co 1", outcome: OutcomeRefused, reason: "pr checkout"},
		{name: "api -F @file (H9)", argv: "api -F q=@/etc/passwd user", outcome: OutcomeRefused,
			reason: "beginning with @"},
		{name: "api -F @- is refused too", argv: "api -F q=@- repos/o/r/issues", outcome: OutcomeRefused,
			reason: "beginning with @"},
		{name: "workflow run -F @file", argv: "workflow run ci.yml -F x=@secret -R o/r", outcome: OutcomeRefused,
			reason: "beginning with @"},
		{name: "--web runs the browser", argv: "pr view --web 1 -R o/r", outcome: OutcomeRefused, reason: "--web"},
		{name: "-w in a bundle", argv: "pr view -cw 1 -R o/r", outcome: OutcomeRefused, reason: "--web"},
		{name: "--editor", argv: "issue create -e -R o/r", outcome: OutcomeRefused, reason: "--editor"},
		{name: "three-segment -R (H1)", argv: "pr view 1 -R github.com/o/r", outcome: OutcomeRefused,
			reason: "two segments"},
		{name: "unknown flag", argv: "pr view --frobnicate 1 -R o/r", outcome: OutcomeRefused,
			reason: "flag --frobnicate is not one"},
		{name: "unknown command word (an extension or alias)", argv: "frobnicate x", outcome: OutcomeRefused,
			reason: "not a gh command"},
		{name: "a group is not a command", argv: "pr -R o/r", outcome: OutcomeRefused, reason: "group of commands"},
		{name: "release upload of a host file (BB-D28)", argv: "release upload v1 /etc/passwd -R o/r",
			outcome: OutcomeRefused, reason: "uploads host files"},
		{name: "deploy-key add of a host file", argv: "repo deploy-key add /home/u/.ssh/id_ed25519.pub -R o/r",
			outcome: OutcomeRefused, reason: "host file"},
		{name: "release create with assets", argv: "release create v1 dist/app.tgz -R o/r",
			outcome: OutcomeRefused, reason: "host files to upload"},
		{name: "--body-file of a host file", argv: "pr comment 1 --body-file /etc/passwd -R o/r",
			outcome: OutcomeRefused, reason: "pipe the jail's file"},
		{name: "--attach reads a host file", argv: "issue comment 1 --attach x.png -R o/r",
			outcome: OutcomeRefused, reason: "host path"},
		{name: "pr create --template reads a host file", argv: "pr create --template t.md -R o/r",
			outcome: OutcomeRefused, reason: "host path"},
		{name: "repo read-file -o writes a host file", argv: "repo read-file README.md -o /tmp/x -R o/r",
			outcome: OutcomeRefused, reason: "host path"},
		{name: "api header other than Accept", argv: "api -H 'X-HTTP-Method-Override: DELETE' repos/o/r/issues/1",
			outcome: OutcomeRefused, reason: "only an Accept"},
		{name: "api --hostname", argv: "api --hostname evil.example repos/o/r", outcome: OutcomeRefused,
			reason: "--hostname"},
		{name: "api --verbose prints headers", argv: "api --verbose repos/o/r", outcome: OutcomeRefused,
			reason: "--verbose"},
		{name: "api dot segment", argv: "api repos/o/r/../../user", outcome: OutcomeRefused, reason: "dot segment"},
		{name: "api encoded dot segments", argv: "api repos/o/r%2F..%2F..", outcome: OutcomeRefused, reason: "dot segment"},
		{name: "api encoded owner", argv: "api repos/o%2Fx/r", outcome: OutcomeRefused, reason: "encodes a character"},
		{name: "api encoded branch", argv: "api repos/o/r/branches/MS%2Fmain", outcome: OutcomeStanding,
			set: SetReadOnly, run: "api repos/o/r/branches/MS%2Fmain"},
		{name: "api {branch}", argv: "api repos/o/r/branches/{branch}", outcome: OutcomeRefused, reason: "{branch}"},
		{name: "a positional after -- beginning with -", argv: "pr view -R o/r -- -1", outcome: OutcomeRefused,
			reason: "begins with '-'"},
		{name: "a URL off github.com", argv: "pr view https://evil.example/o/r/pull/1 -R o/r",
			outcome: OutcomeRefused, reason: "URL off https://github.com/"},
		{name: "browse", argv: "browse -R o/r", outcome: OutcomeRefused, reason: "web browser"},
		{name: "extension install", argv: "extension install o/gh-x", outcome: OutcomeRefused, reason: "extension"},
		{name: "codespace ssh", argv: "codespace ssh", outcome: OutcomeRefused, reason: "codespace"},
		{name: "run download", argv: "run download 1 -R o/r", outcome: OutcomeRefused, reason: "writes files"},
		{name: "skill install", argv: "skill install o/r", outcome: OutcomeRefused, reason: "skills"},
		{name: "a flag before the subcommand swallowing it", argv: "--json pr view 1", outcome: OutcomeRefused},

		// §12 done criterion 8 and §5.6: out of scope, with or without a grant.
		{name: "another private repository", argv: "pr view 1 -R other/private", outcome: OutcomeOutOfScope,
			reason: "other/private is outside"},
		{name: "a URL naming another repository", argv: "pr view https://github.com/other/private/pull/1",
			field: "o/r", outcome: OutcomeOutOfScope, reason: "other/private"},
		{name: "unqualified search", argv: "search code foo", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "search --owner", argv: "search code foo --repo o/r --owner o", outcome: OutcomeOutOfScope,
			reason: "across the account"},
		{name: "a repo: qualifier in the query", argv: "search issues --repo o/r 'repo:x/y bug'",
			outcome: OutcomeOutOfScope, reason: "repo:, org:, user: or owner: qualifier"},

		// BB-D46: a list command that runs a search carries the jail's text beside the
		// repository qualifier gh adds, where GitHub ORs a second repo: and where OR, NOT or
		// a parenthesis can lift a term out of the scoped part (MEASURED queries in the
		// comment on searchBacked).
		{name: "pr list -S with a repo: qualifier", argv: "pr list -S 'repo:victim/secret' -R o/r",
			outcome: OutcomeOutOfScope, reason: "--search \"repo:victim/secret\" carries a repo:"},
		{name: "issue list --search with a repo: qualifier",
			argv: "issue list --search 'repo:victim/secret is:open' -R o/r", outcome: OutcomeOutOfScope,
			reason: "qualifier"},
		{name: "discussion list -S with a repo: qualifier", argv: "discussion list -S repo:victim/secret",
			field: "o/r", outcome: OutcomeOutOfScope, reason: "qualifier"},
		{name: "pr list --search= with an org: qualifier", argv: "pr list --search=org:victim -R o/r",
			outcome: OutcomeOutOfScope, reason: "qualifier"},
		{name: "a negated repo: qualifier", argv: "issue list -S -repo:o/r -R o/r",
			outcome: OutcomeOutOfScope, reason: "qualifier"},
		{name: "pr list -S closing gh's parenthesis", argv: "pr list -S 'repo:x) OR (is:pr' -R o/r",
			outcome: OutcomeOutOfScope, reason: "qualifier"},
		{name: "a parenthesis alone", argv: "pr list -S 'x) OR (is:pr' -R o/r",
			outcome: OutcomeOutOfScope, reason: "a parenthesis"},
		{name: "search issues closing gh's parenthesis", argv: "search issues 'x) OR (is:issue' --repo o/r",
			outcome: OutcomeOutOfScope, reason: "a parenthesis"},
		{name: "OR in an unwrapped search", argv: "search code foo OR bar --repo o/r",
			outcome: OutcomeOutOfScope, reason: "an OR or a NOT"},
		{name: "a trailing NOT negates gh's repo:", argv: "search code foo NOT --repo o/r",
			outcome: OutcomeOutOfScope, reason: "an OR or a NOT"},
		{name: "a lower-case or", argv: "discussion list -S 'foo or bar' -R o/r",
			outcome: OutcomeOutOfScope, reason: "an OR or a NOT"},
		{name: "a quote in a filter value", argv: "pr list --author 'x\" repo:v/s \"' -R o/r",
			outcome: OutcomeOutOfScope, reason: "--author"},
		{name: "OR in a label filter", argv: "discussion list --label 'x\" OR \"y' -R o/r",
			outcome: OutcomeOutOfScope, reason: "--label"},
		{name: "a plain search on pr list runs", argv: "pr list -S 'is:open label:bug' -R o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "pr list --search=is:open label:bug --repo=o/r"},
		{name: "a plain filter runs", argv: "issue list --author monalisa --label bug -R o/r",
			outcome: OutcomeStanding, set: SetReadOnly,
			run: "issue list --author=monalisa --label=bug --repo=o/r"},
		{name: "a word containing or is not the operator", argv: "search issues 'error in origin' --repo o/r",
			outcome: OutcomeStanding, set: SetReadOnly, run: "search issues --repo=o/r error in origin"},
		{name: "a second --repo out of scope", argv: "search issues bug --repo o/r,x/y", outcome: OutcomeOutOfScope,
			reason: "x/y is outside"},
		{name: "GraphQL", argv: "api graphql -f query={viewer{login}}", outcome: OutcomeOutOfScope,
			reason: "across the account"},
		{name: "api path with no repository", argv: "api user", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "status", argv: "status", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "gist view", argv: "gist view abc", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "codespace list", argv: "cs ls", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "secret list --org", argv: "secret list --org acme", outcome: OutcomeOutOfScope,
			reason: "across the account"},
		{name: "repo create", argv: "repo create x --private", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "no repository at all", argv: "pr view 1", outcome: OutcomeOutOfScope, reason: "across the account"},
		{name: "a write out of scope", argv: "pr comment 1 -R x/y --body hi", outcome: OutcomeOutOfScope,
			reason: "x/y is outside"},
		{name: "label clone from out of scope", argv: "label clone x/y -R o/r", outcome: OutcomeOutOfScope,
			reason: "x/y is outside"},
		{name: "repo view of another repository", argv: "repo view other/x", outcome: OutcomeOutOfScope,
			reason: "other/x is outside"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Classify(splitArgv(c.argv), c.field, testScope)
			if d.Outcome != c.outcome {
				t.Fatalf("gh %s: outcome %q (%s), want %q", c.argv, d.Outcome, d.Reason, c.outcome)
			}
			if d.Set != c.set {
				t.Errorf("gh %s: set %q, want %q", c.argv, d.Set, c.set)
			}
			if c.run != "" && strings.Join(d.Argv, " ") != c.run {
				t.Errorf("gh %s: runs %q, want %q", c.argv, strings.Join(d.Argv, " "), c.run)
			}
			if c.reason != "" && !strings.Contains(d.Reason, c.reason) {
				t.Errorf("gh %s: reason %q, want it to contain %q", c.argv, d.Reason, c.reason)
			}
			if d.Outcome == OutcomeRefused || d.Outcome == OutcomeOutOfScope {
				if d.Argv != nil {
					t.Errorf("gh %s: a command that does not run carried an argv %q", c.argv, d.Argv)
				}
			}
		})
	}
}

func TestClassifyStdin(t *testing.T) {
	d := Classify(splitArgv("pr comment 1 -R o/r --body-file -"), "", testScope)
	if !d.ReadsStdin {
		t.Fatalf("--body-file - must read the forwarder's stdin: %+v", d)
	}
	d = Classify(splitArgv("pr view 1 -R o/r"), "", testScope)
	if d.ReadsStdin {
		t.Fatalf("a read with no `-` must not read stdin: %+v", d)
	}
}

// The field is never an authority: a forwarder that sends a repository outside the scope
// gets the command refused as out of scope, not run against it.
func TestClassifyFieldIsCheckedAgainstScope(t *testing.T) {
	d := Classify([]string{"pr", "view", "1"}, "other/private", testScope)
	if d.Outcome != OutcomeOutOfScope {
		t.Fatalf("outcome %q, want out-of-scope", d.Outcome)
	}
	d = Classify([]string{"pr", "view", "1"}, "not a repo", testScope)
	if d.Outcome != OutcomeRefused {
		t.Fatalf("a malformed field: outcome %q, want refused", d.Outcome)
	}
}

func TestClassifyEmptyScopeRunsOnlyScopeFreeReads(t *testing.T) {
	empty := NewScope(nil)
	if d := Classify([]string{"repo", "gitignore", "list"}, "", empty); d.Outcome != OutcomeStanding {
		t.Fatalf("a scope-free read in an empty scope: %q", d.Outcome)
	}
	d := Classify([]string{"pr", "view", "1"}, "o/r", empty)
	if d.Outcome != OutcomeOutOfScope || !strings.Contains(d.Reason, "no GitHub remote") {
		t.Fatalf("a read in an empty scope: %+v", d)
	}
}

func TestNewScopeDropsMalformedAndDuplicates(t *testing.T) {
	s := NewScope([]string{"o/r", "O/R", "x", "a/b/c", "b/a"})
	if want := []string{"b/a", "o/r"}; !reflect.DeepEqual(s.Repos(), want) {
		t.Fatalf("scope %v, want %v", s.Repos(), want)
	}
}

// BB-D59: a write that acts on an organization's or a user's secret or variable, rather than
// the repository's, reaches past the repository scope however -R reads. Found by the fuzz
// oracle's seed corpus (FuzzClassify) and a sweep of the grammar's flags.
func TestClassifyAnAccountFlagOnAWriteIsAccountWide(t *testing.T) {
	for _, c := range []struct {
		argv    string
		outcome Outcome
		reason  string
	}{
		{"secret set X --org other --body y -R o/r", OutcomeOutOfScope, "--org"},
		{"secret set X --user --body y -R o/r", OutcomeOutOfScope, "--user"},
		{"secret set X --repos o/r --body y -R o/r", OutcomeOutOfScope, "--repos"},
		{"secret set X --visibility all --body y -R o/r", OutcomeOutOfScope, "--visibility"},
		{"secret set X --no-repos-selected --body y -R o/r", OutcomeOutOfScope, "--no-repos-selected"},
		{"secret delete X --org other -R o/r", OutcomeOutOfScope, "--org"},
		{"secret delete X --user -R o/r", OutcomeOutOfScope, "--user"},
		{"variable set X --org other --body y -R o/r", OutcomeOutOfScope, "--org"},
		{"variable set X --repos o/r --body y -R o/r", OutcomeOutOfScope, "--repos"},
		{"variable delete X --org other -R o/r", OutcomeOutOfScope, "--org"},
		{"secret list --org other -R o/r", OutcomeOutOfScope, "--org"},
		// The repository's own secrets and variables stay in scope.
		{"secret set X --body y -R o/r", OutcomeWindowed, ""},
		{"secret set X --env prod --body y -R o/r", OutcomeWindowed, ""},
		{"variable delete X -R o/r", OutcomeWindowed, ""},
		// issue develop --branch-repo names the repository the branch is made in.
		{"issue develop 1 --branch-repo other/x -R o/r", OutcomeOutOfScope, "other/x is outside"},
		{"issue develop 1 --list --branch-repo other/x -R o/r", OutcomeOutOfScope, "other/x is outside"},
		{"issue develop 1 --branch-repo https://github.com/other/x -R o/r", OutcomeOutOfScope, "other/x is outside"},
		{"issue develop 1 --branch-repo x -R o/r", OutcomeRefused, "--branch-repo"},
		{"issue develop 1 --branch-repo me/r-fork -R o/r", OutcomeWindowed, ""},
	} {
		d := Classify(splitArgv(c.argv), "", testScope)
		if d.Outcome != c.outcome || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("gh %s: %q (%s), want %q with %q", c.argv, d.Outcome, d.Reason, c.outcome, c.reason)
		}
		if d.Outcome == OutcomeOutOfScope && d.AccountWide && !strings.Contains(d.Reason, "on the host") {
			t.Errorf("gh %s: the refusal names no next step: %s", c.argv, d.Reason)
		}
	}
}

// Every flag that could point a command past its repository, on a command the broker may
// run, is reviewed: it makes the command account-wide, names a repository the scope checks,
// or is a filter inside the repository. A regenerated grammar that adds one fails here until
// it is placed.
func TestEveryAccountShapedFlagIsReviewed(t *testing.T) {
	shaped := regexp.MustCompile(`^(org|user|owner|enterprise|repos|.*-owner|.*-repo|no-repos-selected|visibility)$`)
	filters := map[string]string{
		"run list --user":             "filters runs by who triggered them",
		"repo edit --visibility":      "the repository's own visibility",
		"search commits --visibility": "a search filter, under the search rule",
		"search issues --visibility":  "a search filter, under the search rule",
		"search prs --visibility":     "a search filter, under the search rule",
	}
	for path, c := range grammar {
		if isGroup(path) || refusedFor(&parsed{cmd: c}) != "" || isAccountWide(path) || path == "api" {
			continue
		}
		for _, f := range c.flags {
			if !shaped.MatchString(f.long) {
				continue
			}
			key := path + " --" + f.long
			_, filter := filters[key]
			if filter || accountFlagOf(path, f.long) || repoValuedFlag(path, f.long) ||
				(strings.HasPrefix(path, "search ") && f.long == "owner") {
				continue
			}
			t.Errorf("%s (%s) is neither an account flag, a repository flag nor a reviewed filter", key, f.desc)
		}
	}
}

// BB-D60: gh path-escapes a word it puts into a REST path (MEASURED: `run view
// ../../../other/x -R o/r` sends GET /repos/o/r/actions/runs/..%2F..%2F..%2Fother%2Fx), so
// a server that decodes the %2F and resolves dot segments would read another repository's
// path. A word with a . or .. segment is refused on every command but `gh api`, which has its
// own rule, and the searches, whose words are query text.
func TestClassifyAWordThatClimbsOutOfTheRepositoryPath(t *testing.T) {
	for _, c := range []struct {
		argv    string
		outcome Outcome
		reason  string
	}{
		{"run view ../../../other/x -R o/r", OutcomeRefused, "dot segment"},
		{"run watch 5/../../../../other/x -R o/r", OutcomeRefused, "dot segment"},
		{"run view --job ../../../other/x -R o/r", OutcomeRefused, "--job"},
		{"repo read-file ../../../other/x/contents/secret -R o/r", OutcomeRefused, "dot segment"},
		{"repo read-file ./README.md -R o/r", OutcomeRefused, "dot segment"},
		{"repo gitignore view ../../repos/other/x", OutcomeRefused, "dot segment"},
		{"repo license view ..%2Frepos%2Fother%2Fx", OutcomeRefused, "dot segment"},
		{"repo license view %252e%252e%252Frepos", OutcomeRefused, "dot segment"},
		{"release view ../../../other/x -R o/r", OutcomeRefused, "dot segment"},
		{"secret list --env ../../../other/x -R o/r", OutcomeRefused, "--env"},
		{"variable get X --env 'a\\..\\..\\x' -R o/r", OutcomeRefused, "--env"},
		{"label edit .. --color fff -R o/r", OutcomeRefused, "dot segment"},
		{"pr view https://github.com/o/r/../../other/x/pull/1", OutcomeRefused, "dot segment"},
		// Words without a dot segment, and text that is never a path, run as before.
		{"run view 5 -R o/r", OutcomeStanding, ""},
		{"release view v1.2.3 -R o/r", OutcomeStanding, ""},
		{"repo read-file docs/a..b.md -R o/r", OutcomeStanding, ""},
		{"repo read-file docs/... -R o/r", OutcomeStanding, ""},
		{"pr comment 1 --body './build.sh fails' -R o/r", OutcomeWindowed, ""},
		{"issue create --title ../x --body ./y -R o/r", OutcomeWindowed, ""},
		{"search code ./config --repo o/r", OutcomeStanding, ""},
		{"pr list -S ./x -R o/r", OutcomeStanding, ""},
	} {
		d := Classify(splitArgv(c.argv), "", testScope)
		if d.Outcome != c.outcome || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("gh %s: %q (%s), want %q with %q", c.argv, d.Outcome, d.Reason, c.outcome, c.reason)
		}
		if d.Outcome == OutcomeRefused && !strings.Contains(d.Reason, "without . or .. segments") {
			t.Errorf("gh %s: the refusal names no next step: %s", c.argv, d.Reason)
		}
	}
}

// BB-D60 through the production path: a word that climbs out of the repository's REST path
// answers 64 and never reaches the host gh.
func TestServeRefusesAWordThatClimbsOutOfTheRepositoryPath(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, _, errOut := f.serve(t, Request{Argv: []string{"repo", "read-file", "../../../other/x/contents/secret", "-R", "o/r"}})
	if code != ExitUsage || !strings.Contains(errOut, "dot segment") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatal("a climbing word ran gh")
	}
	if code, out, errOut := f.serve(t, Request{Argv: []string{"repo", "read-file", "docs/README.md", "-R", "o/r"}}); code != 0 ||
		out != "ran repo read-file --repo=o/r docs/README.md\n" {
		t.Fatalf("a plain path: code %d out %q err %q", code, out, errOut)
	}
}

// Found by FuzzClassify (testdata/fuzz/FuzzClassify/c7921640bbbaae74): one argv word holding
// a space resolved as a two-word command, which gh itself refuses as an unknown command, and
// an empty second repository skipped the scope check that every other value gets. Then
// (b165e469e64ef9d2) a github.com URL argument was read decoded, where gh's own reading of an
// encoded one is not that (MEASURED: gh refuses `repo view` of one as an invalid path), and
// (b7c51892e55c1251) a search's --repo was checked with its spaces trimmed.
func TestClassifyWhatTheFuzzerFound(t *testing.T) {
	for _, c := range []struct {
		argv   []string
		reason string
	}{
		{[]string{"label clone", "x/y"}, "not a gh command"},
		{[]string{"pr view", "1", "-R", "o/r"}, "not a gh command"},
		{[]string{"pr", "view\t", "1", "-R", "o/r"}, "group of commands"},
		{[]string{"label", "clone", "", "-R", "o/r"}, "not OWNER/REPO"},
		{[]string{"issue", "transfer", "1", "", "-R", "o/r"}, "not OWNER/REPO"},
		// testdata/fuzz/FuzzClassify/b165e469e64ef9d2: a github.com URL whose path is
		// percent-encoded names o/r decoded and o%2Fr/ as sent.
		{[]string{"pr", "view", "https://github.com/o%2Fr/"}, "-R OWNER/REPO"},
		{[]string{"pr", "view", "https://github.com/o%2Fother/r/pull/1"}, "-R OWNER/REPO"},
		{[]string{"issue", "develop", "1", "--branch-repo", "https://github.com/o%2Fr", "-R", "o/r"}, "OWNER/REPO"},
		// testdata/fuzz/FuzzClassify/b7c51892e55c1251: a search --repo was checked trimmed and
		// sent as given; gh quotes it, `repo:"o/r "` (MEASURED).
		{[]string{"search", "code", "--repo", "o/r "}, "no spaces"},
		{[]string{"search", "issues", "x", "--repo", " o/r"}, "no spaces"},
		{[]string{"search", "issues", "x", "--repo", "o/r, me/r-fork"}, "no spaces"},
		{[]string{"search", "issues", "x", "--repo", `"o/r,other/x"`}, "no spaces"},
	} {
		d := Classify(c.argv, "o/r", testScope)
		if d.Outcome != OutcomeRefused || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("gh %q: %q %q (%s), want refused with %q", c.argv, d.Outcome, d.Argv, d.Reason, c.reason)
		}
	}
}

// BB-D40 makes the repository explicit in every canonical argv, because gh takes one from the
// git checkout it finds from its cwd wherever the argv names none, and git looks upward from
// the broker's empty cwd. A github.com URL argument names the repository only where gh reads
// that URL as the command's own: a pull request's URL to `pr view`, an issue's to `issue
// view`. Anywhere else gh takes the URL as a branch, a tag, a run id or a path, and asks the
// checkout for the repository (MEASURED against gh 2.101.0, with a checkout above an empty
// cwd: `ruleset check https://github.com/o/r/x` read that checkout's rules, and `pr view` of
// an issue's URL looked for a pull request in it). So the URL's repository goes into the argv
// as --repo too, which gh reads the same as the URL wherever it reads the URL (MEASURED).
func TestClassifyAURLArgumentStillGetsAnExplicitRepository(t *testing.T) {
	for _, c := range []struct{ argv, field, run string }{
		{"pr view https://github.com/o/r/pull/1", "", "pr view https://github.com/o/r/pull/1 --repo=o/r"},
		{"pr view https://github.com/o/r/pull/1", "me/r-fork", "pr view https://github.com/o/r/pull/1 --repo=o/r"},
		{"pr view https://github.com/o/r/issues/1", "", "pr view https://github.com/o/r/issues/1 --repo=o/r"},
		{"ruleset check https://github.com/o/r/x", "", "ruleset check https://github.com/o/r/x --repo=o/r"},
		{"run view https://github.com/o/r/x", "", "run view https://github.com/o/r/x --repo=o/r"},
		{"workflow view https://github.com/me/r-fork/x", "o/r",
			"workflow view https://github.com/me/r-fork/x --repo=me/r-fork"},
		// An explicit -R stays the one gh reads.
		{"pr view https://github.com/me/r-fork/pull/1 -R o/r", "", "pr view --repo=o/r https://github.com/me/r-fork/pull/1"},
	} {
		d := Classify(splitArgv(c.argv), c.field, testScope)
		if d.Outcome != OutcomeStanding || strings.Join(d.Argv, " ") != c.run {
			t.Errorf("gh %s: %q runs %q (%s), want a standing read of %q", c.argv, d.Outcome, d.Argv, d.Reason, c.run)
		}
	}
}

// The production path: the host gh is told the repository a URL argument named.
func TestServeGivesAURLArgumentAnExplicitRepository(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	code, out, errOut := f.serve(t, Request{Argv: []string{"ruleset", "check", "https://github.com/o/r/x"}})
	if code != 0 || out != "ran ruleset check https://github.com/o/r/x --repo=o/r\n" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}
