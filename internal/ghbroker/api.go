package ghbroker

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// api.go is `gh api`'s rule (docs/design/boundary-broker.md §5.3), applied to the broker's
// own parse. gh's rule, from its help: the method is GET normally and POST if any
// parameter was added; `--method` overrides.

// apiFlags are the `gh api` flags the broker accepts at all. The rest of api's grammar is
// refused by the global flag rules (--hostname, --jq, --template, --verbose); this list is
// the allowlist that says so for anything a later grammar adds.
var apiFlags = map[string]bool{
	"method": true, "field": true, "raw-field": true, "header": true, "include": true,
	"input": true, "paginate": true, "slurp": true, "silent": true, "preview": true,
	"cache": true, "allow-escape-sequences": true, "help": true,
}

// acceptRE is the one header value the broker passes: a GitHub media type, or JSON. Any
// other header is refused, because `X-HTTP-Method-Override` passes through gh untouched
// (MEASURED), so a GET carrying it would reach GitHub as a DELETE.
var acceptRE = regexp.MustCompile(`^application/(json|vnd\.github([.+][A-Za-z0-9.+-]+)?)$`)

// apiRepoPathRE is a REST path under one repository.
var apiRepoPathRE = regexp.MustCompile(`^repos/([^/]+)/([^/]+)(/.*)?$`)

func apiScope(p *parsed, fieldRepo string) scopeResult {
	for _, u := range p.flags {
		if !apiFlags[u.flag.long] {
			return scopeResult{refused: "gh api --" + u.flag.long + " is refused"}
		}
		if u.flag.long == "header" && !acceptHeader(u.value) {
			return scopeResult{refused: fmt.Sprintf("gh api header %q is refused: only an Accept "+
				"header naming a GitHub media type passes, since any other header can change what "+
				"the request does", u.value)}
		}
	}
	if len(p.positionals) != 1 {
		return scopeResult{refused: "gh api takes exactly one endpoint"}
	}
	endpoint := p.positionals[0]
	if strings.Contains(endpoint, "://") {
		return scopeResult{refused: fmt.Sprintf("gh api endpoint %q is a URL; the broker takes a "+
			"path on api.github.com only, since a URL sends the login to the host it names", endpoint)}
	}

	// Placeholders: {owner} and {repo} from the forwarder's repository; {branch} names the
	// local checkout's branch, which the broker does not have.
	subst := func(s string) (string, string) {
		if strings.Contains(s, "{branch}") {
			return "", "the {branch} placeholder names a local checkout's branch, which the broker " +
				"does not have; spell the branch"
		}
		if strings.Contains(s, "{owner}") || strings.Contains(s, "{repo}") {
			if !ValidRepo(fieldRepo) {
				return "", "the {owner}/{repo} placeholders need the workspace's repository, and the " +
					"forwarder found none; spell OWNER/REPO"
			}
			owner, repo, _ := strings.Cut(fieldRepo, "/")
			s = strings.ReplaceAll(strings.ReplaceAll(s, "{owner}", owner), "{repo}", repo)
		}
		return s, ""
	}
	endpoint, why := subst(endpoint)
	if why != "" {
		return scopeResult{refused: why}
	}
	for i := range p.flags {
		if l := p.flags[i].flag.long; (l == "field" || l == "raw-field") && p.flags[i].hasValue {
			v, why := subst(p.flags[i].value)
			if why != "" {
				return scopeResult{refused: why}
			}
			p.flags[i].value = v
		}
	}
	p.positionals[0] = endpoint

	// The method, by gh's own rule.
	method := "GET"
	if vals := p.values("method"); len(vals) > 0 {
		method = strings.ToUpper(vals[len(vals)-1])
	} else if p.has("field") || p.has("raw-field") || p.has("input") {
		method = "POST"
	}

	path, why := apiEndpointPath(endpoint)
	if why != "" {
		return scopeResult{refused: fmt.Sprintf("gh api endpoint %q is refused: %s", endpoint, why)}
	}
	switch {
	case path == "graphql" || path == "api/graphql":
		// A GraphQL call names no repository the broker can check (§5.3 rule 6, BB-D61).
		return scopeResult{account: true, why: graphqlWhy, next: graphqlAdvice(graphqlQuery(p))}
	case apiRepoPathRE.MatchString(path):
		m := apiRepoPathRE.FindStringSubmatch(path)
		repo := m[1] + "/" + m[2]
		if !ValidRepo(repo) {
			return scopeResult{refused: fmt.Sprintf("gh api endpoint %q names no valid OWNER/REPO", endpoint)}
		}
		return scopeResult{repos: []string{repo}, argv: p.canonical(),
			apiRead: method == "GET" || method == "HEAD"}
	default:
		// A path with no repository reads or writes across the account.
		return scopeResult{account: true, next: "Name a REST path under repos/OWNER/REPO/ for a " +
			"repository in scope; a path across the account (the user, an organization, a search, " +
			"a gist) is the host user's to run on the host."}
	}
}

