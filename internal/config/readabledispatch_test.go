package config_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// The readable spelling of the `runs` grant (docs/design/github-write-scopes.md
// GW-D14): `allow.github.on_request = ["runs"]` in the user's config, and
// `requests.github.runs = ["<file>"]` in the project's, become one grant per
// target repository naming the workflow.

func githubDispatchDeclarations() []config.ReadableBrokerDeclaration {
	return []config.ReadableBrokerDeclaration{{Pack: "github", Label: "github-broker",
		Brokered:       loopholedecl.Brokered{Source: "github", RemoteHost: "github.com", Grants: []string{"create-pr", "create-issue", "runs"}},
		ReadCapability: "read", KindCapabilities: map[string]string{"create-pr": "create_pr", "create-issue": "create_issue", "runs": "runs"}}}
}

func TestReadableDispatchWorkflowsRequestBecomesAGrantPerTarget(t *testing.T) {
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User:          readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["runs"]}}}`),
		Workspace:     readableMap(t, `{"requests":{"github":{"repos":["acme/private"],"runs":["nightly-macos.yml","*"]}}}`),
		SelectedPacks: []string{"github"}, Declarations: githubDispatchDeclarations()})
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
		{Kind: "runs", Repo: "github.com/acme/private", Workflow: "nightly-macos.yml"},
		{Kind: "runs", Repo: "github.com/acme/private", Workflow: "*"},
	} {
		if !containsReadableGrant(gate.Sources[0].Grants, want) {
			t.Fatalf("the gate carries %+v, want %+v among them", gate.Sources[0].Grants, want)
		}
	}
}

func TestReadableDispatchWorkflowsOutsideTheCeilingIsSkipped(t *testing.T) {
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User:          readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["create_pr"]}}}`),
		Workspace:     readableMap(t, `{"requests":{"github":{"repos":["acme/private"],"runs":["nightly.yml"]}}}`),
		SelectedPacks: []string{"github"}, Declarations: githubDispatchDeclarations()})
	if err != nil {
		t.Fatal(err)
	}
	skipped := false
	for _, r := range plan.Requests {
		if r.Path == "requests.github.runs" {
			skipped = r.Status == config.ReadableRequestSkipped && strings.Contains(r.Reason, "allow.github.on_request")
		}
	}
	if !skipped {
		t.Fatalf("request decisions %#v", plan.Requests)
	}
	gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range gate.Sources {
		for _, g := range s.Grants {
			if g.Kind == "runs" {
				t.Fatalf("a request outside the ceiling became a grant: %+v", g)
			}
		}
	}
}

func TestReadableDispatchWorkflowsRefusesWhatIsNotAFileName(t *testing.T) {
	for _, bad := range []string{"12345", ".github/workflows/nightly.yml", "Nightly build", "main"} {
		_, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
			User:          readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["runs"]}}}`),
			Workspace:     readableMap(t, `{"requests":{"github":{"runs":["`+bad+`"]}}}`),
			SelectedPacks: []string{"github"}, Declarations: githubDispatchDeclarations()})
		if err == nil || !strings.Contains(err.Error(), "is not a workflow a grant can name") || !strings.Contains(err.Error(), "nightly.yml") {
			t.Fatalf("%q: error %v", bad, err)
		}
	}
}
