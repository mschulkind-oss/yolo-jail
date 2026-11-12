package config

import "testing"

// A named repository that declares only its repo line, with no requests table, is a valid
// read-only declaration: the projection reads it without a panic and grants nothing under it.
func TestARepoOnlyNamedTableProjectsNoGrant(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", "")
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	declarations := []ReadableBrokerDeclaration{*repoScopeBroker().Readable}
	for _, w := range WorkspaceBrokerGrantsOf(read.Config, workspace, declarations) {
		if len(w.Grants) != 0 {
			t.Errorf("the narrowed projection grants %v under a repo-only table", w.Grants)
		}
	}
	for _, w := range LegacyWorkspaceBrokerGrantsOf(read.Config, workspace, declarations) {
		if len(w.Grants) != 0 {
			t.Errorf("the legacy projection grants %v under a repo-only table", w.Grants)
		}
	}
	check := NewScopeCheck(workspace, []ScopeBroker{repoScopeBroker()}, read)
	if check.Fatal != nil {
		t.Fatalf("a repo-only table is refused: %v", check.Fatal)
	}
}
