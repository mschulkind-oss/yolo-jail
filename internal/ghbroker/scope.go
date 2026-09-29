package ghbroker

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// scope.go is the repository scope as the broker holds it: the `owner/repo` list its
// launch approved (docs/design/boundary-broker.md §5.6, BB-D32). The broker never reads a
// remote or an approval record; it is handed this list in the launch's scope file and
// checks every command against it.

// Scope is the set of repositories a jail's brokered calls may touch.
type Scope struct {
	repos []string
}

// NewScope builds a scope from `owner/repo` names, dropping malformed ones.
func NewScope(repos []string) Scope {
	seen := map[string]bool{}
	var out []string
	for _, r := range repos {
		if !ValidRepo(r) || seen[strings.ToLower(r)] {
			continue
		}
		seen[strings.ToLower(r)] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return Scope{repos: out}
}

// Repos is the scope's repositories, sorted.
func (s Scope) Repos() []string { return append([]string(nil), s.repos...) }

// Contains reports whether repo is in scope. GitHub owner and repository names are
// case-insensitive, so the comparison is too.
func (s Scope) Contains(repo string) bool {
	for _, r := range s.repos {
		if strings.EqualFold(r, repo) {
			return true
		}
	}
	return false
}

// describe renders the scope for a message.
func (s Scope) describe() string {
	if len(s.repos) == 0 {
		return "empty: this workspace has no GitHub remote"
	}
	return strings.Join(s.repos, ", ")
}

// nameRE is one GitHub owner or repository name segment.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidRepo reports whether s is exactly `owner/repo`: two segments, each a GitHub name,
// neither "." nor "..". A three-segment `HOST/owner/repo` is not valid: it sends the call,
// with a token, to the host it names (H1 in the design's Appendix B).
func ValidRepo(s string) bool {
	owner, repo, ok := strings.Cut(s, "/")
	if !ok || strings.Contains(repo, "/") {
		return false
	}
	return validSegment(owner) && validSegment(repo)
}

func validSegment(s string) bool {
	return s != "." && s != ".." && nameRE.MatchString(s)
}

// repoFromGitHubURL returns the `owner/repo` an https://github.com URL names, or "" when
// the URL is not one.
func repoFromGitHubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if !validSegment(parts[0]) || !validSegment(repo) {
		return ""
	}
	return parts[0] + "/" + repo
}