// graphqlWhy is why raw GraphQL is account-wide however its query reads (BB-D61). A query
// whose every repository(owner:, name:) is in scope is still not checkable: GraphQL is a
// graph, and the scope is not closed under its edges, so a query rooted in an in-scope
// repository reaches others through an issue's author's repositories, a fork's parent or a
// cross-reference, and node(id:) or nodes(ids:) reach any object by its global ID with no
// owner or name in the text at all. Holding a query to the scope would take a schema-aware
// proof that no field leaves it, which the broker does not attempt.
const graphqlWhy = "a GraphQL query reaches any object the login can read, not only a " +
	"repository it names: an edge out of one (an issue's author's repositories, a fork's " +
	"parent, a cross-reference) or a global node ID (node(id:)) lands in another, so no check " +
	"of the query's text holds it to the scope"

// graphqlHints map a word in a refused GraphQL query to the gh command that asks GitHub the
// same question for a repository the broker checks; the host gh then runs GitHub's GraphQL
// itself, with gh's own query and the repository as its variables (MEASURED against gh
// 2.101.0: pr view, pr list, issue view, issue list, repo view, release list, label list and
// discussion list each send repository(owner: $owner, name: $repo) with the -R the broker
// checked). The first match wins, so a plural is listed before its singular.
var graphqlHints = []struct{ word, use string }{
	{"pullrequests", "gh pr list -R OWNER/REPO --json <fields>"},
	{"pullrequest", "gh pr view <number> -R OWNER/REPO --json <fields>"},
	{"issues", "gh issue list -R OWNER/REPO --json <fields>"},
	{"issue", "gh issue view <number> -R OWNER/REPO --json <fields>"},
	{"discussion", "gh discussion view <number> or gh discussion list -R OWNER/REPO --json <fields>"},
	{"release", "gh release view <tag> or gh release list -R OWNER/REPO --json <fields>"},
	{"label", "gh label list -R OWNER/REPO --json <fields>"},
	{"statuscheckrollup", "gh pr checks <number> -R OWNER/REPO"},
	{"checkrun", "gh pr checks <number> -R OWNER/REPO, or gh run view <run-id> -R OWNER/REPO"},
}

// graphqlAdvice is the refusal's next step, naming the command for what the query asks
// where one is plain from its words. It is advice alone: nothing in it widens what runs.
func graphqlAdvice(query string) string {
	use := "gh pr view, gh issue view, gh repo view or gh release list with -R OWNER/REPO and " +
		"--json <fields>"
	q := strings.ToLower(query)
	for _, h := range graphqlHints {
		if strings.Contains(q, h.word) {
			use = h.use
			break
		}
	}
	return "Use the gh command that asks the same of a repository the broker checks, which the " +
		"host gh answers with GitHub's GraphQL for you: " + use + "; or a REST path under " +
		"gh api repos/OWNER/REPO/."
}

// graphqlQuery is the `query` field a `gh api graphql` call carries, or "".
func graphqlQuery(p *parsed) string {
	for _, long := range []string{"raw-field", "field"} {
		for _, v := range p.values(long) {
			if q, ok := strings.CutPrefix(v, "query="); ok {
				return q
			}
		}
	}
	return ""
}

func acceptHeader(v string) bool {
	name, val, ok := strings.Cut(v, ":")
	return ok && strings.EqualFold(strings.TrimSpace(name), "accept") &&
		acceptRE.MatchString(strings.TrimSpace(val))
}

