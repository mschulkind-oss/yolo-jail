package ghbroker

import (
	"net/url"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// scope.go is the repository scope as the broker holds it: the `owner/repo` list its launch's
// config-change gate approved, the workspace's remotes and its `brokered.github.repos` entry
// alike (docs/design/boundary-broker.md §5.6, BB-D32; docs/design/workspace-widening.md). The
// broker never reads a remote, an approval record or a config file; it is handed this list in
// the launch's scope file and checks every command against it.

// Scope is the set of repositories a jail's brokered calls may touch.
type Scope struct {
	repos []string
	// configFile and localFile are the names of the workspace's config file and local file as
	// the launch's loader reads them (brokerscope.File, WW-D23), for the out-of-scope message
	// alone; "" when the launch handed none. They admit nothing.
	configFile, localFile string
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

// ForFiles returns s naming the workspace's config file and local file, so the refusal of a
// repository outside the scope can name the exact file to add it to.
func (s Scope) ForFiles(configFile, localFile string) Scope {
	s.configFile, s.localFile = configFile, localFile
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

// entryKey is the workspace config key a repository joins the scope through. This package is
// the github pack's broker, so it names its source literally (WW-D13).
const entryKey = "brokered." + Source + ".repos"

// describe renders the scope for a message.
func (s Scope) describe() string {
	if len(s.repos) == 0 {
		return "empty: this workspace has no approved GitHub remote and no approved `" + entryKey + "` entry"
	}
	return strings.Join(s.repos, ", ")
}

// widenAdvice is the one way a repository outside the scope gets in (WW-D5, WW-D6): the agent
// adds it to the workspace's `brokered.github.repos` entry, checks its edit, and asks the user
// to restart the jail and approve the repository scope at that fresh launch. No answer the jail
// gives and no notification widens the scope.
//
// It tells the agent to add to the list, never to paste a new object: a second top-level
// `brokered` in a file that already has one would replace the first (WW-D24). It names the two
// files the launch's loader reads, so it never sends the agent to create a `yolo-jail.jsonc`
// beside a `yolo-jail.json`, which would shadow that whole file; without them it names the file
// the environment briefing gives. It names no host path and no user config.
func (s Scope) widenAdvice(repo string) string {
	config, local := "the workspace config file your environment briefing names", "the local file beside it"
	readOnly, useLocal := "that file", "the local file"
	if s.configFile != "" && s.localFile != "" {
		config, local, readOnly, useLocal = s.configFile, s.localFile, s.configFile, s.localFile
	}
	return "A repository joins this jail's scope only when the user approves it at a fresh launch. To " +
		"ask for " + repo + ", add it to the `repos` list under `brokered." + Source + "` in " + config +
		", or in " + local + " for what the project should not commit; if " + readOnly + " is " +
		"read-only here, use " + useLocal + ". Only if the file has no `brokered` key yet, add " +
		"\"brokered\": {\"" + Source + "\": {\"repos\": [\"" + repo + "\"]}}. Then run `yolo check " +
		"--no-build`, and ask the user to restart the jail and approve the repository scope at " +
		"launch. Nothing sent through gh adds a repository."
}

// ValidRepo reports whether s is exactly `owner/repo`: two segments, each a GitHub name,
// neither "." nor "..". A three-segment `HOST/owner/repo` is not valid: it sends the call,
// with a token, to the host it names (H1 in the design's Appendix B).
func ValidRepo(s string) bool { return brokerscope.ValidRepo(s) }

// repoFromGitHubURL returns the `owner/repo` an https://github.com URL names, or "" when
// the URL is not one. A path with any percent-encoding is not one: decoded it names one
// repository and as sent another (`https://github.com/o%2Fr/` is o/r decoded), so it is
// refused rather than read, as the `gh api` rule refuses an encoded `repos/OWNER/REPO`
// (BB-D58; found by FuzzClassify). gh itself refuses such a URL for `repo view` (MEASURED).
func repoFromGitHubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return ""
	}
	if u.RawPath != "" || strings.Contains(u.EscapedPath(), "%") {
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
