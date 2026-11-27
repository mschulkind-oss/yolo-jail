package callbroker

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// F1, F2 for the shipped AWS declaration: profile selects AWS_PROFILE, and the three static
// credential names are absent from a routed call and present in a default-context one. The
// program is a fake that records its environment; no AWS CLI, credential or service is used.
func TestShippedAWSContextOnAFakeHostProgram(t *testing.T) {
	data, err := os.ReadFile("../../packs/aws/loopholes/aws-broker/loophole.toml")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loopholedecl.Decode(data, "../../packs/aws/loopholes/aws-broker")
	if err != nil {
		t.Fatal(err)
	}
	b := manifest.CallBroker
	t.Setenv("AWS_PROFILE", "host-profile")
	t.Setenv("AWS_REGION", "us-test-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-key-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-secret")
	t.Setenv("AWS_SESSION_TOKEN", "synthetic-token")
	program, record := fakeHostProgram(t, b.EnvFromHost...)

	matcher, err := brokerpolicy.NewScopeMatcher(b.Scope.Nesting, b.Scope.Separator, b.Scope.Anchors)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := brokerpolicy.NewPolicy("aws", matcher, []brokerpolicy.Cell{{Name: "get_objects", Kind: brokerpolicy.Read}}, true, false, nil, nil,
		[]brokerpolicy.Route{{Source: "aws", Scope: "s3://a/team/", Context: "team", Origin: "scopes.team"}})
	if err != nil {
		t.Fatal(err)
	}
	routing := config.BrokerRouting{Policy: policy, Contexts: map[string]config.BrokerExecutionContext{
		"team": {Name: "team", Source: "aws", Selected: true, Fields: map[string]string{"profile": "team-ro"}}}}
	op := func(ids ...string) brokerpolicy.Operation {
		chain := make([]brokerpolicy.Resource, len(ids))
		for i, id := range ids {
			chain[i] = brokerpolicy.Resource{Type: "object", ID: id}
		}
		return brokerpolicy.Operation{Source: "aws", Kind: brokerpolicy.Read, Cell: "get_objects", Chain: chain}
	}
	rc, err := selectRoutedContext("aws", "decl-hash", b.Context, routing, []brokerpolicy.Operation{op("s3://a/team/x", "s3://a/team/", "s3://a")})
	if err != nil || rc == nil {
		t.Fatalf("%+v %v", rc, err)
	}
	runFixture(t, program, rc)
	routed := recordedEnv(t, record)
	if routed["AWS_PROFILE"] != "team-ro" || routed["AWS_REGION"] != "us-test-1" {
		t.Fatalf("routed env: %v", routed)
	}
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if _, present := routed[name]; present {
			t.Errorf("%s reached a routed call", name)
		}
	}
	// A scope under no route runs in the default context, as does everything at baseline.
	if other, err := selectRoutedContext("aws", "decl-hash", b.Context, routing, []brokerpolicy.Operation{op("s3://b/x", "s3://b")}); other != nil || err != nil {
		t.Fatalf("an unrouted bucket must be the default context: %+v %v", other, err)
	}
	runFixture(t, program, nil)
	plain := recordedEnv(t, record)
	if plain["AWS_PROFILE"] != "host-profile" || plain["AWS_ACCESS_KEY_ID"] != "synthetic-key-id" || plain["AWS_SESSION_TOKEN"] != "synthetic-token" {
		t.Fatalf("the default context changed: %v", plain)
	}
}