// apiEndpointPath returns the path an endpoint names, as gh sends it, or why it is refused.
// gh builds the URL as https://api.github.com/ and the endpoint with one leading slash
// trimmed, and Go's URL parser ends the path at the first `#` and then the first `?`
// (MEASURED against gh 2.101.0 and a local fake API, BB-D58). A `#` is refused rather than
// read past: what follows it is never sent, so an endpoint carrying one is a mistake whose
// result would be some other path's.
func apiEndpointPath(endpoint string) (path, why string) {
	rest := strings.TrimPrefix(endpoint, "/")
	if strings.Contains(rest, "#") {
		return "", "it carries a #, which ends the path gh sends, so what follows it never " +
			"reaches GitHub. Write a # that belongs in the path as %23"
	}
	path, _, _ = strings.Cut(rest, "?")
	return path, apiPathProblem(path)
}

// apiPathProblem refuses a path whose meaning could differ between the broker's reading and
// the server's (BB-D58). gh sends the path's encoding as the jail spelled it, so the broker
// decodes it exactly once, segment by segment, and refuses what any common server reading
// could resolve outside the repository it checks: an invalid escape, bytes that are not
// UTF-8, an escape still left after one decoding (a server that decodes twice), a control
// character, a backslash (read as / by some servers), a dot segment or an interior empty
// segment of the decoded path, and any encoding inside the `repos/OWNER/REPO` prefix, which
// is the part the scope check reads. Anything else may stay encoded, so a branch named MS/main
// is `branches/MS%2Fmain`, and the endpoint gh runs is the one the jail sent.
func apiPathProblem(path string) string {
	if path == "" {
		return "it is empty. Name a REST path, such as repos/OWNER/REPO/pulls"
	}
	raw := strings.Split(path, "/")
	dec := make([]string, len(raw))
	for i, seg := range raw {
		d, err := url.PathUnescape(seg)
		if err != nil {
			return "it carries a % that does not begin a two-digit hex escape, so the broker " +
				"cannot read the path as GitHub will. Encode each character that needs it as %XX " +
				"(a / inside a branch name as %2F); for a file whose name holds a literal %, " +
				"read it with gh repo read-file PATH -R OWNER/REPO, which encodes the name itself"
		}
		dec[i] = d
	}
	decoded := strings.Join(dec, "/")
	switch {
	case !utf8.ValidString(decoded):
		return "it decodes to bytes that are not UTF-8, which a server could read as other " +
			"characters (an overlong %c0%ae is a dot to some). Encode each character's UTF-8 bytes"
	case strings.Contains(decoded, "%"):
		return "it is still encoded after one decoding (a %25 followed by more), which a server " +
			"that decodes twice reads as another path. Encode each character once; for a file " +
			"whose name holds a literal %, read it with gh repo read-file PATH -R OWNER/REPO"
	case strings.ContainsFunc(decoded, unicode.IsControl):
		return "it carries a control character, as typed or encoded, which no GitHub path holds. " +
			"Remove it"
	case strings.Contains(decoded, "\\"):
		return "it carries a backslash, as typed or as %5C, which some servers read as a /. " +
			"Write the path with / alone"
	}
	segs := strings.Split(decoded, "/")
	for i, seg := range segs {
		switch {
		case seg == "." || seg == "..":
			return "it has a dot segment (. or ..), as typed or once decoded, which a server " +
				"that resolves dot segments reads as a path outside repos/OWNER/REPO. Write the " +
				"path without . or .. segments"
		case seg == "" && i < len(segs)-1:
			return "it has an empty segment (//, as typed or once decoded), which some servers " +
				"collapse into another path. Write the path with single slashes"
		}
	}
	// The prefix the scope check reads must read the same decoded or not.
	if segs[0] == "repos" {
		for i := 0; i < 3 && i < len(segs); i++ {
			if i >= len(raw) || raw[i] != segs[i] {
				return "it encodes a character in its repos/OWNER/REPO part, which decoding turns " +
					"into a different path than the one the broker checks. Spell repos/OWNER/REPO " +
					"as plain text, since GitHub owner and repository names never need encoding; " +
					"the rest of the path may stay encoded (repos/OWNER/REPO/branches/MS%2Fmain)"
			}
		}
	}
	return ""
}
