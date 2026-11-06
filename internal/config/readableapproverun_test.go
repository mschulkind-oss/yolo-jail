package config_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// The readable spelling of the `approve-run` grant: `allow.github.on_request =
// ["approve_runs"]` in the user's config, and `requests.github.approve_runs = ["<login>"]` in
// the project's, become one grant per target repository naming the actor.

func githubApproveDeclarations() []config.ReadableBrokerDeclaration {
	return []config.ReadableBrokerDeclaration{{Pack: "github", Label: "github-broker",
		Brokered:       loopholedecl.Brokered{Source: "github", RemoteHost: "github.com", Grants: []string{"create-pr", "create-issue", "runs", "approve-run"}},
		ReadCapability: "read", KindCapabilities: map[string]string{"create-pr": "create_pr", "create-issue": "create_issue",
			"runs": "runs", "approve-run": "approve_runs"}}}
}

func TestReadableApproveRunsRequestBecomesAGrantPerTarget(t *testing.T) {
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User:          readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["approve_runs"]}}}`),
		Workspace:     readableMap(t, `{"requests":{"github":{"repos":["acme/private"],"approve_runs":["github-actions[bot]","*"]}}}`),
		SelectedPacks: []string{"github"}, Declarations: githubApproveDeclarations()})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 1 {
		t.Fatalf("gate sources %#v", gate.Sources)
	}
	for _, want := range []brokerscope.Grant{
		{Kind: "approve-run", Repo: "github.com/acme/private", Actor: "github-actions[bot]"},
		{Kind: "approve-run", Repo: "github.com/acme/private", Actor: "*"},
	} {
		if !containsReadableGrant(gate.Sources[0].Grants, want) {
			t.Fatalf("the gate carries %+v, want %+v among them", gate.Sources[0].Grants, want)
		}
	}
}

func TestReadableApproveRunsOutsideTheCeilingIsSkipped(t *testing.T) {
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User:          readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["runs"]}}}`),
		Workspace:     readableMap(t, `{"requests":{"github":{"repos":["acme/private"],"approve_runs":["octocat"]}}}`),
		SelectedPacks: []string{"github"}, Declarations: githubApproveDeclarations()})
	if err != nil {
		t.Fatal(err)
	}
	skipped := false
	for _, r := range plan.Requests {
		if r.Path == "requests.github.approve_runs" {
			skipped = r.Status == config.ReadableRequestSkipped && strings.Contains(r.Reason, "allow.github.on_request")
		}
	}
	if !skipped {
		t.Fatalf("request decisions %#v", plan.Requests)
	}
}

func TestReadableApproveRunsRefusesWhatIsNotALogin(t *testing.T) {
	for _, bad := range []string{"-octocat", "octo cat", "octo/cat", "bot[bot][bot]", "dependabot*", strings.Repeat("a", 40)} {
		_, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
			User:          readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["approve_runs"]}}}`),
			Workspace:     readableMap(t, `{"requests":{"github":{"approve_runs":["`+bad+`"]}}}`),
			SelectedPacks: []string{"github"}, Declarations: githubApproveDeclarations()})
		if err == nil || !strings.Contains(err.Error(), "is not an actor a grant can name") || !strings.Contains(err.Error(), "github-actions[bot]") {
			t.Fatalf("%q: error %v", bad, err)
		}
	}
}
