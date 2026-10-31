package brokerscope

import "testing"

// A dispatch-workflow grant names one workflow file or `*` in its own field, and spells back
// through String and ParseGrant (docs/design/github-write-scopes.md GW-D14).
func TestDispatchWorkflowGrantShape(t *testing.T) {
	for _, c := range []struct {
		g  Grant
		ok bool
	}{
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: "nightly-macos.yml"}, true},
		{Grant{Kind: KindDispatchWorkflow, Repo: "*", Workflow: "*"}, true},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: "ci.yaml"}, true},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r"}, false},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: "12345"}, false},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: ".github/workflows/ci.yml"}, false},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: "nightly*.yml"}, false},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: "ci.yml", Branch: "main"}, false},
		{Grant{Kind: KindBranch, Repo: "github.com/o/r", Branch: "main", Workflow: "ci.yml"}, false},
		{Grant{Kind: "create-pr", Repo: "github.com/o/r", Workflow: "ci.yml"}, false},
	} {
		if got := ValidGrant(c.g); got != c.ok {
			t.Errorf("ValidGrant(%+v) = %v, want %v", c.g, got, c.ok)
			continue
		}
		if !c.ok {
			continue
		}
		back, ok := ParseGrant(c.g.String())
		if !ok || back != c.g {
			t.Errorf("ParseGrant(%q) = %+v %v, want %+v", c.g.String(), back, ok, c.g)
		}
	}
	if !GrantTakesName(KindDispatchWorkflow) || GrantTakesRef(KindDispatchWorkflow) || GrantNameKey(KindDispatchWorkflow) != "workflow" {
		t.Fatal("dispatch-workflow names a workflow, not a ref")
	}
}
