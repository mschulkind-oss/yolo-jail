package gitbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// RC-D26: the refusal of a github.com repository outside the scope names the request verb, and
// keeps naming `git remote add` for a host a project cannot request.
func TestOutOfScopeRefusalNamesTheRequestVerbForGitHub(t *testing.T) {
	f := brokerscope.File{LiveScope: true}
	for _, remote := range []string{"https://github.com/acme/private.git", "git@github.com:acme/private.git", "ssh://git@github.com/acme/private"} {
		got := outOfScope(remote, f)
		if !strings.Contains(got, "`yolo request repos <alias> github.com/acme/private`") {
			t.Errorf("%s: the refusal does not name the request verb:\n%s", remote, got)
		}
		if !strings.Contains(got, "config-change gate") || !strings.Contains(got, "not in this workspace's approved scope") {
			t.Errorf("%s: the refusal lost its gate advice:\n%s", remote, got)
		}
	}
	other := outOfScope("git@other.test:o/r.git", f)
	if strings.Contains(other, "yolo request") || !strings.Contains(other, "`git remote add <name> <url>`") {
		t.Errorf("a host a project cannot request must keep the remote advice:\n%s", other)
	}
}

// RC-D26: the two URLs a request approves match the spellings a fetch uses, and no others.
func TestRequestedRepoURLsMatchTheirSpellingsOnly(t *testing.T) {
	scope := brokerscope.File{Repos: brokerscope.RequestedRepoURLs("github.com", "acme/private")}
	for _, remote := range []string{
		"https://github.com/acme/private", "https://github.com/acme/private.git", "https://github.com/acme/private/",
		"git@github.com:acme/private.git", "git@github.com:acme/private", "ssh://git@github.com/acme/private.git",
	} {
		if !scopeContains(scope, remote) {
			t.Errorf("%s is the requested repository and is out of scope", remote)
		}
	}
	for _, remote := range []string{
		"https://github.com/acme/private-two", "https://github.com/acme/private/x", "https://github.com/acme/private.git.git",
		"https://user@github.com/acme/private", "https://github.com:8443/acme/private", "https://gitlab.com/acme/private",
		"https://github.com/other/private", "git@github.com:acme/other.git",
	} {
		if scopeContains(scope, remote) {
			t.Errorf("%s is not the requested repository and is in scope", remote)
		}
	}
}
