package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
)

// The shipped AWS declaration owns the route: profile selects AWS_PROFILE, the three
// static credential names are unset, and only s3:// scopes are routable (F1, F4, F10).
func TestCurrentBrokerRoutingReadsTheShippedAWSDeclaration(t *testing.T) {
	scopedPolicyFixture(t, `packs = ["aws"]
[contexts.finance]
source = "aws"
profile = "finance-ro"
[contexts.team]
source = "aws"
profile = "team-ro"
[scopes.bucket]
source = "aws"
resource = "s3://a/"
context = "finance"
[scopes.team]
source = "aws"
resource = "s3://a/team/"
context = "team"
`, "")
	routing, err := CurrentBrokerRouting("aws")
	if err != nil {
		t.Fatal(err)
	}
	op := func(ids ...string) brokerpolicy.Operation {
		chain := make([]brokerpolicy.Resource, len(ids))
		for i, id := range ids {
			chain[i] = brokerpolicy.Resource{Type: "object", ID: id}
		}
		return brokerpolicy.Operation{Source: "aws", Kind: brokerpolicy.Read, Cell: "get_objects", Chain: chain}
	}
	if route, ok := routing.Policy.RouteFor(op("s3://a/team/x", "s3://a/team/", "s3://a/")); !ok || route.Context != "team" || route.Scope != "s3://a/team/" {
		t.Fatalf("the longer route must select: %+v %v", route, ok)
	}
	if route, ok := routing.Policy.RouteFor(op("s3://a/other/x", "s3://a/")); !ok || route.Context != "finance" {
		t.Fatalf("the broad route must select: %+v %v", route, ok)
	}
	if _, ok := routing.Policy.RouteFor(op("s3://b/x", "s3://b")); ok {
		t.Fatal("a call under no route has the default context")
	}
	if c := routing.Contexts["finance"]; c.Fields["profile"] != "finance-ro" {
		t.Fatalf("context field lost: %+v", routing.Contexts)
	}
}

func TestCurrentBrokerRoutingRefusesWhatCheckRefuses(t *testing.T) {
	for _, tc := range []struct{ name, user, want string }{
		{"F10-ce-route", `packs = ["aws"]
[contexts.finance]
source = "aws"
profile = "finance-ro"
[scopes.ce]
source = "aws"
resource = "ce://host-login"
context = "finance"
`, "route_anchors"},
		{"F10-undeclared-field", `packs = ["aws"]
[contexts.finance]
source = "aws"
region = "us-east-1"
[scopes.bucket]
source = "aws"
resource = "s3://a/"
context = "finance"
`, "undeclared"},
		{"F15-control", "packs = [\"aws\"]\n[contexts.finance]\nsource = \"aws\"\nprofile = \"a\\nb\"\n[scopes.bucket]\nsource = \"aws\"\nresource = \"s3://a/\"\ncontext = \"finance\"\n", "control"},
		{"F10-source-without-a-context-block", "packs = [\"circleci\"]\n[contexts.team]\nsource = \"circleci\"\nprofile = \"p\"\n", "declares no context block"},
		{"F4-two-routes-at-one-scope", `packs = ["aws"]
[contexts.finance]
source = "aws"
profile = "finance-ro"
[scopes.one]
source = "aws"
resource = "s3://a/"
context = "finance"
[scopes.two]
source = "aws"
resource = "s3://a/"
context = "finance"
`, "same source/resource"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scopedPolicyFixture(t, tc.user, "")
			if _, err := CurrentBrokerRouting(strings.SplitN(strings.Split(tc.user, "source = \"")[1], "\"", 2)[0]); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %s", err, tc.want)
			}
		})
	}
}

// F8: a workspace file cannot write a route or a context: the load refuses it, naming the
// user config. CurrentBrokerRouting also never reads the workspace.
func TestWorkspaceCannotWriteARouteOrContext(t *testing.T) {
	scopedPolicyFixture(t, "packs = [\"aws\"]\n[contexts.finance]\nsource = \"aws\"\nprofile = \"finance-ro\"\n", "")
	for name, project := range map[string]string{
		"route":   "[scopes.team]\nsource = \"aws\"\nresource = \"s3://a/\"\ncontext = \"finance\"\n",
		"context": "[contexts.finance]\nsource = \"aws\"\nprofile = \"workspace-chosen\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(project), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadReadableConfig(workspace, true, func(string) {}); err == nil || !strings.Contains(err.Error(), "user") {
				t.Fatalf("a workspace %s loaded: %v", name, err)
			}
		})
	}
	routing, err := CurrentBrokerRouting("aws")
	if err != nil {
		t.Fatal(err)
	}
	op := brokerpolicy.Operation{Source: "aws", Kind: brokerpolicy.Read, Cell: "get_objects", Chain: []brokerpolicy.Resource{{Type: "object", ID: "s3://a/x"}, {Type: "location", ID: "s3://a"}}}
	if _, routed := routing.Policy.RouteFor(op); routed {
		t.Fatal("a route selected without any user route")
	}
}
