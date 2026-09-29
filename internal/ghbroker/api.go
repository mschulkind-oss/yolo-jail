package ghbroker

import (
	"fmt"
	"regexp"
	"strings"
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

	path, _, _ := strings.Cut(strings.TrimPrefix(endpoint, "/"), "?")
	if why := apiPathProblem(path); why != "" {
		return scopeResult{refused: fmt.Sprintf("gh api endpoint %q is refused: %s", endpoint, why)}
	}
	switch {
	case path == "graphql" || path == "api/graphql":
		// A GraphQL call names no repository the broker can check (§5.3 rule 6).
		return scopeResult{account: true}
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
		return scopeResult{account: true}
	}
}

func acceptHeader(v string) bool {
	name, val, ok := strings.Cut(v, ":")
	return ok && strings.EqualFold(strings.TrimSpace(name), "accept") &&
		acceptRE.MatchString(strings.TrimSpace(val))
}

// apiPathProblem refuses a path whose meaning could differ between the broker's reading
// and the server's: an encoded character, a dot segment, an empty segment or a backslash.
func apiPathProblem(path string) string {
	switch {
	case path == "":
		return "it is empty"
	case strings.ContainsAny(path, "%\\"):
		return "it carries an encoded or escaped character"
	case strings.Contains(path, "//"):
		return "it has an empty path segment"
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "." || seg == ".." {
			return "it has a dot segment"
		}
	}
	return ""
}
