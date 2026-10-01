package ghbroker

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// scope.go is the repository scope as the broker holds it: the `owner/repo` list its
// launch approved, plus what the user-scope widening entry added for the workspace
// (docs/design/boundary-broker.md §5.6, BB-D32, BB-D33). The broker never reads a remote, an
// approval record or the user config; it is handed this list in the launch's scope file and
// checks every command against it.

// Scope is the set of repositories a jail's brokered calls may touch.
type Scope struct {
	repos []string
	// workspace is the host workspace a widening entry for this jail is keyed by (BB-D33),
	// for the out-of-scope message alone; "" when the broker was handed none. It admits
	// nothing.
	workspace string
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

// ForWorkspace returns s naming the host workspace its launch ran in, so the refusal of a
// repository outside the scope can spell the widening entry that would admit it.
func (s Scope) ForWorkspace(workspace string) Scope {
	s.workspace = workspace
	return s
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
		return "empty: this workspace has no GitHub remote, and no widening entry adds a repository"
	}
	return strings.Join(s.repos, ", ")
}

// widenAdvice is the one way a repository outside the scope gets in (OQ-BB6, BB-D33): a
// widening entry in the HOST user's config, keyed by this workspace's host path, which only
// the host user writes and the next fresh launch reads. No answer the jail gives and no
// notification widens the scope, so the advice names the entry, spelled as `yolo config-ref`
// documents it, and who writes it. The workspace is the host path the jail is already told
// (YOLO_HOST_DIR); JSON-quoting it writes any control character in it as an escape, and
// leaves `&`, `<` and `>` as they are, so the key reads as the folder's own name.
func (s Scope) widenAdvice(repo string) string {
	ws := s.workspace
	if ws == "" {
		ws = "<this workspace's host path>"
	}
	var quoted strings.Builder
	enc := json.NewEncoder(&quoted)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(ws)
	key := strings.TrimSuffix(quoted.String(), "\n")
	return "The scope is this workspace's GitHub remotes, approved at a fresh launch, plus what a " +
		"widening entry in the host user's config adds for this workspace. To admit " + repo +
		", the host user adds it to " + paths.UserConfigPath() + " as \"brokered\": {\"" + Source +
		"\": {\"workspaces\": {" + key + ": {\"repos\": [\"" + repo + "\"]}}}}, and it is " +
		"in scope from the next fresh launch; nothing the jail sends widens the scope."
}

// ValidRepo reports whether s is exactly `owner/repo`: two segments, each a GitHub name,
// neither "." nor "..". A three-segment `HOST/owner/repo` is not valid: it sends the call,
// with a token, to the host it names (H1 in the design's Appendix B).
func ValidRepo(s string) bool { return brokerscope.ValidRepo(s) }

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
	repo := parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if !ValidRepo(repo) {
		return ""
	}
	return repo
}
