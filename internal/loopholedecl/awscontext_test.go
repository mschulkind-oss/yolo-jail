package loopholedecl

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// The AWS pack's declaration owns what a routed context may change (design 7.3): the
// profile field sets AWS_PROFILE, the three static credential names are unset, and only
// S3 scopes are routable. Core executes this metadata and has no AWS branch.
func TestShippedAWSBrokerDeclaresItsExecutionContext(t *testing.T) {
	const path = "../../packs/aws/loopholes/aws-broker"
	data, err := os.ReadFile(path + "/loophole.toml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(data, path)
	if err != nil {
		t.Fatal(err)
	}
	b := m.CallBroker
	if b == nil || b.Context == nil {
		t.Fatal("the shipped aws broker declares no context block")
	}
	if len(b.Context.Fields) != 1 || b.Context.Fields["profile"] != "AWS_PROFILE" {
		t.Fatalf("fields: %v", b.Context.Fields)
	}
	if !slices.Equal(b.Context.Unset, []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}) {
		t.Fatalf("unset: %v", b.Context.Unset)
	}
	if !slices.Equal(b.Context.RouteAnchors, []string{"s3://"}) || !slices.Equal(b.Scope.Anchors, []string{"s3://", "ce://"}) || b.Scope.Nesting != "segment" {
		t.Fatalf("route anchors %v within %v", b.Context.RouteAnchors, b.Scope.Anchors)
	}
	for _, name := range append([]string{"AWS_PROFILE"}, b.Context.Unset...) {
		if !slices.Contains(b.EnvFromHost, name) {
			t.Errorf("%s is not a captured host name, so the context edit would be a no-op", name)
		}
	}
	// The hash binds the unset rule: a context edit is a declaration change.
	changed := strings.Replace(string(data), `"AWS_SESSION_TOKEN"]
route_anchors`, `]
route_anchors`, 1)
	if changed == string(data) {
		t.Fatal("test edit did not apply")
	}
	other, err := Decode([]byte(changed), path)
	if err != nil {
		t.Fatal(err)
	}
	if CallBrokerDeclarationHash(other.CallBroker) == CallBrokerDeclarationHash(b) {
		t.Fatal("the declaration hash ignores the unset list")
	}
}
