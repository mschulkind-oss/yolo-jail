package brokerscope

import (
	"strings"
	"testing"
)

// A REPOSITORY THAT LEFT A LIVE SCOPE IS APPROVED AGAIN WITHOUT A RELAUNCH: `yolo config apply`
// rewrites a live scope while the jail runs (LA-D11), so the refusal names that step, and only a
// scope that is not live still says to relaunch.
func TestARepositoryThatLeftTheScopeNamesTheStepItsScopeTakes(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	const approved = "git@github.com:mschulkind/dotfiles"
	const request = "git@github.com:mschulkind/other"
	path, f := remoteURLFile(t, "left-scope", FormRemoteURL, approved)
	for _, live := range []bool{true, false} {
		f.LiveScope = live
		if _, err := Write(f); err != nil {
			t.Fatal(err)
		}
		err := ValidateCurrent(path, f, request)
		if err == nil {
			t.Fatalf("live=%v: a repository outside the scope was admitted", live)
		}
		names := strings.Contains(err.Error(), "`yolo config apply`") && strings.Contains(err.Error(), "at its next call")
		relaunch := strings.Contains(err.Error(), "relaunch")
		if names != live || relaunch == live {
			t.Errorf("live=%v: the refusal does not name its scope's step: %v", live, err)
		}
	}
}

func TestValidateCurrentRemoteMatcherIsExplicitAndUsesFreshScope(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	const approved = "git@github.com:mschulkind/dotfiles"
	const request = "ssh://git@github.com/mschulkind/dotfiles"
	path, f := remoteURLFile(t, "remote-match", FormRemoteURL, approved)
	matcher := func(entry, candidate string) bool {
		return entry == approved && candidate == request
	}
	if err := ValidateCurrent(path, f, request); err == nil {
		t.Fatal("the default matcher admitted a non-identical remote URL")
	}
	if err := ValidateCurrentWithMatcher(path, f, request, matcher); err != nil {
		t.Fatalf("the explicitly supplied matcher refused the equivalent request: %v", err)
	}

	f.Repos = []string{"git@github.com:other/repository"}
	if _, err := Write(f); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCurrentWithMatcher(path, f, request, matcher); err == nil {
		t.Fatal("the matcher admitted a request after its approved entry was revoked")
	}
}

func TestValidateCurrentMatcherCannotChangeOwnerRepoForm(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	path, f := remoteURLFile(t, "owner-match", "", "owner/repo")
	if err := ValidateCurrentWithMatcher(path, f, "other/repo", func(string, string) bool { return true }); err == nil {
		t.Fatal("the optional remote matcher changed owner/repo admissions")
	}
}
